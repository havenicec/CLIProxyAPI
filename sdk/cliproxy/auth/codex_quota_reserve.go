package auth

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

const (
	// CodexFiveHourReservePercentMetadataKey stores a per-auth reserve override.
	CodexFiveHourReservePercentMetadataKey = "codex_five_hour_reserve_percent"
	// CodexFiveHourReservePercentMetadataDashKey is accepted for auth-file convenience.
	CodexFiveHourReservePercentMetadataDashKey = "codex-five-hour-reserve-percent"
	// CodexQuotaMetadataKey stores the latest observed Codex quota snapshot.
	CodexQuotaMetadataKey = "codex_quota"
)

const (
	codexQuotaWindowFiveHour = "five_hour"
	codexQuotaWindowWeekly   = "weekly"
)

type codexQuotaField int

const (
	codexQuotaFieldUnknown codexQuotaField = iota
	codexQuotaFieldLimit
	codexQuotaFieldRemaining
	codexQuotaFieldUsed
	codexQuotaFieldRemainingPercent
	codexQuotaFieldUsedPercent
	codexQuotaFieldReset
)

type codexQuotaObservation struct {
	FiveHour codexQuotaWindowObservation
	Weekly   codexQuotaWindowObservation
}

type codexQuotaWindowObservation struct {
	Window              string
	Limit               int64
	Remaining           int64
	Used                int64
	RemainingPercent    float64
	ResetAt             time.Time
	HasLimit            bool
	HasRemaining        bool
	HasUsed             bool
	HasRemainingPercent bool
	HasReset            bool
}

type codexQuotaReserveDecision struct {
	Block                    bool
	Reason                   string
	Window                   string
	RecoverAt                time.Time
	RemainingPercent         float64
	HasRemainingPercent      bool
	ConfiguredReservePercent int
}

func (o codexQuotaObservation) hasAny() bool {
	return o.FiveHour.hasAny() || o.Weekly.hasAny()
}

func (o codexQuotaWindowObservation) hasAny() bool {
	return o.HasLimit || o.HasRemaining || o.HasUsed || o.HasRemainingPercent || o.HasReset
}

func (o codexQuotaWindowObservation) remainingPercentValue() (float64, bool) {
	if o.HasRemainingPercent {
		return clampFloatPercent(o.RemainingPercent), true
	}
	if o.HasLimit && o.Limit > 0 && o.HasRemaining {
		return clampFloatPercent(float64(o.Remaining) / float64(o.Limit) * 100), true
	}
	if o.HasLimit && o.Limit > 0 && o.HasUsed {
		remaining := 100 - float64(o.Used)/float64(o.Limit)*100
		return clampFloatPercent(remaining), true
	}
	return 0, false
}

func (o codexQuotaWindowObservation) exhausted() bool {
	if o.HasRemaining && o.Remaining <= 0 {
		return true
	}
	if o.HasRemainingPercent && o.RemainingPercent <= 0 {
		return true
	}
	return o.HasLimit && o.Limit > 0 && o.HasUsed && o.Used >= o.Limit
}

func (o codexQuotaWindowObservation) resetOrDefault(now time.Time, fallback time.Duration) time.Time {
	if o.HasReset && o.ResetAt.After(now) {
		return o.ResetAt
	}
	return now.Add(fallback)
}

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func clampFloatPercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

// CodexFiveHourReservePercentOverride returns the per-auth reserve override.
func CodexFiveHourReservePercentOverride(auth *Auth) (int, bool) {
	if auth == nil || len(auth.Metadata) == 0 {
		return 0, false
	}
	for _, key := range []string{CodexFiveHourReservePercentMetadataKey, CodexFiveHourReservePercentMetadataDashKey} {
		raw, ok := auth.Metadata[key]
		if !ok || raw == nil {
			continue
		}
		value, okValue := intFromAny(raw)
		if !okValue {
			continue
		}
		return clampPercent(value), true
	}
	return 0, false
}

// EffectiveCodexFiveHourReservePercent resolves per-auth override before global config.
func EffectiveCodexFiveHourReservePercent(auth *Auth, cfg *internalconfig.Config) int {
	if value, ok := CodexFiveHourReservePercentOverride(auth); ok {
		return value
	}
	if cfg == nil {
		return 0
	}
	return clampPercent(cfg.QuotaExceeded.CodexFiveHourReservePercent)
}

