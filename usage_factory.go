package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

type factoryQuotaPool struct {
	Primary   usageWindow
	Secondary usageWindow
	Monthly   usageWindow
}

type factoryUsageData struct {
	Core         factoryQuotaPool
	ExtraBalance *float64
	ExtraAllowed bool
	ModelRoutes  map[string]warmupModelRoute
	Prefix       string
}

// 默认只允许官方区域地址；独立集成构建可通过 Go 链接参数指定本机模拟上游。
var factoryUsageBaseURL = "https://api.factory.ai"
var factoryEUUsageBaseURL = "https://api.eu.factory.ai"

func fetchFactoryUsage(authID, authIndex, email string) usageEntry {
	entry := usageEntry{AuthID: authID, Provider: factoryProvider, Email: email, Weight: 1}
	if strings.TrimSpace(authIndex) == "" {
		authIndex = authID
	}
	doc, err := authDocumentForAuth(authIndex)
	if err != nil {
		entry.Err = "无法读取 Factory 凭证"
		return entry
	}
	if stringValue(doc["type"]) != factoryProvider || doc["deleted"] == true || doc["disabled"] == true {
		entry.Err = "Factory 凭证类型不匹配或已禁用"
		return entry
	}
	token := stringValue(doc["api_key"])
	oauth := stringValue(doc["auth_method"]) == "oauth"
	if oauth {
		token = stringValue(doc["access_token"])
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		entry.Err = "Factory 凭证缺少有效的 API Key 或 OAuth 令牌"
		return entry
	}
	baseURL := factoryUsageBaseURL
	if strings.EqualFold(stringValue(doc["region"]), "eu") {
		baseURL = factoryEUUsageBaseURL
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("Accept", "*/*")
	headers.Set("User-Agent", "Bun/1.4.4")
	headers.Set("X-Factory-Client", "cli")
	headers.Set("X-Client-Version", "1.2.0")
	var trace [24]byte
	if _, err := rand.Read(trace[:]); err == nil {
		headers.Set("traceparent", fmt.Sprintf("00-%x-%x-01", trace[:16], trace[16:]))
	}
	if org := stringValue(doc["org_id"]); oauth && validFactoryOrgID(org) {
		headers.Set("X-Factory-Org-Id", org)
	}
	// 只使用固定的官方区域域名；存储 JSON 中的任意 URL 不接收凭据。
	response, err := callHostHTTPDo(http.MethodGet, baseURL+"/api/billing/limits", headers, nil)
	if err != nil {
		entry.Err = "Factory 额度查询失败，请检查 CPA 出站网络"
		return entry
	}
	if response.StatusCode != http.StatusOK {
		entry.Err = fmt.Sprintf("Factory 额度查询失败（HTTP %d）", response.StatusCode)
		return entry
	}
	if err := parseFactoryUsageBody(response.Body, &entry, time.Now()); err != nil {
		entry.Err = "Factory 额度响应缺少有效数据"
		return entry
	}
	entry.Factory.ModelRoutes = factoryCatalogRoutes(doc)
	entry.Factory.Prefix = stringValue(doc["prefix"])
	return entry
}

func validFactoryOrgID(org string) bool {
	if org == "" || len(org) > 200 {
		return false
	}
	for _, r := range org {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func parseFactoryUsageBody(body []byte, entry *usageEntry, now time.Time) error {
	var response struct {
		Limits map[string]map[string]struct {
			UsedPercent      *float64        `json:"usedPercent"`
			WindowEnd        json.RawMessage `json:"windowEnd"`
			SecondsRemaining *int64          `json:"secondsRemaining"`
		} `json:"limits"`
		ExtraBalance *float64 `json:"extraUsageBalanceCents"`
		ExtraAllowed bool     `json:"extraUsageAllowed"`
		Plan         string   `json:"planType"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	// 按完整响应一次性替换；解析失败不会留下可误用的部分结果。
	standard, core := factoryQuotaPool{}, factoryQuotaPool{}
	for name, pool := range map[string]*factoryQuotaPool{"standard": &standard, "core": &core} {
		for key, target := range map[string]*usageWindow{"fiveHour": &pool.Primary, "weekly": &pool.Secondary, "monthly": &pool.Monthly} {
			raw, exists := response.Limits[name][key]
			if !exists || raw.UsedPercent == nil || math.IsNaN(*raw.UsedPercent) || math.IsInf(*raw.UsedPercent, 0) || *raw.UsedPercent < 0 {
				continue
			}
			reset := factoryResetTime(raw.WindowEnd)
			if reset.IsZero() && raw.SecondsRemaining != nil && *raw.SecondsRemaining >= 0 && *raw.SecondsRemaining <= 40*86400 {
				reset = now.Add(time.Duration(*raw.SecondsRemaining) * time.Second)
			}
			if !reset.IsZero() && !reset.After(now) {
				continue
			}
			*target = usageWindow{HasData: true, UsedPercent: *raw.UsedPercent, ResetAt: reset}
		}
	}
	balance := response.ExtraBalance
	if balance != nil {
		if math.IsNaN(*balance) || math.IsInf(*balance, 0) || *balance < 0 {
			balance = nil
		} else {
			usd := *balance / 100
			balance = &usd
		}
	}
	if !factoryPoolHasData(standard) && !factoryPoolHasData(core) && balance == nil {
		return fmt.Errorf("missing Factory quota")
	}
	entry.Primary, entry.Secondary, entry.Monthly = standard.Primary, standard.Secondary, standard.Monthly
	entry.Plan = response.Plan
	entry.Factory = &factoryUsageData{Core: core, ExtraBalance: balance, ExtraAllowed: response.ExtraAllowed}
	return nil
}

func factoryResetTime(raw json.RawMessage) time.Time {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return time.Time{}
	}
	if text, ok := value.(string); ok {
		if parsed, err := time.Parse(time.RFC3339, text); err == nil {
			return parsed
		}
	}
	if number := getAnyFloat(value); number != nil && *number > 0 && *number < 1e15 {
		return time.UnixMilli(int64(*number))
	}
	return time.Time{}
}

func factoryPoolHasData(pool factoryQuotaPool) bool {
	return pool.Primary.HasData || pool.Secondary.HasData || pool.Monthly.HasData
}

func factoryQuotaModelPools(entry usageEntry, models []string, saved warmupModelRoute) []string {
	if entry.Factory == nil {
		return nil
	}
	pools := make([]string, 0, len(models))
	for _, model := range models {
		route, err := resolveFactoryModel(model, entry.Factory.Prefix, entry.Factory.ModelRoutes, saved)
		if err != nil {
			pools = append(pools, "unknown")
		} else {
			pools = append(pools, route.QuotaPool)
		}
	}
	return pools
}

func factoryQuotaBlocked(entry usageEntry, pools []string, now time.Time) usageQuotaBlock {
	standard := factoryQuotaPool{entry.Primary, entry.Secondary, entry.Monthly}
	block := factoryPoolBlock(standard, now)
	if !block.Blocked || entry.Factory == nil {
		return block
	}
	if entry.Factory.ExtraAllowed && entry.Factory.ExtraBalance != nil && *entry.Factory.ExtraBalance > 0 {
		return usageQuotaBlock{}
	}
	for _, pool := range pools {
		if pool != "core" {
			return block
		}
	}
	// Core 模型先使用 Standard，Standard 用完后才使用已知的 Core 额度。
	if len(pools) > 0 && factoryPoolHasData(entry.Factory.Core) {
		return factoryPoolBlock(entry.Factory.Core, now)
	}
	return block
}

func factoryPoolBlock(pool factoryQuotaPool, now time.Time) usageQuotaBlock {
	for _, item := range []struct {
		window usageWindow
		reason string
	}{{pool.Monthly, "30天额度已满"}, {pool.Secondary, "周限额已满"}, {pool.Primary, "5小时额度已满"}} {
		if block := quotaBlockForWindow(item.window, item.reason, now); block.Blocked {
			return block
		}
	}
	return usageQuotaBlock{}
}
