package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
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
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	Status   string `json:"status,omitempty"`
	Email    string `json:"email,omitempty"`
	Label    string `json:"label,omitempty"`
	Source   string `json:"source,omitempty"`
}

func managementRegistration() managementRegistrationResponse {
	return managementRegistrationResponse{
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: "/" + pluginID + "/snapshot", Description: "返回窗口预热插件的配置、认证文件和运行状态。"},
			{Method: http.MethodGet, Path: "/" + pluginID + "/config", Description: "返回窗口预热插件配置。"},
			{Method: http.MethodPut, Path: "/" + pluginID + "/config", Description: "更新窗口预热插件配置。"},
			{Method: http.MethodGet, Path: "/" + pluginID + "/state", Description: "返回窗口预热插件运行状态。"},
			{Method: http.MethodPost, Path: "/" + pluginID + "/run", Description: "手动执行一次窗口预热请求。"},
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
			return okEnvelope(jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()}))
		}
	}
	if body.AuthID == "" && req.Query != nil {
		body.AuthID = strings.TrimSpace(req.Query.Get("auth_id"))
		body.Force = strings.EqualFold(strings.TrimSpace(req.Query.Get("force")), "true")
	}
	body.AuthID = strings.TrimSpace(body.AuthID)
	if body.AuthID == "" {
		return okEnvelope(jsonResponse(http.StatusBadRequest, map[string]string{"error": "auth_id is required"}))
	}
	cfg, _, _ := a.snapshot()
	record := a.executeWarmup(body.AuthID, "manual-"+time.Now().Format("20060102T150405"), cfg, req.HostCallbackID, body.Force)
	status := http.StatusOK
	if !record.Success {
		status = http.StatusBadGateway
	}
	return okEnvelope(jsonResponse(status, record))
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
	cfg, state, lastErr := a.snapshot()
	auths, err := callHostAuthList()
	if err != nil {
		lastErr = err.Error()
	}
	rows := make([]managementAuthView, 0, len(auths))
	for _, auth := range auths {
		if !isSupportedOAuthAuth(auth) {
			continue
		}
		id := authIDForEntry(auth)
		if id == "" {
			continue
		}
		rows = append(rows, managementAuthView{
			ID:       id,
			Name:     auth.Name,
			Provider: auth.Provider,
			Status:   auth.Status,
			Email:    auth.Email,
			Label:    auth.Label,
			Source:   auth.Source,
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

func (a *app) renderStatusPage() []byte {
	rawData, err := json.Marshal(a.managementSnapshot())
	if err != nil {
		rawData = []byte(`{"config":{"enabled":true,"auth_ids":[],"times":["07:00","12:00","17:00"],"model":"gpt-5.4","prompt":"hi","min_interval":"5h","lead_time":"1m","tick_interval":"5s"},"auths":[],"state":{"auths":{}}}`)
	}
	var out bytes.Buffer
	out.WriteString(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>窗口预热</title>
  <style>
    :root {
      color-scheme: light dark;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: Canvas;
      color: CanvasText;
      letter-spacing: 0;
    }
    * { box-sizing: border-box; }
    body { margin: 0; background: Canvas; color: CanvasText; }
    main { max-width: 1180px; margin: 0 auto; padding: 24px; }
    header { display: flex; align-items: end; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
    h1 { margin: 0; font-size: 24px; font-weight: 760; letter-spacing: 0; }
    h2 { margin: 0 0 14px; font-size: 15px; font-weight: 720; letter-spacing: 0; }
    h3 { margin: 0 0 10px; font-size: 13px; font-weight: 720; letter-spacing: 0; }
    label { display: grid; gap: 7px; font-size: 13px; font-weight: 650; min-width: 0; }
    input, select, textarea, button { font: inherit; }
    input, select, textarea {
      width: 100%;
      border: 1px solid color-mix(in srgb, CanvasText 18%, Canvas 82%);
      border-radius: 6px;
      padding: 9px 10px;
      background: Canvas;
      color: CanvasText;
    }
    input[type="checkbox"] { width: auto; }
    textarea { min-height: 92px; resize: vertical; line-height: 1.45; }
    button {
      border: 0;
      border-radius: 6px;
      padding: 9px 12px;
      background: #0f766e;
      color: #fff;
      font-weight: 720;
      cursor: pointer;
      white-space: nowrap;
    }
    button.secondary { background: color-mix(in srgb, CanvasText 10%, Canvas 90%); color: CanvasText; }
    button.warning { background: #b45309; }
    button:disabled { opacity: .54; cursor: not-allowed; }
    .header-actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: end; gap: 10px; }
    .metric {
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
    .layout { display: grid; grid-template-columns: 330px minmax(0, 1fr); gap: 16px; align-items: start; }
    .stack { display: grid; gap: 16px; }
    .panel {
      border: 1px solid color-mix(in srgb, CanvasText 14%, Canvas 86%);
      border-radius: 8px;
      padding: 16px;
      background: color-mix(in srgb, Canvas 96%, CanvasText 4%);
    }
    .fields { display: grid; gap: 13px; }
    .grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 13px; }
    .actions { display: flex; flex-wrap: wrap; gap: 9px; align-items: center; }
    .actions button { width: auto; }
    .inline { display: flex; gap: 8px; align-items: center; }
    .inline input[type="checkbox"] { flex: 0 0 auto; margin: 0; }
    .muted { color: color-mix(in srgb, CanvasText 62%, Canvas 38%); font-size: 12px; font-weight: 520; line-height: 1.45; }
    .summary { display: grid; gap: 8px; }
    .summary-row { display: flex; justify-content: space-between; gap: 12px; font-size: 13px; }
    .summary-row strong { font-weight: 720; }
    .time-list { display: grid; gap: 8px; }
    .time-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px; align-items: center; }
    .table-wrap {
      overflow: auto;
      border: 1px solid color-mix(in srgb, CanvasText 12%, Canvas 88%);
      border-radius: 8px;
      background: Canvas;
    }
    table { width: 100%; border-collapse: collapse; min-width: 760px; }
    th, td {
      border-bottom: 1px solid color-mix(in srgb, CanvasText 10%, Canvas 90%);
      padding: 10px;
      text-align: left;
      vertical-align: top;
      font-size: 12px;
    }
    th { color: color-mix(in srgb, CanvasText 70%, Canvas 30%); font-weight: 720; background: color-mix(in srgb, CanvasText 4%, Canvas 96%); }
    tr:last-child td { border-bottom: 0; }
    code {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 11px;
      overflow-wrap: anywhere;
    }
    .badge {
      display: inline-flex;
      min-height: 24px;
      align-items: center;
      border-radius: 999px;
      padding: 3px 8px;
      background: color-mix(in srgb, CanvasText 9%, Canvas 91%);
      font-size: 12px;
      font-weight: 700;
    }
    .badge.ok { background: color-mix(in srgb, #16a34a 14%, Canvas 86%); color: color-mix(in srgb, #15803d 78%, CanvasText 22%); }
    .badge.warn { background: color-mix(in srgb, #d97706 14%, Canvas 86%); color: color-mix(in srgb, #b45309 78%, CanvasText 22%); }
    .status {
      margin-top: 16px;
      white-space: pre-wrap;
      word-break: break-word;
      border-radius: 8px;
      padding: 13px;
      background: color-mix(in srgb, #2563eb 10%, Canvas 90%);
      border: 1px solid color-mix(in srgb, #2563eb 18%, Canvas 82%);
      font-size: 13px;
      line-height: 1.45;
    }
    .status.error {
      background: color-mix(in srgb, #dc2626 12%, Canvas 88%);
      border-color: color-mix(in srgb, #dc2626 24%, Canvas 76%);
    }
    @media (max-width: 920px) {
      main { padding: 16px; }
      header { display: grid; align-items: start; }
      .header-actions { justify-content: start; }
      .layout, .grid { grid-template-columns: 1fr; }
      .actions { display: grid; }
      .actions button { width: 100%; }
    }
  </style>
</head>
<body>
  <main>
    <header>
      <h1>窗口预热</h1>
      <div class="header-actions">
        <span class="metric" id="enabledMetric">未启用</span>
        <span class="metric" id="selectedMetric">0 个认证文件</span>
      </div>
    </header>
    <div class="layout">
      <div class="stack">
        <section class="panel">
          <h2>连接</h2>
          <div class="fields">
            <label><span>CPA 管理密钥</span>
              <input id="managementKey" type="password" autocomplete="off" spellcheck="false">
            </label>
            <p class="muted">保存配置、刷新状态和手动预热需要 CPA 管理密钥。密钥只用于本次页面请求，不会保存。</p>
            <div class="actions">
              <button id="saveConfig" type="button">保存配置</button>
              <button id="refreshSnapshot" type="button" class="secondary">刷新状态</button>
            </div>
          </div>
        </section>
        <section class="panel">
          <h2>运行概览</h2>
          <div class="summary" id="overview"></div>
        </section>
      </div>
      <div class="stack">
        <section class="panel">
          <h2>预热设置</h2>
          <div class="fields">
            <label class="inline">
              <input id="enabled" type="checkbox">
              <span>启用后台预热</span>
            </label>
            <div>
              <h3>发送窗口</h3>
              <div id="timeList" class="time-list"></div>
              <div class="actions" style="margin-top: 9px;">
                <button id="addTime" type="button" class="secondary">添加时间</button>
                <button id="resetTimes" type="button" class="secondary">恢复 07:00 / 12:00 / 17:00</button>
              </div>
              <p class="muted">插件会在每个目标时间前 1 分钟内发送。如果同一认证文件距离上次成功不足 5 小时，会在该 1 分钟窗口内等待；等不到就跳过，避免提前刷新失败。</p>
            </div>
            <div class="grid">
              <label><span>模型</span>
                <input id="model" spellcheck="false" placeholder="gpt-5.4">
              </label>
              <label><span>最小间隔</span>
                <input id="minInterval" spellcheck="false" placeholder="5h">
              </label>
            </div>
            <div class="grid">
              <label><span>提前触发窗口</span>
                <input id="leadTime" spellcheck="false" placeholder="1m">
              </label>
              <label><span>后台检查间隔</span>
                <input id="tickInterval" spellcheck="false" placeholder="5s">
              </label>
            </div>
            <label><span>预热内容</span>
              <textarea id="prompt" spellcheck="false" placeholder="hi"></textarea>
            </label>
          </div>
        </section>
        <section class="panel">
          <h2>认证文件</h2>
          <div class="actions" style="margin-bottom: 9px;">
            <button id="selectAllAuths" type="button" class="secondary">全选</button>
            <button id="clearAuths" type="button" class="secondary">清空选择</button>
          </div>
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>选择</th>
                  <th>认证文件</th>
                  <th>账号</th>
                  <th>状态</th>
                  <th>最近成功</th>
                  <th>最近尝试</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody id="authRows"></tbody>
            </table>
          </div>
        </section>
      </div>
    </div>
    <section id="statusBox" class="status" hidden></section>
  </main>
  <script>
    const INITIAL_DATA = `)
	out.Write(rawData)
	out.WriteString(`;
    const DEFAULT_TIMES = ['07:00', '12:00', '17:00'];
    const ENDPOINTS = {
      snapshot: '/v0/management/cpa-window-primer/snapshot',
      config: '/v0/management/cpa-window-primer/config',
      run: '/v0/management/cpa-window-primer/run'
    };
    const state = { snapshot: normalizeSnapshot(INITIAL_DATA) };

    function normalizeSnapshot(input) {
      const data = input || {};
      const config = data.config || {};
      return {
        config: {
          enabled: config.enabled !== false,
          auth_ids: Array.isArray(config.auth_ids) ? config.auth_ids : [],
          times: Array.isArray(config.times) && config.times.length ? config.times : DEFAULT_TIMES.slice(),
          model: config.model || 'gpt-5.4',
          prompt: config.prompt || 'hi',
          min_interval: config.min_interval || '5h',
          lead_time: config.lead_time || '1m',
          tick_interval: config.tick_interval || '5s'
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
      return new Set(state.snapshot.config.auth_ids || []);
    }

    function setStatus(message, error) {
      const box = field('statusBox');
      box.hidden = false;
      box.textContent = message;
      box.className = 'status' + (error ? ' error' : '');
    }

    function clearStatus() {
      const box = field('statusBox');
      box.hidden = true;
      box.textContent = '';
      box.className = 'status';
    }

    function authHeaders() {
      const key = field('managementKey').value.trim();
      if (!key) throw new Error('需要填写 CPA 管理密钥');
      return { Authorization: key.toLowerCase().startsWith('bearer ') ? key : 'Bearer ' + key };
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
      return data.message || data.error || fallback;
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
      if (record.success) return '成功 ' + formatTime(record.at);
      return '失败 ' + formatTime(record.at) + (record.error ? '：' + record.error : '');
    }

    function nextAllowedText(authState) {
      if (!authState || !authState.last_success_at) return '现在可发送';
      const duration = parseDuration(state.snapshot.config.min_interval);
      if (!duration) return '按最小间隔判断';
      const next = new Date(new Date(authState.last_success_at).getTime() + duration);
      if (Number.isNaN(next.getTime())) return '按最小间隔判断';
      if (Date.now() >= next.getTime()) return '现在可发送';
      return next.toLocaleString('zh-CN', { hour12: false }) + ' 后';
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
      const selectedCount = (config.auth_ids || []).length;
      field('enabledMetric').textContent = config.enabled ? '后台已启用' : '后台已停用';
      field('selectedMetric').textContent = selectedCount + ' 个认证文件';
      const rows = [
        ['可用认证文件', state.snapshot.auths.length + ' 个'],
        ['已选择认证文件', selectedCount + ' 个'],
        ['发送窗口', (config.times || []).join(' / ') || '未配置'],
        ['模型', config.model || 'gpt-5.4'],
        ['最小间隔', config.min_interval || '5h']
      ];
      if (state.snapshot.last_error) {
        rows.push(['最近错误', state.snapshot.last_error]);
      }
      const box = field('overview');
      box.textContent = '';
      for (const row of rows) {
        const item = document.createElement('div');
        item.className = 'summary-row';
        const key = document.createElement('span');
        key.textContent = row[0];
        const value = document.createElement('strong');
        value.textContent = row[1];
        item.appendChild(key);
        item.appendChild(value);
        box.appendChild(item);
      }
    }

    function renderForm() {
      const config = state.snapshot.config;
      field('enabled').checked = config.enabled !== false;
      field('model').value = config.model || 'gpt-5.4';
      field('prompt').value = config.prompt || 'hi';
      field('minInterval').value = config.min_interval || '5h';
      field('leadTime').value = config.lead_time || '1m';
      field('tickInterval').value = config.tick_interval || '5s';
      renderTimes(config.times || DEFAULT_TIMES);
    }

    function renderTimes(times) {
      const list = field('timeList');
      list.textContent = '';
      for (const value of uniqueSorted(times.length ? times : DEFAULT_TIMES)) {
        addTimeRow(value);
      }
    }

    function addTimeRow(value) {
      const list = field('timeList');
      const row = document.createElement('div');
      row.className = 'time-row';
      const input = document.createElement('input');
      input.type = 'time';
      input.className = 'time-input';
      input.value = value || '07:00';
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'secondary';
      remove.textContent = '删除';
      remove.addEventListener('click', () => {
        row.remove();
        if (!document.querySelector('.time-input')) addTimeRow('07:00');
      });
      row.appendChild(input);
      row.appendChild(remove);
      list.appendChild(row);
    }

    function collectTimes() {
      return uniqueSorted(Array.from(document.querySelectorAll('.time-input')).map((input) => input.value));
    }

    function collectAuthIDs() {
      return Array.from(document.querySelectorAll('.auth-check:checked')).map((item) => item.dataset.authId);
    }

    function collectConfig() {
      return {
        enabled: field('enabled').checked,
        auth_ids: collectAuthIDs(),
        times: collectTimes(),
        model: field('model').value.trim() || 'gpt-5.4',
        prompt: field('prompt').value.trim() || 'hi',
        min_interval: field('minInterval').value.trim() || '5h',
        lead_time: field('leadTime').value.trim() || '1m',
        tick_interval: field('tickInterval').value.trim() || '5s'
      };
    }

    function renderAuths() {
      const body = field('authRows');
      body.textContent = '';
      const selected = selectedSet();
      const authState = (state.snapshot.state && state.snapshot.state.auths) || {};
      if (!state.snapshot.auths.length) {
        const tr = document.createElement('tr');
        const td = createCell('没有找到可用的 OpenAI OAuth 认证文件。');
        td.colSpan = 7;
        tr.appendChild(td);
        body.appendChild(tr);
        return;
      }
      for (const auth of state.snapshot.auths) {
        const itemState = authState[auth.id] || {};
        const tr = document.createElement('tr');
        const selectTd = document.createElement('td');
        const check = document.createElement('input');
        check.type = 'checkbox';
        check.className = 'auth-check';
        check.dataset.authId = auth.id;
        check.checked = selected.has(auth.id);
        check.addEventListener('change', () => {
          state.snapshot.config.auth_ids = collectAuthIDs();
          renderOverview();
        });
        selectTd.appendChild(check);
        tr.appendChild(selectTd);

        const nameTd = document.createElement('td');
        const code = document.createElement('code');
        code.textContent = auth.name || auth.id;
        nameTd.appendChild(code);
        const idLine = document.createElement('div');
        idLine.className = 'muted';
        idLine.textContent = auth.id;
        nameTd.appendChild(idLine);
        tr.appendChild(nameTd);

        tr.appendChild(createCell(auth.email || auth.label || '无'));
        const statusTd = document.createElement('td');
        const badge = document.createElement('span');
        badge.className = 'badge ' + (String(auth.status).toLowerCase() === 'active' ? 'ok' : 'warn');
        badge.textContent = auth.status || '未知';
        statusTd.appendChild(badge);
        tr.appendChild(statusTd);
        tr.appendChild(createCell(formatTime(itemState.last_success_at)));
        tr.appendChild(createCell(attemptText(itemState.last_attempt) + '\n' + nextAllowedText(itemState)));

        const actionTd = document.createElement('td');
        const run = document.createElement('button');
        run.type = 'button';
        run.className = 'secondary';
        run.textContent = '立即预热';
        run.addEventListener('click', () => runWarmup(auth.id, false));
        actionTd.appendChild(run);
        tr.appendChild(actionTd);
        body.appendChild(tr);
      }
    }

    function renderAll() {
      renderOverview();
      renderForm();
      renderAuths();
    }

    async function refreshSnapshot(showMessage = true) {
      if (showMessage) clearStatus();
      try {
        const response = await fetch(ENDPOINTS.snapshot, { headers: authHeaders() });
        const data = await readJSON(response);
        if (!response.ok) throw new Error(formatError(data, '刷新状态失败'));
        state.snapshot = normalizeSnapshot(data);
        renderAll();
        if (showMessage) setStatus('状态已刷新。');
      } catch (error) {
        setStatus(error.message || String(error), true);
      }
    }

    async function saveConfig() {
      clearStatus();
      try {
        const next = collectConfig();
        const response = await fetch(ENDPOINTS.config, {
          method: 'PUT',
          headers: { ...authHeaders(), 'Content-Type': 'application/json' },
          body: JSON.stringify(next)
        });
        const data = await readJSON(response);
        if (!response.ok) throw new Error(formatError(data, '保存配置失败'));
        state.snapshot.config = normalizeSnapshot({ config: data }).config;
        renderAll();
        setStatus('配置已保存，后台调度已按新配置重新加载。');
      } catch (error) {
        setStatus(error.message || String(error), true);
      }
    }

    async function runWarmup(authID, force) {
      clearStatus();
      try {
        const response = await fetch(ENDPOINTS.run, {
          method: 'POST',
          headers: { ...authHeaders(), 'Content-Type': 'application/json' },
          body: JSON.stringify({ auth_id: authID, force: force === true })
        });
        const data = await readJSON(response);
        if (!response.ok) throw new Error(formatError(data, '手动预热失败'));
        await refreshSnapshot(false);
        setStatus('手动预热完成：\n' + JSON.stringify(data, null, 2));
      } catch (error) {
        setStatus(error.message || String(error), true);
      }
    }

    field('saveConfig').addEventListener('click', saveConfig);
    field('refreshSnapshot').addEventListener('click', refreshSnapshot);
    field('addTime').addEventListener('click', () => addTimeRow('07:00'));
    field('resetTimes').addEventListener('click', () => renderTimes(DEFAULT_TIMES));
    field('selectAllAuths').addEventListener('click', () => {
      for (const item of document.querySelectorAll('.auth-check')) item.checked = true;
      state.snapshot.config.auth_ids = collectAuthIDs();
      renderOverview();
    });
    field('clearAuths').addEventListener('click', () => {
      for (const item of document.querySelectorAll('.auth-check')) item.checked = false;
      state.snapshot.config.auth_ids = [];
      renderOverview();
    });
    renderAll();
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
