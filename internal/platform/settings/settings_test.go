package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/great-magician-01/timeline/internal/platform/websearch"
)

// recordedExecutor 记录 Exec 调用参数，用于无数据库断言显式更新时间。
type recordedExecutor struct {
	sql  string
	args []any
}

func (e *recordedExecutor) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.sql = sql
	e.args = args
	return pgconn.CommandTag{}, nil
}

func (e *recordedExecutor) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, errors.New("测试不应调用 Query")
}

func (e *recordedExecutor) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

func TestDefaultsAndApply(t *testing.T) {
	v := Defaults()
	if v.MaxUploadBytes != DefaultMaxUploadBytes || v.PostRateLimit != DefaultPostRateLimit {
		t.Fatalf("默认值不正确: %+v", v)
	}

	apply(&v, KeyUploadMaxFileBytes, json.RawMessage(`1048576`))
	apply(&v, KeyStorageQuotaBytes, json.RawMessage(`"2097152"`))
	apply(&v, KeyPostRateLimit, json.RawMessage(`12`))
	apply(&v, KeyPostRateWindowSeconds, json.RawMessage(`60`))
	apply(&v, KeyTempAttachmentRetentionHour, json.RawMessage(`48`))
	apply(&v, KeyCallLogMaxBytes, json.RawMessage(`1024`))
	apply(&v, KeyCallLogRetentionDays, json.RawMessage(`0`))
	apply(&v, KeyRegistrationEnabled, json.RawMessage(`false`))

	if v.MaxUploadBytes != 1048576 {
		t.Fatalf("单文件上限未生效: %d", v.MaxUploadBytes)
	}
	if v.MaxRequestBytes < v.MaxUploadBytes+DefaultUploadOverhead {
		t.Fatalf("请求上限未按文件上限补齐: %d", v.MaxRequestBytes)
	}
	if v.StorageQuotaBytes != 2097152 {
		t.Fatalf("字符串数字未解析: %d", v.StorageQuotaBytes)
	}
	if v.PostRateLimit != 12 || v.PostRateWindow != time.Minute {
		t.Fatalf("发帖限频未生效: %+v", v)
	}
	if v.TempAttachmentRetention != 48*time.Hour {
		t.Fatalf("临时附件保留期未生效: %s", v.TempAttachmentRetention)
	}
	if v.CallLogRetentionDays != 0 {
		t.Fatalf("保留期 0 应表示关闭自动清理: %d", v.CallLogRetentionDays)
	}
	if v.RegistrationEnabled {
		t.Fatal("注册开关未生效")
	}
}

func TestApplyIgnoresInvalidValues(t *testing.T) {
	v := Defaults()
	apply(&v, KeyUploadMaxFileBytes, json.RawMessage(`-1`))
	apply(&v, KeyPostRateLimit, json.RawMessage(`"abc"`))
	apply(&v, KeyCallLogMaxBytes, json.RawMessage(`0`))
	if v.MaxUploadBytes != DefaultMaxUploadBytes || v.PostRateLimit != DefaultPostRateLimit || v.CallLogMaxBytes != DefaultCallLogMaxBytes {
		t.Fatalf("非法值不应覆盖默认值: %+v", v)
	}
}

func TestValidate(t *testing.T) {
	if details := Defaults().Validate(); len(details) != 0 {
		t.Fatalf("默认设置应合法: %+v", details)
	}
	v := Defaults()
	v.MaxRequestBytes = 1
	v.PostRateLimit = 0
	details := v.Validate()
	if details[KeyUploadMaxRequestBytes] == "" || details[KeyPostRateLimit] == "" {
		t.Fatalf("应返回字段错误: %+v", details)
	}
}

func TestSetPassesExplicitUpdatedAt(t *testing.T) {
	exec := &recordedExecutor{}
	at := time.Date(2026, 10, 18, 11, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	if err := Set(context.Background(), exec, KeyRegistrationEnabled, false, 9, at); err != nil {
		t.Fatalf("写入设置失败: %v", err)
	}
	if exec.sql != sqlUpsertSetting {
		t.Fatal("应使用设置 upsert 语句")
	}
	if len(exec.args) != 4 {
		t.Fatalf("upsert 参数数量应为 4，实际 %d", len(exec.args))
	}
	if got := exec.args[0]; got != KeyRegistrationEnabled {
		t.Fatalf("key 错误: %v", got)
	}
	if got := exec.args[1]; got != "false" {
		t.Fatalf("value 应 JSON 编码，实际 %v", got)
	}
	if got := exec.args[2]; got != int64(9) {
		t.Fatalf("updated_by 错误: %v", got)
	}
	if got := exec.args[3]; got != at {
		t.Fatalf("updated_at 应使用显式应用时间，实际 %v", got)
	}
}

func TestWebSearchValues(t *testing.T) {
	if got := DefaultWebSearch().BaseURL; got != websearch.DefaultBaseURL {
		t.Fatalf("未配置时默认 BaseURL 错误: %s", got)
	}
	if DefaultWebSearch().Configured() || DefaultWebSearch().Ready() {
		t.Fatal("未配置密钥时不应就绪")
	}
	configured := WebSearchValues{APIKeyCipher: "cipher", BaseURL: "https://example.com"}
	if !configured.Configured() || !configured.Ready() {
		t.Fatal("已配置密钥与 BaseURL 时应就绪")
	}
	if (WebSearchValues{APIKeyCipher: "cipher"}).Ready() {
		t.Fatal("缺少 BaseURL 时不应就绪（避免把密钥发往默认服务）")
	}
}

// TestSQLUsesApplicationTimeOnly 静态守卫：settings 的 SQL 不依赖数据库时间函数。
func TestSQLUsesApplicationTimeOnly(t *testing.T) {
	for name, sql := range map[string]string{
		"sqlSelectSettings": sqlSelectSettings,
		"sqlUpsertSetting":  sqlUpsertSetting,
	} {
		lower := strings.ToLower(sql)
		for _, word := range []string{"now()", "current_timestamp", "current_date", "interval"} {
			if strings.Contains(lower, word) {
				t.Fatalf("%s 含数据库时间函数/运算 %q，应改为应用时间参数", name, word)
			}
		}
	}
	if !strings.Contains(strings.ToLower(sqlUpsertSetting), "updated_at") {
		t.Fatal("设置 upsert 必须显式写入 updated_at")
	}
}