func intFromAny(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case uint:
		return int(value), true
	case uint8:
		return int(value), true
	case uint16:
		return int(value), true
	case uint32:
		return int(value), true
	case uint64:
		if value > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(value), true
	case float64:
		return int(value), true
	case float32:
		return int(value), true
	case json.Number:
		if i, err := value.Int64(); err == nil {
			return int(i), true
		}
		if f, err := value.Float64(); err == nil {
			return int(f), true
		}
	case string:
		cleaned := strings.TrimSpace(strings.TrimSuffix(value, "%"))
		if cleaned == "" {
			return 0, false
		}
		if i, err := strconv.Atoi(cleaned); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
			return int(f), true
		}
	}
	return 0, false
}

func parseCodexQuotaObservation(headers http.Header, payload []byte, now time.Time) codexQuotaObservation {
	obs := parseCodexQuotaHeaders(headers, now)
	if len(payload) == 0 {
		return obs
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var body any
	if err := decoder.Decode(&body); err != nil {
		return obs
	}
	mergeCodexQuotaObservation(&obs, parseCodexQuotaJSON(body, now))
	return obs
}

func parseCodexQuotaHeaders(headers http.Header, now time.Time) codexQuotaObservation {
	var obs codexQuotaObservation
	for name, values := range headers {
		window := codexQuotaWindowFromText(name)
		if window == "" {
			continue
		}
		field := codexQuotaFieldFromText(name)
		if field == codexQuotaFieldUnknown {
			continue
		}
		for _, value := range values {
			applyCodexQuotaField(observationWindow(&obs, window), field, name, value, now)
		}
	}
	return obs
}

func parseCodexQuotaJSON(body any, now time.Time) codexQuotaObservation {
	var obs codexQuotaObservation
	collectCodexQuotaJSON(body, nil, &obs, now)
	return obs
}

func collectCodexQuotaJSON(node any, path []string, obs *codexQuotaObservation, now time.Time) {
	switch typed := node.(type) {
	case map[string]any:
		window := codexQuotaWindowFromText(strings.Join(path, " "))
		if labelWindow := codexQuotaWindowFromMapLabels(typed); labelWindow != "" {
			window = labelWindow
		}
		if window != "" {
			parseCodexQuotaObjectFields(typed, observationWindow(obs, window), now)
		}
		for key, value := range typed {
			collectCodexQuotaJSON(value, append(path, key), obs, now)
		}
	case []any:
		for _, value := range typed {
			collectCodexQuotaJSON(value, path, obs, now)
		}
	}
}

func parseCodexQuotaObjectFields(fields map[string]any, target *codexQuotaWindowObservation, now time.Time) {
	for key, value := range fields {
		field := codexQuotaFieldFromText(key)
		if field == codexQuotaFieldUnknown {
			continue
		}
		applyCodexQuotaField(target, field, key, value, now)
	}
}

func observationWindow(obs *codexQuotaObservation, window string) *codexQuotaWindowObservation {
	if obs == nil {
		return nil
	}
	switch window {
	case codexQuotaWindowFiveHour:
		obs.FiveHour.Window = codexQuotaWindowFiveHour
		return &obs.FiveHour
	case codexQuotaWindowWeekly:
		obs.Weekly.Window = codexQuotaWindowWeekly
		return &obs.Weekly
	default:
		return nil
	}
}

func mergeCodexQuotaObservation(dst *codexQuotaObservation, src codexQuotaObservation) {
	if dst == nil {
		return
	}
	mergeCodexQuotaWindow(&dst.FiveHour, src.FiveHour)
	mergeCodexQuotaWindow(&dst.Weekly, src.Weekly)
}

func mergeCodexQuotaWindow(dst *codexQuotaWindowObservation, src codexQuotaWindowObservation) {
	if dst == nil || !src.hasAny() {
		return
	}
	if src.Window != "" {
		dst.Window = src.Window
	}
	if src.HasLimit {
		dst.Limit = src.Limit
		dst.HasLimit = true
	}
	if src.HasRemaining {
		dst.Remaining = src.Remaining
		dst.HasRemaining = true
	}
	if src.HasUsed {
		dst.Used = src.Used
		dst.HasUsed = true
	}
	if src.HasRemainingPercent {
		dst.RemainingPercent = src.RemainingPercent
		dst.HasRemainingPercent = true
	}
	if src.HasReset {
		dst.ResetAt = src.ResetAt
		dst.HasReset = true
	}
}

func codexQuotaWindowFromMapLabels(fields map[string]any) string {
	for _, key := range []string{"window", "period", "bucket", "interval", "limit_type", "limitType", "type", "name"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		if value, okValue := stringFromAny(raw); okValue {
			if window := codexQuotaWindowFromText(value); window != "" {
				return window
			}
		}
	}
	return ""
}

func codexQuotaWindowFromText(text string) string {
	raw := strings.ToLower(strings.TrimSpace(text))
	if raw == "" {
		return ""
	}
	compact := compactQuotaText(raw)
	tokens := quotaTokens(raw)
	if strings.Contains(compact, "5h") || strings.Contains(compact, "5hour") || strings.Contains(compact, "fivehour") || containsToken(tokens, "5h", "5hr", "fivehour") || containsAllTokens(tokens, "five", "hour") || containsAllTokens(tokens, "5", "hour") {
		return codexQuotaWindowFiveHour
	}
	if strings.Contains(compact, "weekly") || strings.Contains(compact, "week") || strings.Contains(compact, "1w") || strings.Contains(compact, "7d") || containsToken(tokens, "weekly", "week", "1w", "7d") {
		return codexQuotaWindowWeekly
	}
	return ""
}

func codexQuotaFieldFromText(text string) codexQuotaField {
	tokens := quotaTokens(text)
	if containsToken(tokens, "reset", "resets", "retry", "recover", "recovery") {
		return codexQuotaFieldReset
	}
	hasPercent := containsToken(tokens, "percent", "percentage", "pct", "ratio", "fraction")
	if containsToken(tokens, "remaining", "remain", "remains", "available", "availability", "left") {
		if hasPercent {
			return codexQuotaFieldRemainingPercent
		}
		return codexQuotaFieldRemaining
	}
	if containsToken(tokens, "used", "use", "usage", "consumed", "spent") {
		if hasPercent {
			return codexQuotaFieldUsedPercent
		}
		return codexQuotaFieldUsed
	}
	if containsToken(tokens, "limit", "limits", "quota", "total", "capacity", "cap", "max", "maximum") {
		return codexQuotaFieldLimit
	}
	return codexQuotaFieldUnknown
}

func applyCodexQuotaField(target *codexQuotaWindowObservation, field codexQuotaField, key string, raw any, now time.Time) {
	if target == nil || field == codexQuotaFieldUnknown || raw == nil {
		return
	}
	switch field {
	case codexQuotaFieldLimit:
		if value, ok := int64FromAny(raw); ok && value >= 0 {
			target.Limit = value
			target.HasLimit = true
		}
	case codexQuotaFieldRemaining:
		if value, ok := int64FromAny(raw); ok && value >= 0 {
			target.Remaining = value
			target.HasRemaining = true
		}
	case codexQuotaFieldUsed:
		if value, ok := int64FromAny(raw); ok && value >= 0 {
			target.Used = value
			target.HasUsed = true
		}
	case codexQuotaFieldRemainingPercent:
		if value, ok := percentFromAny(key, raw); ok {
			target.RemainingPercent = clampFloatPercent(value)
			target.HasRemainingPercent = true
		}
	case codexQuotaFieldUsedPercent:
		if value, ok := percentFromAny(key, raw); ok {
			target.RemainingPercent = clampFloatPercent(100 - value)
			target.HasRemainingPercent = true
		}
	case codexQuotaFieldReset:
		if resetAt, ok := resetTimeFromAny(key, raw, now); ok {
			target.ResetAt = resetAt
			target.HasReset = true
		}
	}
}

func quotaTokens(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	start := -1
	for idx, r := range text {
		isToken := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if isToken {
			if start < 0 {
				start = idx
			}
			continue
		}
		if start >= 0 {
			tokens = append(tokens, text[start:idx])
			start = -1
		}
	}
	if start >= 0 {
		tokens = append(tokens, text[start:])
	}
	return tokens
}

func compactQuotaText(text string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(text) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func containsToken(tokens []string, candidates ...string) bool {
	for _, token := range tokens {
		for _, candidate := range candidates {
			if token == candidate {
				return true
			}
		}
	}
	return false
}

func containsAllTokens(tokens []string, a string, b string) bool {
	hasA := false
	hasB := false
	for _, token := range tokens {
		if token == a {
			hasA = true
		}
		if token == b {
			hasB = true
		}
	}
	return hasA && hasB
}

func stringFromAny(raw any) (string, bool) {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value), strings.TrimSpace(value) != ""
	case []byte:
		trimmed := strings.TrimSpace(string(value))
		return trimmed, trimmed != ""
	case json.Number:
		return value.String(), true
	default:
		return "", false
	}
}

