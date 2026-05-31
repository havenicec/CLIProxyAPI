package executor

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodexExecutorCacheHelper_OpenAIChatCompletions_StablePromptCacheKeyFromAPIKey(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Set("userApiKey", "test-api-key")

	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	executor := &CodexExecutor{}
	rawJSON := []byte(`{"model":"gpt-5.3-codex","stream":true}`)
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.3-codex",
		Payload: []byte(`{"model":"gpt-5.3-codex"}`),
	}
	url := "https://example.com/responses"

	httpReq, err := executor.cacheHelper(ctx, sdktranslator.FromString("openai"), url, req, rawJSON, nil)
	if err != nil {
		t.Fatalf("cacheHelper error: %v", err)
	}

	body, errRead := io.ReadAll(httpReq.Body)
	if errRead != nil {
		t.Fatalf("read request body: %v", errRead)
	}

	expectedKey := uuid.NewSHA1(uuid.NameSpaceOID, []byte("cli-proxy-api:codex:prompt-cache:test-api-key")).String()
	gotKey := gjson.GetBytes(body, "prompt_cache_key").String()
	if gotKey != expectedKey {
		t.Fatalf("prompt_cache_key = %q, want %q", gotKey, expectedKey)
	}
	if gotConversation := httpReq.Header.Get("Conversation_id"); gotConversation != "" {
		t.Fatalf("Conversation_id = %q, want empty", gotConversation)
	}
	if gotSession := httpReq.Header.Get("Session_id"); gotSession != expectedKey {
		t.Fatalf("Session_id = %q, want %q", gotSession, expectedKey)
	}

	httpReq2, err := executor.cacheHelper(ctx, sdktranslator.FromString("openai"), url, req, rawJSON, nil)
	if err != nil {
		t.Fatalf("cacheHelper error (second call): %v", err)
	}
	body2, errRead2 := io.ReadAll(httpReq2.Body)
	if errRead2 != nil {
		t.Fatalf("read request body (second call): %v", errRead2)
	}
	gotKey2 := gjson.GetBytes(body2, "prompt_cache_key").String()
	if gotKey2 != expectedKey {
		t.Fatalf("prompt_cache_key (second call) = %q, want %q", gotKey2, expectedKey)
	}
}

func TestCodexExecutorCacheHelperScopesPromptCacheKeyByOAuthAccount(t *testing.T) {
	executor := &CodexExecutor{}
	rawJSON := []byte(`{"model":"gpt-5-codex","stream":true}`)
	req := cliproxyexecutor.Request{
		Model:   "gpt-5-codex",
		Payload: []byte(`{"prompt_cache_key":"cache-1"}`),
	}
	authA := &cliproxyauth.Auth{ID: "auth-a", Provider: "codex", Metadata: map[string]any{"account_id": "acct-a"}}
	authB := &cliproxyauth.Auth{ID: "auth-b", Provider: "codex", Metadata: map[string]any{"account_id": "acct-b"}}
	url := "https://example.com/responses"

	httpReqA, err := executor.cacheHelper(context.Background(), sdktranslator.FromString("openai-response"), url, req, rawJSON, authA)
	if err != nil {
		t.Fatalf("cacheHelper auth A error: %v", err)
	}
	bodyA, errReadA := io.ReadAll(httpReqA.Body)
	if errReadA != nil {
		t.Fatalf("read auth A request body: %v", errReadA)
	}
	httpReqB, err := executor.cacheHelper(context.Background(), sdktranslator.FromString("openai-response"), url, req, rawJSON, authB)
	if err != nil {
		t.Fatalf("cacheHelper auth B error: %v", err)
	}
	bodyB, errReadB := io.ReadAll(httpReqB.Body)
	if errReadB != nil {
		t.Fatalf("read auth B request body: %v", errReadB)
	}

	cacheA := gjson.GetBytes(bodyA, "prompt_cache_key").String()
	cacheB := gjson.GetBytes(bodyB, "prompt_cache_key").String()
	if cacheA == "" || cacheA == "cache-1" {
		t.Fatalf("auth A prompt_cache_key = %q, want account-scoped replacement", cacheA)
	}
	if cacheB == "" || cacheB == cacheA {
		t.Fatalf("auth B prompt_cache_key = %q, want distinct from auth A %q", cacheB, cacheA)
	}
	if got := httpReqA.Header.Get("Session_id"); got != cacheA {
		t.Fatalf("auth A Session_id = %q, want %q", got, cacheA)
	}
	if got := httpReqB.Header.Get("Session_id"); got != cacheB {
		t.Fatalf("auth B Session_id = %q, want %q", got, cacheB)
	}
}
