package settings

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/platform/websearch"
)

// 本文件用假 db.Executor/pgx.Rows 覆盖 store.go 与 web_search.go 的读写逻辑，不连接数据库。

// settingRow 是 system_settings 的构造行；scanErr 非空时模拟 Scan 失败。
type settingRow struct {
	key     string
	value   string
	scanErr error
}

// fakeSettingsRows 实现 pgx.Rows，供无数据库测试 Load/LoadWebSearch。
type fakeSettingsRows struct {
	rows    []settingRow
	idx     int
	rowsErr error
}

func (r *fakeSettingsRows) Close() {}

func (r *fakeSettingsRows) Err() error { return r.rowsErr }

func (r *fakeSettingsRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *fakeSettingsRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *fakeSettingsRows) Next() bool {
	if r.rowsErr != nil || r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *fakeSettingsRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return errors.New("未调用 Next 就 Scan")
	}
	row := r.rows[r.idx-1]
	if row.scanErr != nil {
		return row.scanErr
	}
	if len(dest) != 2 {
		return fmt.Errorf("设置表列数应为 2，实际 %d", len(dest))
	}
	key, ok := dest[0].(*string)
	if !ok {
		return fmt.Errorf("第一列应扫描为 *string，实际 %T", dest[0])
	}
	raw, ok := dest[1].(*[]byte)
	if !ok {
		return fmt.Errorf("第二列应扫描为 *[]byte，实际 %T", dest[1])
	}
	*key = row.key
	*raw = []byte(row.value)
	return nil
}

func (r *fakeSettingsRows) Values() ([]any, error) {
	return nil, errors.New("测试不应调用 Values")
}

func (r *fakeSettingsRows) RawValues() [][]byte { return nil }

func (r *fakeSettingsRows) Conn() *pgx.Conn { return nil }

func (r *fakeSettingsRows) TypeMap() *pgtype.Map { return nil }

var _ pgx.Rows = (*fakeSettingsRows)(nil)

// fakeSettingsExecutor 返回预置行集合并记录查询参数。
type fakeSettingsExecutor struct {
	rows     pgx.Rows
	queryErr error

	queryCalls int
	querySQL   string
	queryArgs  []any

	execSQL  string
	execArgs []any
	execErr  error
}

func (e *fakeSettingsExecutor) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	e.execSQL = sql
	e.execArgs = args
	return pgconn.CommandTag{}, e.execErr
}

func (e *fakeSettingsExecutor) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	e.queryCalls++
	e.querySQL = sql
	e.queryArgs = args
	if e.queryErr != nil {
		return nil, e.queryErr
	}
	return e.rows, nil
}

func (e *fakeSettingsExecutor) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return nil
}

var _ db.Executor = (*fakeSettingsExecutor)(nil)

// newSettingsExecutor 构造带行集的假执行器。
func newSettingsExecutor(rows ...settingRow) *fakeSettingsExecutor {
	return &fakeSettingsExecutor{rows: &fakeSettingsRows{rows: rows}}
}

// setting 是 settingRow 的具名构造辅助，避免位置字面量遗漏 scanErr 字段。
func setting(key, value string) settingRow {
	return settingRow{key: key, value: value}
}

