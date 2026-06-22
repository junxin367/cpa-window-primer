package main

import (
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
			want: false,
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
