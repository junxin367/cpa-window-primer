package main

import (
	"fmt"
	"html"
	"strings"
)

// warmupMessage 返回给用户的中文消息，原始错误仍保存在 error 中供排查。
func warmupMessage(record attemptRecord) string {
	if record.Success {
		return "预热成功。"
	}
	// 只解释最终失败，避免早先的模型兜底错误掩盖最终的认证或网络错误。
	raw, _, _ := strings.Cut(record.Error, "; 模型兜底记录:")
	text := strings.ToLower(html.UnescapeString(raw + " " + record.ResponseSummary))
	has := func(markers ...string) bool {
		for _, marker := range markers {
			if strings.Contains(text, marker) {
				return true
			}
		}
		return false
	}
	model := "所选模型"
	if strings.TrimSpace(record.Model) != "" {
		model = fmt.Sprintf("模型「%s」", strings.TrimSpace(record.Model))
	}
	switch {
	case has("selected model is at capacity", "model is at capacity"):
		return "预热失败：" + model + "暂时繁忙，请稍后重试或更换模型。"
	case has("model_not_found", "model not found", "unknown provider for model", "unknown model", "unsupported model", "invalid model", "not a valid model", "no such model"):
		return "预热失败：" + model + "不可用。请在“预热设置 → 模型”中改为 CPA 当前提供且该账号支持的模型，然后重试。"
	case has("quota_blocked", "usage_limit_reached", "insufficient_quota", "quota exhausted", "quota exceeded", "额度已满", "限额已满") || record.StatusCode == 402:
		return "预热失败：该账号额度不足，额度恢复后可重试。后台会定期复查额度。"
	case has("rate_limit", "rate limit", "too many requests") || record.StatusCode == 429:
		return "预热失败：请求过于频繁，请稍后重试。"
	case has("min_interval_not_met"):
		return "暂未预热：距离上次成功预热尚未达到最小间隔，请等待冷却结束。"
	case has("warmup_already_running"):
		return "该账号正在预热，请等待本次请求完成。"
	case has("target_auth_not_candidate"):
		return "预热失败：所选认证文件无法使用该模型，请更换该账号支持的模型后重试。"
	case has("target_auth_not_used", "target_auth_verify_failed"):
		return "预热失败：无法确认请求由所选认证文件完成，请检查 CPA 认证文件和调度状态。"
	case has("auth_target_unavailable", "access token not found"):
		return "预热失败：所选认证文件不可用或缺少登录凭据，请检查认证文件或重新登录。"
	case has("invalid_api_key", "unauthorized", "token_expired", "token expired") || record.StatusCode == 401:
		return "预热失败：认证凭据无效或已过期，请重新登录该账号后重试。"
	case has("forbidden", "permission_denied") || record.StatusCode == 403:
		return "预热失败：请求被拒绝，请检查该账号的模型权限和访问限制。"
	case has("timeout", "timed out", "deadline exceeded") || record.StatusCode == 504:
		return "预热失败：请求超时，请检查网络或代理后重试。"
	case has("target_auth_proxy_invalid"):
		return "预热失败：认证文件的代理配置无效，请修正代理地址后重试。"
	case has("codex_warmup_stream_incomplete", "codex_warmup_stream_error"):
		return "预热失败：模型未正常完成响应，请稍后重试。"
	case has("codex_warmup_stream_unavailable"):
		return "预热失败：CPA 无法建立流式请求，请检查宿主服务状态。"
	case has("codex_warmup_response_too_large"):
		return "预热失败：模型响应过大，请缩短预热内容后重试。"
	case has("codex_warmup_request_failed", "connection refused", "no such host"):
		return "预热失败：无法连接模型服务，请检查网络或代理后重试。"
	default:
		return "预热失败：服务未能完成请求。请检查模型、认证文件和网络配置后重试，悬停最近预热记录可查看原始错误。"
	}
}
