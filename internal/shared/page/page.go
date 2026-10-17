// Package page 提供不透明游标分页的纯编码/解析逻辑：
// 时间+ID 复合游标（时间线、评论回复）与纯 ID 游标（后台列表、版本历史），
// 以及通用 limit 解析。只依赖标准库；HTTP query 读取与响应组装由 transport/httpx 负责。
package page

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 通用游标分页大小限制（与旧 internal/api.PageLimit 一致）。
const (
	DefaultLimit = 20
	MinLimit     = 1
	MaxLimit     = 50
)

var (
	// ErrInvalidCursor 由调用方映射为 400 invalid_request。
	ErrInvalidCursor = errors.New("无效的游标")
	// ErrInvalidLimit 由调用方映射为 400 invalid_request。
	ErrInvalidLimit = errors.New("limit 必须是 1-50 的整数")
)

// IDCursor 是按单列 ID 倒序分页的游标。
type IDCursor struct {
	ID int64
}

// Valid 表示游标包含可用 ID。
func (c IDCursor) Valid() bool { return c.ID > 0 }

// TimeCursor 是带时间戳的分页游标。
// 时间线按 (sort_at, item_id) 排序，只带 ID 会在定时发布后错序，因此游标必须包含时间。
type TimeCursor struct {
	Time time.Time
	ID   int64
}

// Valid 表示游标同时包含时间与 ID。
func (c TimeCursor) Valid() bool { return c.ID > 0 && !c.Time.IsZero() }

// idCursorPayload / timeCursorPayload 是游标的内部 JSON 形状，保持旧版字段名（t/i）不变；
// 客户端只能原样回传，不依赖其结构。
type (
	idCursorPayload struct {
		ID int64 `json:"id"`
	}
	timeCursorPayload struct {
		Time time.Time `json:"t"`
		ID   int64     `json:"i"`
	}
)

// EncodeIDCursor 生成不透明 ID 游标。
func EncodeIDCursor(id int64) string {
	payload, err := json.Marshal(idCursorPayload{ID: id})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeIDCursor 解析 ID 游标；空字符串表示首页，返回 (IDCursor{}, nil)。
func DecodeIDCursor(raw string) (IDCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return IDCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return IDCursor{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	var payload idCursorPayload
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil || payload.ID <= 0 {
		return IDCursor{}, ErrInvalidCursor
	}
	return IDCursor{ID: payload.ID}, nil
}

// EncodeTimeCursor 生成不透明时间+ID 游标，时间统一按 UTC 编码。
func EncodeTimeCursor(at time.Time, id int64) string {
	payload, err := json.Marshal(timeCursorPayload{Time: at.UTC(), ID: id})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeTimeCursor 解析时间+ID 游标；空字符串表示首页，返回 (TimeCursor{}, nil)。
func DecodeTimeCursor(raw string) (TimeCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return TimeCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return TimeCursor{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	var payload timeCursorPayload
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil || payload.ID <= 0 || payload.Time.IsZero() {
		return TimeCursor{}, ErrInvalidCursor
	}
	return TimeCursor{Time: payload.Time, ID: payload.ID}, nil
}

// ParseLimit 解析通用游标分页大小；空字符串使用默认值。
func ParseLimit(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DefaultLimit, nil
	}
	limit, err := strconv.Atoi(trimmed)
	if err != nil || limit < MinLimit || limit > MaxLimit {
		return 0, ErrInvalidLimit
	}
	return limit, nil
}
