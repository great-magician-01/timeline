package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// fixedLogTime 是测试用固定记录时间（北京时间毫秒），保证文本输出可精确断言。
var fixedLogTime = time.Date(2026, 10, 18, 18, 46, 35, 123_000_000, testLocation)

// newTestTextHandler 返回写入内存缓冲区的 text handler。
func newTestTextHandler(t *testing.T, level slog.Level) (*bytes.Buffer, *textHandler) {
	t.Helper()
	buf := &bytes.Buffer{}
	return buf, newTextHandler(buf, level, testLocation)
}

// handleRecord 直接把记录交给 handler，绕过 slog.Logger 的 Enabled 检查。
func handleRecord(t *testing.T, handler slog.Handler, rec slog.Record) {
	t.Helper()
	if err := handler.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle 失败: %v", err)
	}
}

// newRecord 构造固定时间的日志记录。
func newRecord(level slog.Level, msg string, attrs ...slog.Attr) slog.Record {
	rec := slog.NewRecord(fixedLogTime, level, msg, 0)
	rec.AddAttrs(attrs...)
	return rec
}

// TestTextHandlerBasicLine 覆盖对齐前缀、消息、字段与 err 置尾。
func TestTextHandlerBasicLine(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelDebug)
	rec := newRecord(slog.LevelWarn, "读取启用中的 AI 失败",
		slog.Any("err", errors.New("unexpected EOF")),
		slog.Int64("ai_id", 1841234567890123456),
	)
	handleRecord(t, handler, rec)

	want := "2026-10-18 18:46:35.123 WARN  读取启用中的 AI 失败 " +
		"ai_id=1841234567890123456 err=\"unexpected EOF\"\n"
	if got := buf.String(); got != want {
		t.Fatalf("text 输出不符合预期:\n got=%q\nwant=%q", got, want)
	}
}

// TestTextHandlerModuleTag 覆盖模块标签列补齐到固定宽度。
func TestTextHandlerModuleTag(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelInfo)
	tagged := handler.WithAttrs([]slog.Attr{slog.String(ModuleAttr, "ai")})
	handleRecord(t, tagged, newRecord(slog.LevelInfo, "消息", slog.Int("attempt", 3)))

	want := "2026-10-18 18:46:35.123 INFO  [ai]      消息 attempt=3\n"
	if got := buf.String(); got != want {
		t.Fatalf("模块标签输出不符合预期:\n got=%q\nwant=%q", got, want)
	}
}

// TestTextHandlerAccessSummary 覆盖访问日志压缩：方法/路径/状态/耗时成摘要，
// 其余结构化字段保留，json 字段语义不变。
func TestTextHandlerAccessSummary(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelInfo)
	tagged := handler.WithAttrs([]slog.Attr{slog.String(ModuleAttr, "http")})
	rec := newRecord(slog.LevelInfo, "http",
		slog.String("method", "GET"),
		slog.String("path", "/api/v1/timeline"),
		slog.Int("status", 200),
		slog.Int64("latency_ms", 12),
		slog.String("ip", "10.0.0.5"),
		slog.String("request_id", "9f2c8a1b6d4e4f0a"),
		slog.String("user_id", "1001"),
	)
	handleRecord(t, tagged, rec)

	want := "2026-10-18 18:46:35.123 INFO  [http]    GET /api/v1/timeline 200 12ms " +
		"ip=10.0.0.5 request_id=9f2c8a1b6d4e4f0a user_id=1001\n"
	if got := buf.String(); got != want {
		t.Fatalf("访问日志压缩输出不符合预期:\n got=%q\nwant=%q", got, want)
	}
}

// TestTextHandlerAccessWithoutEnoughFieldsFallsBack 覆盖访问日志字段缺失时退回普通输出。
func TestTextHandlerAccessWithoutEnoughFieldsFallsBack(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelInfo)
	handleRecord(t, handler, newRecord(slog.LevelInfo, "http", slog.String("method", "GET")))

	want := "2026-10-18 18:46:35.123 INFO  http method=GET\n"
	if got := buf.String(); got != want {
		t.Fatalf("字段不足时不应压缩:\n got=%q\nwant=%q", got, want)
	}
}

// TestTextHandlerQuotesAndSingleLine 覆盖引号规则与消息换行转义。
func TestTextHandlerQuotesAndSingleLine(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelInfo)
	rec := newRecord(slog.LevelInfo, "第一行\n第二行",
		slog.String("reason", "a=b c"),
		slog.String("empty", ""),
	)
	handleRecord(t, handler, rec)

	line := buf.String()
	for _, want := range []string{
		`第一行\n第二行`,
		`reason="a=b c"`,
		`empty=""`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("输出缺少 %q: %q", want, line)
		}
	}
	if strings.Contains(line, "\n第二行") {
		t.Fatalf("消息中的换行必须转义为一行: %q", line)
	}
}

