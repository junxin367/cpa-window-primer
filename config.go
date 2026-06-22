package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type pluginConfig struct {
	Enabled      bool     `json:"enabled" yaml:"enabled"`
	AuthIDs      []string `json:"auth_ids" yaml:"auth_ids"`
	Times        []string `json:"times" yaml:"times"`
	Model        string   `json:"model" yaml:"model"`
	Prompt       string   `json:"prompt" yaml:"prompt"`
	MinInterval  string   `json:"min_interval" yaml:"min_interval"`
	LeadTime     string   `json:"lead_time" yaml:"lead_time"`
	TickInterval string   `json:"tick_interval" yaml:"tick_interval"`
	StatePath    string   `json:"state_path" yaml:"state_path"`
	ConfigPath   string   `json:"config_path" yaml:"config_path"`
}

type pluginConfigPatch struct {
	Enabled      *bool     `json:"enabled"`
	AuthIDs      *[]string `json:"auth_ids"`
	Times        *[]string `json:"times"`
	Model        *string   `json:"model"`
	Prompt       *string   `json:"prompt"`
	MinInterval  *string   `json:"min_interval"`
	LeadTime     *string   `json:"lead_time"`
	TickInterval *string   `json:"tick_interval"`
	StatePath    *string   `json:"state_path"`
	ConfigPath   *string   `json:"config_path"`
}

type runtimeConfig struct {
	pluginConfig
	Clocks       []clockTime
	MinDuration  time.Duration
	LeadDuration time.Duration
	TickDuration time.Duration
}

func defaultPluginConfig() pluginConfig {
	return pluginConfig{
		Enabled:      true,
		Times:        append([]string(nil), defaultTimes...),
		Model:        defaultModel,
		Prompt:       defaultPrompt,
		MinInterval:  defaultMinInterval,
		LeadTime:     defaultLead,
		TickInterval: defaultTick,
	}
}

func configFromLifecycle(raw []byte) (runtimeConfig, error) {
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return runtimeConfig{}, fmt.Errorf("decode lifecycle request: %w", err)
		}
	}
	cfg := defaultPluginConfig()
	if len(req.ConfigYAML) > 0 {
		if err := yaml.Unmarshal(req.ConfigYAML, &cfg); err != nil {
			return runtimeConfig{}, fmt.Errorf("decode plugin config yaml: %w", err)
		}
	}
	applyDefaultConfigPaths(&cfg)
	if override, ok, err := loadConfigOverride(cfg.ConfigPath); err != nil {
		return runtimeConfig{}, err
	} else if ok {
		cfg = override
		applyDefaultConfigPaths(&cfg)
	}
	return normalizeConfig(cfg)
}

func normalizeConfig(cfg pluginConfig) (runtimeConfig, error) {
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	cfg.Prompt = strings.TrimSpace(cfg.Prompt)
	if cfg.Prompt == "" {
		cfg.Prompt = defaultPrompt
	}
	cfg.MinInterval = strings.TrimSpace(cfg.MinInterval)
	if cfg.MinInterval == "" {
		cfg.MinInterval = defaultMinInterval
	}
	cfg.LeadTime = strings.TrimSpace(cfg.LeadTime)
	if cfg.LeadTime == "" {
		cfg.LeadTime = defaultLead
	}
	cfg.TickInterval = strings.TrimSpace(cfg.TickInterval)
	if cfg.TickInterval == "" {
		cfg.TickInterval = defaultTick
	}
	cfg.AuthIDs = uniqueTrimmed(cfg.AuthIDs)
	cfg.Times = uniqueTrimmed(cfg.Times)
	if len(cfg.Times) == 0 {
		cfg.Times = append([]string(nil), defaultTimes...)
	}

	clocks := make([]clockTime, 0, len(cfg.Times))
	for _, item := range cfg.Times {
		clock, err := parseClock(item)
		if err != nil {
			return runtimeConfig{}, err
		}
		clocks = append(clocks, clock)
	}
	sort.Slice(clocks, func(i, j int) bool {
		if clocks[i].Hour == clocks[j].Hour {
			return clocks[i].Minute < clocks[j].Minute
		}
		return clocks[i].Hour < clocks[j].Hour
	})
	cfg.Times = make([]string, 0, len(clocks))
	for _, clock := range clocks {
		cfg.Times = append(cfg.Times, clock.String())
	}

	minDuration, err := time.ParseDuration(cfg.MinInterval)
	if err != nil {
		return runtimeConfig{}, fmt.Errorf("invalid min_interval %q: %w", cfg.MinInterval, err)
	}
	leadDuration, err := time.ParseDuration(cfg.LeadTime)
	if err != nil {
		return runtimeConfig{}, fmt.Errorf("invalid lead_time %q: %w", cfg.LeadTime, err)
	}
	tickDuration, err := time.ParseDuration(cfg.TickInterval)
	if err != nil {
		return runtimeConfig{}, fmt.Errorf("invalid tick_interval %q: %w", cfg.TickInterval, err)
	}
	if minDuration < 0 || leadDuration <= 0 || tickDuration <= 0 {
		return runtimeConfig{}, fmt.Errorf("durations must be positive")
	}
	applyDefaultConfigPaths(&cfg)
	return runtimeConfig{
		pluginConfig: cfg,
		Clocks:       clocks,
		MinDuration:  minDuration,
		LeadDuration: leadDuration,
		TickDuration: tickDuration,
	}, nil
}

func applyDefaultConfigPaths(cfg *pluginConfig) {
	if cfg == nil {
		return
	}
	base := defaultDataDir()
	if strings.TrimSpace(cfg.StatePath) == "" {
		cfg.StatePath = filepath.Join(base, "state.json")
	}
	if strings.TrimSpace(cfg.ConfigPath) == "" {
		cfg.ConfigPath = filepath.Join(base, "config.json")
	}
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "CLIProxyAPI", pluginID)
	}
	return filepath.Join(".", ".cpa-window-primer")
}

func loadConfigOverride(path string) (pluginConfig, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return pluginConfig{}, false, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return pluginConfig{}, false, nil
	}
	if err != nil {
		return pluginConfig{}, false, fmt.Errorf("read config override: %w", err)
	}
	cfg := defaultPluginConfig()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return pluginConfig{}, false, fmt.Errorf("decode config override: %w", err)
	}
	return cfg, true, nil
}

func mergeConfigPatch(current pluginConfig, raw []byte) (pluginConfig, error) {
	if len(raw) == 0 {
		return current, nil
	}
	var patch pluginConfigPatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return pluginConfig{}, fmt.Errorf("decode config body: %w", err)
	}
	next := current
	if patch.Enabled != nil {
		next.Enabled = *patch.Enabled
	}
	if patch.AuthIDs != nil {
		next.AuthIDs = append([]string(nil), (*patch.AuthIDs)...)
	}
	if patch.Times != nil {
		next.Times = append([]string(nil), (*patch.Times)...)
	}
	if patch.Model != nil {
		next.Model = *patch.Model
	}
	if patch.Prompt != nil {
		next.Prompt = *patch.Prompt
	}
	if patch.MinInterval != nil {
		next.MinInterval = *patch.MinInterval
	}
	if patch.LeadTime != nil {
		next.LeadTime = *patch.LeadTime
	}
	if patch.TickInterval != nil {
		next.TickInterval = *patch.TickInterval
	}
	// Runtime management updates intentionally ignore path fields. Paths are
	// boot-time/operator settings and must not become arbitrary API writes.
	return next, nil
}

func saveConfigOverride(path string, cfg pluginConfig) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func uniqueTrimmed(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
