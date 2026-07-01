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
