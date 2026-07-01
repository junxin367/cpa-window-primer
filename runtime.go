package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type app struct {
	mu        sync.Mutex
	cfg       runtimeConfig
	state     *pluginState
	stop      chan struct{}
	runID     uint64
	pending   map[string]uint64
	lastError string
	// lastUsagePushKey 记录最近一次额度推送的窗口键（日期+时钟），避免重复推送。
	lastUsagePushKey string
}

func newApp() *app {
	cfg, _ := normalizeConfig(defaultPluginConfig())
	return &app{cfg: cfg, state: newPluginState(), pending: map[string]uint64{}}
}

func (a *app) handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if err := a.configure(request); err != nil {
			return nil, err
		}
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration())
	case pluginabi.MethodManagementHandle:
		return a.handleManagement(request)
	case pluginabi.MethodSchedulerPick:
		return handleSchedulerPick(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func (a *app) configure(raw []byte) error {
	cfg, err := configFromLifecycle(raw)
	if err != nil {
		return err
	}
	state, err := loadState(cfg.StatePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopWorkerLocked()
	a.cfg = cfg
	a.state = state
	a.lastError = ""
	if cfg.Enabled {
		a.startWorkerLocked()
	}
	return nil
}

func (a *app) shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopWorkerLocked()
}

func (a *app) snapshot() (runtimeConfig, *pluginState, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg, a.state.clone(), a.lastError
}

func (a *app) startWorkerLocked() {
	stop := make(chan struct{})
	a.stop = stop
	a.runID++
	runID := a.runID
	cfg := a.cfg
	go a.worker(stop, cfg.TickDuration, runID)
}

func (a *app) stopWorkerLocked() {
	if a.stop != nil {
		close(a.stop)
		a.stop = nil
	}
	a.pending = map[string]uint64{}
}

func (a *app) worker(stop <-chan struct{}, interval time.Duration, runID uint64) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	a.runDue(time.Now(), stop, runID)
	a.runUsagePushDue(time.Now())
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			a.runDue(now, stop, runID)
			a.runUsagePushDue(now)
		}
	}
}

func (a *app) runDue(now time.Time, stop <-chan struct{}, runID uint64) {
	cfg, _, _ := a.snapshot()
	if !cfg.Enabled || len(cfg.AuthIDs) == 0 || len(cfg.Clocks) == 0 {
		return
	}
	allowed, err := schedulableAuthIDs()
	if err != nil {
		a.setLastError(err.Error())
		return
	}
	seen := map[string]struct{}{}
	for _, authID := range cfg.AuthIDs {
		entry, ok := allowed[authID]
		if !ok {
			continue
		}
		entryID := authIDForEntry(entry)
		if entryID == "" {
			continue
		}
		if _, ok := seen[entryID]; ok {
			continue
		}
		seen[entryID] = struct{}{}
		for _, clock := range cfg.Clocks {
			a.evaluateAuthWindow(now, cfg, entry, clock, stop, runID)
		}
	}
}

func (a *app) evaluateAuthWindow(now time.Time, cfg runtimeConfig, entry pluginapi.HostAuthFileEntry, clock clockTime, stop <-chan struct{}, runID uint64) {
	authID := authIDForEntry(entry)
	a.mu.Lock()
	last := a.state.lastSuccess(authID)
	_, target := windowFor(now, clock, cfg.LeadDuration)
	key := makeWindowKey(target)
	pendingKey := windowPendingKey(authID, key)
	_, isPending := a.pending[pendingKey]
	processed := a.state.windowProcessed(authID, key) || isPending
	decision := evaluateWindow(now, clock, cfg.LeadDuration, cfg.MinDuration, last, processed)
	if decision.Action == windowActionSkip && decision.Reason == "min_interval_not_met" {
		a.state.recordSkip(authID, key, decision.Reason, now)
		_ = saveState(cfg.StatePath, a.state)
	}
	if decision.Action == windowActionWait && !isPending {
		a.pending[pendingKey] = runID
	}
	if decision.Action == windowActionSend && !isPending {
		a.pending[pendingKey] = runID
	}
	a.mu.Unlock()

	switch decision.Action {
	case windowActionSend:
		defer a.clearPending(authID, key, runID)
		if a.skipScheduledWarmupWhenQuotaFull(entry, key, cfg) {
			return
		}
		a.executeWarmup(authID, key, cfg, "", false)
	case windowActionWait:
		if !isPending {
			go a.executeDelayed(stop, runID, entry, key, cfg, decision.SendAt, decision.Target)
		}
	}
}

func windowPendingKey(authID, windowKey string) string {
	return authID + "\x00" + windowKey
}

