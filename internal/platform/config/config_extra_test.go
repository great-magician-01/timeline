package config

import (
	"crypto/sha256"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configEnvKeys 是 Load 会读取的全部进程环境变量；测试必须逐项隔离，
// 避免读到宿主环境或仓库根目录真实 .env 造成结果不确定。
var configEnvKeys = []string{
	"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST", "DB_PORT", "DB_SSLMODE",
	"RUN_MIGRATIONS", "COOKIE_SECURE", "TRUSTED_PROXIES", "SNOWFLAKE_WORKER_ID",
	"LOG_LEVEL", "LOG_FILE", "LOG_FORMAT", "UPLOAD_DIR", "ENCRYPTION_KEY",
}

// isolateConfigEnv 清空全部配置相关环境变量（空值等价未配置），并把工作目录
// 切到临时目录，保证 loadDotEnv 不会读到仓库真实 .env。
func isolateConfigEnv(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	for _, key := range configEnvKeys {
		t.Setenv(key, "")
	}
}

// setRequiredEnv 设置 Load 的四个必填项。
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DB_NAME", "timeline")
	t.Setenv("DB_USER", "timeline")
	t.Setenv("DB_PASSWORD", "secret")
	t.Setenv("DB_HOST", "127.0.0.1")
}

// unsetEnvForTest 临时删除环境变量并在测试结束后恢复原值。
// 用于验证 loadDotEnv 会写入“当前进程不存在”的变量。
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("删除环境变量 %s 失败: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestEnvOr(t *testing.T) {
	const key = "TIMELINE_TEST_ENV_OR"
	t.Setenv(key, "")
	if got := envOr(key, "默认值"); got != "默认值" {
		t.Fatalf("未设置时 envOr = %q，期望默认值", got)
	}
	t.Setenv(key, "显式值")
	if got := envOr(key, "默认值"); got != "显式值" {
		t.Fatalf("已设置时 envOr = %q，期望显式值", got)
	}
}

func TestEnvBool(t *testing.T) {
	const key = "TIMELINE_TEST_ENV_BOOL"
	cases := []struct {
		name    string
		value   string
		def     bool
		want    bool
		wantErr bool
	}{
		{"未设置用默认 true", "", true, true, false},
		{"未设置用默认 false", "", false, false, false},
		{"true", "true", false, true, false},
		{"false", "false", true, false, false},
		{"1 等价 true", "1", false, true, false},
		{"0 等价 false", "0", true, false, false},
		{"非法布尔值", "yes", true, false, true},
		{"空格的非法值", " ", true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value == "" {
				os.Unsetenv(key)
				t.Cleanup(func() { os.Unsetenv(key) })
			} else {
				t.Setenv(key, tc.value)
			}
			got, err := envBool(key, tc.def)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("非法布尔值应返回包含变量名的错误，实际 %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("envBool = (%v, %v)，期望 (%v, nil)", got, err, tc.want)
			}
		})
	}
}

func TestLoadRejectsMissingRequiredEnv(t *testing.T) {
	isolateConfigEnv(t)
	cfg, err := Load()
	if err == nil || cfg != nil {
		t.Fatalf("缺少必填项应返回错误与 nil 配置，实际 cfg=%v err=%v", cfg, err)
	}
	for _, want := range []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST", "缺少必填环境变量"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息缺少 %q: %v", want, err)
		}
	}

	for _, missing := range []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST"} {
		t.Run("缺少"+missing, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(missing, "")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("缺少 %s 时应返回包含该项的错误，实际 %v", missing, err)
			}
			for _, other := range []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST"} {
				if other != missing && strings.Contains(err.Error(), other) {
					t.Errorf("只缺 %s 时不应报告缺少 %s: %v", missing, other, err)
				}
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	isolateConfigEnv(t)
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("加载默认配置失败: %v", err)
	}
	if cfg.DBPort != "5432" || cfg.DBSSLMode != "disable" {
		t.Fatalf("数据库默认值错误: port=%q sslmode=%q", cfg.DBPort, cfg.DBSSLMode)
	}
	if !cfg.RunMigrations || !cfg.CookieSecure {
		t.Fatalf("运行默认值错误: RunMigrations=%v CookieSecure=%v", cfg.RunMigrations, cfg.CookieSecure)
	}
	if cfg.SnowflakeWorkerID != 1 {
		t.Fatalf("雪花 worker 默认值 = %d，期望 1", cfg.SnowflakeWorkerID)
	}
	if cfg.TrustedProxies != nil {
		t.Fatalf("未配置时应不信任任何代理，实际 %v", cfg.TrustedProxies)
	}
	if cfg.LogLevel != "info" || cfg.LogFile != "logs/app.log" || cfg.LogFormat != "text" {
		t.Fatalf("日志默认值错误: %+v", cfg)
	}
	if cfg.UploadDir != "data/uploads" {
		t.Fatalf("上传目录默认值 = %q", cfg.UploadDir)
	}
	if len(cfg.SecretKey) != sha256.Size {
		t.Fatalf("兜底密钥长度 = %d，期望 %d", len(cfg.SecretKey), sha256.Size)
	}
	if cfg.SecretKeyFromEnv {
		t.Fatal("未配置 ENCRYPTION_KEY 时 SecretKeyFromEnv 应为 false")
	}
}