func TestLoadMapsAllKnownKeys(t *testing.T) {
	exec := newSettingsExecutor(
		setting(KeyRegistrationEnabled, `false`),
		setting(KeyAIGlobalEnabled, `true`),
		setting(KeyUploadMaxFileBytes, `1048576`),
		setting(KeyUploadMaxRequestBytes, `8388608`),
		setting(KeyStorageQuotaBytes, `"2097152"`),
		setting(KeyPostRateLimit, `12`),
		setting(KeyPostRateWindowSeconds, `60`),
		setting(KeyUploadRateLimit, `30`),
		setting(KeyUploadRateWindowSeconds, `120`),
		setting(KeyTempAttachmentRetentionHour, `48`),
		setting(KeyCallLogMaxBytes, `1024`),
		setting(KeyCallLogRetentionDays, `7`),
	)
	values, err := Load(context.Background(), exec)
	if err != nil {
		t.Fatalf("加载设置失败: %v", err)
	}
	if values.RegistrationEnabled || !values.AIGlobalEnabled {
		t.Fatalf("开关未生效: %+v", values)
	}
	if values.MaxUploadBytes != 1048576 || values.MaxRequestBytes != 8388608 {
		t.Fatalf("上传上限未生效: %+v", values)
	}
	if values.StorageQuotaBytes != 2097152 {
		t.Fatalf("字符串数字未解析: %+v", values)
	}
	if values.PostRateLimit != 12 || values.PostRateWindow != time.Minute {
		t.Fatalf("发帖限频未生效: %+v", values)
	}
	if values.UploadRateLimit != 30 || values.UploadRateWindow != 2*time.Minute {
		t.Fatalf("上传限频未生效: %+v", values)
	}
	if values.TempAttachmentRetention != 48*time.Hour {
		t.Fatalf("临时附件保留期未生效: %+v", values)
	}
	if values.CallLogMaxBytes != 1024 || values.CallLogRetentionDays != 7 {
		t.Fatalf("调用记录设置未生效: %+v", values)
	}

	// 查询参数应是一次批量读取全部已知键。
	if exec.queryCalls != 1 || exec.querySQL != sqlSelectSettings {
		t.Fatalf("应使用一次批量读取 SQL，实际 calls=%d sql=%q", exec.queryCalls, exec.querySQL)
	}
	if len(exec.queryArgs) != 1 {
		t.Fatalf("查询参数数量 = %d，期望 1", len(exec.queryArgs))
	}
	keys, ok := exec.queryArgs[0].([]string)
	if !ok {
		t.Fatalf("查询参数应为 []string，实际 %T", exec.queryArgs[0])
	}
	if len(keys) != len(allKeys) {
		t.Fatalf("应读取全部 %d 个已知键，实际 %d", len(allKeys), len(keys))
	}
	for _, want := range allKeys {
		found := false
		for _, key := range keys {
			if key == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("查询参数缺少设置键 %q", want)
		}
	}
}

// TestLoadFallsBackOnInvalidOrUnknownRows 覆盖非法值、未知键回退默认值。
func TestLoadFallsBackOnInvalidOrUnknownRows(t *testing.T) {
	defaults := Defaults()
	exec := newSettingsExecutor(
		setting(KeyRegistrationEnabled, `"true"`), // 类型不对：开关需要 JSON bool
		setting(KeyAIGlobalEnabled, `not-json`),   // 非法 JSON
		setting(KeyUploadMaxFileBytes, `-1`),      // 非正值不覆盖
		setting(KeyStorageQuotaBytes, `0`),        // 非正值不覆盖
		setting(KeyPostRateLimit, `0`),            // 非正值不覆盖
		setting(KeyPostRateWindowSeconds, `0`),    // 非正值不覆盖
		setting(KeyUploadRateLimit, `"abc"`),      // 字符串非数字
		setting(KeyTempAttachmentRetentionHour, `0`),
		setting("未知设置键", `123`), // 未知键忽略
	)
	values, err := Load(context.Background(), exec)
	if err != nil {
		t.Fatalf("加载设置失败: %v", err)
	}
	if values.RegistrationEnabled != defaults.RegistrationEnabled || values.AIGlobalEnabled != defaults.AIGlobalEnabled {
		t.Fatalf("非法开关值应回退默认: %+v", values)
	}
	if values.MaxUploadBytes != defaults.MaxUploadBytes || values.StorageQuotaBytes != defaults.StorageQuotaBytes {
		t.Fatalf("非正数上限应回退默认: %+v", values)
	}
	if values.PostRateLimit != defaults.PostRateLimit || values.PostRateWindow != defaults.PostRateWindow {
		t.Fatalf("非正数发帖限频应回退默认: %+v", values)
	}
	if values.UploadRateLimit != defaults.UploadRateLimit {
		t.Fatalf("非数字限频应回退默认: %+v", values)
	}
	if values.TempAttachmentRetention != defaults.TempAttachmentRetention {
		t.Fatalf("非正数保留期应回退默认: %+v", values)
	}
}

