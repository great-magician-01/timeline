package logging

import (
	"context"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
)

// StackAttr 是日志记录中完整调用栈的属性键。
//
// ERROR 级别记录由 stackHandler 自动附加调用栈（调用方已显式提供时不再重复）；
// panic 恢复、后台任务 panic 等场景由调用方用 slog.String(StackAttr, string(debug.Stack()))
// 显式传入，保证栈从 panic 现场开始、包含 goroutine 头。
const StackAttr = "stack"

// maxStackDepth 是自动采集调用栈的最大帧数；正常调用链远小于该值，
// 仅作为极端深栈（如无限递归包装）的内存保护。
const maxStackDepth = 1024

// stackHandler 为 ERROR 级别日志自动附加调用栈：报错时必须能定位到完整调用链。
//
// 本 handler 包在 text/json handler 之外，只补充属性、不渲染字段：
//   - 只处理 ERROR 及以上级别；WARN/INFO 的高频重试日志不附加栈，避免日志膨胀；
//   - 调用方已显式提供 stack 属性（panic 恢复、作业 panic）时原样保留，不重复采集；
//   - 访问日志（msg=http）是请求摘要，不附加栈——真正的错误栈由产生错误的模块日志负责，
//     在访问日志上采到的只会是中间件调用链。
type stackHandler struct {
	next slog.Handler
}

func newStackHandler(next slog.Handler) slog.Handler {
	return &stackHandler{next: next}
}

func (h *stackHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *stackHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError && r.Message != textAccessMsg && !hasStackAttr(r) {
		if stack := captureStack(); stack != "" {
			r.AddAttrs(slog.String(StackAttr, stack))
		}
	}
	return h.next.Handle(ctx, r)
}

func (h *stackHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &stackHandler{next: h.next.WithAttrs(attrs)}
}

func (h *stackHandler) WithGroup(name string) slog.Handler {
	return &stackHandler{next: h.next.WithGroup(name)}
}

// hasStackAttr 报告记录是否已显式携带 stack 属性（顶层键，不进入分组）。
//
// 只识别日志调用点通过 "stack", ... 传入的属性：通过 logger.With("stack", ...) 预置的
// 属性不参与去重（生产代码只在调用点显式传入 stack，panic 恢复与作业 panic 均如此）。
func hasStackAttr(r slog.Record) bool {
	found := false
	r.Attrs(func(attr slog.Attr) bool {
		if attr.Key == StackAttr {
			found = true
			return false
		}
		return true
	})
	return found
}

// captureStack 返回从日志调用方开始（跳过 runtime/slog/logging 包内部帧）的完整调用栈。
//
// 输出格式与 Go 运行时栈一致，每帧两行：函数名 + 制表符缩进的文件:行号；
// 帧缓冲区按需扩容，最多采集 maxStackDepth 帧（极端深栈的内存保护，正常调用链远小于该值）。
func captureStack() string {
	size := 32
	var pcs []uintptr
	var n int
	for {
		pcs = make([]uintptr, size)
		n = runtime.Callers(1, pcs)
		if n < size || size >= maxStackDepth {
			break
		}
		size *= 2
	}
	pcs = pcs[:n]

	frames := runtime.CallersFrames(pcs)
	var b strings.Builder
	recording := false
	for {
		frame, more := frames.Next()
		if !recording {
			if isLoggingFrame(frame.Function) {
				if !more {
					break
				}
				continue
			}
			recording = true
		}
		b.WriteString(frame.Function)
		b.WriteByte('\n')
		b.WriteByte('\t')
		b.WriteString(frame.File)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(frame.Line))
		if !more {
			break
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// isLoggingFrame 判断帧是否属于日志基础设施（slog 的 Logger 方法与本包的采集/分发帧），
// 这些帧对排查业务错误没有意义，采集时从栈顶跳过。
func isLoggingFrame(function string) bool {
	if strings.HasPrefix(function, "log/slog.") {
		return true
	}
	return strings.HasSuffix(function, "/internal/platform/logging.captureStack") ||
		strings.HasSuffix(function, "/internal/platform/logging.(*stackHandler).Handle")
}

// 编译期断言：stackHandler 实现 slog.Handler。
var _ slog.Handler = (*stackHandler)(nil)
