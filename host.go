package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
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

type warmupAuthTarget struct {
	AuthID    string
	AuthIndex string
	Provider  string
	Success   int64
}

type codexWarmupRequest struct {
	Model        string               `json:"model"`
	Instructions string               `json:"instructions"`
	Input        []codexWarmupInput   `json:"input"`
	Stream       bool                 `json:"stream"`
	Store        bool                 `json:"store"`
	Reasoning    codexWarmupReasoning `json:"reasoning"`
	Include      []string             `json:"include"`
}

type codexWarmupInput struct {
	Type    string               `json:"type"`
	Role    string               `json:"role"`
	Content []codexWarmupContent `json:"content"`
}

type codexWarmupContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexWarmupReasoning struct {
	Effort  string `json:"effort"`
	Summary string `json:"summary"`
}

type codexWarmupEvent struct {
	Type     string `json:"type"`
	Status   string `json:"status"`
	Error    any    `json:"error"`
	Response struct {
		Status string `json:"status"`
		Error  any    `json:"error"`
	} `json:"response"`
}

const (
	codexWarmupURL              = "https://chatgpt.com/backend-api/codex/responses"
	codexWarmupUserAgent        = "codex-tui/0.135.0 (Mac OS 26.5.0; arm64) iTerm.app/3.6.10 (codex-tui; 0.135.0)"
	codexWarmupOrigin           = "codex-tui"
	codexWarmupTimeout          = 2 * time.Minute
	codexWarmupMaxResponseBytes = 1 << 20
)

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
		addAuthLookupEntry(out, entry)
	}
	return out, nil
}

func schedulableAuthIDs() (map[string]pluginapi.HostAuthFileEntry, error) {
	entries, err := callHostAuthList()
	if err != nil {
		return nil, err
	}
	out := make(map[string]pluginapi.HostAuthFileEntry)
	for _, entry := range entries {
		if !isSchedulableOAuthAuth(entry) {
			continue
		}
		addAuthLookupEntry(out, entry)
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

func authLookupKeys(entry pluginapi.HostAuthFileEntry) []string {
	return uniqueTrimmed([]string{
		entry.ID,
		entry.AuthIndex,
		entry.Name,
	})
}

func addAuthLookupEntry(out map[string]pluginapi.HostAuthFileEntry, entry pluginapi.HostAuthFileEntry) {
	if out == nil {
		return
	}
	for _, key := range authLookupKeys(entry) {
		out[key] = entry
	}
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

func resolveWarmupAuthTarget(raw string) (warmupAuthTarget, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return warmupAuthTarget{}, fmt.Errorf("auth_id is required")
	}
	auths, err := callHostAuthList()
	if err != nil {
		return warmupAuthTarget{}, fmt.Errorf("list auths for warmup target: %w", err)
	}
	nameMatches := make([]pluginapi.HostAuthFileEntry, 0, 1)
	for _, auth := range auths {
		if !isManagedOAuthAuth(auth) {
			continue
		}
		if target == authIDForEntry(auth) || target == strings.TrimSpace(auth.AuthIndex) {
			return warmupAuthTargetFromEntry(auth, target)
		}
		if target == strings.TrimSpace(auth.Name) {
			nameMatches = append(nameMatches, auth)
		}
	}
	if len(nameMatches) == 1 {
		return warmupAuthTargetFromEntry(nameMatches[0], target)
	}
	if len(nameMatches) > 1 {
		return warmupAuthTarget{}, fmt.Errorf("auth name %s is ambiguous; use auth_id or auth_index", target)
	}
	return warmupAuthTarget{}, fmt.Errorf("auth_id %s is not available for warmup", target)
}

func warmupAuthTargetFromEntry(auth pluginapi.HostAuthFileEntry, requested string) (warmupAuthTarget, error) {
	authID := authIDForEntry(auth)
	if authID == "" {
		return warmupAuthTarget{}, fmt.Errorf("auth_id is empty for warmup target %s", requested)
	}
	authIndex := strings.TrimSpace(auth.AuthIndex)
	if authIndex == "" {
		return warmupAuthTarget{}, fmt.Errorf("auth_index is empty for warmup target %s", authID)
	}
	return warmupAuthTarget{
		AuthID:    authID,
		AuthIndex: authIndex,
		Provider:  classifyProvider(auth.Provider, auth.Type),
		Success:   auth.Success,
	}, nil
}

func refreshWarmupAuthTarget(target warmupAuthTarget) (warmupAuthTarget, error) {
	if strings.TrimSpace(target.AuthIndex) == "" {
		return target, fmt.Errorf("auth_index is empty for warmup target %s", target.AuthID)
	}
	resp, err := callHostAuthGetRuntime(target.AuthIndex)
	if err != nil {
		return target, fmt.Errorf("get auth runtime for %s: %w", target.AuthID, err)
	}
	entry := resp.Auth
	if authID := authIDForEntry(entry); authID != "" {
		target.AuthID = authID
	}
	if authIndex := strings.TrimSpace(entry.AuthIndex); authIndex != "" {
		target.AuthIndex = authIndex
	}
	target.Success = entry.Success
	return target, nil
}

func verifyWarmupTargetSuccess(target warmupAuthTarget, beforeSuccess int64) error {
	latest, err := refreshWarmupAuthTarget(target)
	if err != nil {
		return fmt.Errorf("target_auth_verify_failed: %w", err)
	}
	if latest.Success <= beforeSuccess {
		return fmt.Errorf("target_auth_not_used: 目标认证文件 %s 的成功计数未增加", target.AuthID)
	}
	return nil
}

func isTargetAuthNotCandidate(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "target_auth_not_candidate")
}

func executeCodexDirectWarmup(target warmupAuthTarget, model, prompt, hostCallbackID string) (pluginapi.HostModelExecutionResponse, error) {
	credentials, err := codexCredentialsForAuth(target.AuthIndex)
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("load target auth %s credentials: %w", target.AuthID, err)
	}
	body, err := json.Marshal(codexWarmupRequest{
		Model:        model,
		Instructions: "",
		Input: []codexWarmupInput{{
			Type: "message",
			Role: "user",
			Content: []codexWarmupContent{{
				Type: "input_text",
				Text: prompt,
			}},
		}},
		Stream: true,
		Store:  false,
		Reasoning: codexWarmupReasoning{
			Effort:  "medium",
			Summary: "auto",
		},
		Include: []string{"reasoning.encrypted_content"},
	})
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("encode codex warmup request: %w", err)
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+credentials.AccessToken)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "text/event-stream")
	headers.Set("Connection", "Keep-Alive")
	headers.Set("User-Agent", codexWarmupUserAgent)
	headers.Set("Originator", codexWarmupOrigin)
	headers.Set("Session_id", codexWarmupSessionID())
	if credentials.AccountID != "" {
		headers.Set("Chatgpt-Account-Id", credentials.AccountID)
	}
	resp, requestErr := executeCodexWarmupHTTP(credentials, codexWarmupURL, headers, body, hostCallbackID)
	result := pluginapi.HostModelExecutionResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Headers,
		Body:       resp.Body,
	}
	if requestErr != nil {
		return result, fmt.Errorf("codex_warmup_request_failed: %w", requestErr)
	}
	if result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusMultipleChoices {
		if err := validateCodexWarmupCompletion(result.Body); err != nil {
			return result, err
		}
	}
	return result, nil
}