// TestLoadRaisesRequestLimitAboveFileLimit 覆盖请求上限必须容纳文件上限的兜底逻辑。
func TestLoadRaisesRequestLimitAboveFileLimit(t *testing.T) {
	exec := newSettingsExecutor(
		setting(KeyUploadMaxFileBytes, `10485760`),   // 10 MiB
		setting(KeyUploadMaxRequestBytes, `1048576`), // 1 MiB，小于文件上限
	)
	values, err := Load(context.Background(), exec)
	if err != nil {
		t.Fatalf("加载设置失败: %v", err)
	}
	if want := int64(10485760) + DefaultUploadOverhead; values.MaxRequestBytes != want {
		t.Fatalf("请求上限 = %d，期望自动补齐为 %d", values.MaxRequestBytes, want)
	}
}

func TestLoadErrorPaths(t *testing.T) {
	queryErr := errors.New("数据库不可用")
	t.Run("查询失败返回默认值与错误", func(t *testing.T) {
		values, err := Load(context.Background(), &fakeSettingsExecutor{queryErr: queryErr})
		if !errors.Is(err, queryErr) {
			t.Fatalf("应返回查询错误，实际 %v", err)
		}
		if values != Defaults() {
			t.Fatalf("查询失败应返回默认值: %+v", values)
		}
	})

	t.Run("扫描失败返回错误", func(t *testing.T) {
		scanErr := errors.New("列类型不匹配")
		exec := &fakeSettingsExecutor{rows: &fakeSettingsRows{
			rows: []settingRow{{key: KeyPostRateLimit, scanErr: scanErr}},
		}}
		if _, err := Load(context.Background(), exec); !errors.Is(err, scanErr) {
			t.Fatalf("应返回扫描错误，实际 %v", err)
		}
	})

	t.Run("遍历失败返回错误", func(t *testing.T) {
		rowsErr := errors.New("结果集中断")
		exec := &fakeSettingsExecutor{rows: &fakeSettingsRows{rowsErr: rowsErr}}
		if _, err := Load(context.Background(), exec); !errors.Is(err, rowsErr) {
			t.Fatalf("应返回结果集错误，实际 %v", err)
		}
	})

	t.Run("LoadOrDefaults 回退默认值", func(t *testing.T) {
		got := LoadOrDefaults(context.Background(), &fakeSettingsExecutor{queryErr: queryErr})
		if got != Defaults() {
			t.Fatalf("出错时应返回默认值: %+v", got)
		}
	})

	t.Run("LoadOrDefaults 成功路径", func(t *testing.T) {
		exec := newSettingsExecutor(setting(KeyAIGlobalEnabled, `true`))
		if got := LoadOrDefaults(context.Background(), exec); !got.AIGlobalEnabled {
			t.Fatalf("成功时应返回数据库值: %+v", got)
		}
	})
}

func TestSetErrorPaths(t *testing.T) {
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	t.Run("不可序列化的值直接返回错误", func(t *testing.T) {
		exec := &fakeSettingsExecutor{}
		if err := Set(context.Background(), exec, KeyRegistrationEnabled, make(chan int), 1, at); err == nil {
			t.Fatal("无法 JSON 序列化的值应返回错误")
		}
		if exec.execSQL != "" {
			t.Fatal("序列化失败时不应执行 SQL")
		}
	})
	t.Run("执行错误向上返回", func(t *testing.T) {
		execErr := errors.New("写入失败")
		exec := &fakeSettingsExecutor{execErr: execErr}
		if err := Set(context.Background(), exec, KeyRegistrationEnabled, true, 2, at); !errors.Is(err, execErr) {
			t.Fatalf("应返回执行错误，实际 %v", err)
		}
	})
}