func int64FromAny(raw any) (int64, bool) {
	switch value := raw.(type) {
	case int:
		return int64(value), true
	case int8:
		return int64(value), true
	case int16:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case uint:
		return int64(value), true
	case uint8:
		return int64(value), true
	case uint16:
		return int64(value), true
	case uint32:
		return int64(value), true
	case uint64:
		if value > uint64(1<<63-1) {
			return 0, false
		}
		return int64(value), true
	case float32:
		return int64(value), true
	case float64:
		return int64(value), true
	case json.Number:
		if i, err := value.Int64(); err == nil {
			return i, true
		}
		if f, err := value.Float64(); err == nil {
			return int64(f), true
		}
	case string:
		cleaned := strings.TrimSpace(strings.TrimSuffix(value, "%"))
		if cleaned == "" {
			return 0, false
		}
		if i, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
			return int64(f), true
		}
	}
	return 0, false
}

func float64FromAny(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case json.Number:
		if f, err := value.Float64(); err == nil {
			return f, true
		}
	case string:
		cleaned := strings.TrimSpace(strings.TrimSuffix(value, "%"))
		if cleaned == "" {
			return 0, false
		}
		if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func percentFromAny(key string, raw any) (float64, bool) {
	value, ok := float64FromAny(raw)
	if !ok {
		return 0, false
	}
	tokens := quotaTokens(key)
	if containsToken(tokens, "ratio", "fraction") && value >= 0 && value <= 1 {
		return value * 100, true
	}
	return value, true
}

func resetTimeFromAny(key string, raw any, now time.Time) (time.Time, bool) {
	if text, ok := stringFromAny(raw); ok {
		if ts, okTime := parseResetTimeString(key, text, now); okTime {
			return ts, true
		}
	}
	value, ok := int64FromAny(raw)
	if !ok || value <= 0 {
		return time.Time{}, false
	}
	return resetTimeFromNumber(key, value, now)
}

func parseResetTimeString(key string, value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts, true
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return now.Add(duration), true
	}
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		return resetTimeFromNumber(key, number, now)
	}
	return time.Time{}, false
}