// TestTextHandlerValueKinds 覆盖数值、布尔、时长与时间字段的裸值渲染。
func TestTextHandlerValueKinds(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelInfo)
	rec := newRecord(slog.LevelInfo, "字段类型",
		slog.Int("count", 7),
		slog.Uint64("bytes", 1024),
		slog.Float64("ratio", 0.5),
		slog.Bool("ok", true),
		slog.Duration("backoff", 2*time.Second),
		slog.Time("at", fixedLogTime),
	)
	handleRecord(t, handler, rec)

	line := buf.String()
	for _, want := range []string{
		"count=7",
		"bytes=1024",
		"ratio=0.5",
		"ok=true",
		"backoff=2s",
		"at=2026-10-18T18:46:35+08:00",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("输出缺少 %q: %q", want, line)
		}
	}
}

// TestTextHandlerWithGroup 覆盖 WithGroup 后的字段使用点号分组前缀。
func TestTextHandlerWithGroup(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelInfo)
	grouped := handler.WithGroup("request").WithAttrs([]slog.Attr{slog.String("id", "abc")})
	handleRecord(t, grouped, newRecord(slog.LevelInfo, "分组", slog.String("path", "/x")))

	line := buf.String()
	for _, want := range []string{"request.id=abc", "request.path=/x"} {
		if !strings.Contains(line, want) {
			t.Fatalf("分组字段缺少 %q: %q", want, line)
		}
	}
}

// TestTextHandlerLevelEnabled 覆盖 HandlerOptions 级别过滤的 Enabled 判断。
func TestTextHandlerLevelEnabled(t *testing.T) {
	_, handler := newTestTextHandler(t, slog.LevelWarn)
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info 低于 warn，应被过滤")
	}
	if !handler.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("warn 应通过")
	}
	if !handler.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("error 应通过")
	}
}

// TestWithModule 覆盖模块标签：text 格式渲染为 [module]，json 格式保留 module 字段。
func TestWithModule(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelInfo)
	logger := slog.New(handler)
	logger = WithModule(logger, "ai")
	logger.Info("带模块")
	if !strings.Contains(buf.String(), "[ai]") {
		t.Fatalf("text 格式应渲染模块标签: %q", buf.String())
	}

	jsonBuf := &bytes.Buffer{}
	jsonLogger := WithModule(slog.New(slog.NewJSONHandler(jsonBuf, nil)), "content")
	jsonLogger.Info("带模块")
	var entry map[string]any
	if err := json.Unmarshal(jsonBuf.Bytes(), &entry); err != nil {
		t.Fatalf("json 日志无法解析: %v", err)
	}
	if entry[ModuleAttr] != "content" {
		t.Fatalf("json 应保留 module 字段: %#v", entry)
	}

	plain := slog.New(handler)
	if WithModule(plain, "") != plain {
		t.Fatal("空模块名应原样返回 logger")
	}
}

// TestTextHandlerStackBlock 覆盖 stack 字段渲染为记录之后的多行缩进块，不进入行尾字段。
func TestTextHandlerStackBlock(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelDebug)
	rec := newRecord(slog.LevelError, "写入失败",
		slog.Any("err", errors.New("磁盘已满")),
		slog.Int("attempt", 2),
		slog.String(StackAttr, "main.foo\n\t/app/main.go:10\nmain.bar\n\t/app/main.go:20"),
	)
	handleRecord(t, handler, rec)

	want := "2026-10-18 18:46:35.123 ERROR 写入失败 attempt=2 err=磁盘已满\n" +
		"  main.foo\n" +
		"  \t/app/main.go:10\n" +
		"  main.bar\n" +
		"  \t/app/main.go:20\n"
	if got := buf.String(); got != want {
		t.Fatalf("调用栈渲染不符合预期:\n got=%q\nwant=%q", got, want)
	}
}

// TestTextHandlerEmptyStackIgnored 覆盖空 stack 不产生多余行。
func TestTextHandlerEmptyStackIgnored(t *testing.T) {
	buf, handler := newTestTextHandler(t, slog.LevelInfo)
	handleRecord(t, handler, newRecord(slog.LevelInfo, "普通日志", slog.String(StackAttr, "")))

	want := "2026-10-18 18:46:35.123 INFO  普通日志\n"
	if got := buf.String(); got != want {
		t.Fatalf("空调用栈不应输出额外内容:\n got=%q\nwant=%q", got, want)
	}
}