func TestLoadWebSearch(t *testing.T) {
	t.Run("未配置时回退默认地址", func(t *testing.T) {
		values, err := LoadWebSearch(context.Background(), newSettingsExecutor())
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if values.BaseURL != websearch.DefaultBaseURL || values.Configured() || values.Ready() {
			t.Fatalf("默认搜索配置错误: %+v", values)
		}
	})

	t.Run("读取并裁剪地址与密钥", func(t *testing.T) {
		exec := newSettingsExecutor(
			setting(KeyWebSearchBaseURL, `" https://search.example.com/api "`),
			setting(KeyWebSearchAPIKeyCipher, `"cipher-text"`),
		)
		values, err := LoadWebSearch(context.Background(), exec)
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if values.BaseURL != "https://search.example.com/api" {
			t.Fatalf("BaseURL 应去除首尾空白，实际 %q", values.BaseURL)
		}
		if values.APIKeyCipher != "cipher-text" || !values.Configured() || !values.Ready() {
			t.Fatalf("密钥读取错误: %+v", values)
		}
		if len(exec.queryArgs) != 1 {
			t.Fatalf("查询参数数量 = %d，期望 1", len(exec.queryArgs))
		}
		keys, ok := exec.queryArgs[0].([]string)
		if !ok || len(keys) != 2 || keys[0] != KeyWebSearchBaseURL || keys[1] != KeyWebSearchAPIKeyCipher {
			t.Fatalf("搜索设置查询键错误: %#v", exec.queryArgs[0])
		}
	})

	t.Run("有密钥但无地址时失败关闭", func(t *testing.T) {
		values, err := LoadWebSearch(context.Background(), newSettingsExecutor(
			setting(KeyWebSearchAPIKeyCipher, `"cipher"`),
		))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if values.BaseURL != "" {
			t.Fatalf("有密钥但无地址时不应回退默认地址（避免密钥发往默认服务），实际 %q", values.BaseURL)
		}
		if !values.Configured() || values.Ready() {
			t.Fatalf("有密钥但无地址时应 Configured 且未 Ready: %+v", values)
		}
	})

	t.Run("地址为空字符串视为未配置", func(t *testing.T) {
		values, err := LoadWebSearch(context.Background(), newSettingsExecutor(
			setting(KeyWebSearchBaseURL, `""`),
		))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if values.BaseURL != websearch.DefaultBaseURL {
			t.Fatalf("空地址应回退默认地址，实际 %q", values.BaseURL)
		}
	})

	t.Run("非法地址或密钥忽略", func(t *testing.T) {
		values, err := LoadWebSearch(context.Background(), newSettingsExecutor(
			setting(KeyWebSearchBaseURL, `123`),
			setting(KeyWebSearchAPIKeyCipher, `not-json`),
		))
		if err != nil {
			t.Fatalf("加载失败: %v", err)
		}
		if values.BaseURL != websearch.DefaultBaseURL || values.Configured() {
			t.Fatalf("非法值应被忽略: %+v", values)
		}
	})

	t.Run("查询失败返回零值", func(t *testing.T) {
		queryErr := errors.New("数据库不可用")
		values, err := LoadWebSearch(context.Background(), &fakeSettingsExecutor{queryErr: queryErr})
		if !errors.Is(err, queryErr) {
			t.Fatalf("应返回查询错误，实际 %v", err)
		}
		if values != (WebSearchValues{}) {
			t.Fatalf("查询失败应返回零值: %+v", values)
		}
	})

	t.Run("扫描失败返回错误", func(t *testing.T) {
		scanErr := errors.New("扫描失败")
		exec := &fakeSettingsExecutor{rows: &fakeSettingsRows{
			rows: []settingRow{{key: KeyWebSearchBaseURL, scanErr: scanErr}},
		}}
		if _, err := LoadWebSearch(context.Background(), exec); !errors.Is(err, scanErr) {
			t.Fatalf("应返回扫描错误，实际 %v", err)
		}
	})

	t.Run("遍历失败返回错误", func(t *testing.T) {
		rowsErr := errors.New("结果集中断")
		exec := &fakeSettingsExecutor{rows: &fakeSettingsRows{rowsErr: rowsErr}}
		if _, err := LoadWebSearch(context.Background(), exec); !errors.Is(err, rowsErr) {
			t.Fatalf("应返回结果集错误，实际 %v", err)
		}
	})
}
