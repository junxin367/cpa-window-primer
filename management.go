package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type managementRegistrationResponse struct {
	Routes    []managementRoute `json:"routes,omitempty"`
	Resources []resourceRoute   `json:"resources,omitempty"`
}

type managementRoute struct {
	Method      string `json:"Method"`
	Path        string `json:"Path"`
	Menu        string `json:"Menu,omitempty"`
	Description string `json:"Description,omitempty"`
}

type resourceRoute struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu"`
	Description string `json:"Description"`
}

type managementRequest struct {
	Method         string
	Path           string
	Headers        http.Header
	Query          url.Values
	Body           []byte
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type managementResponse struct {
	StatusCode int         `json:"StatusCode"`
	Headers    http.Header `json:"Headers"`
	Body       []byte      `json:"Body"`
}

type managementSnapshot struct {
	Config    pluginConfig         `json:"config"`
	Auths     []managementAuthView `json:"auths"`
	State     *pluginState         `json:"state"`
	LastError string               `json:"last_error,omitempty"`
}

type managementAuthView struct {
	ID                string     `json:"id"`
	AuthIndex         string     `json:"auth_index,omitempty"`
	Name              string     `json:"name"`
	Type              string     `json:"type"`
	Provider          string     `json:"provider,omitempty"`
	Status            string     `json:"status,omitempty"`
	StatusMessage     string     `json:"status_message,omitempty"`
	Email             string     `json:"email,omitempty"`
	Label             string     `json:"label,omitempty"`
	Source            string     `json:"source,omitempty"`
	Selectable        bool       `json:"selectable"`
	Unavailable       bool       `json:"unavailable,omitempty"`
	Disabled          bool       `json:"disabled,omitempty"`
	NextRetryAfter    *time.Time `json:"next_retry_after,omitempty"`
	QuotaBlockedUntil *time.Time `json:"quota_blocked_until,omitempty"`
	BlockedReason     string     `json:"blocked_reason,omitempty"`
}

func managementRegistration() managementRegistrationResponse {
	return managementRegistrationResponse{
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: "/" + pluginID + "/snapshot", Description: "返回窗口预热插件的配置、认证文件和运行状态。"},
			{Method: http.MethodGet, Path: "/" + pluginID + "/config", Description: "返回窗口预热插件配置。"},
			{Method: http.MethodPut, Path: "/" + pluginID + "/config", Description: "更新窗口预热插件配置。"},
			{Method: http.MethodGet, Path: "/" + pluginID + "/state", Description: "返回窗口预热插件运行状态。"},
			{Method: http.MethodPost, Path: "/" + pluginID + "/run", Description: "手动执行一次窗口预热请求。"},
			{Method: http.MethodGet, Path: "/" + pluginID + "/usage", Description: "采集并返回额度汇总。"},
			{Method: http.MethodPost, Path: "/" + pluginID + "/usage-push", Description: "立即推送一次额度汇总到 webhook。"},
		},
		Resources: []resourceRoute{{
			Path:        "/status",
			Menu:        "窗口预热",
			Description: "配置认证文件、发送窗口，并查看最近预热结果。",
		}},
	}
}

func (a *app) handleManagement(raw []byte) ([]byte, error) {
	var req managementRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	path := strings.TrimRight(strings.TrimSpace(req.Path), "/")
	if method == "" {
		method = http.MethodGet
	}
	switch {
	case method == http.MethodGet && (strings.HasSuffix(path, "/status") || path == ""):
		return okEnvelope(htmlResponse(http.StatusOK, a.renderStatusPage()))
	case method == http.MethodGet && strings.HasSuffix(path, "/snapshot"):
		return okEnvelope(jsonResponse(http.StatusOK, a.managementSnapshot()))
	case method == http.MethodGet && strings.HasSuffix(path, "/config"):
		cfg, _, _ := a.snapshot()
		return okEnvelope(jsonResponse(http.StatusOK, cfg.pluginConfig))
	case method == http.MethodPut && strings.HasSuffix(path, "/config"):
		cfg, err := a.updateConfigFromBody(req.Body)
		if err != nil {
			return okEnvelope(jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()}))
		}
		return okEnvelope(jsonResponse(http.StatusOK, cfg.pluginConfig))
	case method == http.MethodGet && strings.HasSuffix(path, "/state"):
		_, state, lastErr := a.snapshot()
		return okEnvelope(jsonResponse(http.StatusOK, map[string]any{"state": state, "last_error": lastErr}))
	case method == http.MethodPost && strings.HasSuffix(path, "/run"):
		return a.handleManualRun(req)
	case method == http.MethodGet && strings.HasSuffix(path, "/usage"):
		return okEnvelope(jsonResponse(http.StatusOK, a.usageSnapshot()))
	case method == http.MethodPost && strings.HasSuffix(path, "/usage-push"):
		if err := a.pushUsage(); err != nil {
			return okEnvelope(jsonResponse(http.StatusBadGateway, map[string]string{"error": err.Error()}))
		}
		return okEnvelope(jsonResponse(http.StatusOK, map[string]string{"status": "ok"}))
	default:
		return okEnvelope(jsonResponse(http.StatusNotFound, map[string]string{"error": "not found"}))
	}
}

func (a *app) handleManualRun(req managementRequest) ([]byte, error) {
	var body struct {
		AuthID string `json:"auth_id"`
		Force  bool   `json:"force"`
	}
	if len(req.Body) > 0 {
		if err := json.Unmarshal(req.Body, &body); err != nil {
			return okEnvelope(jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error(), "msg": "预热失败：请求参数格式不正确，请刷新页面后重试。"}))
		}
	}
	if body.AuthID == "" && req.Query != nil {
		body.AuthID = strings.TrimSpace(req.Query.Get("auth_id"))
		body.Force = strings.EqualFold(strings.TrimSpace(req.Query.Get("force")), "true")
	}
	body.AuthID = strings.TrimSpace(body.AuthID)
	if body.AuthID == "" {
		return okEnvelope(jsonResponse(http.StatusBadRequest, map[string]string{"error": "auth_id is required", "msg": "请选择需要预热的认证文件。"}))
	}
	cfg, _, _ := a.snapshot()
	authID, err := normalizeWarmupAuthID(body.AuthID)
	if err != nil {
		if !body.Force {
			return okEnvelope(jsonResponse(http.StatusBadGateway, map[string]string{"error": err.Error(), "msg": "预热失败：无法读取 CPA 认证文件，请检查服务连接后重试。"}))
		}
		authID = body.AuthID
	}
	if !body.Force {
		allowed, err := schedulableAuthIDs()
		if err != nil {
			return okEnvelope(jsonResponse(http.StatusBadGateway, map[string]string{"error": err.Error(), "msg": "预热失败：无法读取 CPA 认证文件，请检查服务连接后重试。"}))
		}
		if _, ok := allowed[authID]; !ok {
			return okEnvelope(jsonResponse(http.StatusBadRequest, map[string]string{"error": "auth_id is not available for warmup", "msg": "预热失败：所选认证文件不存在或已禁用，请重新选择可用的认证文件。"}))
		}
	}
	record := a.executeWarmup(authID, "manual-"+time.Now().Format("20060102T150405"), cfg, req.HostCallbackID, body.Force)
	record.Msg = warmupMessage(record)
	status := http.StatusOK
	if !record.Success {
		status = http.StatusBadGateway
	}
	return okEnvelope(jsonResponse(status, record))
}

func normalizeWarmupAuthID(raw string) (string, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", nil
	}
	auths, err := callHostAuthList()
	if err != nil {
		return "", err
	}
	for _, auth := range auths {
		if authEntrySelected(map[string]bool{target: true}, auth) {
			if id := strings.TrimSpace(auth.ID); id != "" {
				return id, nil
			}
			return authIDForEntry(auth), nil
		}
	}
	return target, nil
}

func htmlResponse(statusCode int, body []byte) managementResponse {
	return managementResponse{
		StatusCode: statusCode,
		Headers: http.Header{
			"content-type": []string{"text/html; charset=utf-8"},
		},
		Body: body,
	}
}