func TestLoadExplicitValues(t *testing.T) {
	isolateConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("DB_PORT", "6543")
	t.Setenv("DB_SSLMODE", "require")
	t.Setenv("RUN_MIGRATIONS", "false")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("SNOWFLAKE_WORKER_ID", "1023")
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1, 10.0.0.0/8, ::1")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FILE", "logs/custom.log")
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("UPLOAD_DIR", "data/custom-uploads")
	t.Setenv("ENCRYPTION_KEY", "explicit-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("加载显式配置失败: %v", err)
	}
	if cfg.DBPort != "6543" || cfg.DBSSLMode != "require" {
		t.Fatalf("数据库显式值错误: %+v", cfg)
	}
	if cfg.RunMigrations || cfg.CookieSecure {
		t.Fatalf("显式 false 未生效: RunMigrations=%v CookieSecure=%v", cfg.RunMigrations, cfg.CookieSecure)
	}
	if cfg.SnowflakeWorkerID != 1023 {
		t.Fatalf("雪花 worker 显式值 = %d，期望 1023", cfg.SnowflakeWorkerID)
	}
	wantProxies := []string{"127.0.0.1", "10.0.0.0/8", "::1"}
	if len(cfg.TrustedProxies) != len(wantProxies) {
		t.Fatalf("受信代理解析错误: %v", cfg.TrustedProxies)
	}
	for i, want := range wantProxies {
		if cfg.TrustedProxies[i] != want {
			t.Errorf("受信代理[%d] = %q，期望 %q", i, cfg.TrustedProxies[i], want)
		}
	}
	if cfg.LogLevel != "debug" || cfg.LogFile != "logs/custom.log" || cfg.LogFormat != "json" {
		t.Fatalf("日志显式值错误: %+v", cfg)
	}
	if cfg.UploadDir != "data/custom-uploads" {
		t.Fatalf("上传目录显式值 = %q", cfg.UploadDir)
	}
	if !cfg.SecretKeyFromEnv {
		t.Fatal("配置 ENCRYPTION_KEY 时 SecretKeyFromEnv 应为 true")
	}
	wantKey := sha256.Sum256([]byte("timeline-secret-v1|explicit-key"))
	if string(cfg.SecretKey) != string(wantKey[:]) {
		t.Fatal("ENCRYPTION_KEY 派生密钥与约定不一致")
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{"运行迁移非布尔", "RUN_MIGRATIONS", "maybe", "RUN_MIGRATIONS 必须是布尔值"},
		{"Cookie 安全非布尔", "COOKIE_SECURE", "2", "COOKIE_SECURE 必须是布尔值"},
		{"worker 非数字", "SNOWFLAKE_WORKER_ID", "abc", "SNOWFLAKE_WORKER_ID 必须是 0-1023"},
		{"worker 为负", "SNOWFLAKE_WORKER_ID", "-1", "SNOWFLAKE_WORKER_ID 必须是 0-1023"},
		{"worker 超上限", "SNOWFLAKE_WORKER_ID", "1024", "SNOWFLAKE_WORKER_ID 必须是 0-1023"},
		{"日志级别非法", "LOG_LEVEL", "verbose", "LOG_LEVEL 必须是 debug/info/warn/error"},
		{"日志格式非法", "LOG_FORMAT", "yaml", "LOG_FORMAT 必须是 text/json"},
		{"受信代理非法", "TRUSTED_PROXIES", "proxy.example.com", "TRUSTED_PROXIES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfigEnv(t)
			setRequiredEnv(t)
			t.Setenv(tc.key, tc.value)
			cfg, err := Load()
			if err == nil || cfg != nil {
				t.Fatalf("%s=%q 应被拒绝，实际 cfg=%v err=%v", tc.key, tc.value, cfg, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误信息应包含 %q，实际 %v", tc.want, err)
			}
		})
	}
}

// TestLoadChecksRequiredEnvBeforeOptional 覆盖校验顺序：必填缺失优先于可选项非法。
func TestLoadChecksRequiredEnvBeforeOptional(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("RUN_MIGRATIONS", "not-a-bool")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "缺少必填环境变量") {
		t.Fatalf("必填项缺失应优先报错，实际 %v", err)
	}
}

