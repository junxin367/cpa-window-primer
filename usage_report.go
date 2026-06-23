package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// collectUsage 拉取所有已选认证文件的额度，按 provider 分组返回。
func (a *app) collectUsage() []usageEntry {
	cfg, _, _ := a.snapshot()
	selected := map[string]bool{}
	for _, id := range cfg.AuthIDs {
		selected[id] = true
	}
	auths, err := callHostAuthList()
	if err != nil {
		return []usageEntry{{Err: err.Error()}}
	}
	out := make([]usageEntry, 0, len(auths))
	for _, auth := range auths {
		if !isManagedOAuthAuth(auth) {
			continue
		}
		id := authIDForEntry(auth)
		if id == "" {
			continue
		}
		// 仅统计已选择的认证文件。
		if len(selected) > 0 && !selected[id] {
			continue
		}
		// 无额度/已禁用的忽略。
		if auth.Disabled {
			continue
		}
		provider := classifyProvider(auth.Provider, auth.Type)
		switch provider {
		case "codex":
		case "claude":
			out = append(out, fetchCodexUsage(id, strings.TrimSpace(auth.AuthIndex), auth.Email))
		case "claude":
			out = append(out, fetchClaudeUsage(id, strings.TrimSpace(auth.AuthIndex), auth.Email))
		}
	}
	return out
}

func classifyProvider(provider, typ string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	t := strings.ToLower(strings.TrimSpace(typ))
	if p == "codex" || t == "codex" || p == "openai" || t == "openai" {
		return "codex"
	}
	if p == "claude" || p == "anthropic" || t == "claude" || t == "anthropic" {
		return "claude"
	}
	return ""
}

// aggregateGroup 汇总同一 provider 下多个认证文件的额度。
// 规则：按套餐倍数加权；周限额未用完时把 5h 计入总额度，周用完则 5h 按 0 算。
type aggregateResult struct {
	HasData         bool
	PrimaryPercent  float64
	PrimaryReset    time.Time
	SecondaryPercent float64
	SecondaryReset  time.Time
}

func aggregateGroup(entries []usageEntry, sonnet bool) aggregateResult {
	var (
		primaryUsedWeighted    float64
		primaryTotalWeight     float64
		secondaryUsedWeighted  float64
		secondaryTotalWeight   float64
		latestPrimaryReset     time.Time
		latestSecondaryReset   time.Time
		any                    bool
	)
	for _, e := range entries {
		if e.Err != "" || e.Weight <= 0 {
			continue
		}
		primary := e.Primary
		secondary := e.Secondary
		if sonnet {
			if !e.HasSonnet {
				continue
			}
			primary = e.SonnetPrimary
			secondary = e.SonnetSecondary
		}
		// 周限额加权。
		if secondary.HasData {
			secondaryUsedWeighted += secondary.UsedPercent * e.Weight
			secondaryTotalWeight += e.Weight
			if secondary.ResetAt.After(latestSecondaryReset) {
				latestSecondaryReset = secondary.ResetAt
			}
			any = true
		}
		// 5h 限额：周用完（100%）时按 0 可用计入，即贡献 100% used。
		if primary.HasData {
			effectivePrimary := primary.UsedPercent
			if secondary.HasData && secondary.UsedPercent >= 100 {
				effectivePrimary = 100
			}
			primaryUsedWeighted += effectivePrimary * e.Weight
			primaryTotalWeight += e.Weight
			if primary.ResetAt.After(latestPrimaryReset) {
				latestPrimaryReset = primary.ResetAt
			}
			any = true
		}
	}
	res := aggregateResult{HasData: any}
	if primaryTotalWeight > 0 {
		res.PrimaryPercent = primaryUsedWeighted / primaryTotalWeight
		res.PrimaryReset = latestPrimaryReset
	}
	if secondaryTotalWeight > 0 {
		res.SecondaryPercent = secondaryUsedWeighted / secondaryTotalWeight
		res.SecondaryReset = latestSecondaryReset
	}
	return res
}

func filterProvider(entries []usageEntry, provider string) []usageEntry {
	out := make([]usageEntry, 0, len(entries))
	for _, e := range entries {
		if e.Provider == provider {
			out = append(out, e)
		}
	}
	return out
}

