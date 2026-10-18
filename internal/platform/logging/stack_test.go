package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// stackProbe 用于验证 captureStack 从日志调用点（而非日志库内部）开始采集。
func stackProbe() string { return captureStack() }

// recurseForStack 递归构造深调用栈，验证帧缓冲区按需扩容、栈不被截断。
func recurseForStack(depth int) string {
	if depth <= 0 {
		return captureStack()
	}
	return recurseForStack(depth - 1)
}

// TestCaptureStackSkipsLoggingInternals 覆盖栈顶跳过 slog/logging 内部帧，
// 首帧必须是业务调用点，且帧格式为「函数名 + 制表符缩进的文件:行号」。
func TestCaptureStackSkipsLoggingInternals(t *testing.T) {
	stack := stackProbe()
	lines := strings.Split(stack, "\n")
	if len(lines) < 4 {
		t.Fatalf("调用栈帧数不足: %q", stack)
	}
	if !strings.HasSuffix(lines[0], ".stackProbe") {
		t.Fatalf("首帧应为业务调用点 stackProbe，实际 %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "\t") || !strings.Contains(lines[1], "stack_test.go:") {
		t.Fatalf("帧的文件行号格式错误: %q", lines[1])
	}
	if !strings.Contains(stack, "TestCaptureStackSkipsLoggingInternals") {
		t.Fatalf("调用栈应包含测试函数帧: %q", stack)
	}
	for _, internal := range []string{"captureStack", "stackHandler", "log/slog."} {
		if strings.Contains(stack, internal) {
			t.Fatalf("调用栈不应包含日志基础设施帧 %q: %q", internal, stack)
		}
	}
}

// TestCaptureStackGrowsBeyondInitialBuffer 覆盖深调用栈：初始帧缓冲区不足时扩容，
// 保证完整栈（含调用入口帧）都能采集。
func TestCaptureStackGrowsBeyondInitialBuffer(t *testing.T) {
	stack := recurseForStack(80)
	if !strings.Contains(stack, "TestCaptureStackGrowsBeyondInitialBuffer") {
		t.Fatalf("扩容后仍应包含调用入口帧: %q", stack)
	}
	if got := strings.Count(stack, ".recurseForStack\n"); got < 80 {
		t.Fatalf("递归帧数量不足，实际 %d 帧", got)
	}
}

// newStackTestLogger 构造带自动栈能力、写入内存缓冲区的 text logger。
func newStackTestLogger(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	return slog.New(newStackHandler(newTextHandler(buf, level, testLocation)))
}

// TestStackHandlerAddsStackToError 覆盖 ERROR 自动附加栈、栈不内联、err 保持行尾。
func TestStackHandlerAddsStackToError(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := newStackTestLogger(buf, slog.LevelDebug)
	logger.Error("写入失败", "err", errors.New("磁盘已满"))

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("ERROR 日志应是记录行加多行栈，实际 %q", out)
	}
	if !strings.Contains(lines[0], "ERROR") || !strings.Contains(lines[0], "err=磁盘已满") {
		t.Fatalf("首行应为含 err 的记录行: %q", lines[0])
	}
	if strings.Contains(lines[0], "stack=") {
		t.Fatalf("stack 不应内联到记录行: %q", lines[0])
	}
	if !strings.Contains(out, "TestStackHandlerAddsStackToError") {
		t.Fatalf("栈内容应包含日志调用点: %q", out)
	}
}

// TestStackHandlerSkipsBelowError 覆盖 WARN/INFO 不附加栈（避免高频重试日志膨胀）。
func TestStackHandlerSkipsBelowError(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := newStackTestLogger(buf, slog.LevelDebug)
	logger.Warn("可重试的失败", "err", errors.New("连接超时"))
	logger.Info("普通日志", "count", 1)

	out := buf.String()
	if got := strings.Count(out, "\n"); got != 2 {
		t.Fatalf("WARN/INFO 应各占一行，实际 %d 行: %q", got, out)
	}
	if strings.Contains(out, "TestStackHandlerSkipsBelowError") {
		t.Fatalf("非 ERROR 级别不应采集调用栈: %q", out)
	}
}

// TestStackHandlerKeepsExplicitStack 覆盖显式 stack（panic 恢复等）不重复采集。
func TestStackHandlerKeepsExplicitStack(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := newStackTestLogger(buf, slog.LevelDebug)
	logger.Error("panic 恢复", "stack", "显式栈内容")

	out := buf.String()
	if !strings.Contains(out, "显式栈内容") {
		t.Fatalf("显式栈内容丢失: %q", out)
	}
	if strings.Contains(out, "TestStackHandlerKeepsExplicitStack") {
		t.Fatalf("已显式提供 stack 时不应重复采集: %q", out)
	}
}

// TestStackHandlerSkipsAccessLog 覆盖访问日志（msg=http）不采集栈：
// 访问日志是请求摘要，栈由产生错误的模块日志负责。
func TestStackHandlerSkipsAccessLog(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := newStackTestLogger(buf, slog.LevelDebug)
	logger.Error("http",
		"method", "GET", "path", "/api/v1/timeline", "status", 500, "latency_ms", 12,
		"ip", "10.0.0.5", "request_id", "req-1")

	out := buf.String()
	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("访问日志不应附加调用栈，实际 %d 行: %q", got, out)
	}
	if !strings.Contains(out, "GET /api/v1/timeline 500 12ms") {
		t.Fatalf("访问日志压缩格式被破坏: %q", out)
	}
}

// TestStackHandlerWithAttrsAndGroup 覆盖 WithAttrs/WithGroup 委托后仍能附加栈。
func TestStackHandlerWithAttrsAndGroup(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := newStackTestLogger(buf, slog.LevelDebug).
		With(ModuleAttr, "ai").
		WithGroup("request")
	logger.Error("分组日志", "path", "/x")

	out := buf.String()
	if !strings.Contains(out, "[ai]") || !strings.Contains(out, "request.path=/x") {
		t.Fatalf("既有属性/分组丢失: %q", out)
	}
	if !strings.Contains(out, "TestStackHandlerWithAttrsAndGroup") {
		t.Fatalf("分组 logger 也应自动附加栈: %q", out)
	}
}

// TestStackHandlerEnabledDelegates 覆盖 Enabled 透传底层 handler 的级别过滤。
func TestStackHandlerEnabledDelegates(t *testing.T) {
	handler := newStackHandler(newTextHandler(&bytes.Buffer{}, slog.LevelWarn, testLocation))
	if handler.Enabled(t.Context(), slog.LevelInfo) {
		t.Fatal("info 低于 warn，应被过滤")
	}
	if !handler.Enabled(t.Context(), slog.LevelError) {
		t.Fatal("error 应通过")
	}
}

// TestIsLoggingFrame 覆盖日志基础设施帧识别。
func TestIsLoggingFrame(t *testing.T) {
	cases := map[string]bool{
		"log/slog.(*Logger).Error": true,
		"log/slog.(*Logger).log":   true,
		"github.com/great-magician-01/timeline/internal/platform/logging.captureStack":           true,
		"github.com/great-magician-01/timeline/internal/platform/logging.(*stackHandler).Handle": true,
		"github.com/great-magician-01/timeline/internal/modules/content/http.(*Handler).fail":    false,
	}
	for fn, want := range cases {
		if got := isLoggingFrame(fn); got != want {
			t.Errorf("isLoggingFrame(%q)=%v，期望 %v", fn, got, want)
		}
	}
}
