package main

const (
	pluginID           = "cpa-window-primer"
	pluginName         = "CPA Window Primer"
	pluginVersion      = "0.2.13"
	pluginAuthor       = "junxin367"
	pluginRepository   = "https://github.com/junxin367/cpa-window-primer"
	primerHeader       = "X-CPA-Window-Primer-Auth-ID"
	defaultModel       = "gpt-5.4, claude-sonnet-4-6"
	defaultPrompt      = "hi"
	defaultMinInterval = "5h"
	defaultTick        = "5s"
	defaultLead        = "1m"
)

const (
	// 额度查询上游接口。
	codexUsageURL  = "https://chatgpt.com/backend-api/wham/usage"
	claudeProfileURL = "https://api.anthropic.com/api/oauth/profile"
	claudeUsageURL   = "https://api.anthropic.com/api/oauth/usage"
	claudeOAuthBeta  = "oauth-2025-04-20"
)

// 套餐 → 相对 Plus 的额度倍数。Codex 管理中心显示：plus = 1，pro = Pro 20x，prolite = Pro 5x。
// Claude：Max = 5 个 Pro。这里以 Plus 为基准单位，数值可后续调整。
var codexPlanMultiplier = map[string]float64{
	"free":     0,
	"plus":     1,
	"pro":      20,
	"prolite":  5,
	"pro-lite": 5,
	"pro_lite": 5,
	"pro_20x":  20,
	"team":     1,
	"business": 1,
}

// Claude 套餐倍数，以 Pro 为 1 个单位，Max = 5。
var claudePlanMultiplier = map[string]float64{
	"free":     0,
	"pro":      1,
	"max":      5,
	"max_5x":   5,
	"max_20x":  20,
}

var defaultTimes = []string{"07:00", "12:00", "17:00"}
