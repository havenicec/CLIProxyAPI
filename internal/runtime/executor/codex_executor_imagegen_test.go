package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestEnsureImageGenerationTool_NoTools(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"draw a cat"}`)
	result := ensureImageGenerationTool(body, "gpt-5.4", nil)

	tools := gjson.GetBytes(result, "tools")
	if !tools.IsArray() {
		t.Fatalf("expected tools array, got %v", tools.Type)
	}
	arr := tools.Array()
	if len(arr) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(arr))
	}
	if arr[0].Get("type").String() != "image_generation" {
		t.Fatalf("expected type=image_generation, got %s", arr[0].Get("type").String())
	}
	if arr[0].Get("output_format").String() != "png" {
		t.Fatalf("expected output_format=png, got %s", arr[0].Get("output_format").String())
	}
}

func TestEnsureImageGenerationTool_ExistingToolsWithoutImageGen(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"function","name":"get_weather","parameters":{}}]}`)
	result := ensureImageGenerationTool(body, "gpt-5.4", nil)

	tools := gjson.GetBytes(result, "tools")
	arr := tools.Array()
	if len(arr) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(arr))
	}
	if arr[0].Get("type").String() != "function" {
		t.Fatalf("expected first tool type=function, got %s", arr[0].Get("type").String())
	}
	if arr[1].Get("type").String() != "image_generation" {
		t.Fatalf("expected second tool type=image_generation, got %s", arr[1].Get("type").String())
	}
}

func TestEnsureImageGenerationTool_AlreadyPresent(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"image_generation","output_format":"webp"},{"type":"function","name":"f1"}]}`)
	result := ensureImageGenerationTool(body, "gpt-5.4", nil)

	tools := gjson.GetBytes(result, "tools")
	arr := tools.Array()
	if len(arr) != 2 {
		t.Fatalf("expected 2 tools (no duplicate), got %d", len(arr))
	}
	if arr[0].Get("output_format").String() != "webp" {
		t.Fatalf("expected original output_format=webp preserved, got %s", arr[0].Get("output_format").String())
	}
}

func TestEnsureImageGenerationTool_EmptyToolsArray(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","tools":[]}`)
	result := ensureImageGenerationTool(body, "gpt-5.4", nil)

	tools := gjson.GetBytes(result, "tools")
	arr := tools.Array()
	if len(arr) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(arr))
	}
	if arr[0].Get("type").String() != "image_generation" {
		t.Fatalf("expected type=image_generation, got %s", arr[0].Get("type").String())
	}
}

func TestEnsureImageGenerationTool_WebSearchAndImageGen(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","tools":[{"type":"web_search"}]}`)
	result := ensureImageGenerationTool(body, "gpt-5.4", nil)

	tools := gjson.GetBytes(result, "tools")
	arr := tools.Array()
	if len(arr) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(arr))
	}
	if arr[0].Get("type").String() != "web_search" {
		t.Fatalf("expected first tool type=web_search, got %s", arr[0].Get("type").String())
	}
	if arr[1].Get("type").String() != "image_generation" {
		t.Fatalf("expected second tool type=image_generation, got %s", arr[1].Get("type").String())
	}
}

func TestEnsureImageGenerationTool_GPT53CodexSparkDoesNotInjectTool(t *testing.T) {
	body := []byte(`{"model":"gpt-5.3-codex-spark","input":"draw a cat"}`)
	result := ensureImageGenerationTool(body, "gpt-5.3-codex-spark", nil)

	if string(result) != string(body) {
		t.Fatalf("expected body to be unchanged, got %s", string(result))
	}
	if gjson.GetBytes(result, "tools").Exists() {
		t.Fatalf("expected no tools for gpt-5.3-codex-spark, got %s", gjson.GetBytes(result, "tools").Raw)
	}
}

func TestEnsureImageGenerationTool_FreeCodexAuthDoesNotInjectTool(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"draw a cat"}`)
	freeAuth := &cliproxyauth.Auth{
		Provider:   "codex",
		Attributes: map[string]string{"plan_type": "free"},
	}
	result := ensureImageGenerationTool(body, "gpt-5.4", freeAuth)

	if string(result) != string(body) {
		t.Fatalf("expected body to be unchanged, got %s", string(result))
	}
	if gjson.GetBytes(result, "tools").Exists() {
		t.Fatalf("expected no tools for free codex auth, got %s", gjson.GetBytes(result, "tools").Raw)
	}
}

func TestCodexBuildImagesResponsesRequestConfiguresImageTool(t *testing.T) {
	tool := []byte(`{"type":"image_generation","action":"edit","model":"gpt-image-2"}`)

	req := codexBuildImagesResponsesRequest("edit this", []string{"data:image/png;base64,AA=="}, tool)

	if got := gjson.GetBytes(req, "tool_choice").String(); got != "required" {
		t.Fatalf("tool_choice = %q, want required; body=%s", got, string(req))
	}
	if got := gjson.GetBytes(req, "tools.0.type").String(); got != "image_generation" {
		t.Fatalf("tools.0.type = %q, want image_generation; body=%s", got, string(req))
	}
	if got := gjson.GetBytes(req, "input.0.content.1.type").String(); got != "input_image" {
		t.Fatalf("input image part type = %q, want input_image; body=%s", got, string(req))
	}
}

func TestPrepareCodexOpenAIImageBodyRestoresImageTool(t *testing.T) {
	body := []byte(`{"instructions":"","stream":true,"model":"gpt-5.4-mini","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"draw"}]}],"tool_choice":{"type":"image_generation"}}`)

	out, err := NewCodexExecutor(&config.Config{}).prepareCodexOpenAIImageBody(body, cliproxyexecutor.Request{
		Model:   "gpt-image-2",
		Payload: []byte(`{"model":"gpt-image-2","prompt":"draw"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-image"),
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/images/generations",
		},
	}, "gpt-5.4-mini")
	if err != nil {
		t.Fatalf("prepareCodexOpenAIImageBody error: %v", err)
	}

	if got := gjson.GetBytes(out, "tool_choice").String(); got != "required" {
		t.Fatalf("tool_choice = %q, want required; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "tools.0.type").String(); got != "image_generation" {
		t.Fatalf("tools.0.type = %q, want image_generation; body=%s", got, string(out))
	}
}

func TestCodexExecutorImageEndpointResponsesPathConfiguresImageTool(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		gotBody = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"background\":false,\"error\":null}}\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	result, err := executor.ExecuteStream(context.Background(), &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL,
		"api_key":  "test",
	}}, cliproxyexecutor.Request{
		Model:   "gpt-5.4-mini",
		Payload: codexBuildImagesResponsesRequest("draw", nil, []byte(`{"type":"image_generation","action":"generate","model":"gpt-image-2"}`)),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Stream:       true,
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/images/generations",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	for range result.Chunks {
	}

	if got := gjson.GetBytes(gotBody, "tool_choice").String(); got != "required" {
		t.Fatalf("tool_choice = %q, want required; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "tools.0.type").String(); got != "image_generation" {
		t.Fatalf("tools.0.type = %q, want image_generation; body=%s", got, string(gotBody))
	}
}
