package httpx

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/shared/format"
)

// MaxJSONBody 是请求体 JSON 的防御性上限（1 MiB），与旧 internal/api 一致。
const MaxJSONBody = 1 << 20

// DecodeJSON 限制请求体大小、拒绝未知字段，并确保请求只包含一个 JSON 值。
func DecodeJSON(c *gin.Context, dst any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxJSONBody)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("请求体只能包含一个 JSON 对象")
		}
		return err
	}
	return nil
}

// ParseID 解析路径中的雪花 ID。数据库使用 BIGINT，JSON 输出统一走 FormatID。
func ParseID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("无效资源 ID")
	}
	return id, nil
}

// FormatID 把雪花 ID 序列化为字符串，避免 JavaScript 精度丢失。
// 统一委托 shared/format，保持全仓单一实现。
func FormatID(id int64) string { return format.FormatID(id) }

// FormatTime 明确输出北京时间和 RFC3339 偏移，避免依赖数据库返回值的 Location。
// 统一委托 shared/format，保持全仓单一实现。
func FormatTime(t time.Time) string { return format.FormatTime(t) }

// PageLimit 解析通用游标分页大小（默认 20，取值 1-50）。
func PageLimit(c *gin.Context) (int, error) {
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return 20, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 50 {
		return 0, fmt.Errorf("limit 必须是 1-50 的整数")
	}
	return limit, nil
}

// PageCursor 解析列表请求的游标参数。
func PageCursor(c *gin.Context) (int64, error) {
	return DecodeCursor(strings.TrimSpace(c.Query("cursor")))
}

// EncodeCursor 生成客户端无需也不能依赖其内部结构的不透明 ID 游标。
// 编码格式与旧实现保持一致（base64url(JSON{"id":N})），保证迁移期 API 契约不变。
func EncodeCursor(id int64) string {
	payload, _ := json.Marshal(map[string]int64{"id": id})
	return base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeCursor 解析服务端生成的 ID 游标。
func DecodeCursor(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, fmt.Errorf("无效游标")
	}
	var cursor struct {
		ID int64 `json:"id"`
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cursor); err != nil || cursor.ID <= 0 {
		return 0, fmt.Errorf("无效游标")
	}
	return cursor.ID, nil
}

// ListResponse 组装统一列表响应；hasMore 为真时按本页最后一条的 ID 生成下一页游标。
func ListResponse(items any, lastID int64, hasMore bool) gin.H {
	var nextCursor any
	if hasMore && lastID > 0 {
		nextCursor = EncodeCursor(lastID)
	}
	return gin.H{"items": items, "next_cursor": nextCursor, "has_more": hasMore}
}
