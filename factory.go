package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const factoryProvider = "factory-droid"

// warmupModelRoute 保存管理页解析的别名与原生模型关系，不含凭据。
type warmupModelRoute struct {
	Model          string `json:"model" yaml:"model"`
	CanonicalModel string `json:"canonical_model" yaml:"canonical_model"`
	Protocol       string `json:"protocol" yaml:"protocol"`
	QuotaPool      string `json:"quota_pool" yaml:"quota_pool"`
}

func cloneModelRoutes(routes map[string]warmupModelRoute) map[string]warmupModelRoute {
	out := make(map[string]warmupModelRoute, len(routes))
	for id, route := range routes {
		if id = strings.TrimSpace(id); id != "" {
			out[id] = route
		}
	}
	return out
}

func factoryRoute(protocol, pool string) warmupModelRoute {
	return warmupModelRoute{Protocol: protocol, QuotaPool: pool}
}

// 与 Factory Droid v0.1.8 的内置目录和官方目录路由契约一致。
// 未配置 CLI 时使用精确目录；未知模型不会按名称猜测协议。
func factoryBundledRoutes() map[string]warmupModelRoute {
	out := make(map[string]warmupModelRoute)
	for _, id := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-sonnet-4-6"} {
		out["factory/"+id] = factoryRoute("anthropic", "standard")
	}
	for _, id := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-astra", "gpt-5.4", "gpt-5.3-codex"} {
		out["factory/"+id] = factoryRoute("responses", "standard")
	}
	for _, id := range []string{"glm-5.3", "kimi-k3", "deepseek-v4.1-flash", "minimax-m3"} {
		out["factory/"+id] = factoryRoute("chat_completions", "core")
	}
	return out
}

func factoryCandidateRoute(id, provider string) (warmupModelRoute, bool) {
	switch provider {
	case "anthropic":
		return factoryRoute("anthropic", "standard"), true
	case "openai", "xai":
		return factoryRoute("responses", "standard"), true
	case "factory":
		switch id {
		case "inkling", "glm-5.3-flash", "glm-5.3", "kimi-k3", "qwen3.8-max",
			"deepseek-v4.1-flash", "deepseek-v4-flash-0731", "deepseek-v4-pro", "minimax-m3",
			"glm-5.2", "glm-5.2-fast", "nemotron-3-ultra", "mistral-large-4", "mistral-medium-3.5":
			return factoryRoute("chat_completions", "core"), true
		case "muse-spark-1.2":
			return factoryRoute("chat_completions", "standard"), true
		case "minimax-m2.7":
			return factoryRoute("anthropic", "core"), true
		}
	}
	return warmupModelRoute{}, false
}

func factoryCatalogRoutes(doc map[string]any) map[string]warmupModelRoute {
	routes := factoryBundledRoutes()
	catalog, ok := doc["factory_catalog"].(map[string]any)
	if !ok {
		return routes
	}
	// 已持久化目录存在时，以该目录为准，不把内置模型冒充账号可用模型。
	routes = make(map[string]warmupModelRoute)
	if catalog["version"] != float64(1) {
		return routes
	}
	candidates, _ := catalog["candidates"].([]any)
	for _, value := range candidates {
		candidate, ok := value.(map[string]any)
		if !ok || !factoryCandidateAllowed(candidate) {
			continue
		}
		id := stringValue(candidate["id"])
		route, supported := factoryCandidateRoute(id, stringValue(candidate["modelProvider"]))
		if supported && id != "" {
			routes["factory/"+id] = route
		}
	}
	return routes
}

func factoryCandidateAllowed(candidate map[string]any) bool {
	for _, key := range []string{"disabled", "deprecated", "isCustom", "isRouter"} {
		if value, exists := candidate[key]; exists && value != nil {
			flag, valid := value.(bool)
			if !valid || flag {
				return false
			}
		}
	}
	switch strings.ToLower(stringValue(candidate["kind"])) {
	case "router", "auto", "automatic", "local":
		return false
	}
	return true
}