func (a *app) executeDelayed(stop <-chan struct{}, runID uint64, entry pluginapi.HostAuthFileEntry, windowKey string, cfg runtimeConfig, sendAt, target time.Time) {
	authID := authIDForEntry(entry)
	delay := time.Until(sendAt)
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-stop:
		return
	case <-timer.C:
	}
	defer a.clearPending(authID, windowKey, runID)

	now := time.Now()
	if !now.Before(target) {
		a.recordWindowSkip(authID, windowKey, "min_interval_not_met", now, cfg.StatePath)
		return
	}
	if a.skipScheduledWarmupWhenQuotaFull(entry, windowKey, cfg) {
		return
	}
	a.executeWarmup(authID, windowKey, cfg, "", false)
}

func (a *app) skipScheduledWarmupWhenQuotaFull(entry pluginapi.HostAuthFileEntry, windowKey string, cfg runtimeConfig) bool {
	authID := authIDForEntry(entry)
	if authID == "" {
		return true
	}
	now := time.Now()
	models := warmupModelCandidates(cfg, authID)
	usage := fetchAuthUsage(entry)
	_, blocked := a.reconcileQuotaBlockFromUsage(authID, windowKey, cfg, models, usage, now, true)
	if blocked {
		return true
	}
	if usage.Err != "" && isHostAuthQuotaBlocked(entry, now) {
		return a.recordHostQuotaBlockedAttempt(authID, windowKey, cfg, models, entry, now)
	}
	return false
}

func (a *app) clearPending(authID, windowKey string, runID uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := windowPendingKey(authID, windowKey)
	if current, ok := a.pending[key]; ok && current == runID {
		delete(a.pending, key)
	}
}

func (a *app) recordWindowSkip(authID, windowKey, reason string, at time.Time, statePath string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.recordSkip(authID, windowKey, reason, at)
	if err := saveState(statePath, a.state); err != nil {
		a.lastError = err.Error()
	}
}

func (a *app) executeWarmup(authID, windowKey string, cfg runtimeConfig, hostCallbackID string, force bool) attemptRecord {
	now := time.Now()
	if !force {
		a.mu.Lock()
		if _, _, blocked := a.state.quotaBlockInfo(authID, now); blocked {
			a.mu.Unlock()
			if record, stillBlocked := a.recheckLocalQuotaBlockBeforeWarmup(authID, windowKey, cfg, now); stillBlocked {
				return record
			}
			a.mu.Lock()
		}
		last := a.state.lastSuccess(authID)
		if !last.IsZero() && now.Before(last.Add(cfg.MinDuration)) {
			record := attemptRecord{
				At:        now,
				WindowKey: windowKey,
				Model:     cfg.Model,
				Success:   false,
				Error:     "min_interval_not_met",
			}
			a.state.recordAttempt(authID, record)
			_ = saveState(cfg.StatePath, a.state)
			a.mu.Unlock()
			return record
		}
		a.mu.Unlock()
	}

	if !a.claimActiveWarmup(authID) {
		record := attemptRecord{
			At:        now,
			WindowKey: windowKey,
			Model:     cfg.Model,
			Success:   false,
			Error:     "warmup_already_running",
		}
		a.recordAttemptWithoutWindow(authID, record, cfg.StatePath)
		return record
	}
	defer a.clearActiveWarmup(authID)

	models := warmupModelCandidates(cfg, authID)
	if len(models) == 0 {
		models = []string{defaultFirstModel()}
	}
	var (
		resp       pluginapi.HostModelExecutionResponse
		err        error
		record     attemptRecord
		fallbacks  []string
	)
	for idx, model := range models {
		resp, err = executeHostWarmup(authID, model, cfg.Prompt, hostCallbackID)
		record = attemptRecord{
			At:        now,
			WindowKey: windowKey,
			Model:     model,
		}
		if err != nil {
			record.Error = err.Error()
		} else {
			record.StatusCode = resp.StatusCode
			record.ResponseSummary = summarizeResponse(resp.Body)
			record.Success = resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
			if !record.Success {
				record.Error = fmt.Sprintf("status %d", resp.StatusCode)
			}
		}
		if record.Success {
			break
		}
		quotaBlocked := isWarmupQuotaBlocked(record.StatusCode, resp.Headers, resp.Body, err)
		modelUnavailable := isWarmupModelUnavailable(record.StatusCode, resp.Body, err)
		if quotaBlocked || !modelUnavailable || idx == len(models)-1 {
			break
		}
		fallbacks = append(fallbacks, fmt.Sprintf("%s: %s", model, warmupErrorText(record)))
	}
	if len(fallbacks) > 0 && !record.Success {
		if strings.TrimSpace(record.Error) == "" {
			record.Error = "模型兜底记录: " + strings.Join(fallbacks, " | ")
		} else {
			record.Error = strings.TrimSpace(record.Error) + "; 模型兜底记录: " + strings.Join(fallbacks, " | ")
		}
	}
	quotaBlocked := !record.Success && isWarmupQuotaBlocked(record.StatusCode, resp.Headers, resp.Body, err)
	quotaUntil := time.Time{}
	if quotaBlocked {
		quotaUntil = warmupQuotaBlockUntil(resp.Headers, cfg.MinDuration, now)
	}

	a.mu.Lock()
	a.state.recordAttempt(authID, record)
	if quotaBlocked {
		a.state.recordQuotaBlocked(authID, now, quotaUntil, warmupQuotaReason(record))
	}
	if err := saveState(cfg.StatePath, a.state); err != nil {
		a.lastError = err.Error()
	}
	a.mu.Unlock()
	return record
}

