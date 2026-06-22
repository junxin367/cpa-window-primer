package main

import "testing"

func TestNormalizeConfigDefaults(t *testing.T) {
	cfg, err := normalizeConfig(pluginConfig{})
	if err != nil {
		t.Fatalf("normalizeConfig returned error: %v", err)
	}
	if cfg.Model != defaultModel || cfg.Prompt != defaultPrompt {
		t.Fatalf("defaults = model %q prompt %q", cfg.Model, cfg.Prompt)
	}
	if got := cfg.Times; len(got) != 3 || got[0] != "07:00" || got[1] != "12:00" || got[2] != "17:00" {
		t.Fatalf("Times = %#v, want defaults", got)
	}
}

func TestNormalizeConfigSortsAndDeduplicatesTimes(t *testing.T) {
	cfg, err := normalizeConfig(pluginConfig{Times: []string{"17:00", "07:00", "12:00", "07:00"}})
	if err != nil {
		t.Fatalf("normalizeConfig returned error: %v", err)
	}
	if got := cfg.Times; len(got) != 3 || got[0] != "07:00" || got[1] != "12:00" || got[2] != "17:00" {
		t.Fatalf("Times = %#v, want sorted unique", got)
	}
}

func TestNormalizeConfigRejectsBadTime(t *testing.T) {
	if _, err := normalizeConfig(pluginConfig{Times: []string{"25:00"}}); err == nil {
		t.Fatal("normalizeConfig accepted invalid time")
	}
}

func TestMergeConfigPatchPreservesOmittedFields(t *testing.T) {
	current := defaultPluginConfig()
	current.AuthIDs = []string{"auth-a"}
	next, err := mergeConfigPatch(current, []byte(`{"times":["12:00"]}`))
	if err != nil {
		t.Fatalf("mergeConfigPatch returned error: %v", err)
	}
	if !next.Enabled {
		t.Fatal("Enabled was cleared by omitted field")
	}
	if got := next.AuthIDs; len(got) != 1 || got[0] != "auth-a" {
		t.Fatalf("AuthIDs = %#v, want preserved auth-a", got)
	}
	if got := next.Times; len(got) != 1 || got[0] != "12:00" {
		t.Fatalf("Times = %#v, want patched 12:00", got)
	}
}

func TestMergeConfigPatchAllowsClearingAuthsAndDisabling(t *testing.T) {
	current := defaultPluginConfig()
	current.AuthIDs = []string{"auth-a"}
	next, err := mergeConfigPatch(current, []byte(`{"enabled":false,"auth_ids":[]}`))
	if err != nil {
		t.Fatalf("mergeConfigPatch returned error: %v", err)
	}
	if next.Enabled {
		t.Fatal("Enabled = true, want false")
	}
	if len(next.AuthIDs) != 0 {
		t.Fatalf("AuthIDs = %#v, want empty", next.AuthIDs)
	}
}

func TestMergeConfigPatchIgnoresPathFields(t *testing.T) {
	current := defaultPluginConfig()
	current.StatePath = "state-current.json"
	current.ConfigPath = "config-current.json"
	next, err := mergeConfigPatch(current, []byte(`{"state_path":"C:\\temp\\state.json","config_path":"C:\\temp\\config.json"}`))
	if err != nil {
		t.Fatalf("mergeConfigPatch returned error: %v", err)
	}
	if next.StatePath != current.StatePath || next.ConfigPath != current.ConfigPath {
		t.Fatalf("paths = %q/%q, want preserved %q/%q", next.StatePath, next.ConfigPath, current.StatePath, current.ConfigPath)
	}
}