func executeCodexWarmupHTTP(credentials codexAuthCredentials, rawURL string, headers http.Header, body []byte, hostCallbackID string) (pluginapi.HTTPResponse, error) {
	proxyURL := strings.TrimSpace(credentials.ProxyURL)
	if proxyURL == "" {
		return executeCodexWarmupHostHTTP(rawURL, headers, body, hostCallbackID)
	}

	transport, _, err := proxyutil.BuildHTTPTransport(proxyURL)
	if err != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("target_auth_proxy_invalid: %w", err)
	}
	if transport == nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("target_auth_proxy_invalid: no transport")
	}
	defer transport.CloseIdleConnections()

	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("create direct Codex request: %w", err)
	}
	req.Header = headers.Clone()
	client := &http.Client{
		Transport: transport,
		Timeout:   codexWarmupTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("execute direct Codex request: %w", err)
	}
	defer resp.Body.Close()
	result := pluginapi.HTTPResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
	}
	buffer := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			if len(result.Body)+n > codexWarmupMaxResponseBytes {
				return result, fmt.Errorf("codex_warmup_response_too_large")
			}
			result.Body = append(result.Body, buffer[:n]...)
			if codexWarmupResponseTerminal(result.Body) {
				return result, nil
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return result, nil
			}
			return result, fmt.Errorf("read direct Codex response: %w", readErr)
		}
	}
}

func executeCodexWarmupHostHTTP(rawURL string, headers http.Header, body []byte, hostCallbackID string) (pluginapi.HTTPResponse, error) {
	return executeCodexWarmupHostHTTPWithTimeout(rawURL, headers, body, hostCallbackID, codexWarmupTimeout)
}