func (a *app) recheckLocalQuotaBlockBeforeWarmup(authID, windowKey string, cfg runtimeConfig, now time.Time) (attemptRecord, bool) {
	a.mu.Lock()
	until, reason, blocked := a.state.quotaBlockInfo(authID, now)
	a.mu.Unlock()
	if !blocked {
		return attemptRecord{}, false
	}
	models := warmupModelCandidates(cfg, authID)
	allowed, err := schedulableAuthIDs()
	if err != nil {
		a.setLastError(err.Error())
		return a.recordLocalQuotaBlockedAttempt(authID, windowKey, cfg, models, until, reason, now), true
	}
	entry, ok := allowed[authID]
	if !ok {
		return a.recordLocalQuotaBlockedAttempt(authID, windowKey, cfg, models, until, reason, now), true
	}
	return a.reconcileQuotaBlockFromUsage(authID, windowKey, cfg, models, fetchAuthUsage(entry), now, true)
}

func (a *app) recordLocalQuotaBlockedAttempt(authID, windowKey string, cfg runtimeConfig, models []string, until time.Time, reason string, now time.Time) attemptRecord {
	record := attemptRecord{
		At:        now,
		WindowKey: windowKey,
		Model:     strings.Join(models, ", "),
		Success:   false,
		Error:     quotaBlockedError(reason, until),
	}
	if record.Model == "" {
		record.Model = cfg.Model
	}
	a.mu.Lock()
	a.state.recordAttempt(authID, record)
	if err := saveState(cfg.StatePath, a.state); err != nil {
		a.lastError = err.Error()
	}
	a.mu.Unlock()
	return record
}

func (a *app) recordHostQuotaBlockedAttempt(authID, windowKey string, cfg runtimeConfig, models []string, entry pluginapi.HostAuthFileEntry, now time.Time) bool {
	reason := authBlockedReason(entry, true, false, "")
	until := hostQuotaBlockUntil(entry, cfg.MinDuration, now)
	record := attemptRecord{
		At:        now,
		WindowKey: windowKey,
		Model:     strings.Join(models, ", "),
		Success:   false,
		Error:     quotaBlockedError(reason, until),
	}
	if record.Model == "" {
		record.Model = cfg.Model
	}
	a.mu.Lock()
	a.state.recordAttempt(authID, record)
	a.state.recordQuotaBlocked(authID, now, until, reason)
	if err := saveState(cfg.StatePath, a.state); err != nil {
		a.lastError = err.Error()
	}
	a.mu.Unlock()
	return true
}

func hostQuotaBlockUntil(entry pluginapi.HostAuthFileEntry, fallback time.Duration, now time.Time) time.Time {
	if entry.NextRetryAfter.After(now) {
		return entry.NextRetryAfter
	}
	if fallback <= 0 {
		fallback = 5 * time.Hour
	}
	return now.Add(fallback)
}

