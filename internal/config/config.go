// Package config 负责从环境变量加载并校验运行配置。
// 变量清单与 docs/DEPLOYMENT.md 第 5 章及 .env.example 保持一致。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// 数据库（独立实例，本部署仅通过连接信息访问）
	DBName     string
	DBUser     string
	DBPassword string
	DBHost     string
	DBPort     string
	DBSSLMode  string

	// 运行行为
	RunMigrations     bool
	SnowflakeWorkerID int64
	CookieSecure      bool

	// 日志
	LogLevel  string
	LogFile   string
	LogFormat string

	// 存储
	UploadDir string
}

// Load 读取环境变量并校验必填项；缺失时返回包含全部缺失项的错误，应用应拒绝启动。
func Load() (*Config, error) {
	loadDotEnv(".env") // 本地开发便利；已存在的进程环境变量优先，容器部署可忽略
	cfg := &Config{
		DBName:     os.Getenv("DB_NAME"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),
		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     envOr("DB_PORT", "5432"),
		DBSSLMode:  envOr("DB_SSLMODE", "disable"),

		LogLevel:  envOr("LOG_LEVEL", "info"),
		LogFile:   envOr("LOG_FILE", "logs/app.log"),
		LogFormat: envOr("LOG_FORMAT", "text"),

		UploadDir: envOr("UPLOAD_DIR", "data/uploads"),
	}

	var missing []string
	for _, f := range []struct {
		name  string
		value string
	}{
		{"DB_NAME", cfg.DBName},
		{"DB_USER", cfg.DBUser},
		{"DB_PASSWORD", cfg.DBPassword},
		{"DB_HOST", cfg.DBHost},
	} {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("缺少必填环境变量: %s（参考 .env.example）", strings.Join(missing, ", "))
	}

	var err error
	if cfg.RunMigrations, err = envBool("RUN_MIGRATIONS", true); err != nil {
		return nil, err
	}
	if cfg.CookieSecure, err = envBool("COOKIE_SECURE", true); err != nil {
		return nil, err
	}

	worker := envOr("SNOWFLAKE_WORKER_ID", "1")
	if cfg.SnowflakeWorkerID, err = strconv.ParseInt(worker, 10, 64); err != nil || cfg.SnowflakeWorkerID < 0 || cfg.SnowflakeWorkerID > 1023 {
		return nil, fmt.Errorf("SNOWFLAKE_WORKER_ID 必须是 0-1023 的整数，当前值: %q", worker)
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return nil, fmt.Errorf("LOG_LEVEL 必须是 debug/info/warn/error 之一，当前值: %q", cfg.LogLevel)
	}
	switch cfg.LogFormat {
	case "text", "json":
	default:
		return nil, fmt.Errorf("LOG_FORMAT 必须是 text/json 之一，当前值: %q", cfg.LogFormat)
	}

	return cfg, nil
}

// DSN 构造 PostgreSQL 连接串：
//   - 连接层固定会话时区为 Asia/Shanghai（与全系统时区约定一致）；
//   - 固定 search_path 为 timeline 与 "user"（user 是保留字，需引号）；
//   - 具体库名来自配置，不硬编码。
func (c *Config) DSN() string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword),
		Host:   c.DBHost + ":" + c.DBPort,
		Path:   "/" + c.DBName,
	}
	q := url.Values{}
	q.Set("sslmode", c.DBSSLMode)
	q.Set("timezone", "Asia/Shanghai")
	q.Set("search_path", `timeline,"user"`)
	u.RawQuery = q.Encode()
	return u.String()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s 必须是布尔值（true/false），当前值: %q", key, v)
	}
	return b, nil
}

// loadDotEnv 从 .env 文件加载键值对（不覆盖已存在的进程环境变量）。
// 只做最简解析：KEY=VALUE，忽略空行与 # 注释，支持去除成对引号。
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, val)
	}
}