func jsonResponse(statusCode int, value any) managementResponse {
	raw, _ := json.MarshalIndent(value, "", "  ")
	return managementResponse{
		StatusCode: statusCode,
		Headers: http.Header{
			"content-type": []string{"application/json; charset=utf-8"},
		},
		Body: raw,
	}
}

func (a *app) managementSnapshot() managementSnapshot {
	a.recheckQuotaBlocks(time.Now())
	cfg, state, lastErr := a.snapshot()
	auths, err := callHostAuthList()
	if err != nil {
		lastErr = err.Error()
	}
	now := time.Now()
	rows := make([]managementAuthView, 0, len(auths))
	for _, auth := range auths {
		if !isManagedOAuthAuth(auth) {
			continue
		}
		id := authIDForEntry(auth)
		if id == "" {
			continue
		}
		// 刚查询到的额度结果优先于宿主尚未清理的额度冷却缓存。
		// 禁用、认证失败等非额度错误仍由宿主决定。
		item := state.Auths[id]
		if !auth.Disabled && state.quotaRecentlyAvailable(id, now) &&
			!item.QuotaCheckedAt.Before(auth.UpdatedAt) &&
			(quotaLikeText(auth.StatusMessage) || (strings.TrimSpace(auth.StatusMessage) == "" && auth.NextRetryAfter.After(now))) {
			auth.NextRetryAfter = time.Time{}
			auth.Unavailable = false
			auth.Status = "active"
			auth.StatusMessage = ""
		}
		hostBlocked := isHostAuthQuotaBlocked(auth, now)
		quotaUntil, quotaReason, quotaBlocked := state.quotaBlockInfo(id, now)
		blockedReason := authBlockedReason(auth, hostBlocked, quotaBlocked, quotaReason)
		var nextRetryAfter *time.Time
		if auth.NextRetryAfter.After(now) {
			next := auth.NextRetryAfter
			nextRetryAfter = &next
		}
		var quotaBlockedUntil *time.Time
		if quotaBlocked && !quotaUntil.IsZero() {
			until := quotaUntil
			quotaBlockedUntil = &until
		}
		rows = append(rows, managementAuthView{
			ID:            id,
			AuthIndex:     auth.AuthIndex,
			Name:          auth.Name,
			Type:          formatAuthType(auth),
			Provider:      auth.Provider,
			Status:        auth.Status,
			StatusMessage: auth.StatusMessage,
			Email:         auth.Email,
			Label:         auth.Label,
			Source:        auth.Source,
			// 仅“手动禁用”的认证文件不可勾选；无额度/限流只会让后台定时任务跳过，
			// 不影响在管理页继续勾选、保存与手动预热。
			Selectable:        !auth.Disabled,
			Unavailable:       auth.Unavailable,
			Disabled:          auth.Disabled,
			NextRetryAfter:    nextRetryAfter,
			QuotaBlockedUntil: quotaBlockedUntil,
			BlockedReason:     blockedReason,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		left := strings.ToLower(rows[i].Email + rows[i].Name + rows[i].ID)
		right := strings.ToLower(rows[j].Email + rows[j].Name + rows[j].ID)
		return left < right
	})
	return managementSnapshot{
		Config:    cfg.pluginConfig,
		Auths:     rows,
		State:     state,
		LastError: lastErr,
	}
}

func authBlockedReason(auth pluginapi.HostAuthFileEntry, hostBlocked, quotaBlocked bool, quotaReason string) string {
	if auth.Disabled {
		return "认证文件已禁用"
	}
	if quotaBlocked {
		if strings.TrimSpace(quotaReason) != "" {
			return strings.TrimSpace(quotaReason)
		}
		return "无额度或限流中"
	}
	if auth.NextRetryAfter.After(time.Now()) {
		return "认证文件冷却中"
	}
	if auth.Unavailable {
		if strings.TrimSpace(auth.StatusMessage) != "" {
			return strings.TrimSpace(auth.StatusMessage)
		}
		return "认证文件当前不可用"
	}
	if hostBlocked {
		if strings.TrimSpace(auth.StatusMessage) != "" {
			return strings.TrimSpace(auth.StatusMessage)
		}
		return "疑似无额度或限流中"
	}
	return ""
}

func (a *app) renderStatusPage() []byte {
	rawData, err := json.Marshal(a.managementSnapshot())
	if err != nil {
		rawData = []byte(`{"config":{"enabled":true,"auth_ids":[],"times":["07:00","12:00","17:00"],"model":"gpt-6-luna, claude-sonnet-4-6","prompt":"hi","min_interval":"5h","lead_time":"1m","tick_interval":"5s"},"auths":[],"state":{"auths":{}}}`)
	}
	var out bytes.Buffer
	out.WriteString(`<!doctype html>
<html lang="zh-CN" style="background:#ffffff;color:#111827;">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light">
  <title>窗口预热</title>
  <style>
    :root {
      color-scheme: light;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #ffffff;
      color: #111827;
      letter-spacing: 0;
    }
    .cwp-body { margin: 0; background: #ffffff; color: #111827; }
    .cwp-page, .cwp-page * { box-sizing: border-box; }
    .cwp-page { max-width: 1480px; margin: 0 auto; padding: 24px; }
    .cwp-header { display: flex; align-items: end; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
    .cwp-page h1 { margin: 0; font-size: 24px; font-weight: 760; letter-spacing: 0; }
    .cwp-page h2 { margin: 0 0 14px; font-size: 15px; font-weight: 720; letter-spacing: 0; }
    .cwp-page h3 { margin: 0 0 10px; font-size: 13px; font-weight: 720; letter-spacing: 0; }
    .cwp-page label { display: grid; gap: 7px; font-size: 13px; font-weight: 650; min-width: 0; }
    .cwp-page input, .cwp-page select, .cwp-page textarea, .cwp-page button { font: inherit; }
    .cwp-page input, .cwp-page select, .cwp-page textarea {
      width: 100%;
      border: 1px solid color-mix(in srgb, CanvasText 18%, Canvas 82%);
      border-radius: 6px;
      padding: 9px 10px;
      background: Canvas;
      color: CanvasText;
    }
    .cwp-page input[type="checkbox"] { width: auto; }
    .cwp-page textarea { min-height: 46px; resize: vertical; line-height: 1.45; }
    .cwp-page button {
      border: 0;
      border-radius: 6px;
      padding: 9px 12px;
      background: #0f766e;
      color: #fff;
      font-weight: 720;
      cursor: pointer;
      white-space: nowrap;
    }
    .cwp-page button.cwp-secondary { background: color-mix(in srgb, CanvasText 10%, Canvas 90%); color: CanvasText; }
    .cwp-page button.cwp-warning { background: #b45309; }
    .cwp-page button:disabled { opacity: .54; cursor: not-allowed; }
    .cwp-header-actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: end; gap: 10px; }
    .cwp-metric {
      min-height: 34px;
      display: inline-flex;
      align-items: center;
      border-radius: 6px;
      padding: 6px 9px;
      font-size: 12px;
      font-weight: 700;
      background: color-mix(in srgb, #2563eb 12%, Canvas 88%);
      color: color-mix(in srgb, #2563eb 72%, CanvasText 28%);
    }
    .cwp-layout { display: flex; gap: 16px; align-items: flex-start; width: 100%; min-width: 0; }
    .cwp-main { flex: 1 1 auto; min-width: 0; display: grid; gap: 16px; }
    .cwp-side { flex: 0 0 330px; width: 330px; min-width: 0; display: grid; gap: 16px; }
    .cwp-panel {
      border: 1px solid color-mix(in srgb, CanvasText 14%, Canvas 86%);
      border-radius: 8px;
      padding: 16px;
      background: color-mix(in srgb, Canvas 96%, CanvasText 4%);
      min-width: 0;
    }
    .cwp-fields { display: grid; gap: 13px; }
    .cwp-settings-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 13px; align-items: start; }
    .cwp-model-field { grid-column: 1 / -1; }
    .cwp-wide-field { grid-column: 1 / -1; }
    .cwp-actions { display: flex; flex-wrap: wrap; gap: 9px; align-items: center; }
    .cwp-actions button { width: auto; }
    .cwp-page label.cwp-inline { display: flex; gap: 8px; align-items: center; }
    .cwp-page .cwp-inline input[type="checkbox"] { flex: 0 0 auto; width: auto; margin: 0; }
    .cwp-muted { color: color-mix(in srgb, CanvasText 62%, Canvas 38%); font-size: 12px; font-weight: 520; line-height: 1.45; }
    .cwp-summary { display: grid; gap: 8px; }
    .cwp-summary-row { display: flex; justify-content: space-between; gap: 12px; font-size: 13px; }
    .cwp-summary-row strong { font-weight: 720; }
    .cwp-usage-message {
      overflow-x: auto;
      padding: 12px;
      border: 1px solid color-mix(in srgb, CanvasText 14%, Canvas 86%);
      border-radius: 6px;
      background: Canvas;
      font-size: 13px;
      line-height: 1.8;
    }
    .cwp-usage-message h3 { margin: 0 0 6px; font-size: 16px; }
    .cwp-usage-group { margin-top: 12px; font-weight: 720; }
    .cwp-usage-quote {
      border-left: 3px solid color-mix(in srgb, CanvasText 16%, Canvas 84%);
      padding-left: 10px;
      white-space: nowrap;
    }
    .cwp-page .cwp-usage-message code {
      font-size: 12px;
      white-space: pre;
      overflow-wrap: normal;
    }
    .cwp-usage-info { color: #16a34a; }
    .cwp-usage-comment { color: color-mix(in srgb, CanvasText 62%, Canvas 38%); }
    .cwp-usage-warning { color: #d97706; }
    .cwp-time-list { display: grid; gap: 8px; }
    .cwp-time-row { display: grid; grid-template-columns: minmax(0, 1fr) max-content; gap: 8px; align-items: center; min-width: 0; }
    .cwp-table-wrap {
      overflow: auto;
      border: 1px solid color-mix(in srgb, CanvasText 12%, Canvas 88%);
      border-radius: 8px;
      background: Canvas;
    }
    .cwp-table { width: 100%; border-collapse: collapse; min-width: 760px; }
    .cwp-table th, .cwp-table td {
      border-bottom: 1px solid color-mix(in srgb, CanvasText 10%, Canvas 90%);
      padding: 10px;
      text-align: left;
      vertical-align: top;
      font-size: 12px;
    }
    .cwp-table th { color: color-mix(in srgb, CanvasText 70%, Canvas 30%); font-weight: 720; background: color-mix(in srgb, CanvasText 4%, Canvas 96%); }
    .cwp-table tr:last-child td { border-bottom: 0; }
    .cwp-page code {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 11px;
      overflow-wrap: anywhere;
    }
    .cwp-badge {
      display: inline-flex;
      min-height: 24px;
      align-items: center;
      border-radius: 999px;
      padding: 3px 8px;
      background: color-mix(in srgb, CanvasText 9%, Canvas 91%);
      font-size: 12px;
      font-weight: 700;
    }
    .cwp-badge.cwp-ok { background: color-mix(in srgb, #16a34a 14%, Canvas 86%); color: color-mix(in srgb, #15803d 78%, CanvasText 22%); }
    .cwp-badge.cwp-warn { background: color-mix(in srgb, #d97706 14%, Canvas 86%); color: color-mix(in srgb, #b45309 78%, CanvasText 22%); }
    .cwp-type-badge {
      display: inline-block;
      padding: 2px 7px;
      border-radius: 4px;
      font-size: 12px;
      font-weight: 500;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      background: color-mix(in srgb, CanvasText 8%, Canvas 92%);
      color: CanvasText;
      border: 1px solid color-mix(in srgb, CanvasText 15%, Canvas 85%);
      text-transform: lowercase;
      white-space: nowrap;
    }
    .cwp-status-cell { max-width: 220px; min-width: 100px; }
    .cwp-status-cell .cwp-muted { overflow-wrap: anywhere; word-break: break-word; }
    .cwp-toast-host {
      position: fixed;
      top: 18px;
      left: 50%;
      transform: translateX(-50%);
      display: flex;
      flex-direction: column;
      gap: 10px;
      z-index: 2147483000;
      pointer-events: none;
      width: min(440px, calc(100vw - 32px));
    }
    .cwp-toast {
      pointer-events: auto;
      cursor: pointer;
      border-radius: 10px;
      padding: 11px 14px;
      font-size: 13px;
      font-weight: 650;
      line-height: 1.45;
      white-space: pre-wrap;
      word-break: break-word;
      color: #fff;
      background: #1f2937;
      box-shadow: 0 12px 30px rgba(15, 23, 42, 0.22);
      opacity: 0;
      transform: translateY(-14px) scale(0.98);
      transition: opacity .26s ease, transform .26s ease;
    }
    .cwp-toast-success { background: #0f766e; }
    .cwp-toast-error { background: #b91c1c; }
    .cwp-toast-info { background: #1f2937; }
    .cwp-toast-show { opacity: 1; transform: translateY(0) scale(1); }
    .cwp-toast-hide { opacity: 0; transform: translateY(-14px) scale(0.98); }
    @media (prefers-reduced-motion: reduce) {
      .cwp-toast { transition: opacity .12s ease; transform: none; }
      .cwp-toast-show { transform: none; }
      .cwp-toast-hide { transform: none; }
    }
    @media (max-width: 920px) {
      .cwp-page { padding: 16px; }
      .cwp-header { display: grid; align-items: start; }
      .cwp-header-actions { justify-content: start; }
      .cwp-layout { display: grid; grid-template-columns: 1fr; }
      .cwp-side { width: auto; }
      .cwp-settings-grid { grid-template-columns: 1fr; }
      .cwp-model-field { grid-column: auto; }
      .cwp-wide-field { grid-column: auto; }
      .cwp-actions { display: grid; }
      .cwp-actions button { width: 100%; }
    }
  </style>
  <style>
    [hidden] { display: none !important; }
    .cwp-breadcrumb { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 16px; }
    .cwp-breadcrumb button { padding: 6px 10px; }
  </style>
</head>
<body class="cwp-body">
  <main class="cwp-page">
    <header class="cwp-header">
      <h1>窗口预热</h1>
      <div class="cwp-header-actions">
        <span class="cwp-metric" id="enabledMetric">未启用</span>
        <span class="cwp-metric" id="selectedMetric">0 个认证文件</span>
      </div>
    </header>
    <div class="cwp-layout">
      <div class="cwp-main">

        <section class="cwp-panel">
          <nav class="cwp-breadcrumb" aria-label="凭证配置导航">
            <span>凭证配置</span><span aria-hidden="true">/</span>
            <button id="warmupTab" type="button" aria-current="page">预热设置</button>
            <span aria-hidden="true">/</span>
            <button id="usageTab" type="button" class="cwp-secondary">额度推送设置</button>
          </nav>
          <h2 id="authSectionTitle">预热凭证</h2>
          <p class="cwp-muted" id="authSelectionHelp">选择参与定时预热的 CPA 凭证，与额度推送独立保存。</p>
          <div class="cwp-actions" style="margin-bottom: 9px;">
            <button id="batchWarmup" type="button" class="cwp-secondary">批量预热</button>
            <button id="selectAllAuths" type="button" class="cwp-secondary">全选</button>
            <button id="clearAuths" type="button" class="cwp-secondary">清空选择</button>
          </div>
          <div class="cwp-table-wrap">
            <table class="cwp-table">
              <thead>
                <tr>
                  <th>选择</th>
                  <th>类型</th>
                  <th>认证文件</th>
                  <th>账号</th>
                  <th>状态</th>
                  <th data-warmup-column>最近成功</th>
                  <th data-warmup-column>最近预热</th>
                  <th data-warmup-column>操作</th>
                </tr>
              </thead>
              <tbody id="authRows"></tbody>
            </table>
          </div>
        </section>
        <section class="cwp-panel" id="warmupSettings">
          <h2>预热设置</h2>
          <div class="cwp-fields">
            <label class="cwp-inline">
              <input id="enabled" type="checkbox">
              <span>启用后台预热</span>
            </label>
            <div>
              <h3>发送窗口</h3>
              <div id="timeList" class="cwp-time-list"></div>
              <div class="cwp-actions" style="margin-top: 9px;">
                <button id="addTime" type="button" class="cwp-secondary">添加时间</button>
                <button id="resetTimes" type="button" class="cwp-secondary">恢复 07:00 / 12:00 / 17:00</button>
              </div>
              <p class="cwp-muted">插件会在每个目标时间前 1 分钟内发送。如果同一认证文件距离上次成功不足 5 小时，会在该 1 分钟窗口内等待；等不到就跳过，避免未满 5 小时就重复触发。</p>
            </div>
            <div class="cwp-settings-grid">
              <label class="cwp-model-field"><span>模型</span>
                <textarea id="model" spellcheck="false" placeholder="gpt-6-luna, claude-sonnet-4-6"></textarea>
                <span class="cwp-muted">可填多个模型，支持逗号、中文逗号、顿号、分号或换行分隔。若凭证属于不同渠道（如 Kimi、Claude、GPT、Gemini 等），插件将自动识别对应渠道的可用模型进行预热。</span>
              </label>
              <label><span>最小间隔</span>
                <input id="minInterval" spellcheck="false" placeholder="5h">
              </label>
              <label><span>提前触发窗口</span>
                <input id="leadTime" spellcheck="false" placeholder="1m">
              </label>
              <label><span>后台检查间隔</span>
                <input id="tickInterval" spellcheck="false" placeholder="5s">
              </label>
              <label><span>预热内容</span>
                <input id="prompt" spellcheck="false" placeholder="hi">
              </label>
            </div>
          </div>
        </section>
        <section class="cwp-panel" id="usageSettings" hidden>
          <h2>额度推送</h2>
          <div class="cwp-fields">
            <label class="cwp-inline">
              <input id="usagePushEnabled" type="checkbox">
              <span>开启工作日定时推送</span>
            </label>
            <label><span>企微 / Webhook 地址</span>
              <input id="webhookUrl" spellcheck="false" placeholder="https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...">
            </label>
            <div>
              <h3>推送时间</h3>
              <div id="pushTimeList" class="cwp-time-list"></div>
              <div class="cwp-actions" style="margin-top: 9px;">
                <button id="addPushTime" type="button" class="cwp-secondary">添加时间</button>
              </div>
              <p class="cwp-muted">按额度推送凭证汇总 GPT / Claude 实际窗口和 Devin 日 / 周限额，仅工作日到点推送。预览和立即推送使用已保存的额度设置。</p>
            </div>
            <div class="cwp-actions">
              <button id="refreshUsage" type="button" class="cwp-secondary">预览额度</button>
              <button id="pushUsageNow" type="button" class="cwp-secondary">立即推送</button>
            </div>
            <div class="cwp-summary" id="usagePreview"></div>
          </div>
        </section>
      </div>
      <div class="cwp-side">
        <section class="cwp-panel">
          <h2>连接</h2>
          <div class="cwp-fields">
            <label><span>CPA 管理密钥</span>
              <input id="managementKey" type="password" autocomplete="off" spellcheck="false">
            </label>
            <p class="cwp-muted">刷新状态、保存配置和手动预热需要 CPA 管理密钥。已保存的后台预热不依赖本页面持续打开；密钥会保存在浏览器本地，不会写入插件配置。</p>
            <div class="cwp-actions">
              <button id="saveConfig" type="button">保存预热设置</button>
              <button id="refreshSnapshot" type="button" class="cwp-secondary">刷新状态</button>
            </div>
          </div>
        </section>
        <section class="cwp-panel">
          <h2>运行概览</h2>
          <div class="cwp-summary" id="overview"></div>
        </section>

      </div>
    </div>
  </main>
  <script>
    const INITIAL_DATA = `)
	out.Write(rawData)
	out.WriteString(`;
    const DEFAULT_TIMES = ['07:00', '12:00', '17:00'];
    const DEFAULT_PUSH_TIMES = ['10:00', '15:00'];
    const DEFAULT_MODEL = 'gpt-6-luna, claude-sonnet-4-6';
    const BATCH_WARMUP_CONCURRENCY = 4;
    const ENDPOINTS = {
      snapshot: '/v0/management/cpa-window-primer/snapshot',
      config: '/v0/management/cpa-window-primer/config',
      run: '/v0/management/cpa-window-primer/run',
      usage: '/v0/management/cpa-window-primer/usage',
      usagePush: '/v0/management/cpa-window-primer/usage-push'
    };
    const MANAGEMENT_KEY_STORAGE = 'cpa-window-primer.management-key';
    const state = { snapshot: normalizeSnapshot(INITIAL_DATA), activeSettings: 'warmup' };

    function normalizeSnapshot(input) {
      const data = input || {};
      const config = data.config || {};
      return {
        config: {
          enabled: config.enabled !== false,
          auth_ids: Array.isArray(config.auth_ids) ? config.auth_ids : [],
          usage_auth_ids: Array.isArray(config.usage_auth_ids) ? config.usage_auth_ids : (Array.isArray(config.auth_ids) ? config.auth_ids.slice() : []),
          times: Array.isArray(config.times) && config.times.length ? config.times : DEFAULT_TIMES.slice(),
          model: config.model || DEFAULT_MODEL,
          prompt: config.prompt || 'hi',
          min_interval: config.min_interval || '5h',
          lead_time: config.lead_time || '1m',
          tick_interval: config.tick_interval || '5s',
          webhook_url: config.webhook_url || '',
          usage_push_enabled: config.usage_push_enabled === true,
          usage_push_times: Array.isArray(config.usage_push_times) && config.usage_push_times.length ? config.usage_push_times : DEFAULT_PUSH_TIMES.slice()
        },
        auths: Array.isArray(data.auths) ? data.auths : [],
        state: data.state && typeof data.state === 'object' ? data.state : { auths: {} },
        last_error: data.last_error || ''
      };
    }

    function field(id) {
      return document.getElementById(id);
    }

    function selectedSet() {
      return new Set(state.snapshot.config[activeAuthKey()] || []);
    }

    function activeAuthKey() {
      return state.activeSettings === 'usage' ? 'usage_auth_ids' : 'auth_ids';
    }

    function switchSettings(section) {
      state.activeSettings = section;
      renderSettingsNavigation();
      renderAuths();
      renderOverview();
    }

    function renderSettingsNavigation() {
      const usage = state.activeSettings === 'usage';
      field('warmupSettings').hidden = usage;
      field('usageSettings').hidden = !usage;
      field('batchWarmup').hidden = usage;
      field('authSectionTitle').textContent = usage ? '额度推送凭证' : '预热凭证';
      field('authSelectionHelp').textContent = usage
        ? '选择参与额度预览和推送的 CPA 凭证，不影响预热选择。支持 Codex / Claude / Devin。'
        : '选择参与定时预热的 CPA 凭证，与额度推送独立保存。支持各类凭证（如 Codex / Devin / Kimi 等）。';
      field('saveConfig').textContent = usage ? '保存额度推送设置' : '保存预热设置';
      for (const section of ['warmup', 'usage']) {
        const button = field(section + 'Tab');
        button.className = section === state.activeSettings ? '' : 'cwp-secondary';
        if (section === state.activeSettings) button.setAttribute('aria-current', 'page');
        else button.removeAttribute('aria-current');
      }
      for (const cell of document.querySelectorAll('[data-warmup-column]')) cell.hidden = usage;
    }

    function loadStoredManagementKey() {
      try {
        return window.localStorage.getItem(MANAGEMENT_KEY_STORAGE) || '';
      } catch (error) {
        return '';
      }
    }

    function saveStoredManagementKey(value) {
      try {
        if (value) {
          window.localStorage.setItem(MANAGEMENT_KEY_STORAGE, value);
        } else {
          window.localStorage.removeItem(MANAGEMENT_KEY_STORAGE);
        }
      } catch (error) {
        // Ignore storage failures; the key still works for the current page session.
      }
    }

    let toastSeq = 0;

    function toastHost() {
      let host = document.getElementById('cwpToastHost');
      if (!host) {
        host = document.createElement('div');
        host.id = 'cwpToastHost';
        host.className = 'cwp-toast-host';
        document.body.appendChild(host);
      }
      return host;
    }

    function showToast(message, type) {
      const text = String(message == null ? '' : message).trim();
      if (!text) return;
      const host = toastHost();
      const toast = document.createElement('div');
      toast.className = 'cwp-toast cwp-toast-' + (type || 'info');
      toast.setAttribute('role', type === 'error' ? 'alert' : 'status');
      toast.textContent = text;
      host.appendChild(toast);
      requestAnimationFrame(() => toast.classList.add('cwp-toast-show'));
      const id = ++toastSeq;
      const ttl = type === 'error' ? 6000 : 3200;
      const dismiss = () => {
        if (toast.dataset.closing === String(id)) return;
        toast.dataset.closing = String(id);
        toast.classList.remove('cwp-toast-show');
        toast.classList.add('cwp-toast-hide');
        toast.addEventListener('transitionend', () => toast.remove(), { once: true });
        setTimeout(() => toast.remove(), 400);
      };
      toast.addEventListener('click', dismiss);
      setTimeout(dismiss, ttl);
    }

    function setStatus(message, error) {
      showToast(message, error ? 'error' : 'success');
    }

    function clearStatus() {
      // toast 会自动消失，无需手动清除。
    }

    function setConnectionStatus(message) {
      setStatus(message, true);
    }

    function clearConnectionStatus() {
      clearStatus();
    }

    function authHeaders() {
      const key = field('managementKey').value.trim();
      if (!key) {
        saveStoredManagementKey('');
        setConnectionStatus('需要填写 CPA 管理密钥');
        const error = new Error('需要填写 CPA 管理密钥');
        error.connectionStatus = true;
        throw error;
      }
      saveStoredManagementKey(key);
      clearConnectionStatus();
      return { Authorization: /^bearer\s+/i.test(key) ? key : 'Bearer ' + key };
    }

    async function readJSON(response) {
      const text = await response.text();
      if (!text) return {};
      try {
        return JSON.parse(text);
      } catch (error) {
        return { error: text };
      }
    }

    function formatError(data, fallback) {
      if (!data) return fallback;
      if (typeof data === 'string') return data;
      return data.msg || data.message || data.error || fallback;
    }

    function parseDuration(text) {
      const match = String(text || '').trim().match(/^(\d+)(h|m|s)$/);
      if (!match) return 0;
      const value = Number.parseInt(match[1], 10);
      if (!Number.isFinite(value)) return 0;
      if (match[2] === 'h') return value * 60 * 60 * 1000;
      if (match[2] === 'm') return value * 60 * 1000;
      return value * 1000;
    }

    function formatTime(value) {
      if (!value || String(value).startsWith('0001-')) return '无';
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) return String(value);
      return date.toLocaleString('zh-CN', { hour12: false });
    }

    function attemptText(record) {
      if (!record) return '无';
      if (record.success) return '成功';
      return warmupFeedback(record);
    }

    function warmupFeedback(record, fallbackError) {
      if (record && record.msg) return record.msg;
      const raw = (record && (record.error || record.response_summary)) || fallbackError || '';
      const text = decodeErrorText(raw).split('; 模型兜底记录:')[0];
      const model = record && record.model ? '模型「' + record.model + '」' : '所选模型';
      if (/selected model is at capacity|model is at capacity/i.test(text)) {
        return '预热失败：' + model + '暂时繁忙，请稍后重试或更换模型。';
      }
      if (/model_not_found|model not found|unknown provider for model|unknown model|unsupported model|invalid model|not a valid model|no such model/i.test(text)) {
        return '预热失败：' + model + '不可用。请在“预热设置 → 模型”中改为 CPA 当前提供且该账号支持的模型，然后重试。';
      }
      if (/failed to fetch|networkerror|network error|load failed/i.test(text)) {
        return '预热失败：无法连接 CPA，请检查网络及服务地址后重试。';
      }
      const friendly = humanizeError(text);
      if (/[\u4e00-\u9fff]/.test(friendly) && !/host_call_failed|[{}]/.test(friendly)) {
        return '预热失败：' + friendly;
      }
      return '预热失败：服务未能完成请求。请检查模型、认证文件和网络配置后重试。';
    }

    function humanizeError(raw) {
      const text = decodeErrorText(raw);
      if (!text) return '';
      const lower = text.toLowerCase();
      if (lower.includes('quota') || lower.includes('usage_limit') || lower.includes('exhausted') || lower.includes('credit') || lower.includes('payment') || text.includes('额度已满') || text.includes('限额已满')) {
        return '额度已满';
      }
      if (lower.includes('model_not_found') || lower.includes('unknown provider for model')) {
        return '预热模型不可用，请检查模型配置';
      }
      if (lower.includes('rate_limit') || lower.includes('rate limit') || lower.includes('too many requests') || lower.includes('429') || text.includes('限流')) {
        return '请求限流，请稍后重试';
      }
      if (lower.includes('min_interval_not_met')) return '未到最小间隔';
      if (lower.includes('warmup_already_running')) return '正在预热';
      if (lower.includes('target_auth_not_used')) return '宿主没有使用目标认证文件';
      if (lower.includes('target_auth_not_candidate')) return '目标认证文件没有进入调度候选';
      if (lower.includes('target_auth_verify_failed')) return '目标认证文件结果确认失败';
      if (lower.includes('auth_target_unavailable')) return '目标认证文件不可用';
      if (lower.includes('codex_warmup_stream_incomplete')) return '目标认证请求未返回完成事件';
      if (lower.includes('codex_warmup_stream_error')) return '目标认证请求返回失败';
      if (lower.includes('codex_warmup_timeout')) return '目标认证请求超时';
      if (lower.includes('codex_warmup_response_too_large')) return '目标认证响应过大';
      if (lower.includes('codex_warmup_stream_unavailable')) return '宿主流式请求不可用';
      if (lower.includes('target_auth_proxy_invalid')) return '目标认证文件的代理配置无效';
      if (lower.includes('codex_warmup_request_failed')) return '目标认证直连请求失败';
      if (lower.includes('access token not found')) return '目标认证文件缺少 Access Token';
      if (lower.includes('need') && lower.includes('密钥')) return text;
      const bodyStart = text.indexOf('{');
      if (bodyStart >= 0) {
        try {
          const body = JSON.parse(text.slice(bodyStart));
          const detail = body.error && (body.error.message || body.error);
          if (typeof detail === 'string') return compactText(detail, 96);
        } catch (_) {}
      }
      return compactText(text, 96);
    }

    function decodeErrorText(raw) {
      return String(raw || '').trim()
        .replace(/&amp;/g, '&')
        .replace(/&#(?:34|x22);|&quot;/gi, '"')
        .replace(/&#(?:39|x27);|&apos;/gi, "'");
    }

    function compactText(raw, maxLength) {
      const text = String(raw || '').trim();
      const limit = maxLength || 96;
      return text.length > limit ? text.slice(0, limit) + '…' : text;
    }

    function statusLabel(auth) {
      const raw = String(auth && auth.status || '').trim();
      const lower = raw.toLowerCase();
      // 优先用后端给出的阻塞原因（已是中文），额度类会被归一为“额度已满”。
      if (auth && auth.blocked_reason) return humanizeError(auth.blocked_reason);
      if (lower === 'active' || lower === 'ok' || lower === 'normal') return '正常';
      if (lower === 'error' || lower === 'blocked' || lower === 'unavailable') return '不可用';
      return raw || '未知';
    }

    function nextAllowedText(authState, auth) {
      if (authState && authState.quota_blocked_until) {
        const until = new Date(authState.quota_blocked_until);
        if (!Number.isNaN(until.getTime()) && Date.now() < until.getTime()) {
          return '额度已满，预计 ' + until.toLocaleString('zh-CN', { hour12: false }) + ' 恢复；每分钟复查';
        }
      }
      const hostUntilValue = auth && (auth.quota_blocked_until || auth.next_retry_after);
      if (hostUntilValue) {
        const until = new Date(hostUntilValue);
        if (!Number.isNaN(until.getTime()) && Date.now() < until.getTime()) {
          return '暂不可用，预计 ' + until.toLocaleString('zh-CN', { hour12: false }) + ' 重试；每分钟复查额度';
        }
      }
      if (auth && auth.blocked_reason && humanizeError(auth.blocked_reason) === '额度已满') {
        return '额度已满，每分钟复查，恢复后解除限制';
      }
      if (!authState || !authState.last_success_at) return '可立即发送';
      const duration = parseDuration(state.snapshot.config.min_interval);
      if (!duration) return '可立即发送';
      const next = new Date(new Date(authState.last_success_at).getTime() + duration);
      if (Number.isNaN(next.getTime())) return '可立即发送';
      if (Date.now() >= next.getTime()) return '可立即发送';
      return '冷却至 ' + next.toLocaleString('zh-CN', { hour12: false });
    }

    function uniqueSorted(values) {
      return Array.from(new Set(values.map((item) => String(item || '').trim()).filter(Boolean))).sort();
    }

    function createCell(text) {
      const td = document.createElement('td');
      td.textContent = text || '';
      return td;
    }

    function renderOverview() {
      const config = state.snapshot.config;
      const selectableIDs = new Set((state.snapshot.auths || []).filter((auth) => auth.selectable !== false).map((auth) => auth.id));
      const selectedCount = (config.auth_ids || []).filter((id) => selectableIDs.has(id)).length;
      const usageCount = (config.usage_auth_ids || []).filter((id) => selectableIDs.has(id)).length;
      field('enabledMetric').textContent = '预热' + (config.enabled ? '已启用' : '已停用') + ' · 额度推送' + (config.usage_push_enabled ? '已启用' : '已停用');
      field('selectedMetric').textContent = '预热 ' + selectedCount + ' / 额度 ' + usageCount;
      const rows = [
        ['可管理认证文件', state.snapshot.auths.length + ' 个'],
        ['预热凭证', selectedCount + ' 个'],
        ['额度推送凭证', usageCount + ' 个'],
        ['发送窗口', (config.times || []).join(' / ') || '未配置'],
        ['预热模型', config.model || DEFAULT_MODEL],
        ['最小间隔', config.min_interval || '5h'],
        ['额度推送时间', (config.usage_push_times || []).join(' / ') || '未配置']
      ];
      if (state.snapshot.last_error) {
        rows.push(['最近错误', state.snapshot.last_error]);
      }
      const box = field('overview');
      box.textContent = '';
      for (const row of rows) {
        const item = document.createElement('div');
        item.className = 'cwp-summary-row';
        const key = document.createElement('span');
        key.textContent = row[0];
        const value = document.createElement('strong');
        value.textContent = row[1];
        item.appendChild(key);
        item.appendChild(value);
        box.appendChild(item);
      }
    }

    function renderForm(section) {
      const config = state.snapshot.config;
      if (!section || section === 'warmup') {
        field('enabled').checked = config.enabled !== false;
        field('model').value = config.model || DEFAULT_MODEL;
        field('prompt').value = config.prompt || 'hi';
        field('minInterval').value = config.min_interval || '5h';
        field('leadTime').value = config.lead_time || '1m';
        field('tickInterval').value = config.tick_interval || '5s';
        renderTimes(config.times || DEFAULT_TIMES);
      }
      if (!section || section === 'usage') {
        field('webhookUrl').value = config.webhook_url || '';
        field('usagePushEnabled').checked = config.usage_push_enabled === true;
        renderPushTimes(config.usage_push_times || DEFAULT_PUSH_TIMES);
      }
    }

    function renderTimes(times) {
      renderTimeList('timeList', 'cwp-time-input', times, DEFAULT_TIMES);
    }

    function renderPushTimes(times) {
      renderTimeList('pushTimeList', 'cwp-push-time-input', times || [], DEFAULT_PUSH_TIMES);
    }

    function renderTimeList(listID, inputClass, times, fallback) {
      const list = field(listID);
      list.textContent = '';
      const values = uniqueSorted((times && times.length) ? times : fallback);
      for (const value of values) {
        addTimeRow(listID, inputClass, value);
      }
    }

    function addTimeRow(listID, inputClass, value) {
      const list = field(listID);
      const row = document.createElement('div');
      row.className = 'cwp-time-row';
      const input = document.createElement('input');
      input.type = 'time';
      input.className = inputClass;
      input.value = value || '09:00';
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'cwp-secondary';
      remove.textContent = '删除';
      remove.addEventListener('click', () => { row.remove(); });
      row.appendChild(input);
      row.appendChild(remove);
      list.appendChild(row);
    }

    function collectTimes() {
      return uniqueSorted(Array.from(document.querySelectorAll('.cwp-time-input')).map((input) => input.value));
    }

    function collectPushTimes() {
      return uniqueSorted(Array.from(document.querySelectorAll('.cwp-push-time-input')).map((input) => input.value));
    }

    function collectAuthIDs() {
      return Array.from(document.querySelectorAll('.cwp-auth-check:checked:not(:disabled)')).map((item) => item.dataset.authId);
    }

    function collectConfig() {
      return {
        enabled: field('enabled').checked,
        auth_ids: (state.snapshot.config.auth_ids || []).slice(),
        usage_auth_ids: (state.snapshot.config.usage_auth_ids || []).slice(),
        times: collectTimes(),
        model: field('model').value.trim() || DEFAULT_MODEL,
        prompt: field('prompt').value.trim() || 'hi',
        min_interval: field('minInterval').value.trim() || '5h',
        lead_time: field('leadTime').value.trim() || '1m',
        tick_interval: field('tickInterval').value.trim() || '5s',
        webhook_url: field('webhookUrl').value.trim(),
        usage_push_enabled: field('usagePushEnabled').checked,
        usage_push_times: collectPushTimes()
      };
    }

    function validateConfigBeforeSave(config) {
      const missing = [];
      if (state.activeSettings === 'warmup') {
        if (config.enabled && !config.auth_ids.length) missing.push('预热认证文件');
        if (!config.times.length) missing.push('发送窗口');
        if (!config.model) missing.push('模型');
        if (!config.prompt) missing.push('预热内容');
        if (!config.min_interval) missing.push('最小间隔');
        if (!config.lead_time) missing.push('提前触发窗口');
        if (!config.tick_interval) missing.push('后台检查间隔');
      } else if (config.usage_push_enabled) {
        if (!config.usage_auth_ids.length) missing.push('额度推送认证文件');
        if (!config.webhook_url) missing.push('企微 / Webhook 地址');
        if (!config.usage_push_times.length) missing.push('推送时间');
      }
      if (missing.length) {
        throw new Error('请填写必填项：' + missing.join('、'));
      }
    }

    function renderAuths() {
      const body = field('authRows');
      body.textContent = '';
      const selected = selectedSet();
      const authState = (state.snapshot.state && state.snapshot.state.auths) || {};
      if (!state.snapshot.auths.length) {
        const tr = document.createElement('tr');
        const td = createCell('没有找到可管理的凭证，请先在 CPA 中添加。');
        td.colSpan = state.activeSettings === 'usage' ? 5 : 8;
        tr.appendChild(td);
        body.appendChild(tr);
        return;
      }
      for (const auth of state.snapshot.auths) {
        const itemState = authState[auth.id] || {};
        const selectable = auth.selectable !== false;
        const tr = document.createElement('tr');
        const selectTd = document.createElement('td');
        const check = document.createElement('input');
        check.type = 'checkbox';
        check.className = 'cwp-auth-check';
        check.dataset.authId = auth.id;
        check.disabled = !selectable;
        check.checked = selectable && selected.has(auth.id);
        check.addEventListener('change', () => {
          state.snapshot.config[activeAuthKey()] = collectAuthIDs();
          renderOverview();
        });
        selectTd.appendChild(check);
        tr.appendChild(selectTd);

        const typeTd = document.createElement('td');
        const typeBadge = document.createElement('span');
        typeBadge.className = 'cwp-type-badge';
        typeBadge.textContent = auth.type || auth.provider || 'unknown';
        typeTd.appendChild(typeBadge);
        tr.appendChild(typeTd);

        const nameTd = document.createElement('td');
        const code = document.createElement('code');
        code.textContent = auth.name || auth.id;
        nameTd.appendChild(code);
        const idLine = document.createElement('div');
        idLine.className = 'cwp-muted';
        idLine.textContent = auth.id;
        nameTd.appendChild(idLine);
        tr.appendChild(nameTd);

        tr.appendChild(createCell(auth.email || auth.label || '无'));
        const statusTd = document.createElement('td');
        statusTd.className = 'cwp-status-cell';
        const badge = document.createElement('span');
        const label = statusLabel(auth);
        const isActive = label === '正常';
        badge.className = 'cwp-badge ' + (isActive ? 'cwp-ok' : 'cwp-warn');
        badge.textContent = label;
        statusTd.appendChild(badge);
        const details = [];
        // badge 已显示了友好化状态，不再重复追加相同语义的消息。
        const badgeText = badge.textContent;
        if (!isActive && auth.blocked_reason) {
          const friendlyReason = humanizeError(auth.blocked_reason);
          if (friendlyReason !== badgeText) details.push(friendlyReason);
        }
        if (details.length) {
          const detail = document.createElement('div');
          detail.className = 'cwp-muted';
          detail.textContent = details.join('；');
          statusTd.appendChild(detail);
        }
        tr.appendChild(statusTd);
        const successTd = createCell(formatTime(itemState.last_success_at));
        successTd.hidden = state.activeSettings === 'usage';
        tr.appendChild(successTd);
        const attemptTd = document.createElement('td');
        attemptTd.hidden = state.activeSettings === 'usage';
        const attemptLine = document.createElement('div');
        attemptLine.textContent = attemptText(itemState.last_attempt);
        if (itemState.last_attempt) {
          attemptLine.title = decodeErrorText(itemState.last_attempt.error) +
            (itemState.last_attempt.response_summary ? '\n' + decodeErrorText(itemState.last_attempt.response_summary) : '');
        }
        attemptTd.appendChild(attemptLine);
        const nextLine = document.createElement('div');
        nextLine.className = 'cwp-muted';
        nextLine.textContent = nextAllowedText(itemState, auth);
        attemptTd.appendChild(nextLine);
        tr.appendChild(attemptTd);

        const actionTd = document.createElement('td');
        actionTd.hidden = state.activeSettings === 'usage';
        const run = document.createElement('button');
        run.type = 'button';
        run.className = 'cwp-secondary';
        run.textContent = '立即预热';
        run.disabled = !selectable;
        run.addEventListener('click', () => runWarmup(auth.id, true));
        actionTd.appendChild(run);
        tr.appendChild(actionTd);
        body.appendChild(tr);
      }
    }

    function renderAll() {
      renderSettingsNavigation();
      renderOverview();
      renderForm();
      renderAuths();
    }

    function restoreManagementKey() {
      const input = field('managementKey');
      if (!input.value) {
        input.value = loadStoredManagementKey();
      }
    }

    let snapshotRefreshing = false;
    async function refreshSnapshot(showMessage = true, preserveForm = false) {
      if (snapshotRefreshing) return;
      snapshotRefreshing = true;
      if (showMessage) clearStatus();
      try {
        const response = await fetch(ENDPOINTS.snapshot, { headers: authHeaders() });
        const data = await readJSON(response);
        if (!response.ok) throw new Error(formatError(data, '刷新状态失败'));
        const next = normalizeSnapshot(data);
        if (preserveForm) next.config = collectConfig();
        state.snapshot = next;
        if (preserveForm) {
          renderOverview();
          renderAuths();
        } else {
          renderAll();
        }
        if (showMessage) setStatus('状态已刷新。');
      } catch (error) {
        if (error.connectionStatus) return;
        if (!preserveForm) setStatus(error.message || String(error), true);
      } finally {
        snapshotRefreshing = false;
      }
    }

    async function refreshUsage(showMessage) {
      if (showMessage) clearStatus();
      try {
        const response = await fetch(ENDPOINTS.usage, { headers: authHeaders() });
        const data = await readJSON(response);
        if (!response.ok) throw new Error(formatError(data, '额度预览失败'));
        renderUsage(data);
        await refreshSnapshot(false, true);
        if (showMessage) setStatus('额度已刷新。');
      } catch (error) {
        if (error.connectionStatus) return;
        setStatus(error.message || String(error), true);
      }
    }

    function renderUsage(data) {
      const box = field('usagePreview');
      box.textContent = '';
      // 使用服务端实际发送的消息预览，避免另算进度、百分比或重置时间。
      if (data && typeof data.message === 'string' && data.message.trim()) {
        box.appendChild(renderUsageMessage(data.message));
        renderUsageErrors(box, data.errors);
        return;
      }
      const groups = (data && Array.isArray(data.groups)) ? data.groups : [];
      if (!groups.length) {
        const empty = document.createElement('div');
        empty.className = 'cwp-muted';
        empty.textContent = data && Array.isArray(data.errors) && data.errors.length
          ? '额度读取失败，请检查认证文件或 CPA 管理密钥。'
          : '暂无额度数据，请先保存额度推送凭证（Codex / Claude / Devin）。';
        box.appendChild(empty);
        renderUsageErrors(box, data && data.errors);
        return;
      }
      for (const g of groups) {
        const title = document.createElement('div');
        title.className = 'cwp-summary-row';
        const k = document.createElement('span');
        k.textContent = g.label;
        const v = document.createElement('strong');
        v.textContent = g.has_data ? '' : '无数据';
        title.appendChild(k); title.appendChild(v);
        box.appendChild(title);
        if (g.has_data) {
          const lines = Array.isArray(g.lines) ? g.lines : [
            ...(g.has_primary ? [{ label: g.provider === 'devin' ? '日限额' : '5小时限额', percent: g.primary_percent, reset: g.primary_reset }] : []),
            ...(g.has_secondary ? [{ label: '周限额', percent: g.secondary_percent, reset: g.secondary_reset }] : [])
          ];
          for (const line of lines) {
            box.appendChild(usageLine(line.label, line.percent, line.reset));
          }
        }
      }
      renderUsageErrors(box, data && data.errors);
    }

    function renderUsageMessage(message) {
      // CPA 宿主会对管理 API 的 JSON 字符串执行 HTML 转义。
      // 仅解码一层，再交给下方有限格式解析器；最终内容仍通过 textContent 写入。
      const entities = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", '#34': '"', '#39': "'", '#x22': '"', '#x27': "'" };
      message = message.replace(/&(amp|lt|gt|quot|apos|#34|#39|#x22|#x27);/gi,
        (_, name) => entities[name.toLowerCase()]);
      const content = document.createElement('div');
      content.className = 'cwp-usage-message';
      content.setAttribute('aria-label', '额度推送消息预览');
      for (const line of message.split(/\r?\n/)) {
        if (!line.trim()) continue;
        let text = line;
        let row;
        if (line.startsWith('# ')) {
          row = document.createElement('h3');
          text = line.slice(2);
        } else if (line.startsWith('**') && line.endsWith('**')) {
          row = document.createElement('div');
          row.className = 'cwp-usage-group';
          text = line.slice(2, -2);
        } else {
          row = document.createElement('div');
          if (line.startsWith('> ')) {
            row.className = 'cwp-usage-quote';
            text = line.slice(2);
          }
        }
        appendUsageMessageText(row, text);
        content.appendChild(row);
      }
      return content;
    }

    function appendUsageMessageText(row, text) {
      // 仅支持推送模板使用的等宽文本和企微颜色标签；其余内容作为纯文本。
      // 不将接口响应交给 innerHTML，防止响应内容变成可执行 HTML。
      const tokens = /\x60([^\x60]*)\x60|<font color="(info|comment|warning)">([^<]*)<\/font>/g;
      let offset = 0;
      for (const match of text.matchAll(tokens)) {
        row.appendChild(document.createTextNode(text.slice(offset, match.index)));
        const node = document.createElement(match[1] !== undefined ? 'code' : 'span');
        if (match[1] !== undefined) {
          node.textContent = match[1];
        } else {
          node.className = 'cwp-usage-' + match[2];
          node.textContent = match[3];
        }
        row.appendChild(node);
        offset = match.index + match[0].length;
      }
      row.appendChild(document.createTextNode(text.slice(offset)));
    }

    function renderUsageErrors(box, errors) {
      if (!Array.isArray(errors) || !errors.length) return;
      const title = document.createElement('div');
      title.className = 'cwp-muted';
      title.textContent = '部分认证文件读取失败：';
      box.appendChild(title);
      for (const error of errors.slice(0, 4)) {
        const item = document.createElement('div');
        item.className = 'cwp-muted';
        item.textContent = compactText(error, 120);
        box.appendChild(item);
      }
      if (errors.length > 4) {
        const more = document.createElement('div');
        more.className = 'cwp-muted';
        more.textContent = '还有 ' + (errors.length - 4) + ' 条失败信息未显示。';
        box.appendChild(more);
      }
    }

    function usageLine(label, percent, reset) {
      const row = document.createElement('div');
      row.className = 'cwp-summary-row';
      const k = document.createElement('span');
      k.className = 'cwp-muted';
      k.textContent = label;
      const v = document.createElement('strong');
      const remain = Math.round(100 - (Number(percent) || 0));
      v.textContent = '剩余 ' + remain + '%' + (reset ? '   ' + reset : '');
      row.appendChild(k); row.appendChild(v);
      return row;
    }

    async function pushUsageNow() {
      clearStatus();
      try {
        const response = await fetch(ENDPOINTS.usagePush, { method: 'POST', headers: authHeaders() });
        const data = await readJSON(response);
        if (!response.ok) throw new Error(formatError(data, '推送失败'));
        setStatus('额度已推送到 webhook。');
      } catch (error) {
        if (error.connectionStatus) return;
        setStatus(error.message || String(error), true);
      }
    }

    function configForSection(config, section) {
      const keys = section === 'usage'
        ? ['usage_auth_ids', 'usage_push_enabled', 'usage_push_times', 'webhook_url']
        : ['enabled', 'auth_ids', 'times', 'model', 'prompt', 'min_interval', 'lead_time', 'tick_interval'];
      return Object.fromEntries(keys.map((key) => [key, config[key]]));
    }

    async function saveConfig() {
      clearStatus();
      try {
        const next = collectConfig();
        validateConfigBeforeSave(next);
        const section = state.activeSettings;
        const response = await fetch(ENDPOINTS.config, {
          method: 'PUT',
          headers: { ...authHeaders(), 'Content-Type': 'application/json' },
          body: JSON.stringify(configForSection(next, section))
        });
        const data = await readJSON(response);
        if (!response.ok) throw new Error(formatError(data, '保存配置失败'));
        const otherDraft = configForSection(collectConfig(), section === 'usage' ? 'warmup' : 'usage');
        state.snapshot.config = { ...normalizeSnapshot({ config: data }).config, ...otherDraft };
        renderForm(section);
        renderSettingsNavigation();
        renderOverview();
        renderAuths();
        if (section === 'usage') field('usagePreview').textContent = '设置已更新，请重新预览额度。';
        setStatus((section === 'usage' ? '额度推送' : '预热') + '设置已保存。');
      } catch (error) {
        if (error.connectionStatus) return;
        setStatus(error.message || String(error), true);
      }
    }

    async function runWarmupRequest(authID, force) {
      const response = await fetch(ENDPOINTS.run, {
        method: 'POST',
        headers: { ...authHeaders(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ auth_id: authID, force: force === true })
      });
      const data = await readJSON(response);
      if (!response.ok) {
        const error = new Error(warmupFeedback(data));
        error.data = data;
        throw error;
      }
      return data;
    }

    async function runWarmup(authID, force) {
      clearStatus();
      try {
        const data = await runWarmupRequest(authID, force);
        if (data && data.success === false) {
          setStatus(warmupFeedback(data), true);
        } else {
          setStatus('手动预热完成，不影响后台定时计划。');
        }
        await refreshSnapshot(false, true);
      } catch (error) {
        if (error.connectionStatus) return;
        const data = error.data || {};
        const message = warmupFeedback(data, error.message || String(error));
        setStatus(message, true);
        await refreshSnapshot(false, true);
      }
    }

    async function runBatchWarmup() {
      clearStatus();
      const authIDs = collectAuthIDs();
      if (!authIDs.length) {
        setStatus('请先选择认证文件。', true);
        return;
      }
      const button = field('batchWarmup');
      button.disabled = true;
      button.textContent = '预热中...';
      let success = 0;
      const failures = [];
      let nextIndex = 0;
      let done = 0;
      const total = authIDs.length;
      const workers = Math.min(BATCH_WARMUP_CONCURRENCY, total);
      const updateProgress = () => {
        button.textContent = '预热中 ' + done + '/' + total;
      };
      try {
        updateProgress();
        async function worker() {
          while (nextIndex < total) {
            const authID = authIDs[nextIndex++];
            try {
              const data = await runWarmupRequest(authID, true);
              if (data && data.success === false) {
                failures.push(authID + '：' + warmupFeedback(data));
              } else {
                success += 1;
              }
            } catch (error) {
              if (error.connectionStatus) throw error;
              const data = error.data || {};
              const message = warmupFeedback(data, error.message || String(error));
              failures.push(authID + '：' + message);
            } finally {
              done += 1;
              updateProgress();
            }
          }
        }
        const tasks = [];
        for (let i = 0; i < workers; i += 1) {
          tasks.push(worker());
        }
        await Promise.all(tasks);
        await refreshSnapshot(false, true);
        if (failures.length) {
          const preview = failures.slice(0, 2).join('；');
          const suffix = failures.length > 2 ? ' 等 ' + failures.length + ' 个失败' : '';
          setStatus('批量预热完成：成功 ' + success + '，失败 ' + failures.length + '。' + preview + suffix, true);
        } else {
          setStatus('批量预热完成：成功 ' + success + '。');
        }
      } catch (error) {
        if (!error.connectionStatus) setStatus(warmupFeedback(null, error.message || String(error)), true);
      } finally {
        button.disabled = false;
        button.textContent = '批量预热';
      }
    }

    field('saveConfig').addEventListener('click', saveConfig);
    field('warmupTab').addEventListener('click', () => switchSettings('warmup'));
    field('usageTab').addEventListener('click', () => switchSettings('usage'));
    field('refreshSnapshot').addEventListener('click', refreshSnapshot);
    field('managementKey').addEventListener('input', () => {
      saveStoredManagementKey(field('managementKey').value.trim());
      clearStatus();
    });
    field('addTime').addEventListener('click', () => addTimeRow('timeList', 'cwp-time-input', '07:00'));
    field('resetTimes').addEventListener('click', () => renderTimes(DEFAULT_TIMES));
    field('addPushTime').addEventListener('click', () => addTimeRow('pushTimeList', 'cwp-push-time-input', '09:00'));
    field('pushUsageNow').addEventListener('click', pushUsageNow);
    field('refreshUsage').addEventListener('click', () => refreshUsage(true));
    field('batchWarmup').addEventListener('click', runBatchWarmup);
    field('selectAllAuths').addEventListener('click', () => {
      for (const item of document.querySelectorAll('.cwp-auth-check:not(:disabled)')) item.checked = true;
      state.snapshot.config[activeAuthKey()] = collectAuthIDs();
      renderOverview();
    });
    field('clearAuths').addEventListener('click', () => {
      for (const item of document.querySelectorAll('.cwp-auth-check')) item.checked = false;
      state.snapshot.config[activeAuthKey()] = [];
      renderOverview();
    });
    restoreManagementKey();
    renderAll();
    setInterval(() => {
      if (!document.hidden && field('managementKey').value.trim()) {
        refreshSnapshot(false, true);
      }
    }, 30000);
  </script>
</body>
</html>`)
	return out.Bytes()
}

func prettyJSON(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(raw)
}