func resetTimeFromNumber(key string, value int64, now time.Time) (time.Time, bool) {
	tokens := quotaTokens(key)
	if containsToken(tokens, "after", "in", "seconds", "secs", "sec") {
		return now.Add(time.Duration(value) * time.Second), true
	}
	switch {
	case value > 1_000_000_000_000:
		return time.UnixMilli(value), true
	case value > 1_000_000_000:
		return time.Unix(value, 0), true
	default:
		return now.Add(time.Duration(value) * time.Second), true
	}
}

func codexQuotaReserveDecisionForObservation(obs codexQuotaObservation, reservePercent int, now time.Time) codexQuotaReserveDecision {
	reservePercent = clampPercent(reservePercent)
	if obs.Weekly.exhausted() {
		return codexQuotaReserveDecision{
			Block:               true,
			Reason:              "codex_weekly_quota",
			Window:              codexQuotaWindowWeekly,
			RecoverAt:           obs.Weekly.resetOrDefault(now, 7*24*time.Hour),
			RemainingPercent:    0,
			HasRemainingPercent: true,
		}
	}
	if reservePercent <= 0 {
		return codexQuotaReserveDecision{}
	}
	if obs.FiveHour.exhausted() {
		return codexQuotaReserveDecision{
			Block:                    true,
			Reason:                   "codex_five_hour_reserve",
			Window:                   codexQuotaWindowFiveHour,
			RecoverAt:                obs.FiveHour.resetOrDefault(now, 5*time.Hour),
			RemainingPercent:         0,
			HasRemainingPercent:      true,
			ConfiguredReservePercent: reservePercent,
		}
	}
	remainingPercent, ok := obs.FiveHour.remainingPercentValue()
	if !ok || remainingPercent > float64(reservePercent) {
		return codexQuotaReserveDecision{}
	}
	return codexQuotaReserveDecision{
		Block:                    true,
		Reason:                   "codex_five_hour_reserve",
		Window:                   codexQuotaWindowFiveHour,
		RecoverAt:                obs.FiveHour.resetOrDefault(now, 5*time.Hour),
		RemainingPercent:         remainingPercent,
		HasRemainingPercent:      true,
		ConfiguredReservePercent: reservePercent,
	}
}

