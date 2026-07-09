package main

import (
	"encoding/json"
	"strings"
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

func TestPickAuthFromSchedulerHeadersMatchesCandidateAttributes(t *testing.T) {
	got, ok := pickAuthFromSchedulerHeaders(map[string][]string{
		primerHeader: {"idx-b"},
	}, []pluginapi.SchedulerAuthCandidate{
		{ID: "auth-a", Attributes: map[string]string{"auth_index": "idx-a"}},
		{ID: "auth-b", Attributes: map[string]string{"auth_index": "idx-b"}},
	})
	if !ok || got != "auth-b" {
		t.Fatalf("pickAuthFromSchedulerHeaders = %q/%v, want auth-b/true", got, ok)
	}
}

func TestHandleSchedulerPickRejectsMissingPrimerTarget(t *testing.T) {
	raw, err := json.Marshal(pluginapi.SchedulerPickRequest{
		Options: pluginapi.SchedulerOptions{Headers: map[string][]string{
			primerHeader: {"missing"},
		}},
		Candidates: []pluginapi.SchedulerAuthCandidate{{ID: "auth-a"}},
	})
	if err != nil {
		t.Fatalf("marshal scheduler request: %v", err)
	}
	_, err = handleSchedulerPick(raw)
	if err == nil || !strings.Contains(err.Error(), "target_auth_not_candidate") {
		t.Fatalf("handleSchedulerPick error = %v, want target_auth_not_candidate", err)
	}
}