func executeCodexWarmupHostHTTPWithTimeout(rawURL string, headers http.Header, body []byte, hostCallbackID string, timeout time.Duration) (pluginapi.HTTPResponse, error) {
	type openResult struct {
		Response rpcHTTPStreamResponse
		Err      error
	}
	type readResult struct {
		Response rpcHTTPStreamReadResponse
		Err      error
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	done := make(chan struct{})
	defer close(done)

	openCh := make(chan openResult)
	go func() {
		resp, err := callHostHTTPDoStream(http.MethodPost, rawURL, headers, body, hostCallbackID)
		result := openResult{Response: resp, Err: err}
		select {
		case openCh <- result:
		case <-done:
			if err == nil && strings.TrimSpace(resp.StreamID) != "" {
				_ = callHostHTTPStreamClose(resp.StreamID)
			}
		}
	}()

	var stream rpcHTTPStreamResponse
	select {
	case opened := <-openCh:
		if opened.Err != nil {
			return pluginapi.HTTPResponse{}, opened.Err
		}
		stream = opened.Response
	case <-timer.C:
		return pluginapi.HTTPResponse{}, fmt.Errorf("codex_warmup_timeout: waiting for response headers")
	}
	if strings.TrimSpace(stream.StreamID) == "" {
		return pluginapi.HTTPResponse{}, fmt.Errorf("codex_warmup_stream_unavailable")
	}

	streamClosed := false
	closeStream := func() {
		if streamClosed {
			return
		}
		streamClosed = true
		_ = callHostHTTPStreamClose(stream.StreamID)
	}
	defer closeStream()

	result := pluginapi.HTTPResponse{
		StatusCode: stream.StatusCode,
		Headers:    stream.Headers.Clone(),
	}
	for {
		readCh := make(chan readResult)
		go func() {
			resp, err := callHostHTTPStreamRead(stream.StreamID)
			select {
			case readCh <- readResult{Response: resp, Err: err}:
			case <-done:
			}
		}()

		select {
		case chunk := <-readCh:
			if chunk.Err != nil {
				return result, chunk.Err
			}
			if len(chunk.Response.Payload) > 0 {
				if len(result.Body)+len(chunk.Response.Payload) > codexWarmupMaxResponseBytes {
					return result, fmt.Errorf("codex_warmup_response_too_large")
				}
				result.Body = append(result.Body, chunk.Response.Payload...)
				if codexWarmupResponseTerminal(result.Body) {
					return result, nil
				}
			}
			if chunk.Response.Done {
				return result, nil
			}
		case <-timer.C:
			closeStream()
			return result, fmt.Errorf("codex_warmup_timeout: waiting for response completion")
		}
	}
}

func codexWarmupSessionID() string {
	raw := make([]byte, 16)
	if _, err := cryptorand.Read(raw); err != nil {
		return fmt.Sprintf("cwp-%d", time.Now().UnixNano())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	text := hex.EncodeToString(raw)
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

func validateCodexWarmupCompletion(body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return fmt.Errorf("codex_warmup_stream_incomplete: empty response")
	}
	completed, terminalErr := inspectCodexWarmupCompletion(trimmed)
	if terminalErr != nil {
		return terminalErr
	}
	if completed {
		return nil
	}
	return fmt.Errorf("codex_warmup_stream_incomplete")
}

func codexWarmupResponseTerminal(body []byte) bool {
	completed, terminalErr := inspectCodexWarmupCompletion(body)
	return completed || terminalErr != nil
}

func inspectCodexWarmupCompletion(body []byte) (bool, error) {
	trimmed := bytes.TrimSpace(body)
	for _, line := range bytes.Split(trimmed, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		eventData := bytes.TrimSpace(line[len("data:"):])
		if bytes.Equal(eventData, []byte("[DONE]")) {
			continue
		}
		var event codexWarmupEvent
		if err := json.Unmarshal(eventData, &event); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(event.Type)) {
		case "response.completed":
			return true, nil
		case "error", "response.failed", "response.incomplete":
			return false, fmt.Errorf("codex_warmup_stream_error: %s", summarizeResponse(eventData))
		}
	}

	var result codexWarmupEvent
	if err := json.Unmarshal(trimmed, &result); err == nil {
		eventType := strings.ToLower(strings.TrimSpace(result.Type))
		if eventType == "response.completed" {
			return true, nil
		}
		if eventType == "error" || eventType == "response.failed" || eventType == "response.incomplete" ||
			result.Error != nil || result.Response.Error != nil {
			return false, fmt.Errorf("codex_warmup_stream_error: %s", summarizeResponse(trimmed))
		}
	}
	return false, nil
}

func isSupportedOAuthAuth(entry pluginapi.HostAuthFileEntry) bool {
	if entry.Disabled || isHostAuthQuotaBlocked(entry, time.Now()) {
		return false
	}
	return isManagedOAuthAuth(entry)
}

func isSchedulableOAuthAuth(entry pluginapi.HostAuthFileEntry) bool {
	if entry.Disabled {
		return false
	}
	return isManagedOAuthAuth(entry)
}

func isManagedOAuthAuth(entry pluginapi.HostAuthFileEntry) bool {
	return authIDForEntry(entry) != "" || strings.TrimSpace(entry.Name) != ""
}

// formatAuthType 返回展示用的凭证类型，如 codex、devin、kimi、claude 等。
func formatAuthType(entry pluginapi.HostAuthFileEntry) string {
	typ := strings.ToLower(strings.TrimSpace(entry.Type))
	provider := strings.ToLower(strings.TrimSpace(entry.Provider))
	name := strings.ToLower(strings.TrimSpace(entry.Name))

	detect := func(substr string) bool {
		return strings.Contains(typ, substr) || strings.Contains(provider, substr) || strings.Contains(name, substr)
	}

	switch {
	case detect("devin"):
		return "devin"
	case detect("kimi") || detect("moonshot"):
		return "kimi"
	case detect("codex"):
		return "codex"
	case detect("claude") || detect("anthropic"):
		return "claude"
	case detect("openai"):
		if strings.Contains(name, "oauth") || strings.Contains(typ, "oauth") || strings.Contains(provider, "oauth") {
			return "codex"
		}
		return "openai"
	case detect("gemini"):
		return "gemini"
	case detect("antigravity"):
		return "antigravity"
	case detect("deepseek"):
		return "deepseek"
	case detect("grok") || detect("xai"):
		return "grok"
	case detect("qwen"):
		return "qwen"
	case detect("minimax"):
		return "minimax"
	case detect("glm") || detect("zhipu"):
		return "glm"
	}

	if typ != "" {
		return typ
	}
	if provider != "" {
		return provider
	}
	cleanName := strings.TrimSuffix(name, ".json")
	if idx := strings.IndexAny(cleanName, "-_"); idx > 0 {
		return cleanName[:idx]
	}
	if cleanName != "" {
		return cleanName
	}
	return "unknown"
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

func isWarmupQuotaBlocked(provider string, statusCode int, headers http.Header, body []byte, err error) bool {
	text := string(body)
	if err != nil {
		text += " " + err.Error()
	}
	if strings.EqualFold(strings.TrimSpace(provider), "codex") {
		if statusCode == http.StatusPaymentRequired {
			return true
		}
		return usageQuotaLikeText(text)
	}
	if statusCode == http.StatusPaymentRequired || statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode == http.StatusServiceUnavailable && headers != nil && strings.TrimSpace(headers.Get("Retry-After")) != "" {
		return true
	}
	return quotaLikeText(text)
}

func usageQuotaLikeText(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"usage_limit_reached",
		"usage limit reached",
		"insufficient_quota",
		"quota exhausted",
		"quota exceeded",
		"credit exhausted",
		"credits exhausted",
		"no credit",
		"no credits",
		"payment required",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
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
	// Devin 可代理不同模型家族；实际可用性仍由指定凭证的宿主调度确认。
	if provider == "devin" {
		return strings.TrimSpace(model) != "" && !strings.Contains(strings.ToLower(model), "image")
	}
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
	for _, model := range parseModelList(defaultModel) {
		if modelMatchesProvider(model, provider) {
			return model
		}
	}
	return defaultFirstModel()
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
	// 项目默认值优先；仅接受模型列表中的精确匹配，避免把图像模型当成文本预热模型。
	preferred := append(parseModelList(defaultModel), "gpt-5.4", "gpt-5.3-codex", "claude-sonnet-4-5")
	for _, want := range preferred {
		for _, model := range models {
			if modelMatchesAvailableProvider(model, provider) && strings.EqualFold(model.ID, want) {
				return model.ID
			}
		}
	}
	for _, model := range models {
		if modelMatchesAvailableProvider(model, provider) {
			return model.ID
		}
	}
	return ""
}

func modelMatchesAvailableProvider(model availableModel, provider string) bool {
	if strings.Contains(strings.ToLower(model.ID), "image") {
		return false
	}
	if provider == "devin" {
		return strings.EqualFold(model.Owner, "devin") || strings.EqualFold(model.RawType, "devin")
	}
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
	text := strings.ToLower(strings.TrimSpace(string(body)))
	if err != nil {
		text += " " + strings.ToLower(err.Error())
	}
	if strings.Contains(text, "selected model is at capacity") ||
		strings.Contains(text, "model is at capacity. please try a different model") {
		return true
	}
	if statusCode != 0 && statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound {
		return false
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
		"target_auth_not_candidate",
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
