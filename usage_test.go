package main

import "testing"

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
