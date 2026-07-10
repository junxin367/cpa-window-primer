package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestParseModelListAcceptsCommonSeparators(t *testing.T) {
	got := parseModelList("gpt-5.4， claude-sonnet-4-6、gpt-5.3-codex-spark; claude-opus-4-6\n gpt-5.4")
	want := []string{"gpt-5.4", "claude-sonnet-4-6", "gpt-5.3-codex-spark", "claude-opus-4-6"}
	if len(got) != len(want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models = %#v, want %#v", got, want)
		}
	}
}

func TestChooseFallbackModelPrefersProviderMatch(t *testing.T) {
	models := []availableModel{
		{ID: "gpt-5.3-codex-spark", Owner: "openai"},
		{ID: "claude-sonnet-4-6", Owner: "anthropic"},
		{ID: "gpt-5.4", Owner: "openai"},
	}
	if got := chooseFallbackModel("claude", models); got != "claude-sonnet-4-6" {
		t.Fatalf("claude fallback = %q", got)
	}
	if got := chooseFallbackModel("codex", models); got != "gpt-5.4" {
		t.Fatalf("codex fallback = %q", got)
	}
}

func TestWarmupModelUnavailableAllowsCallbackErrors(t *testing.T) {
	if !isWarmupModelUnavailable(0, nil, errString("model not found")) {
		t.Fatal("callback model-not-found error should allow fallback")
	}
	if !isWarmupModelUnavailable(http.StatusTooManyRequests, []byte(`{"error":{"message":"selected model is at capacity"}}`), nil) {
		t.Fatal("model capacity should allow fallback to the next model")
	}
}

func TestExecuteWarmupRejectsSuccessWhenTargetAuthCounterUnchanged(t *testing.T) {
	app := newTestWarmupApp(t)
	withHostRPC(t, func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostAuthList:
			return rawJSON(t, authListResponse{Files: []pluginapi.HostAuthFileEntry{warmupTestAuth(0)}}), nil
		case pluginabi.MethodHostModelExecute:
			req := payload.(hostModelExecutionRequest)
			if got := req.Headers.Get(primerHeader); got != "auth-b" {
				t.Fatalf("warmup auth header = %q, want auth-b", got)
			}
			return rawJSON(t, pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}), nil
		case pluginabi.MethodHostAuthGetRuntime:
			return rawJSON(t, pluginapi.HostAuthGetRuntimeResponse{Auth: warmupTestAuth(0)}), nil
		case pluginabi.MethodHostHTTPDo:
			return nil, fmt.Errorf("models endpoint unavailable")
		default:
			return nil, fmt.Errorf("unexpected host method %s", method)
		}
	})

	record := app.executeWarmup("auth-b", "manual-test", app.cfg, "", true)
	if record.Success {
		t.Fatalf("record.Success = true, want false")
	}
	if !strings.Contains(record.Error, "target_auth_not_used") {
		t.Fatalf("record.Error = %q, want target_auth_not_used", record.Error)
	}
	if got := app.state.Auths["auth-b"].LastAttempt; got == nil || got.Success {
		t.Fatalf("LastAttempt = %#v, want failed attempt", got)
	}
}

