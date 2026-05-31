package auth

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type codexQuotaReserveExecutor struct {
	mu            sync.Mutex
	headersByAuth map[string]http.Header
	calls         []string
}

func (e *codexQuotaReserveExecutor) Identifier() string { return "codex" }

func (e *codexQuotaReserveExecutor) Execute(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	_ = ctx
	_ = req
	_ = opts
	e.mu.Lock()
	e.calls = append(e.calls, auth.ID)
	headers := e.headersByAuth[auth.ID].Clone()
	e.mu.Unlock()
	return cliproxyexecutor.Response{Payload: []byte(auth.ID), Headers: headers}, nil
}

func (e *codexQuotaReserveExecutor) ExecuteStream(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	_ = ctx
	_ = auth
	_ = req
	_ = opts
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "ExecuteStream not implemented"}
}

func (e *codexQuotaReserveExecutor) Refresh(ctx context.Context, auth *Auth) (*Auth, error) {
	_ = ctx
	return auth, nil
}

func (e *codexQuotaReserveExecutor) CountTokens(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	_ = ctx
	_ = auth
	_ = req
	_ = opts
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusNotImplemented, Message: "CountTokens not implemented"}
}

func (e *codexQuotaReserveExecutor) HttpRequest(ctx context.Context, auth *Auth, req *http.Request) (*http.Response, error) {
	_ = ctx
	_ = auth
	_ = req
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "HttpRequest not implemented"}
}

func codexQuotaHeaders(limit, remaining int, resetAt time.Time) http.Header {
	return http.Header{
		"X-Codex-Quota-Limit-5h":       {strconv.Itoa(limit)},
		"X-Codex-Quota-Remaining-5h":   {strconv.Itoa(remaining)},
		"X-Codex-Quota-Reset-5h":       {strconv.FormatInt(resetAt.Unix(), 10)},
		"X-Codex-Quota-Limit-Weekly":   {"1000"},
		"X-Codex-Quota-Remaining-Week": {"1000"},
	}
}

func registerCodexQuotaTestAuths(t *testing.T, manager *Manager, model string, auths ...*Auth) {
	t.Helper()
	reg := registry.GetGlobalRegistry()
	for _, auth := range auths {
		reg.RegisterClient(auth.ID, "codex", []*registry.ModelInfo{{ID: model}})
		if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
			t.Fatalf("register %s: %v", auth.ID, errRegister)
		}
	}
	t.Cleanup(func() {
		for _, auth := range auths {
			reg.UnregisterClient(auth.ID)
		}
	})
}

func TestManagerCodexFiveHourReserveBlocksAuthFromHeaders(t *testing.T) {
	model := "gpt-5.5"
	resetAt := time.Now().Add(5 * time.Hour).UTC()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.SetConfig(&internalconfig.Config{
		QuotaExceeded: internalconfig.QuotaExceeded{CodexFiveHourReservePercent: 20},
	})
	executor := &codexQuotaReserveExecutor{
		headersByAuth: map[string]http.Header{
			"codex-reserve-a": codexQuotaHeaders(100, 20, resetAt),
			"codex-reserve-b": codexQuotaHeaders(100, 80, resetAt),
		},
	}
	manager.RegisterExecutor(executor)
	registerCodexQuotaTestAuths(t, manager, model,
		&Auth{ID: "codex-reserve-a", Provider: "codex", Metadata: map[string]any{"type": "codex"}},
		&Auth{ID: "codex-reserve-b", Provider: "codex", Metadata: map[string]any{"type": "codex"}},
	)

	_, errExecute := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.PinnedAuthMetadataKey: "codex-reserve-a"},
	})
	if errExecute != nil {
		t.Fatalf("execute pinned auth: %v", errExecute)
	}

	blocked, ok := manager.GetByID("codex-reserve-a")
	if !ok {
		t.Fatal("expected blocked auth to exist")
	}
	if !blocked.Quota.Exceeded || blocked.Quota.Reason != "codex_five_hour_reserve" {
		t.Fatalf("quota = %#v, want codex_five_hour_reserve", blocked.Quota)
	}
	if blocked.Metadata[CodexQuotaMetadataKey] == nil {
		t.Fatal("expected codex quota metadata snapshot")
	}

	resp, errExecute := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if errExecute != nil {
		t.Fatalf("execute after reserve block: %v", errExecute)
	}
	if got := string(resp.Payload); got != "codex-reserve-b" {
		t.Fatalf("selected auth = %q, want codex-reserve-b", got)
	}
}

func TestManagerCodexFiveHourReservePerAuthOverrideCanDisableGlobal(t *testing.T) {
	model := "gpt-5.5"
	resetAt := time.Now().Add(5 * time.Hour).UTC()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.SetConfig(&internalconfig.Config{
		QuotaExceeded: internalconfig.QuotaExceeded{CodexFiveHourReservePercent: 50},
	})
	executor := &codexQuotaReserveExecutor{
		headersByAuth: map[string]http.Header{
			"codex-reserve-override": codexQuotaHeaders(100, 10, resetAt),
		},
	}
	manager.RegisterExecutor(executor)
	registerCodexQuotaTestAuths(t, manager, model,
		&Auth{
			ID:       "codex-reserve-override",
			Provider: "codex",
			Metadata: map[string]any{
				"type":                                 "codex",
				CodexFiveHourReservePercentMetadataKey: 0,
			},
		},
	)

	if _, errExecute := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); errExecute != nil {
		t.Fatalf("execute: %v", errExecute)
	}
	updated, ok := manager.GetByID("codex-reserve-override")
	if !ok {
		t.Fatal("expected auth to exist")
	}
	if updated.Quota.Exceeded {
		t.Fatalf("quota should not be blocked when per-auth override is 0: %#v", updated.Quota)
	}
}

func TestManagerCodexWeeklyQuotaBlocksEvenWhenFiveHourAvailable(t *testing.T) {
	model := "gpt-5.5"
	fiveHourReset := time.Now().Add(5 * time.Hour).UTC()
	weeklyReset := time.Now().Add(7 * 24 * time.Hour).UTC()
	headers := codexQuotaHeaders(100, 100, fiveHourReset)
	headers.Set("X-Codex-Quota-Limit-Weekly", "1000")
	headers.Set("X-Codex-Quota-Remaining-Weekly", "0")
	headers.Set("X-Codex-Quota-Reset-Weekly", strconv.FormatInt(weeklyReset.Unix(), 10))

	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.SetConfig(&internalconfig.Config{})
	executor := &codexQuotaReserveExecutor{
		headersByAuth: map[string]http.Header{"codex-weekly-empty": headers},
	}
	manager.RegisterExecutor(executor)
	registerCodexQuotaTestAuths(t, manager, model,
		&Auth{ID: "codex-weekly-empty", Provider: "codex", Metadata: map[string]any{"type": "codex"}},
	)

	if _, errExecute := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); errExecute != nil {
		t.Fatalf("execute: %v", errExecute)
	}
	updated, ok := manager.GetByID("codex-weekly-empty")
	if !ok {
		t.Fatal("expected auth to exist")
	}
	if !updated.Quota.Exceeded || updated.Quota.Reason != "codex_weekly_quota" {
		t.Fatalf("quota = %#v, want codex_weekly_quota", updated.Quota)
	}
	if updated.NextRetryAfter.Before(time.Now().Add(6 * 24 * time.Hour)) {
		t.Fatalf("NextRetryAfter = %s, want weekly cooldown", updated.NextRetryAfter)
	}
}
