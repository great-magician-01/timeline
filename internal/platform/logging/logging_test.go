package logging

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testLocation 固定北京时间，日志日期目录由写入时刻换算，避免依赖进程本地时区。
var testLocation = time.FixedZone("CST", 8*60*60)

// testLogPath 返回 t.TempDir() 下的日志文件路径，不写仓库内任何位置。
func testLogPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "logs", "app.log")
}

// currentLogFile 返回 writer 当前已打开的按天目录日志文件路径。
// day 取自 writer 自身，避免测试跨天（真实时钟）时目录不一致。
func currentLogFile(t *testing.T, w *dailyWriter) string {
	t.Helper()
	return filepath.Join(filepath.Dir(w.path), w.day, filepath.Base(w.path))
}

// readLogFile 读取日志文件内容；文件不存在时返回说明性错误。
func readLogFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件 %s 失败: %v", path, err)
	}
	return string(data)
}

func TestInitRejectsUnknownLevel(t *testing.T) {
	path := testLogPath(t)
	for _, level := range []string{"trace", "", "verbose", "INFO "} {
		logger, closer, err := Init(level, "text", path, testLocation)
		if err == nil || !strings.Contains(err.Error(), "未知日志级别") {
			t.Errorf("未知级别 %q 应被拒绝，err=%v", level, err)
		}
		if logger != nil || closer != nil {
			t.Errorf("失败时不应返回 logger/closer，level=%q", level)
		}
	}
}

// TestInitAcceptsSupportedLevelsAndFormats 覆盖级别大小写、text/json 两种格式。
func TestInitAcceptsSupportedLevelsAndFormats(t *testing.T) {
	cases := []struct {
		level  string
		format string
	}{
		{"debug", "text"},
		{"info", "text"},
		{"warn", "text"},
		{"error", "text"},
		{"DEBUG", "json"},
		{"Info", "json"},
		{"info", "yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.level+"/"+tc.format, func(t *testing.T) {
			path := testLogPath(t)
			logger, closer, err := Init(tc.level, tc.format, path, testLocation)
			if err != nil {
				t.Fatalf("Init 失败: %v", err)
			}
			if logger == nil || closer == nil {
				t.Fatal("Init 成功时 logger 与 closer 均不应为 nil")
			}
			if err := closer.Close(); err != nil {
				t.Fatalf("关闭日志失败: %v", err)
			}
		})
	}
}

// TestInitFailsWhenLogPathBlocked 覆盖日志目录不可创建时 Init 立即返回错误。
func TestInitFailsWhenLogPathBlocked(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("占位文件"), 0o600); err != nil {
		t.Fatalf("准备占位文件失败: %v", err)
	}
	// 日志路径的父目录是普通文件，按天目录无法创建。
	logger, closer, err := Init("info", "text", filepath.Join(blocker, "app.log"), testLocation)
	if err == nil || !strings.Contains(err.Error(), "创建日志目录失败") {
		t.Fatalf("日志目录不可写时应返回错误，实际 err=%v", err)
	}
	if logger != nil || closer != nil {
		t.Fatal("失败时不应返回 logger/closer")
	}
}

// TestInitWritesDailyTextFile 覆盖 text 格式、按日期目录写入并可读回。
func TestInitWritesDailyTextFile(t *testing.T) {
	path := testLogPath(t)
	logger, closer, err := Init("debug", "text", path, testLocation)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	defer closer.Close()

	w, ok := closer.(*dailyWriter)
	if !ok {
		t.Fatalf("closer 类型错误: %T", closer)
	}
	logger.Info("日志写入测试", "key", "value")

	content := readLogFile(t, currentLogFile(t, w))
	if !strings.Contains(content, "msg=日志写入测试") || !strings.Contains(content, "key=value") {
		t.Fatalf("日志文件内容缺少消息或属性: %q", content)
	}
	if !strings.Contains(content, "level=INFO") {
		t.Fatalf("text 格式应包含 level=INFO: %q", content)
	}
	if len(w.day) != 8 {
		t.Fatalf("日期目录应为 yyyyMMdd，实际 %q", w.day)
	}
}

// TestInitJSONFormat 覆盖 json 格式输出可按行解析。
func TestInitJSONFormat(t *testing.T) {
	path := testLogPath(t)
	logger, closer, err := Init("info", "json", path, testLocation)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	defer closer.Close()

	w := closer.(*dailyWriter)
	logger.Error("JSON 日志", "attempt", 3)

	content := readLogFile(t, currentLogFile(t, w))
	line := strings.TrimSpace(strings.Split(content, "\n")[0])
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("json 格式日志无法解析: %v，内容 %q", err, line)
	}
	if record["msg"] != "JSON 日志" || record["level"] != "ERROR" {
		t.Fatalf("json 字段错误: %#v", record)
	}
	if record["attempt"] != float64(3) {
		t.Fatalf("json 属性错误: %#v", record)
	}
}

// TestInitFiltersBelowLevel 覆盖级别过滤：info 级别下 debug 不写入文件。
func TestInitFiltersBelowLevel(t *testing.T) {
	path := testLogPath(t)
	logger, closer, err := Init("info", "text", path, testLocation)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	defer closer.Close()

	w := closer.(*dailyWriter)
	logger.Debug("不应出现")
	logger.Warn("应出现")

	content := readLogFile(t, currentLogFile(t, w))
	if strings.Contains(content, "不应出现") {
		t.Fatalf("低于级别的日志不应写入: %q", content)
	}
	if !strings.Contains(content, "应出现") {
		t.Fatalf("达到级别的日志应写入: %q", content)
	}
}

