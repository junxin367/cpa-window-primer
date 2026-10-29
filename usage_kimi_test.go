package main

import (
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestParseKimiUsageBody(t *testing.T) {
	rawJSON := `{
		"limits": [
			{
				"duration": 5,
				"timeUnit": "HOURS",
				"detail": {
					"limit": 100,
					"used": 33,
					"remaining": 67,
					"reset_time": 1730198880
				}
			},
			{
				"duration": 7,
				"timeUnit": "DAYS",
				"detail": {
					"limit": 1000,
					"used": 200,
					"reset_time": 1730600000
				}
			}
		]
	}`

	var entry usageEntry
	if err := parseKimiUsageBody([]byte(rawJSON), &entry); err != nil {
		t.Fatalf("parseKimiUsageBody failed: %v", err)
	}

	if !entry.Primary.HasData {
		t.Fatal("expected primary 5h window to have data")
	}
	if entry.Primary.UsedPercent != 33 {
		t.Fatalf("expected primary used 33%%, got %v", entry.Primary.UsedPercent)
	}
	if entry.Primary.ResetAt.Unix() != 1730198880 {
		t.Fatalf("expected reset 1730198880, got %v", entry.Primary.ResetAt.Unix())
	}

	if !entry.Secondary.HasData {
		t.Fatal("expected secondary weekly window to have data")
	}
	if entry.Secondary.UsedPercent != 20 {
		t.Fatalf("expected secondary used 20%%, got %v", entry.Secondary.UsedPercent)
	}
}

func TestCollectUsageTargetsIncludesKimi(t *testing.T) {
	auths := []pluginapi.HostAuthFileEntry{
		{ID: "auth-codex-1", Provider: "codex", Name: "codex.json", AuthIndex: "idx-codex"},
		{ID: "auth-kimi-1", Provider: "kimi", Name: "kimi.json", AuthIndex: "idx-kimi"},
		{ID: "auth-devin-1", Provider: "devin", Name: "devin.json", AuthIndex: "idx-devin"},
		{ID: "auth-claude-1", Provider: "claude", Name: "claude.json", AuthIndex: "idx-claude"},
	}

	selected := []string{"auth-codex-1", "auth-kimi-1"}
	targets := collectUsageTargets(selected, auths)

	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}

	foundKimi := false
	for _, target := range targets {
		if target.AuthID == "auth-kimi-1" {
			foundKimi = true
			if target.Provider != "kimi" {
				t.Fatalf("expected target provider to be 'kimi', got '%s'", target.Provider)
			}
		}
	}
	if !foundKimi {
		t.Fatal("expected kimi target to be collected")
	}
}

func TestBuildUsageMessageDynamicGrouping(t *testing.T) {
	entries := []usageEntry{
		{
			AuthID:   "codex-1",
			Provider: "codex",
			Weight:   1,
			Primary:  usageWindow{UsedPercent: 40, HasData: true},
		},
		{
			AuthID:   "kimi-1",
			Provider: "kimi",
			Weight:   1,
			Primary:  usageWindow{UsedPercent: 33, HasData: true, ResetAt: time.Unix(1730198880, 0)},
		},
	}

	msg := buildUsageMessage(entries)
	if !strings.Contains(msg, "**GPT**") {
		t.Fatalf("expected **GPT** in message, got:\n%s", msg)
	}
	if !strings.Contains(msg, "**KIMI**") {
		t.Fatalf("expected **KIMI** in message, got:\n%s", msg)
	}
	if !strings.Contains(msg, "5小时限额") {
		t.Fatalf("expected 5小时限额 in message, got:\n%s", msg)
	}
}