func TestExecuteWarmupSucceedsWhenTargetAuthCounterIncreases(t *testing.T) {
	app := newTestWarmupApp(t)
	successCount := int64(0)
	withHostRPC(t, func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostAuthList:
			return rawJSON(t, authListResponse{Files: []pluginapi.HostAuthFileEntry{warmupTestAuth(0)}}), nil
		case pluginabi.MethodHostModelExecute:
			successCount = 1
			return rawJSON(t, pluginapi.HostModelExecutionResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true}`)}), nil
		case pluginabi.MethodHostAuthGetRuntime:
			return rawJSON(t, pluginapi.HostAuthGetRuntimeResponse{Auth: warmupTestAuth(successCount)}), nil
		case pluginabi.MethodHostHTTPDo:
			return nil, fmt.Errorf("models endpoint unavailable")
		default:
			return nil, fmt.Errorf("unexpected host method %s", method)
		}
	})

	record := app.executeWarmup("auth-b", "manual-test", app.cfg, "", true)
	if !record.Success {
		t.Fatalf("record.Success = false, error = %q", record.Error)
	}
	if got := app.state.Auths["auth-b"].LastAttempt; got == nil || !got.Success {
		t.Fatalf("LastAttempt = %#v, want successful attempt", got)
	}
}

func TestExecuteWarmupUsesDirectCodexFallbackWhenTargetIsNotCandidate(t *testing.T) {
	app := newTestWarmupApp(t)
	streamOpenCalls := 0
	streamReadCalls := 0
	streamCloseCalls := 0
	withHostRPC(t, func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostAuthList:
			return rawJSON(t, authListResponse{Files: []pluginapi.HostAuthFileEntry{warmupTestAuth(0)}}), nil
		case pluginabi.MethodHostModelExecute:
			return nil, fmt.Errorf("target_auth_not_candidate: auth-b")
		case pluginabi.MethodHostAuthGet:
			return rawJSON(t, pluginapi.HostAuthGetResponse{
				AuthIndex: "idx-b",
				JSON:      json.RawMessage(`{"token_data":{"access_token":"token-b","account_id":"account-b"},"base_url":"http://untrusted.invalid"}`),
			}), nil
		case pluginabi.MethodHostAuthGetRuntime:
			t.Fatal("direct Codex warmup must not rely on the host success counter")
			return nil, fmt.Errorf("unexpected host runtime verification")
		case pluginabi.MethodHostHTTPDoStream:
			req := payload.(rpcHTTPRequest)
			if req.HostCallbackID != "callback-b" {
				t.Fatalf("host callback id = %q, want callback-b", req.HostCallbackID)
			}
			if req.Method != http.MethodPost {
				return nil, fmt.Errorf("models endpoint unavailable")
			}
			if req.URL != codexWarmupURL {
				t.Fatalf("direct URL = %q, want %q", req.URL, codexWarmupURL)
			}
			if got := req.Headers.Get("Authorization"); got != "Bearer token-b" {
				t.Fatalf("Authorization = %q, want target auth token", got)
			}
			if got := req.Headers.Get("Chatgpt-Account-Id"); got != "account-b" {
				t.Fatalf("Chatgpt-Account-Id = %q, want account-b", got)
			}
			if got := req.Headers.Get("Session_id"); got == "" {
				t.Fatal("Session_id is required for the Codex direct request")
			}
			var body codexWarmupRequest
			if err := json.Unmarshal(req.Body, &body); err != nil {
				t.Fatalf("decode direct body: %v", err)
			}
			if body.Model != "gpt-5.4" || !body.Stream || body.Store {
				t.Fatalf("direct body settings = %#v", body)
			}
			var rawBody map[string]any
			if err := json.Unmarshal(req.Body, &rawBody); err != nil {
				t.Fatalf("decode raw direct body: %v", err)
			}
			if _, ok := rawBody["parallel_tool_calls"]; ok {
				t.Fatalf("parallel_tool_calls must be omitted without tools: %s", req.Body)
			}
			if body.Reasoning.Effort != "medium" || body.Reasoning.Summary != "auto" ||
				len(body.Include) != 1 || body.Include[0] != "reasoning.encrypted_content" {
				t.Fatalf("direct body reasoning = %#v", body)
			}
			if len(body.Input) != 1 || body.Input[0].Type != "message" ||
				len(body.Input[0].Content) != 1 || body.Input[0].Content[0].Text != app.cfg.Prompt {
				t.Fatalf("direct body = %#v", body)
			}
			streamOpenCalls++
			return rawJSON(t, rpcHTTPStreamResponse{
				StatusCode: http.StatusOK,
				StreamID:   "stream-b",
			}), nil
		case pluginabi.MethodHostHTTPStreamRead:
			req := payload.(rpcHTTPStreamReadRequest)
			if req.StreamID != "stream-b" {
				t.Fatalf("stream read id = %q, want stream-b", req.StreamID)
			}
			streamReadCalls++
			return rawJSON(t, rpcHTTPStreamReadResponse{
				Payload: []byte("data: {\"type\":\"response.completed\"}\n\n"),
			}), nil
		case pluginabi.MethodHostHTTPStreamClose:
			req := payload.(rpcHTTPStreamCloseRequest)
			if req.StreamID != "stream-b" {
				t.Fatalf("stream close id = %q, want stream-b", req.StreamID)
			}
			streamCloseCalls++
			return rawJSON(t, struct{}{}), nil
		default:
			return nil, fmt.Errorf("unexpected host method %s", method)
		}
	})

	record := app.executeWarmup("auth-b", "manual-test", app.cfg, "callback-b", true)
	if !record.Success {
		t.Fatalf("record.Success = false, error = %q", record.Error)
	}
	if streamOpenCalls != 1 || streamReadCalls != 1 || streamCloseCalls != 1 {
		t.Fatalf("stream calls = open:%d read:%d close:%d, want 1/1/1", streamOpenCalls, streamReadCalls, streamCloseCalls)
	}
	if got := app.state.Auths["auth-b"].LastAttempt; got == nil || !got.Success {
		t.Fatalf("LastAttempt = %#v, want target auth success", got)
	}
}

func TestValidateCodexWarmupCompletionRequiresCompletedEvent(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "completed",
			body: "data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.completed\"}\n\n",
		},
		{
			name:    "done without completed",
			body:    "data: {\"type\":\"response.created\"}\n\ndata: [DONE]\n\n",
			wantErr: true,
		},
		{
			name:    "plain completed status",
			body:    "{\"status\":\"completed\"}",
			wantErr: true,
		},
		{
			name:    "failed",
			body:    "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"failed\"}}}\n\n",
			wantErr: true,
		},
		{
			name:    "empty",
			body:    "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCodexWarmupCompletion([]byte(tt.body))
			if tt.wantErr && err == nil {
				t.Fatal("validateCodexWarmupCompletion() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateCodexWarmupCompletion() error = %v", err)
			}
		})
	}
}

func TestExecuteCodexWarmupHTTPUsesTargetAuthProxy(t *testing.T) {
	const targetBaseURL = "http://codex-upstream.invalid/backend-api/codex"
	proxyCalls := 0
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		proxyCalls++
		if got := req.URL.String(); got != targetBaseURL+"/responses" {
			t.Fatalf("proxy request URL = %q, want %q", got, targetBaseURL+"/responses")
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token-proxy" {
			t.Fatalf("proxy Authorization = %q, want target token", got)
		}
		if got := req.Header.Get("Chatgpt-Account-Id"); got != "account-proxy" {
			t.Fatalf("proxy Chatgpt-Account-Id = %q, want target account", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer proxyServer.Close()

	withHostRPC(t, func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostAuthGet:
			return rawJSON(t, pluginapi.HostAuthGetResponse{
				AuthIndex: "idx-proxy",
				JSON: json.RawMessage(fmt.Sprintf(
					`{"access_token":"token-proxy","account_id":"account-proxy","proxy_url":%q}`,
					proxyServer.URL,
				)),
			}), nil
		default:
			return nil, fmt.Errorf("unexpected host method %s", method)
		}
	})

	credentials, err := codexCredentialsForAuth("idx-proxy")
	if err != nil {
		t.Fatalf("codexCredentialsForAuth() error = %v", err)
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+credentials.AccessToken)
	headers.Set("Chatgpt-Account-Id", credentials.AccountID)
	resp, err := executeCodexWarmupHTTP(credentials, targetBaseURL+"/responses", headers, []byte(`{"model":"gpt-5.4"}`), "")
	if err != nil {
		t.Fatalf("executeCodexWarmupHTTP() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if proxyCalls != 1 {
		t.Fatalf("proxy calls = %d, want 1", proxyCalls)
	}
}

func TestExecuteCodexWarmupHTTPDoesNotFollowRedirect(t *testing.T) {
	const targetURL = "http://codex-upstream.invalid/backend-api/codex/responses"
	proxyCalls := 0
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		proxyCalls++
		if proxyCalls > 1 {
			t.Fatalf("redirect was followed to %q", req.URL.String())
		}
		w.Header().Set("Location", "http://redirect-target.invalid/steal")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer proxyServer.Close()

	resp, err := executeCodexWarmupHTTP(
		codexAuthCredentials{ProxyURL: proxyServer.URL},
		targetURL,
		http.Header{"Authorization": {"Bearer token"}},
		[]byte(`{"model":"gpt-5.4"}`),
		"",
	)
	if err != nil {
		t.Fatalf("executeCodexWarmupHTTP() error = %v", err)
	}
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusTemporaryRedirect)
	}
	if proxyCalls != 1 {
		t.Fatalf("proxy calls = %d, want 1", proxyCalls)
	}
}

func TestExecuteCodexWarmupHostHTTPTimesOutAndClosesStream(t *testing.T) {
	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	readFinished := make(chan struct{})
	withHostRPC(t, func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostHTTPDoStream:
			return rawJSON(t, rpcHTTPStreamResponse{
				StatusCode: http.StatusOK,
				StreamID:   "stream-timeout",
			}), nil
		case pluginabi.MethodHostHTTPStreamRead:
			close(readStarted)
			<-releaseRead
			close(readFinished)
			return rawJSON(t, rpcHTTPStreamReadResponse{Done: true}), nil
		case pluginabi.MethodHostHTTPStreamClose:
			close(releaseRead)
			return rawJSON(t, struct{}{}), nil
		default:
			return nil, fmt.Errorf("unexpected host method %s", method)
		}
	})

	startedAt := time.Now()
	_, err := executeCodexWarmupHostHTTPWithTimeout(
		codexWarmupURL,
		http.Header{"Authorization": {"Bearer token"}},
		[]byte(`{"model":"gpt-5.4"}`),
		"",
		100*time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "codex_warmup_timeout") {
		t.Fatalf("executeCodexWarmupHostHTTPWithTimeout() error = %v, want timeout", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("timeout returned after %s, want under 1s", elapsed)
	}
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("host stream read was not started")
	}
	select {
	case <-readFinished:
	case <-time.After(time.Second):
		t.Fatal("host stream read did not finish after stream close")
	}
}

func TestResolveWarmupAuthTargetRejectsAmbiguousName(t *testing.T) {
	first := warmupTestAuth(0)
	first.ID = "auth-a"
	first.AuthIndex = "idx-a"
	first.Name = "duplicate.json"
	second := warmupTestAuth(0)
	second.ID = "auth-b"
	second.AuthIndex = "idx-b"
	second.Name = "duplicate.json"
	withHostRPC(t, func(method string, payload any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostAuthList {
			return nil, fmt.Errorf("unexpected host method %s", method)
		}
		return rawJSON(t, authListResponse{Files: []pluginapi.HostAuthFileEntry{first, second}}), nil
	})

	_, err := resolveWarmupAuthTarget("duplicate.json")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("resolveWarmupAuthTarget() error = %v, want ambiguous name", err)
	}
}

func TestCodexCredentialsUseTopLevelProxyOnly(t *testing.T) {
	withHostRPC(t, func(method string, payload any) (json.RawMessage, error) {
		if method != pluginabi.MethodHostAuthGet {
			return nil, fmt.Errorf("unexpected host method %s", method)
		}
		return rawJSON(t, pluginapi.HostAuthGetResponse{
			AuthIndex: "idx-b",
			JSON:      json.RawMessage(`{"access_token":"token-b","metadata":{"proxy_url":"http://nested.invalid"}}`),
		}), nil
	})

	credentials, err := codexCredentialsForAuth("idx-b")
	if err != nil {
		t.Fatalf("codexCredentialsForAuth() error = %v", err)
	}
	if credentials.ProxyURL != "" {
		t.Fatalf("ProxyURL = %q, want top-level proxy only", credentials.ProxyURL)
	}
}

func TestWarmupQuotaBlockedDistinguishesUsageLimitFromTransientRateLimit(t *testing.T) {
	if !isWarmupQuotaBlocked("codex", http.StatusTooManyRequests, nil, []byte(`{"error":{"type":"usage_limit_reached"}}`), nil) {
		t.Fatal("usage_limit_reached should block quota")
	}
	if isWarmupQuotaBlocked("codex", http.StatusTooManyRequests, nil, []byte(`{"error":{"type":"rate_limit_error"}}`), nil) {
		t.Fatal("transient rate_limit_error must not block quota")
	}
	if isWarmupQuotaBlocked("codex", http.StatusTooManyRequests, nil, []byte(`{"error":{"message":"selected model is at capacity"}}`), nil) {
		t.Fatal("model capacity must not block auth quota")
	}
	if !isWarmupQuotaBlocked("claude", 0, nil, nil, errString(`rate_limit_error: This request would exceed your account's rate limit`)) {
		t.Fatal("Claude rate_limit_error should preserve local quota blocking")
	}
	headers := http.Header{}
	headers.Set("Retry-After", "60")
	if !isWarmupQuotaBlocked("claude", http.StatusServiceUnavailable, headers, nil, nil) {
		t.Fatal("Claude 503 with Retry-After should preserve local quota blocking")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func newTestWarmupApp(t *testing.T) *app {
	t.Helper()
	cfg, err := normalizeConfig(defaultPluginConfig())
	if err != nil {
		t.Fatalf("normalizeConfig returned error: %v", err)
	}
	cfg.StatePath = filepath.Join(t.TempDir(), "state.json")
	cfg.Model = "gpt-5.4"
	return &app{cfg: cfg, state: newPluginState(), pending: map[string]uint64{}}
}

func withHostRPC(t *testing.T, fn func(string, any) (json.RawMessage, error)) {
	t.Helper()
	previous := callHostRPC
	callHostRPC = fn
	t.Cleanup(func() {
		callHostRPC = previous
	})
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test value: %v", err)
	}
	return raw
}

func warmupTestAuth(success int64) pluginapi.HostAuthFileEntry {
	return pluginapi.HostAuthFileEntry{
		ID:        "auth-b",
		AuthIndex: "idx-b",
		Name:      "codex-oauth-b.json",
		Provider:  "codex",
		Type:      "codex",
		Success:   success,
	}
}