func TestDSNEscapesAndFixesSessionSettings(t *testing.T) {
	cfg := &Config{
		DBName:     "timeline",
		DBUser:     "timeline",
		DBPassword: "p@ss:word/?#",
		DBHost:     "db.local",
		DBPort:     "5432",
		DBSSLMode:  "disable",
	}
	dsn := cfg.DSN()
	if !strings.HasPrefix(dsn, "postgres://") {
		t.Fatalf("DSN 应以 postgres:// 开头: %q", dsn)
	}
	// 密码中的特殊字符必须转义，连接串才能被正确解析。
	if !strings.Contains(dsn, "p%40ss%3Aword%2F%3F%23") {
		t.Fatalf("密码特殊字符未转义: %q", dsn)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DSN 无法解析: %v", err)
	}
	if parsed.Scheme != "postgres" {
		t.Errorf("scheme = %q，期望 postgres", parsed.Scheme)
	}
	if got := parsed.User.Username(); got != "timeline" {
		t.Errorf("用户名 = %q", got)
	}
	if password, ok := parsed.User.Password(); !ok || password != "p@ss:word/?#" {
		t.Errorf("密码解析错误: %q ok=%v", password, ok)
	}
	if parsed.Host != "db.local:5432" {
		t.Errorf("host = %q，期望 db.local:5432", parsed.Host)
	}
	if parsed.Path != "/timeline" {
		t.Errorf("path = %q，期望 /timeline", parsed.Path)
	}
	query := parsed.Query()
	if got := query.Get("sslmode"); got != "disable" {
		t.Errorf("sslmode = %q", got)
	}
	if got := query.Get("timezone"); got != "Asia/Shanghai" {
		t.Errorf("timezone 应固定为 Asia/Shanghai，实际 %q", got)
	}
	if got := query.Get("search_path"); got != `timeline,"user"` {
		t.Errorf("search_path = %q，期望 timeline,\"user\"", got)
	}
	// 固定参数必须经过 URL 编码（保留字 user 的引号与逗号）。
	if !strings.Contains(dsn, "timezone=Asia%2FShanghai") || !strings.Contains(dsn, "search_path=timeline%2C%22user%22") {
		t.Errorf("固定会话参数未按 URL 编码: %q", dsn)
	}
}

func TestDeriveSecretKey(t *testing.T) {
	isolateConfigEnv(t)
	base := &Config{DBName: "timeline", DBUser: "u", DBPassword: "p", DBHost: "h"}

	first, fromEnv := deriveSecretKey(base)
	if fromEnv {
		t.Fatal("未配置 ENCRYPTION_KEY 时应回退数据库连接信息")
	}
	if len(first) != sha256.Size {
		t.Fatalf("派生密钥长度 = %d，期望 %d", len(first), sha256.Size)
	}
	want := sha256.Sum256([]byte("timeline-secret-fallback-v1|timeline|u|p|h"))
	if string(first) != string(want[:]) {
		t.Fatal("兜底派生密钥与约定算法不一致")
	}
	second, _ := deriveSecretKey(base)
	if string(first) != string(second) {
		t.Fatal("同一数据库配置应派生出相同密钥（重启后可解密）")
	}
	changed := *base
	changed.DBPassword = "另一个密码"
	third, _ := deriveSecretKey(&changed)
	if string(first) == string(third) {
		t.Fatal("数据库连接信息变化时兜底密钥应随之变化")
	}

	t.Setenv("ENCRYPTION_KEY", "secret-key")
	explicit, fromEnv := deriveSecretKey(base)
	if !fromEnv {
		t.Fatal("配置 ENCRYPTION_KEY 时应标记来自显式配置")
	}
	wantExplicit := sha256.Sum256([]byte("timeline-secret-v1|secret-key"))
	if string(explicit) != string(wantExplicit[:]) {
		t.Fatal("显式密钥派生结果与约定不一致")
	}
	t.Setenv("ENCRYPTION_KEY", "another-key")
	another, _ := deriveSecretKey(base)
	if string(explicit) == string(another) {
		t.Fatal("不同 ENCRYPTION_KEY 应派生出不同密钥")
	}
}

