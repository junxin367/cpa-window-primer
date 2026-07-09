package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req pluginapi.SchedulerPickRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	target := schedulerHeaderValue(req.Options.Headers, primerHeader)
	if target == "" {
		return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
	}
	authID, ok := pickAuthFromSchedulerTarget(target, req.Candidates)
	if !ok {
		return nil, fmt.Errorf("target_auth_not_candidate: %s", target)
	}
	return okEnvelope(pluginapi.SchedulerPickResponse{Handled: true, AuthID: authID})
}

func pickAuthFromSchedulerHeaders(headers map[string][]string, candidates []pluginapi.SchedulerAuthCandidate) (string, bool) {
	target := schedulerHeaderValue(headers, primerHeader)
	if target == "" {
		return "", false
	}
	return pickAuthFromSchedulerTarget(target, candidates)
}

func pickAuthFromSchedulerTarget(target string, candidates []pluginapi.SchedulerAuthCandidate) (string, bool) {
	for _, candidate := range candidates {
		candidateID := strings.TrimSpace(candidate.ID)
		if candidateID != "" && schedulerCandidateMatches(candidate, target) {
			return candidateID, true
		}
	}
	return "", false
}

func schedulerCandidateMatches(candidate pluginapi.SchedulerAuthCandidate, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if strings.TrimSpace(candidate.ID) == target {
		return true
	}
	for _, key := range []string{"auth_index", "auth-index", "index", "name", "file_name", "filename"} {
		if strings.TrimSpace(candidate.Attributes[key]) == target {
			return true
		}
	}
	for _, key := range []string{"auth_index", "auth-index", "index", "name", "file_name", "filename"} {
		if strings.TrimSpace(schedulerMetadataString(candidate.Metadata[key])) == target {
			return true
		}
	}
	return false
}

func schedulerMetadataString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}

func schedulerHeaderValue(headers map[string][]string, name string) string {
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), name) {
			continue
		}
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
	}
	return ""
}
