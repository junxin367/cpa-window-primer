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

func TestAggregateGroupUsesEarliestResetTime(t *testing.T) {
	primaryEarly := time.Date(2026, 6, 25, 16, 28, 0, 0, time.Local)
	primaryMiddle := time.Date(2026, 6, 25, 19, 43, 0, 0, time.Local)
	primaryLate := time.Date(2026, 6, 25, 19, 45, 0, 0, time.Local)
	secondaryEarly := time.Date(2026, 7, 2, 9, 35, 0, 0, time.Local)
	secondaryMiddle := time.Date(2026, 7, 2, 9, 37, 0, 0, time.Local)
	secondaryLate := time.Date(2026, 7, 2, 9, 38, 0, 0, time.Local)

	entries := []usageEntry{
		{
			Provider:  "codex",
			Plan:      "pro",
			Weight:    codexPlanWeight("pro"),
			Primary:   usageWindow{UsedPercent: 95, ResetAt: primaryEarly, HasData: true},
			Secondary: usageWindow{UsedPercent: 22, ResetAt: secondaryLate, HasData: true},
		},
		{
			Provider:  "codex",
			Plan:      "plus",
			Weight:    codexPlanWeight("plus"),
			Primary:   usageWindow{UsedPercent: 47, ResetAt: primaryMiddle, HasData: true},
			Secondary: usageWindow{UsedPercent: 23, ResetAt: secondaryEarly, HasData: true},
		},
		{
			Provider:  "codex",
			Plan:      "plus",
			Weight:    codexPlanWeight("plus"),
			Primary:   usageWindow{UsedPercent: 6, ResetAt: primaryLate, HasData: true},
			Secondary: usageWindow{UsedPercent: 17, ResetAt: secondaryMiddle, HasData: true},
		},
	}

	got := aggregateGroup(entries, false)
	if !got.PrimaryReset.Equal(primaryEarly) {
		t.Fatalf("PrimaryReset = %s, want %s", got.PrimaryReset, primaryEarly)
	}
	if !got.SecondaryReset.Equal(secondaryEarly) {
		t.Fatalf("SecondaryReset = %s, want %s", got.SecondaryReset, secondaryEarly)
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

func TestUsagePushWorkday(t *testing.T) {
	tests := []struct {
		name string
		day  time.Time
		want bool
	}{
		{name: "monday", day: time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC), want: true},
		{name: "friday", day: time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC), want: true},
		{name: "saturday", day: time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC), want: false},
		{name: "sunday", day: time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUsagePushWorkday(tt.day); got != tt.want {
				t.Fatalf("isUsagePushWorkday(%s) = %v, want %v", tt.day, got, tt.want)
			}
		})
	}
}

func TestReconcileQuotaBlockClearsRecoveredUsage(t *testing.T) {
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	app := newTestUsageApp(t)
	app.state.recordQuotaBlocked("auth-a", now.Add(-time.Minute), now.Add(24*time.Hour), "周限额已满")

	usage := usageEntry{
		AuthID:    "auth-a",
		Provider:  "codex",
		Primary:   usageWindow{UsedPercent: 10, ResetAt: now.Add(time.Hour), HasData: true},
		Secondary: usageWindow{UsedPercent: 20, ResetAt: now.Add(24 * time.Hour), HasData: true},
	}
	if _, blocked := app.reconcileQuotaBlockFromUsage("auth-a", "window-a", app.cfg, []string{"gpt-5.4"}, usage, now, true); blocked {
		t.Fatal("recovered usage should not remain blocked")
	}
	if _, _, blocked := app.state.quotaBlockInfo("auth-a", now); blocked {
		t.Fatal("recovered usage should clear local quota block")
	}
}

func TestReconcileQuotaBlockKeepsBlockOnUsageError(t *testing.T) {
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	until := now.Add(24 * time.Hour)
	app := newTestUsageApp(t)
	app.state.recordQuotaBlocked("auth-a", now.Add(-time.Minute), until, "周限额已满")

	usage := usageEntry{AuthID: "auth-a", Provider: "codex", Err: "host_call_failed"}
	record, blocked := app.reconcileQuotaBlockFromUsage("auth-a", "window-a", app.cfg, []string{"gpt-5.4"}, usage, now, true)
	if !blocked {
		t.Fatal("usage error should preserve active local quota block")
	}
	if !app.state.Auths["auth-a"].QuotaBlockedUntil.Equal(until) {
		t.Fatalf("QuotaBlockedUntil = %v, want %v", app.state.Auths["auth-a"].QuotaBlockedUntil, until)
	}
	if record.Error == "" || app.state.Auths["auth-a"].LastAttempt == nil {
		t.Fatal("usage error while locally blocked should record a skipped attempt")
	}
}

func TestReconcileQuotaBlockUpdatesStillExhaustedUsage(t *testing.T) {
	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	app := newTestUsageApp(t)

	usage := usageEntry{
		AuthID:    "auth-a",
		Provider:  "codex",
		Primary:   usageWindow{UsedPercent: 10, ResetAt: now.Add(time.Hour), HasData: true},
		Secondary: usageWindow{UsedPercent: 100, ResetAt: reset, HasData: true},
	}
	record, blocked := app.reconcileQuotaBlockFromUsage("auth-a", "window-a", app.cfg, []string{"gpt-5.4"}, usage, now, true)
	if !blocked {
		t.Fatal("exhausted usage should block warmup")
	}
	until, reason, active := app.state.quotaBlockInfo("auth-a", now)
	if !active || !until.Equal(reset) || reason != "周限额已满" {
		t.Fatalf("quota block = %s/%q/%v, want %s/周限额已满/true", until, reason, active, reset)
	}
	if record.Error == "" || app.state.Auths["auth-a"].LastAttempt == nil {
		t.Fatal("exhausted usage should record skipped attempt")
	}
}

func newTestUsageApp(t *testing.T) *app {
	t.Helper()
	cfg, err := normalizeConfig(defaultPluginConfig())
	if err != nil {
		t.Fatalf("normalizeConfig returned error: %v", err)
	}
	cfg.StatePath = t.TempDir() + "/state.json"
	return &app{cfg: cfg, state: newPluginState(), pending: map[string]uint64{}}
}