// TestDailyWriterRotateCreatesDatedDirectory 覆盖按指定日期建立目录与文件。
func TestDailyWriterRotateCreatesDatedDirectory(t *testing.T) {
	path := testLogPath(t)
	w := newDailyWriter(path, testLocation)
	defer w.Close()
	if err := w.rotate("19990101"); err != nil {
		t.Fatalf("rotate 失败: %v", err)
	}
	target := filepath.Join(filepath.Dir(path), "19990101", filepath.Base(path))
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("按天日志文件未创建: %v", err)
	}
	if w.day != "19990101" {
		t.Fatalf("writer 当前日期 = %q，期望 19990101", w.day)
	}
	if _, err := w.Write([]byte("历史日期内容\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
}

// TestDailyWriterRotatesOnDayChange 覆盖跨天切换：日期变化时关闭旧文件并重新按当前日期写入。
func TestDailyWriterRotatesOnDayChange(t *testing.T) {
	path := testLogPath(t)
	w := newDailyWriter(path, testLocation)
	defer w.Close()

	if _, err := w.Write([]byte("第一天内容\n")); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	file := currentLogFile(t, w)
	// 手工把 writer 的“当前日期”改成过去的一天，模拟跨天；下次 Write 会按真实日期重新 rotate。
	w.day = "19990101"
	if _, err := w.Write([]byte("第二天内容\n")); err != nil {
		t.Fatalf("跨天写入失败: %v", err)
	}
	wantDay := time.Now().In(testLocation).Format("20060102")
	if w.day != wantDay || w.day == "19990101" {
		t.Fatalf("跨天写入后日期目录 = %q，期望 %q", w.day, wantDay)
	}
	if currentLogFile(t, w) != file {
		t.Fatalf("当前日期下应复用同一天文件: got=%q want=%q", currentLogFile(t, w), file)
	}
	content := readLogFile(t, file)
	for _, want := range []string{"第一天内容", "第二天内容"} {
		if !strings.Contains(content, want) {
			t.Fatalf("跨天后日志内容缺少 %q: %q", want, content)
		}
	}
}

// TestDailyWriterCloseIdempotent 覆盖 Close 幂等与关闭已关闭文件的错误路径。
func TestDailyWriterCloseIdempotent(t *testing.T) {
	path := testLogPath(t)
	w := newDailyWriter(path, testLocation)
	if err := w.Close(); err != nil {
		t.Fatalf("未打开文件时 Close 应返回 nil: %v", err)
	}
	if _, err := w.Write([]byte("内容\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("首次 Close 失败: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("重复 Close 应幂等返回 nil: %v", err)
	}

	// 文件已被外部关闭时，Close 应返回底层错误而不是 panic。
	w2 := newDailyWriter(testLogPath(t), testLocation)
	if _, err := w2.Write([]byte("内容\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := w2.file.Close(); err != nil {
		t.Fatalf("准备关闭文件失败: %v", err)
	}
	if err := w2.Close(); err == nil {
		t.Fatal("关闭已关闭的底层文件应返回错误")
	}
}

// TestDailyWriterWriteErrorWhenDirectoryBlocked 覆盖文件通道失败时 Write 返回错误。
func TestDailyWriterWriteErrorWhenDirectoryBlocked(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("占位文件"), 0o600); err != nil {
		t.Fatalf("准备占位文件失败: %v", err)
	}
	w := newDailyWriter(filepath.Join(blocker, "app.log"), testLocation)
	n, err := w.Write([]byte("内容\n"))
	if err == nil {
		t.Fatal("目录不可创建时 Write 应返回错误")
	}
	if n != 0 {
		t.Fatalf("失败写入应返回 0，实际 %d", n)
	}
	if !strings.Contains(err.Error(), "创建日志目录失败") {
		t.Fatalf("错误信息应说明创建目录失败: %v", err)
	}
}

// TestDailyWriterRotateOpenFileError 覆盖目标日志文件无法打开时 rotate 返回包装错误。
func TestDailyWriterRotateOpenFileError(t *testing.T) {
	path := testLogPath(t)
	// 预先在目标位置创建目录：MkdirAll 成功，但 OpenFile 打开目录失败。
	target := filepath.Join(filepath.Dir(path), "19990101", filepath.Base(path))
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("准备目标目录失败: %v", err)
	}
	w := newDailyWriter(path, testLocation)
	if err := w.rotate("19990101"); err == nil || !strings.Contains(err.Error(), "打开日志文件失败") {
		t.Fatalf("目标不可打开时应返回打开失败错误，实际 %v", err)
	}
}

// TestInitDoesNotReplaceDefaultLogger 确认 Init 只返回 logger，不修改全局 slog.Default。
func TestInitDoesNotReplaceDefaultLogger(t *testing.T) {
	original := slog.Default()
	path := testLogPath(t)
	logger, closer, err := Init("info", "text", path, testLocation)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	defer closer.Close()
	if logger == nil {
		t.Fatal("Init 应返回 logger")
	}
	if slog.Default() != original {
		t.Fatal("Init 不应修改全局 slog.Default")
	}
	// 使用返回的 logger 写日志，确保可正常使用。
	logger.Info("直接使用返回的 logger")
}

// 编译期断言：dailyWriter 满足 io.Closer，供组合根按接口关闭。
var _ io.Closer = (*dailyWriter)(nil)
