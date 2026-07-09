package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

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
