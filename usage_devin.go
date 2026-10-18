package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

const devinUsageURL = "https://server.codeium.com/exa.seat_management_pb.SeatManagementService/GetUserStatus"

// 与 CPA internal/auth/devin/user_status.go 的 Connect-RPC 协议保持一致。
// 凭证从宿主读取，插件配置仅保存 auth ID，不复制 session token。
func fetchDevinUsage(authID, authIndex, email string) usageEntry {
	entry := usageEntry{AuthID: authID, Provider: "devin", Email: email, Weight: 1}
	if strings.TrimSpace(authIndex) == "" {
		authIndex = authID
	}
	doc, err := authDocumentForAuth(authIndex)
	if err != nil {
		entry.Err = "无法读取 Devin 凭证：" + err.Error()
		return entry
	}
	entry.Plan = extractAuthString(doc, "plan")
	token := extractAuthString(doc, "session_token", "api_key")
	if token == "" {
		entry.Err = "Devin 凭证缺少 session_token，请在 CPA 中重新登录"
		return entry
	}
	if strings.HasPrefix(token, "eyJ") {
		token = "devin-session-token$" + token
	}
	headers := http.Header{}
	headers.Set("Authorization", "Basic "+token+"-"+token)
	headers.Set("Connect-Protocol-Version", "1")
	headers.Set("Content-Type", "application/proto")
	headers.Set("Accept", "*/*")
	headers["User-Agent"] = []string{""}
	body := buildDevinUsageRequest(token, extractAuthString(doc, "device_seed"))
	resp, err := callHostHTTPDo(http.MethodPost, devinUsageURL, headers, body)
	if err != nil {
		entry.Err = "Devin 额度查询失败：" + err.Error()
		return entry
	}
	if resp.StatusCode != http.StatusOK {
		entry.Err = fmt.Sprintf("Devin 额度查询失败（HTTP %d），请检查凭证和网络连接", resp.StatusCode)
		return entry
	}
	if err := parseDevinUsageBody(resp.Body, &entry); err != nil {
		// 不能让部分解析成功的字段被误当成完整额度证据。
		entry.Daily, entry.Secondary = usageWindow{}, usageWindow{}
		entry.Err = "Devin 额度响应解析失败：" + err.Error()
	}
	return entry
}

func appendDevinBytes(dst []byte, field protowire.Number, value []byte) []byte {
	dst = protowire.AppendTag(dst, field, protowire.BytesType)
	return protowire.AppendBytes(dst, value)
}

func buildDevinUsageRequest(token, seed string) []byte {
	if seed == "" {
		seed = token
	}
	var fingerprint strings.Builder
	for i := 0; fingerprint.Len() < 732; i++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", seed, i)))
		fingerprint.WriteString(hex.EncodeToString(digest[:]))
	}
	var metadata []byte
	for _, field := range []struct {
		number protowire.Number
		value  string
	}{
		{1, "chisel"}, {2, "3000.10.21"}, {3, token}, {4, "en"},
		{5, runtime.GOOS}, {7, "3000.10.21"}, {12, "chisel"}, {31, fingerprint.String()[:732]},
	} {
		metadata = appendDevinBytes(metadata, field.number, []byte(field.value))
	}
	return appendDevinBytes(nil, 1, metadata)
}

// 统一检查所有层的 wire 长度；未知字段按 protobuf 规则跳过。
func walkDevinProto(data []byte, visit func(protowire.Number, protowire.Type, []byte, uint64) error) error {
	for len(data) > 0 {
		number, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return protowire.ParseError(n)
		}
		data = data[n:]
		var value []byte
		var integer uint64
		switch typ {
		case protowire.BytesType:
			value, n = protowire.ConsumeBytes(data)
		case protowire.VarintType:
			integer, n = protowire.ConsumeVarint(data)
		default:
			n = protowire.ConsumeFieldValue(number, typ, data)
		}
		if n < 0 {
			return protowire.ParseError(n)
		}
		if err := visit(number, typ, value, integer); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func parseDevinUsageBody(body []byte, entry *usageEntry) error {
	entry.Daily, entry.Secondary = usageWindow{}, usageWindow{}
	err := walkDevinProto(body, func(number protowire.Number, typ protowire.Type, user []byte, _ uint64) error {
		if number != 1 || typ != protowire.BytesType {
			return nil
		}
		return walkDevinProto(user, func(number protowire.Number, typ protowire.Type, value []byte, _ uint64) error {
			if typ != protowire.BytesType {
				return nil
			}
			switch number {
			case 7:
				entry.Email = string(value)
			case 13:
				return parseDevinPlanStatus(value, entry)
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	if !entry.Daily.HasData && !entry.Secondary.HasData {
		return fmt.Errorf("未返回日额度或周额度")
	}
	return nil
}

func parseDevinPlanStatus(data []byte, entry *usageEntry) error {
	var dailyRemaining, weeklyRemaining uint64
	var hasDaily, hasWeekly bool
	err := walkDevinProto(data, func(number protowire.Number, typ protowire.Type, value []byte, integer uint64) error {
		if number == 1 && typ == protowire.BytesType {
			return walkDevinProto(value, func(number protowire.Number, typ protowire.Type, text []byte, _ uint64) error {
				if number == 2 && typ == protowire.BytesType && strings.TrimSpace(string(text)) != "" {
					entry.Plan = strings.TrimSpace(string(text))
				}
				return nil
			})
		}
		if typ != protowire.VarintType {
			return nil
		}
		switch number {
		case 14, 15:
			if number == 14 {
				dailyRemaining, hasDaily = integer, true
			} else {
				weeklyRemaining, hasWeekly = integer, true
			}
		case 17, 18:
			if integer > 0 && integer <= 253402300799 {
				window := &entry.Daily
				if number == 18 {
					window = &entry.Secondary
				}
				window.ResetAt = time.Unix(int64(integer), 0)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Max 没有日限额。上游可能仍携带日窗口占位字段，不能用于展示、
	// 汇总或预热阻塞判断。等整个 plan_status 解析结束，避免依赖字段顺序。
	weeklyOnly := strings.EqualFold(strings.TrimSpace(entry.Plan), "max")
	if weeklyRemaining > 100 || (!weeklyOnly && dailyRemaining > 100) {
		return fmt.Errorf("剩余额度百分比超出范围")
	}
	// protobuf 可省略值为 0 的标量；有重置时间的窗口仍表示额度已用尽。
	entry.Daily.HasData = hasDaily || !entry.Daily.ResetAt.IsZero()
	entry.Secondary.HasData = hasWeekly || !entry.Secondary.ResetAt.IsZero()
	if entry.Daily.HasData {
		entry.Daily.UsedPercent = 100 - float64(dailyRemaining)
	}
	if entry.Secondary.HasData {
		entry.Secondary.UsedPercent = 100 - float64(weeklyRemaining)
	}
	if weeklyOnly {
		entry.Daily = usageWindow{}
	}
	return nil
}
