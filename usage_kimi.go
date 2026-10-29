package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	kimiUsageURL = "https://api.kimi.com/coding/v1/usages"
)

// fetchKimiUsage 通过 Kimi 开放接口拉取额度。
func fetchKimiUsage(authID, authIndex, email string) usageEntry {
	entry := usageEntry{AuthID: authID, Provider: "kimi", Email: email, Weight: 1}
	if strings.TrimSpace(authIndex) == "" {
		authIndex = authID
	}
	token, err := accessTokenForAuth(authIndex)
	if err != nil {
		entry.Err = "无法读取 Kimi 凭证：" + err.Error()
		return entry
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("User-Agent", "KimiCLI/1.10.6")
	headers.Set("Accept", "application/json")
	resp, err := callHostHTTPDo(http.MethodGet, kimiUsageURL, headers, nil)
	if err != nil {
		entry.Err = "Kimi 额度查询失败：" + err.Error()
		return entry
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		entry.Err = fmt.Sprintf("Kimi 额度查询失败（HTTP %d）", resp.StatusCode)
		return entry
	}
	if err := parseKimiUsageBody(resp.Body, &entry); err != nil {
		entry.Err = "Kimi 额度解析失败：" + err.Error()
	}
	return entry
}

func parseKimiUsageBody(body []byte, entry *usageEntry) error {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}

	entry.Primary = usageWindow{}
	entry.Secondary = usageWindow{}

	var items []map[string]any
	if limits, ok := raw["limits"].([]any); ok {
		for _, item := range limits {
			if m, ok := item.(map[string]any); ok {
				items = append(items, m)
			}
		}
	}
	if usages, ok := raw["usages"].([]any); ok {
		for _, u := range usages {
			if um, ok := u.(map[string]any); ok {
				if limits, ok := um["limits"].([]any); ok {
					for _, item := range limits {
						if m, ok := item.(map[string]any); ok {
							items = append(items, m)
						}
					}
				}
				if detail, ok := um["detail"].(map[string]any); ok {
					items = append(items, detail)
				}
			}
		}
	}
	if usage, ok := raw["usage"].(map[string]any); ok {
		items = append(items, usage)
	}

	for _, item := range items {
		win, seconds := parseKimiLimitItem(item)
		if !win.HasData {
			continue
		}
		// 5小时窗口（约 18000 秒，允许 3.5h ~ 6h）
		if seconds >= 3*3600+1800 && seconds <= 6*3600 {
			if !entry.Primary.HasData || win.UsedPercent > entry.Primary.UsedPercent {
				entry.Primary = win
			}
		} else if seconds >= 6*24*3600 && seconds <= 8*24*3600 {
			// 周限额窗口（约 7 天 = 604800 秒）
			if !entry.Secondary.HasData || win.UsedPercent > entry.Secondary.UsedPercent {
				entry.Secondary = win
			}
		} else if seconds >= 18*3600 && seconds <= 30*3600 {
			// 日限额窗口（约 24 小时）
			if !entry.Daily.HasData {
				entry.Daily = win
			}
		} else {
			if !entry.Primary.HasData {
				entry.Primary = win
			} else if !entry.Secondary.HasData {
				entry.Secondary = win
			}
		}
	}

	if !entry.Primary.HasData && !entry.Secondary.HasData && !entry.Daily.HasData {
		return fmt.Errorf("未包含有效的额度窗口数据")
	}
	return nil
}

func parseKimiLimitItem(item map[string]any) (usageWindow, int64) {
	detail, _ := item["detail"].(map[string]any)

	durationVal := getAnyFloat(item["duration"])
	if durationVal == nil && detail != nil {
		durationVal = getAnyFloat(detail["duration"])
	}
	unitStr := getAnyString(item["timeUnit"])
	if unitStr == "" && detail != nil {
		unitStr = getAnyString(detail["timeUnit"])
	}
	var seconds int64
	if durationVal != nil && *durationVal > 0 {
		unit := strings.ToUpper(strings.TrimPrefix(unitStr, "TIME_UNIT_"))
		switch unit {
		case "MINUTE", "MINUTES":
			seconds = int64(*durationVal * 60)
		case "HOUR", "HOURS":
			seconds = int64(*durationVal * 3600)
		case "DAY", "DAYS":
			seconds = int64(*durationVal * 86400)
		case "WEEK", "WEEKS":
			seconds = int64(*durationVal * 604800)
		default:
			if unit == "SECOND" || unit == "SECONDS" || unit == "" {
				seconds = int64(*durationVal)
			}
		}
	}

	limit := getAnyFloat(item["limit"])
	if limit == nil && detail != nil {
		limit = getAnyFloat(detail["limit"])
	}
	used := getAnyFloat(item["used"])
	if used == nil && detail != nil {
		used = getAnyFloat(detail["used"])
	}
	remaining := getAnyFloat(item["remaining"])
	if remaining == nil && detail != nil {
		remaining = getAnyFloat(detail["remaining"])
	}

	if used == nil && remaining != nil && limit != nil {
		calcUsed := *limit - *remaining
		used = &calcUsed
	}

	if limit == nil && used == nil && remaining == nil {
		return usageWindow{}, 0
	}

	var usedPercent float64
	if limit != nil && *limit > 0 && used != nil {
		usedPercent = (*used / *limit) * 100
	} else if remaining != nil && *remaining <= 100 {
		usedPercent = 100 - *remaining
	} else if used != nil && *used <= 100 {
		usedPercent = *used
	}

	if usedPercent < 0 {
		usedPercent = 0
	}
	if usedPercent > 100 {
		usedPercent = 100
	}

	resetAt := parseAnyResetTime(item, detail)

	return usageWindow{
		UsedPercent: usedPercent,
		ResetAt:     resetAt,
		HasData:     true,
	}, seconds
}

func getAnyFloat(v any) *float64 {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case float64:
		return &val
	case float32:
		f := float64(val)
		return &f
	case int:
		f := float64(val)
		return &f
	case int64:
		f := float64(val)
		return &f
	case json.Number:
		if f, err := val.Float64(); err == nil {
			return &f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			return &f
		}
	}
	return nil
}

func getAnyString(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func parseAnyResetTime(maps ...map[string]any) time.Time {
	keys := []string{"reset_at", "resetAt", "reset_time", "resetTime"}
	for _, m := range maps {
		if m == nil {
			continue
		}
		for _, k := range keys {
			val, ok := m[k]
			if !ok || val == nil {
				continue
			}
			switch v := val.(type) {
			case float64:
				if v > 0 {
					sec := int64(v)
					if sec > 1e11 {
						sec = sec / 1000
					}
					return time.Unix(sec, 0)
				}
			case int64:
				if v > 0 {
					sec := v
					if sec > 1e11 {
						sec = sec / 1000
					}
					return time.Unix(sec, 0)
				}
			case string:
				s := strings.TrimSpace(v)
				if s == "" {
					continue
				}
				if num, err := strconv.ParseInt(s, 10, 64); err == nil && num > 0 {
					if num > 1e11 {
						num = num / 1000
					}
					return time.Unix(num, 0)
				}
				if t, err := time.Parse(time.RFC3339, s); err == nil {
					return t
				}
				if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
					return t
				}
			}
		}
	}
	return time.Time{}
}