func resolveFactoryModel(model, prefix string, routes map[string]warmupModelRoute, saved warmupModelRoute) (warmupModelRoute, error) {
	selected := strings.TrimSpace(model)
	lookup := selected
	if prefix = strings.Trim(strings.TrimSpace(prefix), "/"); prefix != "" {
		lookup = strings.TrimPrefix(lookup, prefix+"/")
	}
	canonical := lookup
	if !strings.HasPrefix(canonical, "factory/") {
		canonical = "factory/" + canonical
	}
	route, ok := routes[canonical]
	if saved.Model == selected && saved.CanonicalModel != "" {
		canonical = saved.CanonicalModel
		route, ok = routes[canonical]
		if ok && (route.Protocol != saved.Protocol || route.QuotaPool != saved.QuotaPool) {
			ok = false
		}
	}
	if !ok {
		return warmupModelRoute{}, fmt.Errorf("factory_model_route_unknown: 无法确认模型 %s 的 Factory 原生协议，请刷新模型列表并重新保存预热设置", selected)
	}
	route.Model, route.CanonicalModel = selected, canonical
	return route, nil
}

func factoryRegisteredModels(authIndex string) ([]string, error) {
	raw, err := callHostRPC("host.auth.models", map[string]string{"auth_index": authIndex})
	if err != nil {
		return nil, fmt.Errorf("factory_host_unsupported: 无法读取凭证模型，需要支持 host.auth.models 的 CPA v8.0.53 或更新内核")
	}
	var response struct {
		Credentials []struct {
			AuthIndex string   `json:"auth_index"`
			Provider  string   `json:"provider"`
			Models    []string `json:"models"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("factory_models_invalid: Factory 凭证模型响应无法解析")
	}
	for _, credential := range response.Credentials {
		if credential.AuthIndex == authIndex && credential.Provider == factoryProvider {
			return uniqueTrimmed(credential.Models), nil
		}
	}
	return nil, fmt.Errorf("factory_models_unavailable: Factory 凭证没有已注册的模型")
}

func executeFactoryWarmup(target warmupAuthTarget, model, prompt, callback string, saved warmupModelRoute) (pluginapi.HostModelExecutionResponse, error) {
	models, err := factoryRegisteredModels(target.AuthIndex)
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, err
	}
	found := false
	for _, available := range models {
		found = found || available == model
	}
	if !found {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("model_not_found: 所选模型未在该 Factory 凭证中注册")
	}
	doc, err := authDocumentForAuth(target.AuthIndex)
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("factory_auth_unavailable: 无法读取 Factory 凭证")
	}
	if stringValue(doc["type"]) != factoryProvider || doc["deleted"] == true || doc["disabled"] == true {
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("factory_auth_unavailable: Factory 凭证类型不匹配或已禁用")
	}
	route, err := resolveFactoryModel(model, stringValue(doc["prefix"]), factoryCatalogRoutes(doc), saved)
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, err
	}
	protocol, path := "", ""
	payload := map[string]any{"model": model, "stream": false}
	switch route.Protocol {
	case "anthropic":
		protocol, path = "claude", "/v1/messages"
		payload["messages"] = []chatMessage{{Role: "user", Content: prompt}}
		payload["max_tokens"] = 64
	case "responses":
		protocol, path = "openai-response", "/v1/responses"
		payload["input"], payload["store"] = prompt, false
		payload["max_output_tokens"] = 64
	case "chat_completions":
		protocol, path = "openai", "/v1/chat/completions"
		payload["messages"] = []chatMessage{{Role: "user", Content: prompt}}
		payload["max_tokens"] = 64
	default:
		return pluginapi.HostModelExecutionResponse{}, fmt.Errorf("factory_model_route_unknown: Factory 模型协议不受支持")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, err
	}
	return callWarmupModel(target.AuthID, model, callback, protocol, path, factoryProvider, body)
}
