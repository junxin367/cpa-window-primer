package main

import (
	"testing"
	"time"
)

func TestParseClock(t *testing.T) {
	got, err := parseClock("07:05")
	if err != nil {
		t.Fatalf("parseClock returned error: %v", err)
	}
	if got.Hour != 7 || got.Minute != 5 {
		t.Fatalf("parseClock = %#v, want 07:05", got)
	}
	if _, err := parseClock("24:00"); err == nil {
		t.Fatal("parseClock accepted out-of-range hour")
	}
	if _, err := parseClock("7"); err == nil {
		t.Fatal("parseClock accepted missing minute")
	}
}

func TestWindowForDefaultTimes(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	now := time.Date(2026, 6, 22, 6, 59, 30, 0, loc)
	clock, err := parseClock("07:00")
	if err != nil {
		t.Fatal(err)
	}
	start, target := windowFor(now, clock, time.Minute)
	wantStart := time.Date(2026, 6, 22, 6, 59, 0, 0, loc)
	wantTarget := time.Date(2026, 6, 22, 7, 0, 0, 0, loc)
	if !start.Equal(wantStart) || !target.Equal(wantTarget) {
		t.Fatalf("windowFor = %s/%s, want %s/%s", start, target, wantStart, wantTarget)
	}
}

func TestEvaluateWindowSendsWithinWindowWithoutHistory(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 22, 6, 59, 10, 0, loc)
	decision := evaluateWindow(now, clockTime{Hour: 7}, time.Minute, 5*time.Hour, time.Time{}, false)
	if decision.Action != windowActionSend {
		t.Fatalf("Action = %q, want send", decision.Action)
	}
}

func TestEvaluateWindowWaitsUntilFiveHoursWithinWindow(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 22, 11, 59, 10, 0, loc)
	lastSuccess := time.Date(2026, 6, 22, 6, 59, 30, 0, loc)
	decision := evaluateWindow(now, clockTime{Hour: 12}, time.Minute, 5*time.Hour, lastSuccess, false)
	if decision.Action != windowActionWait {
		t.Fatalf("Action = %q, want wait", decision.Action)
	}
	want := time.Date(2026, 6, 22, 11, 59, 30, 0, loc)
	if !decision.SendAt.Equal(want) {
		t.Fatalf("SendAt = %s, want %s", decision.SendAt, want)
	}
}

func TestEvaluateWindowSkipsWhenFiveHoursWouldMissTarget(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 22, 11, 59, 10, 0, loc)
	lastSuccess := time.Date(2026, 6, 22, 7, 0, 0, 0, loc)
	decision := evaluateWindow(now, clockTime{Hour: 12}, time.Minute, 5*time.Hour, lastSuccess, false)
	if decision.Action != windowActionSkip || decision.Reason != "min_interval_not_met" {
		t.Fatalf("decision = %#v, want min_interval_not_met skip", decision)
	}
}

func TestEvaluateWindowWaitsWhenEarliestAllowedIsJustBeforeTarget(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 22, 11, 59, 55, 0, loc)
	lastSuccess := time.Date(2026, 6, 22, 6, 59, 59, 0, loc)
	decision := evaluateWindow(now, clockTime{Hour: 12}, time.Minute, 5*time.Hour, lastSuccess, false)
	if decision.Action != windowActionWait {
		t.Fatalf("Action = %q, want wait", decision.Action)
	}
	want := time.Date(2026, 6, 22, 11, 59, 59, 0, loc)
	if !decision.SendAt.Equal(want) {
		t.Fatalf("SendAt = %s, want %s", decision.SendAt, want)
	}
}

func TestEvaluateWindowOutsideWindow(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 22, 6, 58, 59, 0, loc)
	decision := evaluateWindow(now, clockTime{Hour: 7}, time.Minute, 5*time.Hour, time.Time{}, false)
	if decision.Action != windowActionNone {
		t.Fatalf("Action = %q, want none", decision.Action)
	}
}
