package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	windowActionNone = "none"
	windowActionSend = "send"
	windowActionWait = "wait"
	windowActionSkip = "skip"
)

type clockTime struct {
	Hour   int
	Minute int
}

func (c clockTime) String() string {
	return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute)
}

type windowDecision struct {
	Action    string
	Reason    string
	WindowKey string
	Start     time.Time
	Target    time.Time
	SendAt    time.Time
}

func parseClock(raw string) (clockTime, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return clockTime{}, fmt.Errorf("invalid time %q: use HH:mm", raw)
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return clockTime{}, fmt.Errorf("invalid hour in %q", raw)
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return clockTime{}, fmt.Errorf("invalid minute in %q", raw)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return clockTime{}, fmt.Errorf("invalid time %q: out of range", raw)
	}
	return clockTime{Hour: hour, Minute: minute}, nil
}

func windowFor(now time.Time, clock clockTime, lead time.Duration) (time.Time, time.Time) {
	loc := now.Location()
	target := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour, clock.Minute, 0, 0, loc)
	return target.Add(-lead), target
}

func makeWindowKey(target time.Time) string {
	return target.Format("2006-01-02T15:04")
}

func evaluateWindow(now time.Time, clock clockTime, lead time.Duration, minInterval time.Duration, lastSuccess time.Time, processed bool) windowDecision {
	start, target := windowFor(now, clock, lead)
	decision := windowDecision{
		Action:    windowActionNone,
		WindowKey: makeWindowKey(target),
		Start:     start,
		Target:    target,
	}
	if now.Before(start) || !now.Before(target) {
		return decision
	}
	if processed {
		decision.Action = windowActionSkip
		decision.Reason = "window_already_processed"
		return decision
	}
	if lastSuccess.IsZero() || minInterval <= 0 {
		decision.Action = windowActionSend
		decision.SendAt = now
		return decision
	}
	earliest := lastSuccess.Add(minInterval)
	if !now.Before(earliest) {
		decision.Action = windowActionSend
		decision.SendAt = now
		return decision
	}
	if earliest.Before(target) {
		decision.Action = windowActionWait
		decision.Reason = "waiting_for_min_interval"
		decision.SendAt = earliest
		return decision
	}
	decision.Action = windowActionSkip
	decision.Reason = "min_interval_not_met"
	return decision
}
