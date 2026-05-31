package executor

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestSanitizeCodexToolChoice_RemovesRequiredWithoutTools(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hi","tool_choice":"required"}`)

	out := sanitizeCodexToolChoice(body)

	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("expected tool_choice to be removed, got %s", string(out))
	}
}

func TestSanitizeCodexToolChoice_RemovesRequiredWithEmptyTools(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hi","tools":[],"tool_choice":"required"}`)

	out := sanitizeCodexToolChoice(body)

	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("expected tool_choice to be removed, got %s", string(out))
	}
}

func TestSanitizeCodexToolChoice_KeepsRequiredWithTools(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"lookup"}],"tool_choice":"required"}`)

	out := sanitizeCodexToolChoice(body)

	if got := gjson.GetBytes(out, "tool_choice").String(); got != "required" {
		t.Fatalf("tool_choice = %q, want required; body=%s", got, string(out))
	}
}

func TestSanitizeCodexToolChoice_RemovesMissingSpecificTool(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"web_search"}],"tool_choice":{"type":"image_generation"}}`)

	out := sanitizeCodexToolChoice(body)

	if gjson.GetBytes(out, "tool_choice").Exists() {
		t.Fatalf("expected tool_choice to be removed, got %s", string(out))
	}
}

func TestSanitizeCodexToolChoice_KeepsSpecificBuiltinTool(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"image_generation"}],"tool_choice":{"type":"image_generation"}}`)

	out := sanitizeCodexToolChoice(body)

	if got := gjson.GetBytes(out, "tool_choice.type").String(); got != "image_generation" {
		t.Fatalf("tool_choice.type = %q, want image_generation; body=%s", got, string(out))
	}
}

func TestSanitizeCodexToolChoice_KeepsSpecificFunctionTool(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"hi","tools":[{"type":"function","name":"lookup"}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`)

	out := sanitizeCodexToolChoice(body)

	if got := gjson.GetBytes(out, "tool_choice.function.name").String(); got != "lookup" {
		t.Fatalf("tool_choice.function.name = %q, want lookup; body=%s", got, string(out))
	}
}
