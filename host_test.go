package main

import "testing"

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

type errString string

func (e errString) Error() string { return string(e) }
