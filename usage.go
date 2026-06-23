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

// usageWindow 表示一个限额窗口（5 小时或一周）。
type usageWindow struct {
	UsedPercent float64
	ResetAt     time.Time
	HasData     bool
}

// usageEntry 表示单个认证文件的额度结果。
type usageEntry struct {
	AuthID    string
	Provider  string // codex / claude
	Email     string
	Plan      string  // 原始套餐字符串
	Weight    float64 // 套餐倍数（相对基准单位）
	Primary   usageWindow
	Secondary usageWindow
	// Sonnet 为 Claude 单独统计的窗口（如有）。
	SonnetPrimary   usageWindow
	SonnetSecondary usageWindow
	HasSonnet       bool
	Err             string
}

// rpcHTTPRequest 是 host.http.do 的请求包裹结构。
type rpcHTTPRequest struct {
	Method  string      `json:"method,omitempty"`
	URL     string      `json:"url,omitempty"`
	Headers http.Header `json:"headers,omitempty"`
	Body    []byte      `json:"body,omitempty"`
}

func callHostHTTPDo(method, rawURL string, headers http.Header, body []byte) (pluginapi.HTTPResponse, error) {
	result, err := callHostRPC(pluginabi.MethodHostHTTPDo, rpcHTTPRequest{
		Method:  method,
		URL:     rawURL,
		Headers: headers,
		Body:    body,
	})
	if err != nil {
		return pluginapi.HTTPResponse{}, err
	}
	var resp pluginapi.HTTPResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("decode host http response: %w", err)
	}
	return resp, nil
}

// accessTokenForAuth 通过 host.auth.get 拿物理 JSON 并提取 access token。
func accessTokenForAuth(authIndex string) (string, error) {
	result, err := callHostRPC(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return "", err
	}
	var resp pluginapi.HostAuthGetResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", fmt.Errorf("decode auth json: %w", err)
	}
	var doc map[string]any
	if len(resp.JSON) > 0 {
		_ = json.Unmarshal(resp.JSON, &doc)
	}
	token := extractAccessToken(doc)
	if token == "" {
		return "", fmt.Errorf("access token not found")
	}
	return token, nil
}

