package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type authListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Stream   bool          `json:"stream"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type hostModelExecutionRequest struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func callHostAuthList() ([]pluginapi.HostAuthFileEntry, error) {
	result, err := callHostRPC(pluginabi.MethodHostAuthList, map[string]any{})
	if err != nil {
		return nil, err
	}
	var resp authListResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("decode auth list: %w", err)
	}
	return resp.Files, nil
}

func allowedAuthIDs() (map[string]pluginapi.HostAuthFileEntry, error) {
	entries, err := callHostAuthList()
	if err != nil {
		return nil, err
	}
	out := make(map[string]pluginapi.HostAuthFileEntry)
	for _, entry := range entries {
		if !isSupportedOAuthAuth(entry) {
			continue
		}
		authID := authIDForEntry(entry)
		if authID != "" {
			out[authID] = entry
		}
	}
	return out, nil
}

func authIDForEntry(entry pluginapi.HostAuthFileEntry) string {
	authID := strings.TrimSpace(entry.ID)
	if authID == "" {
		authID = strings.TrimSpace(entry.AuthIndex)
	}
	return authID
}

func callHostAuthGetRuntime(authIndex string) (pluginapi.HostAuthGetRuntimeResponse, error) {
	result, err := callHostRPC(pluginabi.MethodHostAuthGetRuntime, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return pluginapi.HostAuthGetRuntimeResponse{}, err
	}
	var resp pluginapi.HostAuthGetRuntimeResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return pluginapi.HostAuthGetRuntimeResponse{}, fmt.Errorf("decode auth runtime: %w", err)
	}
	return resp, nil
}

func isSupportedOAuthAuth(entry pluginapi.HostAuthFileEntry) bool {
	if entry.Disabled || isHostAuthQuotaBlocked(entry, time.Now()) {
		return false
	}
	return isManagedOAuthAuth(entry)
}

func isManagedOAuthAuth(entry pluginapi.HostAuthFileEntry) bool {
	provider := strings.ToLower(strings.TrimSpace(entry.Provider))
	typ := strings.ToLower(strings.TrimSpace(entry.Type))
	name := strings.ToLower(strings.TrimSpace(entry.Name))
	if provider == "codex" || typ == "codex" {
		return true
	}
	if provider == "claude" || provider == "anthropic" || typ == "claude" || typ == "anthropic" {
		return true
	}
	if provider == "openai" || typ == "openai" {
		if strings.Contains(name, "api") && strings.Contains(name, "key") {
			return false
		}
		return strings.Contains(name, "oauth") || strings.Contains(typ, "oauth") || strings.Contains(provider, "oauth")
	}
	return false
}

func isHostAuthQuotaBlocked(entry pluginapi.HostAuthFileEntry, now time.Time) bool {
	if entry.Unavailable {
		return true
	}
	if !entry.NextRetryAfter.IsZero() && entry.NextRetryAfter.After(now) {
		return true
	}
	return quotaLikeText(entry.Status + " " + entry.StatusMessage)
}

func isWarmupQuotaBlocked(statusCode int, headers http.Header, body []byte, err error) bool {
	if statusCode == http.StatusPaymentRequired || statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode == http.StatusServiceUnavailable && headers != nil && strings.TrimSpace(headers.Get("Retry-After")) != "" {
		return true
	}
	text := string(body)
	if err != nil {
		text += " " + err.Error()
	}
	return quotaLikeText(text)
}

func quotaLikeText(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"quota",
		"rate_limit",
		"rate limit",
		"usage_limit",
		"usage limit",
		"usage limit reached",
		"exhausted",
		"insufficient_quota",
		"no credit",
		"no credits",
		"credit exhausted",
		"too many requests",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func executeHostWarmup(authID, model, prompt, hostCallbackID string) (pluginapi.HostModelExecutionResponse, error) {
	body, err := json.Marshal(chatCompletionRequest{
		Model:  model,
		Stream: false,
		Messages: []chatMessage{{
			Role:    "user",
			Content: prompt,
		}},
	})
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, err
	}
	headers := http.Header{}
	headers.Set(primerHeader, authID)
	result, err := callHostRPC(pluginabi.MethodHostModelExecute, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: "openai",
			ExitProtocol:  "openai",
			Model:         model,
			Stream:        false,
			Body:          body,
			Headers:       headers,
		},
		HostCallbackID: hostCallbackID,
	})
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, err
	}
	var resp pluginapi.HostModelExecutionResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("decode model execution: %w", err)
	}
	if resp.StatusCode == 0 {
		resp.StatusCode = http.StatusOK
	}
	return resp, nil
}

func summarizeResponse(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 512 {
		return text[:512]
	}
	return text
}
