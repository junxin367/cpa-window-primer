package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

type availableModel struct {
	ID      string
	Owner   string
	RawType string
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

func providerForAuthID(authID string) string {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return ""
	}
	entries, err := callHostAuthList()
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if authIDForEntry(entry) == authID || strings.TrimSpace(entry.AuthIndex) == authID {
			return classifyProvider(entry.Provider, entry.Type)
		}
	}
	return ""
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

func parseModelList(raw string) []string {
	return uniqueTrimmed(strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	}))
}

func warmupModelCandidates(cfg runtimeConfig, authID string) []string {
	provider := providerForAuthID(authID)
	configured := parseModelList(cfg.Model)
	var out []string
	for _, model := range configured {
		if modelMatchesProvider(model, provider) {
			out = append(out, model)
		}
	}
	for _, model := range configured {
		if modelProvider(model) == "" {
			out = append(out, model)
		}
	}
	if fallback := fallbackModelForProvider(provider); fallback != "" {
		out = append(out, fallback)
	}
	if provider == "" {
		out = append(out, configured...)
	}
	return uniqueTrimmed(out)
}

func modelMatchesProvider(model, provider string) bool {
	if provider == "" {
		return true
	}
	return modelProvider(model) == provider
}

func modelProvider(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case normalized == "":
		return ""
	case strings.Contains(normalized, "claude") || strings.Contains(normalized, "anthropic"):
		return "claude"
	case strings.HasPrefix(normalized, "gpt-") ||
		strings.HasPrefix(normalized, "o1") ||
		strings.HasPrefix(normalized, "o3") ||
		strings.HasPrefix(normalized, "o4") ||
		strings.Contains(normalized, "codex") ||
		strings.Contains(normalized, "openai"):
		return "codex"
	default:
		return ""
	}
}

func fallbackModelForProvider(provider string) string {
	models, err := availableModels()
	if err == nil {
		if model := chooseFallbackModel(provider, models); model != "" {
			return model
		}
	}
	switch provider {
	case "claude":
		return "claude-sonnet-4-6"
	case "codex":
		return "gpt-5.4"
	default:
		return defaultFirstModel()
	}
}

func defaultFirstModel() string {
	models := parseModelList(defaultModel)
	if len(models) == 0 {
		return "gpt-5.4"
	}
	return models[0]
}

func chooseFallbackModel(provider string, models []availableModel) string {
	if len(models) == 0 {
		return ""
	}
	preferred := map[string][]string{
		"codex":  []string{"gpt-5.4", "gpt-5.3-codex", "gpt-5"},
		"claude": []string{"claude-sonnet-4-6", "claude-sonnet-4-5", "claude-sonnet"},
	}
	for _, want := range preferred[provider] {
		for _, model := range models {
			if modelMatchesAvailableProvider(model, provider) && strings.Contains(strings.ToLower(model.ID), want) {
				return model.ID
			}
		}
	}
	for _, model := range models {
		if modelMatchesAvailableProvider(model, provider) {
			return model.ID
		}
	}
	if provider == "" {
		return strings.TrimSpace(models[0].ID)
	}
	return ""
}

func modelMatchesAvailableProvider(model availableModel, provider string) bool {
	if provider == "" {
		return strings.TrimSpace(model.ID) != ""
	}
	if detected := modelProvider(model.ID); detected != "" {
		return detected == provider
	}
	return modelProvider(model.Owner) == provider || modelProvider(model.RawType) == provider
}

func availableModels() ([]availableModel, error) {
	var lastErr error
	for _, rawURL := range modelListURLs() {
		resp, err := callHostHTTPDo(http.MethodGet, rawURL, http.Header{}, nil)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf("models endpoint %s returned %d", rawURL, resp.StatusCode)
			continue
		}
		models, err := parseAvailableModels(resp.Body)
		if err != nil {
			lastErr = err
			continue
		}
		if len(models) > 0 {
			return models, nil
		}
		lastErr = fmt.Errorf("models endpoint %s returned no models", rawURL)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no models endpoint configured")
}

func modelListURLs() []string {
	var urls []string
	for _, key := range []string{"CPA_MODELS_URL", "CPA_BASE_URL", "CLIPROXY_BASE_URL"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		if strings.HasSuffix(value, "/v1/models") {
			urls = append(urls, value)
		} else {
			urls = append(urls, strings.TrimRight(value, "/")+"/v1/models")
		}
	}
	urls = append(urls,
		"http://127.0.0.1:8317/v1/models",
		"http://localhost:8317/v1/models",
	)
	return uniqueTrimmed(urls)
}

func parseAvailableModels(body []byte) ([]availableModel, error) {
	var doc struct {
		Data   []map[string]any `json:"data"`
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	items := doc.Data
	if len(items) == 0 {
		items = doc.Models
	}
	out := make([]availableModel, 0, len(items))
	for _, item := range items {
		id := stringValue(item["id"])
		if id == "" {
			id = stringValue(item["name"])
		}
		if id == "" {
			continue
		}
		out = append(out, availableModel{
			ID:      id,
			Owner:   stringValue(item["owned_by"]),
			RawType: stringValue(item["type"]),
		})
	}
	return out, nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func isWarmupModelUnavailable(statusCode int, body []byte, err error) bool {
	if statusCode != 0 && statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(string(body)))
	if err != nil {
		text += " " + strings.ToLower(err.Error())
	}
	for _, marker := range []string{
		"model_not_found",
		"model not found",
		"unknown model",
		"unsupported model",
		"invalid model",
		"does not exist",
		"not a valid model",
		"no such model",
	} {
		if strings.Contains(text, marker) {
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