// extractAccessToken 在常见路径里查找 access token。
func extractAccessToken(doc map[string]any) string {
	if doc == nil {
		return ""
	}
	candidates := []string{"access_token", "accessToken"}
	for _, key := range candidates {
		if v, ok := doc[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	// 嵌套 metadata / tokens / token 容器。
	for _, container := range []string{"metadata", "tokens", "token"} {
		if sub, ok := doc[container].(map[string]any); ok {
			if v := extractAccessToken(sub); v != "" {
				return v
			}
		}
	}
	return ""
}

// fetchCodexUsage 拉取 Codex 额度。
func fetchCodexUsage(authID, authIndex, email string) usageEntry {
	entry := usageEntry{AuthID: authID, Provider: "codex", Email: email}
	entry.Weight = codexPlanWeight("")
	if strings.TrimSpace(authIndex) == "" {
		authIndex = authID
	}
	token, err := accessTokenForAuth(authIndex)
	if err != nil {
		entry.Err = err.Error()
		return entry
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Content-Type", "application/json")
	resp, err := callHostHTTPDo(http.MethodGet, codexUsageURL, headers, nil)
	if err != nil {
		entry.Err = err.Error()
		return entry
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		entry.Err = fmt.Sprintf("status %d", resp.StatusCode)
		return entry
	}
	parseCodexUsageBody(resp.Body, &entry)
	return entry
}

func parseCodexUsageBody(body []byte, entry *usageEntry) {
	var doc struct {
		PlanType  string `json:"plan_type"`
		RateLimit struct {
			Primary   codexWindowJSON `json:"primary_window"`
			Secondary codexWindowJSON `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		entry.Err = "parse usage: " + err.Error()
		return
	}
	if strings.TrimSpace(doc.PlanType) != "" {
		entry.Plan = doc.PlanType
		entry.Weight = codexPlanWeight(doc.PlanType)
	}
	entry.Primary = doc.RateLimit.Primary.toWindow()
	entry.Secondary = doc.RateLimit.Secondary.toWindow()
}

type codexWindowJSON struct {
	UsedPercent float64 `json:"used_percent"`
	ResetAt     int64   `json:"reset_at"`
}

func (w codexWindowJSON) toWindow() usageWindow {
	out := usageWindow{UsedPercent: w.UsedPercent, HasData: true}
	if w.ResetAt > 0 {
		out.ResetAt = time.Unix(w.ResetAt, 0)
	}
	return out
}

// fetchClaudeUsage 先调 profile 拿套餐，再调 usage 拿 5 小时/周/Sonnet 额度。
func fetchClaudeUsage(authID, authIndex, email string) usageEntry {
	entry := usageEntry{AuthID: authID, Provider: "claude", Email: email}
	entry.Weight = claudePlanWeight("")
	if strings.TrimSpace(authIndex) == "" {
		authIndex = authID
	}
	token, err := accessTokenForAuth(authIndex)
	if err != nil {
		entry.Err = err.Error()
		return entry
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Content-Type", "application/json")
	headers.Set("anthropic-beta", claudeOAuthBeta)
	resp, err := callHostHTTPDo(http.MethodGet, claudeProfileURL, headers, nil)
	if err != nil {
		entry.Err = err.Error()
		return entry
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		entry.Err = fmt.Sprintf("status %d", resp.StatusCode)
		return entry
	}
	parseClaudeProfileBody(resp.Body, &entry)

	// 再拉额度。额度拉取失败不覆盖已获取的套餐信息。
	usageResp, usageErr := callHostHTTPDo(http.MethodGet, claudeUsageURL, headers, nil)
	if usageErr != nil {
		if entry.Err == "" {
			entry.Err = usageErr.Error()
		}
		return entry
	}
	if usageResp.StatusCode < 200 || usageResp.StatusCode >= 300 {
		if entry.Err == "" {
			entry.Err = fmt.Sprintf("usage status %d", usageResp.StatusCode)
		}
		return entry
	}
	parseClaudeUsageBody(usageResp.Body, &entry)
	return entry
}

func parseClaudeProfileBody(body []byte, entry *usageEntry) {
	var doc struct {
		Account struct {
			Email        string `json:"email"`
			HasClaudeMax bool   `json:"has_claude_max"`
			HasClaudePro bool   `json:"has_claude_pro"`
		} `json:"account"`
		Organization struct {
			OrganizationType string `json:"organization_type"`
			RateLimitTier    string `json:"rate_limit_tier"`
		} `json:"organization"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		entry.Err = "parse profile: " + err.Error()
		return
	}
	if strings.TrimSpace(doc.Account.Email) != "" {
		entry.Email = doc.Account.Email
	}
	plan := claudePlanFromProfile(doc.Organization.RateLimitTier, doc.Organization.OrganizationType, doc.Account.HasClaudeMax, doc.Account.HasClaudePro)
	if plan != "" {
		entry.Plan = plan
		entry.Weight = claudePlanWeight(plan)
	}
}

// claudePlanFromProfile 把 profile 字段归一为套餐键。
// claudeUsageWindowJSON 是 Claude usage 接口的单窗口结构。
type claudeUsageWindowJSON struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

func (w claudeUsageWindowJSON) toWindow() usageWindow {
	out := usageWindow{UsedPercent: w.Utilization, HasData: true}
	if strings.TrimSpace(w.ResetsAt) != "" {
		if t, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
			out.ResetAt = t
		}
	}
	return out
}

// parseClaudeUsageBody 解析 Claude 额度：five_hour / seven_day / seven_day_sonnet。
func parseClaudeUsageBody(body []byte, entry *usageEntry) {
	var doc struct {
		FiveHour       *claudeUsageWindowJSON `json:"five_hour"`
		SevenDay       *claudeUsageWindowJSON `json:"seven_day"`
		SevenDaySonnet *claudeUsageWindowJSON `json:"seven_day_sonnet"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		if entry.Err == "" {
			entry.Err = "parse usage: " + err.Error()
		}
		return
	}
	if doc.FiveHour != nil {
		entry.Primary = doc.FiveHour.toWindow()
	}
	if doc.SevenDay != nil {
		entry.Secondary = doc.SevenDay.toWindow()
	}
	if doc.SevenDaySonnet != nil {
		entry.SonnetSecondary = doc.SevenDaySonnet.toWindow()
		entry.HasSonnet = true
	}
}

func claudePlanFromProfile(tier, orgType string, hasMax, hasPro bool) string {
	tier = strings.ToLower(strings.TrimSpace(tier))
	switch {
	case strings.Contains(tier, "max_20x"):
		return "max_20x"
	case strings.Contains(tier, "max_5x") || strings.Contains(tier, "max"):
		return "max_5x"
	case strings.Contains(tier, "pro"):
		return "pro"
	}
	if hasMax {
		return "max_5x"
	}
	if hasPro {
		return "pro"
	}
	if strings.Contains(strings.ToLower(orgType), "max") {
		return "max_5x"
	}
	return ""
}

func codexPlanWeight(plan string) float64 {
	if w, ok := codexPlanMultiplier[strings.ToLower(strings.TrimSpace(plan))]; ok {
		return w
	}
	return 1
}

func claudePlanWeight(plan string) float64 {
	if w, ok := claudePlanMultiplier[strings.ToLower(strings.TrimSpace(plan))]; ok {
		return w
	}
	return 1
}
