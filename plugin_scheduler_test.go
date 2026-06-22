package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestPickAuthFromSchedulerHeaders(t *testing.T) {
	got, ok := pickAuthFromSchedulerHeaders(map[string][]string{
		"x-cpa-window-primer-auth-id": {"auth-b"},
	}, []pluginapi.SchedulerAuthCandidate{
		{ID: "auth-a"},
		{ID: "auth-b"},
	})
	if !ok || got != "auth-b" {
		t.Fatalf("pickAuthFromSchedulerHeaders = %q/%v, want auth-b/true", got, ok)
	}
}

func TestPickAuthFromSchedulerHeadersIgnoresOrdinaryRequests(t *testing.T) {
	got, ok := pickAuthFromSchedulerHeaders(map[string][]string{}, []pluginapi.SchedulerAuthCandidate{{ID: "auth-a"}})
	if ok || got != "" {
		t.Fatalf("pickAuthFromSchedulerHeaders = %q/%v, want empty/false", got, ok)
	}
}

func TestPickAuthFromSchedulerHeadersRequiresCandidate(t *testing.T) {
	got, ok := pickAuthFromSchedulerHeaders(map[string][]string{
		primerHeader: {"missing"},
	}, []pluginapi.SchedulerAuthCandidate{{ID: "auth-a"}})
	if ok || got != "" {
		t.Fatalf("pickAuthFromSchedulerHeaders = %q/%v, want empty/false", got, ok)
	}
}