func (a *app) reconcileQuotaBlockFromUsage(authID, windowKey string, cfg runtimeConfig, models []string, usage usageEntry, now time.Time, recordAttempt bool) (attemptRecord, bool) {
	if authID == "" {
		authID = usage.AuthID
	}
	if authID == "" {
		return attemptRecord{}, false
	}
	modelText := strings.Join(models, ", ")
	if modelText == "" {
		modelText = cfg.Model
	}
	if usage.Err != "" {
		a.mu.Lock()
		until, reason, blocked := a.state.quotaBlockInfo(authID, now)
		if !blocked {
			a.mu.Unlock()
			return attemptRecord{}, false
		}
		record := attemptRecord{
			At:        now,
			WindowKey: windowKey,
			Model:     modelText,
			Success:   false,
			Error:     quotaBlockedError(reason, until),
		}
		if recordAttempt {
			a.state.recordAttempt(authID, record)
			if err := saveState(cfg.StatePath, a.state); err != nil {
				a.lastError = err.Error()
			}
		}
		a.mu.Unlock()
		return record, true
	}

	block := usageQuotaBlocked(usage, models, now)
	a.mu.Lock()
	defer a.mu.Unlock()
	if block.Blocked {
		record := attemptRecord{
			At:        now,
			WindowKey: windowKey,
			Model:     modelText,
			Success:   false,
			Error:     quotaBlockedError(block.Reason, block.Until),
		}
		if recordAttempt {
			a.state.recordAttempt(authID, record)
		}
		a.state.recordQuotaBlocked(authID, now, block.Until, block.Reason)
		if err := saveState(cfg.StatePath, a.state); err != nil {
			a.lastError = err.Error()
		}
		return record, true
	}
	if a.state.clearQuotaBlocked(authID) {
		if err := saveState(cfg.StatePath, a.state); err != nil {
			a.lastError = err.Error()
		}
	}
	return attemptRecord{}, false
}

func warmupErrorText(record attemptRecord) string {
	if strings.TrimSpace(record.Error) != "" {
		return strings.TrimSpace(record.Error)
	}
	if strings.TrimSpace(record.ResponseSummary) != "" {
		return strings.TrimSpace(record.ResponseSummary)
	}
	if record.StatusCode != 0 {
		return fmt.Sprintf("status %d", record.StatusCode)
	}
	return "failed"
}

func (a *app) claimActiveWarmup(authID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := windowPendingKey(authID, "__active__")
	if _, ok := a.pending[key]; ok {
		return false
	}
	a.pending[key] = 0
	return true
}

func (a *app) clearActiveWarmup(authID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, windowPendingKey(authID, "__active__"))
}

func (a *app) recordAttemptWithoutWindow(authID string, record attemptRecord, statePath string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	stateRecord := record
	stateRecord.WindowKey = ""
	a.state.recordAttempt(authID, stateRecord)
	if err := saveState(statePath, a.state); err != nil {
		a.lastError = err.Error()
	}
}

func warmupQuotaBlockUntil(headers http.Header, fallback time.Duration, now time.Time) time.Time {
	if headers != nil {
		retryAfter := strings.TrimSpace(headers.Get("Retry-After"))
		if retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
				return now.Add(time.Duration(seconds) * time.Second)
			}
			if at, err := http.ParseTime(retryAfter); err == nil && at.After(now) {
				return at
			}
		}
	}
	if fallback <= 0 {
		fallback = 5 * time.Hour
	}
	return now.Add(fallback)
}

func warmupQuotaReason(record attemptRecord) string {
	reason := strings.TrimSpace(record.Error)
	summary := strings.TrimSpace(record.ResponseSummary)
	if summary != "" {
		if reason != "" {
			reason += ": "
		}
		reason += summary
	}
	if reason == "" {
		return "quota_or_rate_limit"
	}
	if len(reason) > 512 {
		return reason[:512]
	}
	return reason
}

func quotaBlockedError(reason string, until time.Time) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "quota_or_rate_limit"
	}
	if until.IsZero() {
		return "quota_blocked: " + reason
	}
	return fmt.Sprintf("quota_blocked_until %s: %s", until.Format(time.RFC3339), reason)
}

func (a *app) setLastError(message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastError = message
}

func (a *app) updateConfigFromBody(raw []byte) (runtimeConfig, error) {
	a.mu.Lock()
	current := a.cfg.pluginConfig
	a.mu.Unlock()
	cfg, err := mergeConfigPatch(current, raw)
	if err != nil {
		return runtimeConfig{}, err
	}
	if err := validateConfigForSave(cfg); err != nil {
		return runtimeConfig{}, err
	}
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return runtimeConfig{}, err
	}
	state, err := loadState(normalized.StatePath)
	if err != nil {
		return runtimeConfig{}, err
	}
	if err := saveConfigOverride(normalized.ConfigPath, normalized.pluginConfig); err != nil {
		return runtimeConfig{}, err
	}
	a.mu.Lock()
	a.stopWorkerLocked()
	a.cfg = normalized
	a.state = state
	if normalized.Enabled {
		a.startWorkerLocked()
	}
	a.mu.Unlock()
	return normalized, nil
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	Scheduler     bool `json:"scheduler"`
	ManagementAPI bool `json:"management_api"`
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             pluginName,
			Version:          pluginVersion,
			Author:           pluginAuthor,
			GitHubRepository: pluginRepository,
		},
		Capabilities: registrationCapabilities{Scheduler: true, ManagementAPI: true},
	}
}
