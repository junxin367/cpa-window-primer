package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestIsSupportedOAuthAuth(t *testing.T) {
	tests := []struct {
		name string
		auth pluginapi.HostAuthFileEntry
		want bool
	}{
		{
			name: "codex provider",
			auth: pluginapi.HostAuthFileEntry{Provider: "codex", ID: "codex-a"},
			want: true,
		},
		{
			name: "disabled codex",
			auth: pluginapi.HostAuthFileEntry{Provider: "codex", ID: "codex-a", Disabled: true},
			want: false,
		},
		{
			name: "openai oauth name",
			auth: pluginapi.HostAuthFileEntry{Provider: "openai", Name: "openai-oauth.json"},
			want: true,
		},
		{
			name: "openai api key name",
			auth: pluginapi.HostAuthFileEntry{Provider: "openai", Name: "openai-api-key.json"},
			want: true,
		},
		{
			name: "kimi provider",
			auth: pluginapi.HostAuthFileEntry{Provider: "kimi", ID: "kimi-1", Name: "kimi.json"},
			want: true,
		},
		{
			name: "devin provider",
			auth: pluginapi.HostAuthFileEntry{Provider: "devin", ID: "devin-1", Name: "devin.json"},
			want: true,
		},
		{
			name: "claude provider",
			auth: pluginapi.HostAuthFileEntry{Provider: "claude", Name: "claude.json"},
			want: true,
		},
		{
			name: "anthropic provider",
			auth: pluginapi.HostAuthFileEntry{Provider: "anthropic", Name: "anthropic.json"},
			want: true,
		},
		{
			name: "unavailable quota",
			auth: pluginapi.HostAuthFileEntry{Provider: "claude", Name: "claude.json", Unavailable: true, StatusMessage: "quota exhausted"},
			want: false,
		},
		{
			name: "next retry in future",
			auth: pluginapi.HostAuthFileEntry{Provider: "codex", Name: "codex.json", NextRetryAfter: time.Now().Add(time.Hour)},
			want: false,
		},
		{
			name: "rate limit status message",
			auth: pluginapi.HostAuthFileEntry{Provider: "codex", Name: "codex.json", StatusMessage: "rate limit exceeded"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSupportedOAuthAuth(tt.auth); got != tt.want {
				t.Fatalf("isSupportedOAuthAuth() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsSchedulableOAuthAuthIncludesQuotaBlocked(t *testing.T) {
	auth := pluginapi.HostAuthFileEntry{
		Provider:      "claude",
		ID:            "auth-a",
		Unavailable:   true,
		StatusMessage: "quota exhausted",
	}
	if isSupportedOAuthAuth(auth) {
		t.Fatal("quota blocked auth should not be directly supported")
	}
	if !isSchedulableOAuthAuth(auth) {
		t.Fatal("quota blocked auth should remain schedulable for usage recheck")
	}
	auth.Disabled = true
	if isSchedulableOAuthAuth(auth) {
		t.Fatal("disabled auth should not be schedulable")
	}
}

func TestAuthEntrySelectedMatchesIDIndexAndName(t *testing.T) {
	auth := pluginapi.HostAuthFileEntry{
		ID:        "auth-id",
		AuthIndex: "auth-index",
		Name:      "auth-file.json",
	}
	for _, selected := range []string{"auth-id", "auth-index", "auth-file.json"} {
		if !authEntrySelected(map[string]bool{selected: true}, auth) {
			t.Fatalf("authEntrySelected(%q) = false, want true", selected)
		}
	}
	if authEntrySelected(map[string]bool{"missing": true}, auth) {
		t.Fatal("authEntrySelected accepted unrelated key")
	}
}

func TestFormatAuthType(t *testing.T) {
	tests := []struct {
		name string
		auth pluginapi.HostAuthFileEntry
		want string
	}{
		{name: "codex provider", auth: pluginapi.HostAuthFileEntry{Provider: "codex"}, want: "codex"},
		{name: "devin provider", auth: pluginapi.HostAuthFileEntry{Provider: "devin"}, want: "devin"},
		{name: "kimi provider", auth: pluginapi.HostAuthFileEntry{Provider: "kimi"}, want: "kimi"},
		{name: "kimi-ai type", auth: pluginapi.HostAuthFileEntry{Type: "kimi-ai"}, want: "kimi"},
		{name: "claude provider", auth: pluginapi.HostAuthFileEntry{Provider: "claude"}, want: "claude"},
		{name: "anthropic provider", auth: pluginapi.HostAuthFileEntry{Provider: "anthropic"}, want: "claude"},
		{name: "openai oauth", auth: pluginapi.HostAuthFileEntry{Provider: "openai", Name: "openai-oauth.json"}, want: "codex"},
		{name: "openai api key", auth: pluginapi.HostAuthFileEntry{Provider: "openai", Name: "openai-api-key.json"}, want: "openai"},
		{name: "gemini provider", auth: pluginapi.HostAuthFileEntry{Provider: "gemini"}, want: "gemini"},
		{name: "deepseek provider", auth: pluginapi.HostAuthFileEntry{Provider: "deepseek"}, want: "deepseek"},
		{name: "fallback by type", auth: pluginapi.HostAuthFileEntry{Type: "custom-llm"}, want: "custom-llm"},
		{name: "fallback by name prefix", auth: pluginapi.HostAuthFileEntry{Name: "custom_auth.json"}, want: "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatAuthType(tt.auth); got != tt.want {
				t.Fatalf("formatAuthType() = %q, want %q", got, tt.want)
			}
		})
	}
}
func TestWarmupModelAutoRecognition(t *testing.T) {
	withHostRPC(t, func(string, any) (json.RawMessage, error) { return nil, fmt.Errorf("no models") })
	cfg := runtimeConfig{
		pluginConfig: pluginConfig{
			Model: "gpt-6-luna, claude-sonnet-4-6",
		},
	}

	// 测试 kimi 提供商：即使配置只有 gpt 和 claude，也能自动识别出 kimi 模型
	kimiCandidates := fallbackModelsForProvider("kimi")
	if len(kimiCandidates) == 0 || kimiCandidates[0] != "kimi-k2.5" {
		t.Fatalf("fallbackModelsForProvider(kimi) = %v, want kimi-k2.5 as first", kimiCandidates)
	}
	for _, m := range kimiCandidates {
		if !strings.Contains(m, "kimi") {
			t.Fatalf("kimi candidates contain non-kimi model: %s", m)
		}
	}

	// 测试 antigravity 提供商：自动识别 gemini 模型
	agCandidates := fallbackModelsForProvider("antigravity")
	if len(agCandidates) == 0 || !strings.Contains(agCandidates[0], "gemini") {
		t.Fatalf("fallbackModelsForProvider(antigravity) = %v, want gemini model", agCandidates)
	}

	// 测试 modelMatchesProvider
	if !modelMatchesProvider("kimi-k3.1-pro", "kimi") {
		t.Fatal("modelMatchesProvider(kimi-k3.1-pro, kimi) should be true")
	}
	if modelMatchesProvider("gpt-6-luna", "kimi") {
		t.Fatal("modelMatchesProvider(gpt-6-luna, kimi) should be false")
	}
	if !modelMatchesProvider("gemini-2.5-flash", "antigravity") {
		t.Fatal("modelMatchesProvider(gemini-2.5-flash, antigravity) should be true")
	}

	_ = cfg
}