func TestParseTrustedProxiesTable(t *testing.T) {
	valid := []struct {
		name string
		raw  string
		want []string
	}{
		{"单 IPv4", "127.0.0.1", []string{"127.0.0.1"}},
		{"单 IPv6", "::1", []string{"::1"}},
		{"IPv4 CIDR", "10.0.0.0/8", []string{"10.0.0.0/8"}},
		{"IPv6 CIDR", "fd00::/8", []string{"fd00::/8"}},
		{"最大主机前缀", "127.0.0.1/32, ::1/128", []string{"127.0.0.1/32", "::1/128"}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTrustedProxies(tc.raw)
			if err != nil {
				t.Fatalf("合法配置被拒绝: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("解析结果 = %v，期望 %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("代理[%d] = %q，期望 %q", i, got[i], tc.want[i])
				}
			}
		})
	}

	invalid := []string{
		"proxy.example.com",
		"localhost",
		"10.0.0.0/33",
		"::/0",
		"0.0.0.0/0",
		"127.0.0.1,",
		",127.0.0.1",
		"127.0.0.1,,10.0.0.1",
		"  ,  ",
		"1.2.3.4/abc",
		"999.999.999.999",
	}
	for _, raw := range invalid {
		if got, err := parseTrustedProxies(raw); err == nil {
			t.Errorf("非法配置 %q 应被拒绝，实际 %v", raw, got)
		}
	}
	if got, err := parseTrustedProxies(""); err != nil || got != nil {
		t.Fatalf("空配置应表示不信任代理: %v %v", got, err)
	}
}

func TestLoadDotEnvParsesFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	dotenvKeys := []string{
		"TIMELINE_DOTENV_ONE", "TIMELINE_DOTENV_TWO",
		"TIMELINE_DOTENV_THREE", "TIMELINE_DOTENV_UNQUOTED",
	}
	for _, key := range dotenvKeys {
		unsetEnvForTest(t, key)
	}
	content := strings.Join([]string{
		"# 注释行被忽略",
		"",
		"TIMELINE_DOTENV_ONE=value1",
		`TIMELINE_DOTENV_TWO = "带 空格的值"`,
		"TIMELINE_DOTENV_THREE='单引号'",
		"TIMELINE_DOTENV_UNQUOTED=  原样保留  ",
		"# TIMELINE_DOTENV_ONE=被注释覆盖",
		"没有等号的行",
		"=没有键的行",
		"TIMELINE_DOTENV_ONE=重复时应保留首次值",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatalf("写 .env 失败: %v", err)
	}

	loadDotEnv(".env")
	cases := map[string]string{
		"TIMELINE_DOTENV_ONE":      "value1",
		"TIMELINE_DOTENV_TWO":      "带 空格的值",
		"TIMELINE_DOTENV_THREE":    "单引号",
		"TIMELINE_DOTENV_UNQUOTED": "原样保留",
	}
	for key, want := range cases {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q，期望 %q", key, got, want)
		}
	}

	// 文件不存在时应静默忽略。
	loadDotEnv(filepath.Join(dir, "missing.env"))
	if _, exists := os.LookupEnv("没有等号的行"); exists {
		t.Fatal("没有等号的行不应被写入环境变量")
	}
}

// TestLoadDotEnvKeepsProcessEnv 覆盖已存在的进程环境变量优先于 .env。
func TestLoadDotEnvKeepsProcessEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	const key = "TIMELINE_DOTENV_PROCESS_WINS"
	t.Setenv(key, "from-process")
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(key+"=from-file\n"), 0o600); err != nil {
		t.Fatalf("写 .env 失败: %v", err)
	}
	loadDotEnv(".env")
	if got := os.Getenv(key); got != "from-process" {
		t.Fatalf("进程环境变量应优先，实际 %q", got)
	}
}

// TestLoadReadsDotEnvWhenEnvAbsent 端到端覆盖：进程环境不存在时 Load 从 .env 读取。
func TestLoadReadsDotEnvWhenEnvAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, key := range configEnvKeys {
		unsetEnvForTest(t, key)
	}
	content := strings.Join([]string{
		"DB_NAME=dotenv_db",
		"DB_USER=dotenv_user",
		"DB_PASSWORD=dotenv_password",
		"DB_HOST=dotenv-host",
		"DB_PORT=5555",
		"LOG_LEVEL=warn",
		"LOG_FORMAT=json",
		"UPLOAD_DIR=data/dotenv-uploads",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatalf("写 .env 失败: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("从 .env 加载配置失败: %v", err)
	}
	if cfg.DBName != "dotenv_db" || cfg.DBUser != "dotenv_user" ||
		cfg.DBPassword != "dotenv_password" || cfg.DBHost != "dotenv-host" {
		t.Fatalf(".env 数据库配置未生效: %+v", cfg)
	}
	if cfg.DBPort != "5555" || cfg.LogLevel != "warn" || cfg.LogFormat != "json" ||
		cfg.UploadDir != "data/dotenv-uploads" {
		t.Fatalf(".env 可选配置未生效: %+v", cfg)
	}
}
