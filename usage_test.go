package main

import (
	"testing"
	"time"
)

func TestCodexPlanWeightMatchesCPAManager(t *testing.T) {
	tests := []struct {
		plan string
		want float64
	}{
		{plan: "plus", want: 1},
		{plan: "pro", want: 20},
		{plan: "prolite", want: 5},
		{plan: "pro-lite", want: 5},
		{plan: "pro_lite", want: 5},
		{plan: "free", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.plan, func(t *testing.T) {
			if got := codexPlanWeight(tt.plan); got != tt.want {
				t.Fatalf("codexPlanWeight(%q) = %v, want %v", tt.plan, got, tt.want)
			}
		})
	}
}

func TestAggregateCodexGroupUsesPlanWeights(t *testing.T) {
	entries := []usageEntry{
		{
			Provider:  "codex",
			Plan:      "plus",
			Weight:    codexPlanWeight("plus"),
			Primary:   usageWindow{UsedPercent: 10, HasData: true},
			Secondary: usageWindow{UsedPercent: 10, HasData: true},
		},
		{
			Provider:  "codex",
			Plan:      "plus",
			Weight:    codexPlanWeight("plus"),
			Primary:   usageWindow{UsedPercent: 20, HasData: true},
			Secondary: usageWindow{UsedPercent: 20, HasData: true},
		},
		{
			Provider:  "codex",
			Plan:      "pro",
			Weight:    codexPlanWeight("pro"),
			Primary:   usageWindow{UsedPercent: 50, HasData: true},
			Secondary: usageWindow{UsedPercent: 40, HasData: true},
		},
	}

	got := aggregateGroup(entries, false)
	if !got.HasData {
		t.Fatal("aggregateGroup HasData = false, want true")
	}
	wantPrimary := (10*1 + 20*1 + 50*20) / 22.0
	wantSecondary := (10*1 + 20*1 + 40*20) / 22.0
	if got.PrimaryPercent != wantPrimary {
		t.Fatalf("PrimaryPercent = %v, want %v", got.PrimaryPercent, wantPrimary)
	}
	if got.SecondaryPercent != wantSecondary {
		t.Fatalf("SecondaryPercent = %v, want %v", got.SecondaryPercent, wantSecondary)
	}
}

func TestAggregateCodexGroupTreatsWeeklyExhaustedAsNoPrimaryQuota(t *testing.T) {
	proReset := time.Date(2026, 6, 23, 16, 59, 0, 0, time.UTC)
	plusReset := time.Date(2026, 6, 23, 19, 54, 0, 0, time.UTC)
	entries := []usageEntry{
		{
			Provider: "codex",
			Plan:     "pro",
			Weight:   codexPlanWeight("pro"),
			// Screenshot shows remaining 61% / 55%, upstream used values are 39% / 45%.
			Primary:   usageWindow{UsedPercent: 39, ResetAt: proReset, HasData: true},
			Secondary: usageWindow{UsedPercent: 45, HasData: true},
		},
		{
			Provider:  "codex",
			Plan:      "plus",
			Weight:    codexPlanWeight("plus"),
			Primary:   usageWindow{UsedPercent: 1, ResetAt: plusReset, HasData: true},
			Secondary: usageWindow{UsedPercent: 100, HasData: true},
		},
		{
			Provider:  "codex",
			Plan:      "plus",
			Weight:    codexPlanWeight("plus"),
			Primary:   usageWindow{UsedPercent: 1, ResetAt: plusReset, HasData: true},
			Secondary: usageWindow{UsedPercent: 100, HasData: true},
		},
	}

	got := aggregateGroup(entries, false)
	wantUsed := (39*20 + 100*1 + 100*1) / 22.0
	if got.PrimaryPercent != wantUsed {
		t.Fatalf("PrimaryPercent = %v, want %v", got.PrimaryPercent, wantUsed)
	}
	if !got.PrimaryReset.Equal(proReset) {
		t.Fatalf("PrimaryReset = %s, want %s", got.PrimaryReset, proReset)
	}
}

func TestResetTextUsesShanghaiTime(t *testing.T) {
	utcReset := time.Date(2026, 6, 30, 2, 0, 0, 0, time.UTC)
	if got := resetText(utcReset); got != "06/30 10:00" {
		t.Fatalf("resetText() = %q, want %q", got, "06/30 10:00")
	}
}

func TestUsageQuotaBlockedByWeeklyLimit(t *testing.T) {
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	block := usageQuotaBlocked(usageEntry{
		Provider:  "codex",
		Primary:   usageWindow{UsedPercent: 10, ResetAt: now.Add(time.Hour), HasData: true},
		Secondary: usageWindow{UsedPercent: 100, ResetAt: reset, HasData: true},
	}, []string{"gpt-5.4"}, now)
	if !block.Blocked {
		t.Fatal("weekly exhausted usage should block scheduled warmup")
	}
	if block.Reason != "周限额已满" {
		t.Fatalf("Reason = %q, want 周限额已满", block.Reason)
	}
	if !block.Until.Equal(reset) {
		t.Fatalf("Until = %s, want %s", block.Until, reset)
	}
}

func TestUsageQuotaBlockedByPrimaryLimit(t *testing.T) {
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	reset := now.Add(30 * time.Minute)
	block := usageQuotaBlocked(usageEntry{
		Provider:  "claude",
		Primary:   usageWindow{UsedPercent: 100, ResetAt: reset, HasData: true},
		Secondary: usageWindow{UsedPercent: 20, ResetAt: now.Add(24 * time.Hour), HasData: true},
	}, []string{"claude-sonnet-4-6"}, now)
	if !block.Blocked {
		t.Fatal("5-hour exhausted usage should block scheduled warmup")
	}
	if block.Reason != "5小时额度已满" {
		t.Fatalf("Reason = %q, want 5小时额度已满", block.Reason)
	}
}

func TestUsageQuotaBlockedBySonnetWeeklyLimitOnlyForSonnetModel(t *testing.T) {
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	entry := usageEntry{
		Provider:        "claude",
		Primary:         usageWindow{UsedPercent: 10, ResetAt: now.Add(time.Hour), HasData: true},
		Secondary:       usageWindow{UsedPercent: 20, ResetAt: now.Add(24 * time.Hour), HasData: true},
		SonnetSecondary: usageWindow{UsedPercent: 100, ResetAt: now.Add(48 * time.Hour), HasData: true},
		HasSonnet:       true,
	}
	if block := usageQuotaBlocked(entry, []string{"claude-opus-4-6"}, now); block.Blocked {
		t.Fatal("sonnet quota should not block non-sonnet warmup model")
	}
	if block := usageQuotaBlocked(entry, []string{"claude-sonnet-4-6"}, now); !block.Blocked || block.Reason != "Sonnet 周限额已满" {
		t.Fatalf("sonnet model block = %#v, want Sonnet weekly block", block)
	}
}