// buildUsageMessage 生成企微 markdown 文本。
func buildUsageMessage(entries []usageEntry) string {
	var b strings.Builder
	b.WriteString("# 额度汇总\n")
	b.WriteString("> 更新时间：<font color=\"comment\">" + time.Now().Format("2006-01-02 15:04") + "</font>\n")

	codex := filterProvider(entries, "codex")
	if len(codex) > 0 {
		b.WriteString("\n**🤖 GPT**\n")
		writeGroup(&b, aggregateGroup(codex, false), nil)
	}

	claude := filterProvider(entries, "claude")
	if len(claude) > 0 {
		b.WriteString("\n**🧠 CLAUDE**\n")
		sonnet := aggregateGroup(claude, true)
		var extra []usageLineItem
		if sonnet.HasData {
			extra = append(extra, usageLineItem{"Sonnet周限额", sonnet.SecondaryPercent, sonnet.SecondaryReset})
		}
		writeGroup(&b, aggregateGroup(claude, false), extra)
	}

	// 错误提示。
	var errs []string
	for _, e := range entries {
		if e.Err != "" {
			label := e.Email
			if label == "" {
				label = e.AuthID
			}
			errs = append(errs, fmt.Sprintf("%s: %s", label, e.Err))
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		b.WriteString("\n> <font color=\"warning\">部分认证文件读取失败</font>\n")
		for _, e := range errs {
			b.WriteString("> " + e + "\n")
		}
	}
	return b.String()
}

// usageLineItem 是一行额度明细。
type usageLineItem struct {
	Label   string
	Percent float64
	Reset   time.Time
}

func writeGroup(b *strings.Builder, agg aggregateResult, extra []usageLineItem) {
	if !agg.HasData {
		b.WriteString("> <font color=\"comment\">暂无额度数据</font>\n")
		return
	}
	writeUsageLine(b, "5小时限额", agg.PrimaryPercent, agg.PrimaryReset)
	writeUsageLine(b, "周限额", agg.SecondaryPercent, agg.SecondaryReset)
	for _, item := range extra {
		writeUsageLine(b, item.Label, item.Percent, item.Reset)
	}
}

// writeUsageLine 输出一行：名称 + 进度条 + 着色百分比 + 刷新时间。
func writeUsageLine(b *strings.Builder, label string, percent float64, reset time.Time) {
	color := usageColor(percent)
	line := fmt.Sprintf("> %s `%s` <font color=\"%s\">%s</font>", label, usageBar(percent), color, percentText(percent))
	if r := resetText(reset); r != "" {
		line += "  <font color=\"comment\">" + r + " 重置</font>"
	}
	b.WriteString(line + "\n")
}

// usageColor 按使用率选颜色：低=绿、中=橙、高=红。
func usageColor(percent float64) string {
	switch {
	case percent >= 90:
		return "warning"
	case percent >= 60:
		return "comment"
	default:
		return "info"
	}
}

// usageBar 生成 10 格进度条。
func usageBar(percent float64) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := int(math.Round(percent / 10))
	if filled > 10 {
		filled = 10
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
}

func percentText(used float64) string {
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	return fmt.Sprintf("%d%%", int(math.Round(used)))
}

func resetText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("01/02 15:04")
}

// pushUsage 采集额度并推送到 webhook。
// runUsagePushDue 在到达推送时钟的当分钟内触发一次额度推送。
func (a *app) runUsagePushDue(now time.Time) {
	cfg, _, _ := a.snapshot()
	if !cfg.UsagePushEnabled || len(cfg.UsagePushClocks) == 0 {
		return
	}
	if strings.TrimSpace(cfg.WebhookURL) == "" {
		return
	}
	for _, clock := range cfg.UsagePushClocks {
		if now.Hour() != clock.Hour || now.Minute() != clock.Minute {
			continue
		}
		key := now.Format("2006-01-02") + "T" + clock.String()
		a.mu.Lock()
		if a.lastUsagePushKey == key {
			a.mu.Unlock()
			return
		}
		a.lastUsagePushKey = key
		a.mu.Unlock()
		if err := a.pushUsage(); err != nil {
			a.setLastError("额度推送失败：" + err.Error())
		}
		return
	}
}

func (a *app) pushUsage() error {
	cfg, _, _ := a.snapshot()
	webhook := strings.TrimSpace(cfg.WebhookURL)
	if webhook == "" {
		return fmt.Errorf("webhook 地址未配置")
	}
	entries := a.collectUsage()
	message := buildUsageMessage(entries)
	return sendWebhook(webhook, message)
}

// sendWebhook 以企微机器人 markdown 格式推送。
func sendWebhook(webhook, content string) error {
	payload := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": content},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	resp, err := callHostHTTPDo(http.MethodPost, webhook, headers, raw)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook 返回 %d: %s", resp.StatusCode, truncateBody(resp.Body))
	}
	return nil
}

func truncateBody(body []byte) string {
	s := strings.TrimSpace(string(bytes.TrimSpace(body)))
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// usageSnapshotView 是额度预览的返回结构。
type usageSnapshotView struct {
	GeneratedAt string             `json:"generated_at"`
	Message     string             `json:"message"`
	Groups      []usageGroupView   `json:"groups"`
	Errors      []string           `json:"errors,omitempty"`
}

type usageGroupView struct {
	Provider         string  `json:"provider"`
	Label            string  `json:"label"`
	PrimaryPercent   float64 `json:"primary_percent"`
	PrimaryReset     string  `json:"primary_reset,omitempty"`
	SecondaryPercent float64 `json:"secondary_percent"`
	SecondaryReset   string  `json:"secondary_reset,omitempty"`
	HasData          bool    `json:"has_data"`
}

func (a *app) usageSnapshot() usageSnapshotView {
	entries := a.collectUsage()
	view := usageSnapshotView{
		GeneratedAt: time.Now().Format("2006-01-02 15:04"),
		Message:     buildUsageMessage(entries),
	}
	if codex := filterProvider(entries, "codex"); len(codex) > 0 {
		view.Groups = append(view.Groups, groupView("codex", "GPT", aggregateGroup(codex, false)))
	}
	if claude := filterProvider(entries, "claude"); len(claude) > 0 {
		view.Groups = append(view.Groups, groupView("claude", "CLAUDE", aggregateGroup(claude, false)))
		if sonnet := aggregateGroup(claude, true); sonnet.HasData {
			view.Groups = append(view.Groups, groupView("claude", "Sonnet", sonnet))
		}
	}
	for _, e := range entries {
		if e.Err != "" {
			label := e.Email
			if label == "" {
				label = e.AuthID
			}
			view.Errors = append(view.Errors, label+": "+e.Err)
		}
	}
	return view
}

func groupView(provider, label string, agg aggregateResult) usageGroupView {
	return usageGroupView{
		Provider:         provider,
		Label:            label,
		PrimaryPercent:   agg.PrimaryPercent,
		PrimaryReset:     resetText(agg.PrimaryReset),
		SecondaryPercent: agg.SecondaryPercent,
		SecondaryReset:   resetText(agg.SecondaryReset),
		HasData:          agg.HasData,
	}
}