func (m *Manager) applyCodexQuotaReserveLocked(auth *Auth, result Result, now time.Time) (bool, string) {
	if auth == nil {
		return false, ""
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") && !strings.EqualFold(strings.TrimSpace(result.Provider), "codex") {
		return false, ""
	}
	obs := parseCodexQuotaObservation(result.Headers, result.Payload, now)
	if !obs.hasAny() {
		return false, ""
	}
	cfg, _ := m.runtimeConfig.Load().(*internalconfig.Config)
	reservePercent := EffectiveCodexFiveHourReservePercent(auth, cfg)
	storeCodexQuotaObservationMetadata(auth, obs, reservePercent, now)
	decision := codexQuotaReserveDecisionForObservation(obs, reservePercent, now)
	if !decision.Block {
		return false, ""
	}
	applyCodexQuotaReserveBlock(auth, result.Model, decision, now)
	return true, decision.Reason
}

func storeCodexQuotaObservationMetadata(auth *Auth, obs codexQuotaObservation, reservePercent int, now time.Time) {
	if auth == nil || !obs.hasAny() {
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	quotaMeta := mapFromAny(auth.Metadata[CodexQuotaMetadataKey])
	if obs.FiveHour.hasAny() {
		quotaMeta[codexQuotaWindowFiveHour] = codexQuotaWindowMetadata(obs.FiveHour, reservePercent, now)
	}
	if obs.Weekly.hasAny() {
		quotaMeta[codexQuotaWindowWeekly] = codexQuotaWindowMetadata(obs.Weekly, 0, now)
	}
	auth.Metadata[CodexQuotaMetadataKey] = quotaMeta
}

func codexQuotaWindowMetadata(obs codexQuotaWindowObservation, reservePercent int, now time.Time) map[string]any {
	out := make(map[string]any)
	if obs.HasLimit {
		out["limit"] = obs.Limit
	}
	if obs.HasRemaining {
		out["remaining"] = obs.Remaining
	}
	if obs.HasUsed {
		out["used"] = obs.Used
	}
	if percent, ok := obs.remainingPercentValue(); ok {
		out["remaining_percent"] = percent
	}
	if obs.HasReset {
		out["reset_at"] = obs.ResetAt.UTC().Format(time.RFC3339)
	}
	if reservePercent > 0 && obs.Window == codexQuotaWindowFiveHour {
		out["reserve_percent"] = clampPercent(reservePercent)
	}
	out["updated_at"] = now.UTC().Format(time.RFC3339)
	return out
}

func mapFromAny(raw any) map[string]any {
	switch typed := raw.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	default:
		return make(map[string]any)
	}
}

func applyCodexQuotaReserveBlock(auth *Auth, model string, decision codexQuotaReserveDecision, now time.Time) {
	if auth == nil || !decision.Block {
		return
	}
	recoverAt := decision.RecoverAt
	if recoverAt.IsZero() || recoverAt.Before(now) {
		if decision.Window == codexQuotaWindowWeekly {
			recoverAt = now.Add(7 * 24 * time.Hour)
		} else {
			recoverAt = now.Add(5 * time.Hour)
		}
	}
	quota := QuotaState{
		Exceeded:      true,
		Reason:        decision.Reason,
		NextRecoverAt: recoverAt,
	}
	auth.Unavailable = true
	auth.Status = StatusError
	auth.StatusMessage = decision.Reason
	auth.Quota = quota
	auth.NextRetryAfter = recoverAt
	auth.UpdatedAt = now
	if model == "" {
		return
	}
	state := ensureModelState(auth, model)
	if state == nil {
		return
	}
	state.Unavailable = true
	state.Status = StatusError
	state.StatusMessage = decision.Reason
	state.NextRetryAfter = recoverAt
	state.Quota = quota
	state.UpdatedAt = now
}
