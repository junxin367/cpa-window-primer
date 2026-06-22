package main

import (
	"bytes"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
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

func managementRegistration() managementRegistrationResponse {
	return managementRegistrationResponse{
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: "/" + pluginID + "/config", Description: "Returns CPA Window Primer config."},
			{Method: http.MethodPut, Path: "/" + pluginID + "/config", Description: "Updates CPA Window Primer config."},
			{Method: http.MethodGet, Path: "/" + pluginID + "/state", Description: "Returns CPA Window Primer state."},
			{Method: http.MethodPost, Path: "/" + pluginID + "/run", Description: "Runs one primer request."},
		},
		Resources: []resourceRoute{{
			Path:        "/status",
			Menu:        pluginName,
			Description: "Shows selected auths, schedule, recent primer results, and manual run options.",
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

func (a *app) renderStatusPage() []byte {
	cfg, state, lastErr := a.snapshot()
	auths, err := callHostAuthList()
	if err != nil {
		lastErr = err.Error()
	}
	var out bytes.Buffer
	out.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>CPA Window Primer</title>")
	out.WriteString("<style>body{font-family:-apple-system,BlinkMacSystemFont,\"Segoe UI\",sans-serif;margin:2rem;line-height:1.45;color:#1f2937}table{border-collapse:collapse;width:100%;margin:1rem 0}th,td{border:1px solid #d1d5db;padding:.45rem;text-align:left;vertical-align:top}code,pre{background:#f3f4f6;border-radius:6px}code{padding:.1rem .25rem}pre{padding:1rem;overflow:auto}.error{color:#b42318}</style>")
	out.WriteString("</head><body><main>")
	out.WriteString("<h1>CPA Window Primer</h1>")
	out.WriteString("<h2>Config</h2><pre>")
	out.WriteString(html.EscapeString(prettyJSON(cfg.pluginConfig)))
	out.WriteString("</pre>")
	if lastErr != "" {
		out.WriteString("<h2>Error</h2><pre class=\"error\">")
		out.WriteString(html.EscapeString(lastErr))
		out.WriteString("</pre>")
	}
	out.WriteString("<h2>Supported Auths</h2><table><thead><tr><th>ID</th><th>Name</th><th>Provider</th><th>Status</th><th>Email</th></tr></thead><tbody>")
	for _, auth := range auths {
		if !isSupportedOAuthAuth(auth) {
			continue
		}
		out.WriteString("<tr><td><code>")
		out.WriteString(html.EscapeString(auth.ID))
		out.WriteString("</code></td><td>")
		out.WriteString(html.EscapeString(auth.Name))
		out.WriteString("</td><td>")
		out.WriteString(html.EscapeString(auth.Provider))
		out.WriteString("</td><td>")
		out.WriteString(html.EscapeString(auth.Status))
		out.WriteString("</td><td>")
		out.WriteString(html.EscapeString(auth.Email))
		out.WriteString("</td></tr>")
	}
	out.WriteString("</tbody></table>")
	out.WriteString("<h2>State</h2><pre>")
	out.WriteString(html.EscapeString(prettyJSON(state)))
	out.WriteString("</pre>")
	out.WriteString("</main></body></html>")
	return out.Bytes()
}

func prettyJSON(value any) string {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(raw)
}
