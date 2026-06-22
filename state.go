package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type pluginState struct {
	UpdatedAt time.Time             `json:"updated_at"`
	Auths     map[string]*authState `json:"auths"`
}

type authState struct {
	LastSuccessAt *time.Time              `json:"last_success_at,omitempty"`
	LastAttempt   *attemptRecord          `json:"last_attempt,omitempty"`
	Windows       map[string]windowRecord `json:"windows,omitempty"`
}

type attemptRecord struct {
	At              time.Time `json:"at"`
	WindowKey       string    `json:"window_key,omitempty"`
	Model           string    `json:"model,omitempty"`
	Success         bool      `json:"success"`
	StatusCode      int       `json:"status_code,omitempty"`
	Error           string    `json:"error,omitempty"`
	ResponseSummary string    `json:"response_summary,omitempty"`
}

type windowRecord struct {
	At     time.Time `json:"at"`
	Status string    `json:"status"`
	Reason string    `json:"reason,omitempty"`
}

func newPluginState() *pluginState {
	return &pluginState{Auths: map[string]*authState{}}
}

func (s *pluginState) clone() *pluginState {
	if s == nil {
		return newPluginState()
	}
	out := &pluginState{
		UpdatedAt: s.UpdatedAt,
		Auths:     make(map[string]*authState, len(s.Auths)),
	}
	for authID, item := range s.Auths {
		if item == nil {
			continue
		}
		copyItem := &authState{}
		if item.LastSuccessAt != nil {
			at := *item.LastSuccessAt
			copyItem.LastSuccessAt = &at
		}
		if item.LastAttempt != nil {
			attempt := *item.LastAttempt
			copyItem.LastAttempt = &attempt
		}
		if item.Windows != nil {
			copyItem.Windows = make(map[string]windowRecord, len(item.Windows))
			for key, record := range item.Windows {
				copyItem.Windows[key] = record
			}
		}
		out.Auths[authID] = copyItem
	}
	return out
}

func loadState(path string) (*pluginState, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return newPluginState(), nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return newPluginState(), nil
	}
	if err != nil {
		return nil, err
	}
	var state pluginState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state.Auths == nil {
		state.Auths = map[string]*authState{}
	}
	return &state, nil
}

func saveState(path string, state *pluginState) error {
	path = strings.TrimSpace(path)
	if path == "" || state == nil {
		return nil
	}
	state.UpdatedAt = time.Now()
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func (s *pluginState) ensureAuth(authID string) *authState {
	if s.Auths == nil {
		s.Auths = map[string]*authState{}
	}
	item := s.Auths[authID]
	if item == nil {
		item = &authState{Windows: map[string]windowRecord{}}
		s.Auths[authID] = item
	}
	if item.Windows == nil {
		item.Windows = map[string]windowRecord{}
	}
	return item
}

func (s *pluginState) lastSuccess(authID string) time.Time {
	if s == nil || s.Auths == nil || s.Auths[authID] == nil || s.Auths[authID].LastSuccessAt == nil {
		return time.Time{}
	}
	return *s.Auths[authID].LastSuccessAt
}

func (s *pluginState) windowProcessed(authID, windowKey string) bool {
	if s == nil || s.Auths == nil || s.Auths[authID] == nil {
		return false
	}
	_, ok := s.Auths[authID].Windows[windowKey]
	return ok
}

func (s *pluginState) recordAttempt(authID string, record attemptRecord) {
	item := s.ensureAuth(authID)
	item.LastAttempt = &record
	if record.WindowKey != "" {
		status := "failed"
		reason := record.Error
		if record.Success {
			status = "success"
			reason = ""
		}
		item.Windows[record.WindowKey] = windowRecord{At: record.At, Status: status, Reason: reason}
	}
	if record.Success {
		at := record.At
		item.LastSuccessAt = &at
	}
}

func (s *pluginState) recordSkip(authID, windowKey, reason string, at time.Time) {
	item := s.ensureAuth(authID)
	item.Windows[windowKey] = windowRecord{At: at, Status: "skipped", Reason: reason}
	item.LastAttempt = &attemptRecord{
		At:        at,
		WindowKey: windowKey,
		Success:   false,
		Error:     reason,
	}
}
