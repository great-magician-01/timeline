package logging

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// ModuleAttr 是日志模块标签的 slog 属性键。text 格式把它渲染为消息前固定宽度的
// [module] 列，json 格式保留为普通字段 module；module 不进入行尾字段列表。
// 具体模块名由组合根等调用方传入（如 WithModule(log, "ai")），本包不感知业务模块。
const ModuleAttr = "module"

// WithModule 返回带模块标签的 logger；module 为空时原样返回。
func WithModule(logger *slog.Logger, module string) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	if module == "" {
		return logger
	}
	return logger.With(ModuleAttr, module)
}

// text 格式的固定列宽与保留字段：时间 23 字符、级别 5 字符、
// 模块标签 9 字符（最长模块名 content 的 [content] 恰为 9）。
const (
	textTimeLayout  = "2006-01-02 15:04:05.000"
	textLevelWidth  = 5
	textModuleWidth = 9
	textAccessMsg   = "http"
	textErrorKey    = "err"
)

// textHandler 是 text 格式的 slog.Handler：单行对齐文本，人读优先。
//
// 输出结构：<时间> <LEVEL> [<module>] <消息> key=value ... err="..."
//   - 时间固定为北京时间毫秒；级别补齐到 5 列；模块标签补齐到 9 列，消息起始列对齐；
//   - 访问日志（msg=http）压缩为「方法 路径 状态 耗时ms ip=... request_id=...」，
//     字段顺序与结构化字段保持一致，json 格式不受影响；
//   - err 字段固定放行尾并加引号；其它字符串值按需加引号，换行转义，保证一行一条；
//   - stack 字段（错误调用栈）不内联，而是在记录之后按行缩进输出完整多行栈，
//     方便直接阅读与复制；这是唯一允许多行的字段。
type textHandler struct {
	writer io.Writer
	level  slog.Level
	loc    *time.Location
	fields []textField
	groups []string
}

// textField 记录预先带上的属性及其所在分组前缀（WithAttrs 之后 WithGroup 不影响旧属性）。
type textField struct {
	prefix string
	attr   slog.Attr
}

func newTextHandler(w io.Writer, level slog.Level, loc *time.Location) *textHandler {
	if loc == nil {
		loc = time.Local
	}
	return &textHandler{writer: w, level: level, loc: loc}
}

func (h *textHandler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	groupPrefix := ""
	if len(h.groups) > 0 {
		groupPrefix = strings.Join(h.groups, ".") + "."
	}
	fields := make([]textField, 0, len(h.fields)+r.NumAttrs())
	fields = append(fields, h.fields...)
	r.Attrs(func(attr slog.Attr) bool {
		fields = append(fields, textField{prefix: groupPrefix, attr: attr})
		return true
	})

	var b strings.Builder
	b.Grow(160)
	b.WriteString(r.Time.In(h.loc).Format(textTimeLayout))
	b.WriteByte(' ')
	b.WriteString(padText(strings.ToUpper(r.Level.String()), textLevelWidth))
	if module, ok := textModule(fields); ok {
		b.WriteByte(' ')
		b.WriteString(padText("["+module+"]", textModuleWidth))
	}
	b.WriteByte(' ')

	skip := map[string]bool{ModuleAttr: true}
	compressed := false
	if r.Message == textAccessMsg {
		if method, path, status, latency, ok := accessFields(fields); ok {
			compressed = true
			b.WriteString(method)
			b.WriteByte(' ')
			b.WriteString(path)
			b.WriteByte(' ')
			b.WriteString(strconv.FormatInt(status, 10))
			b.WriteByte(' ')
			b.WriteString(strconv.FormatInt(latency, 10))
			b.WriteString("ms")
			for _, key := range []string{"method", "path", "status", "latency_ms"} {
				skip[key] = true
			}
		}
	}
	if !compressed {
		b.WriteString(oneLine(r.Message))
	}

	var errValue slog.Value
	hasErr := false
	var stackValue string
	hasStack := false
	for _, field := range fields {
		// 调用栈单独渲染为多行块，不进入行尾字段列表（分组前缀也按 stack 识别）。
		if field.attr.Key == StackAttr {
			stackValue = field.attr.Value.Resolve().String()
			hasStack = true
			continue
		}
		if field.prefix == "" {
			if skip[field.attr.Key] {
				continue
			}
			if field.attr.Key == textErrorKey {
				errValue = field.attr.Value
				hasErr = true
				continue
			}
		}
		h.writeAttr(&b, field.prefix, field.attr)
	}
	if hasErr {
		b.WriteString(" ")
		b.WriteString(textErrorKey)
		b.WriteString("=")
		b.WriteString(quoteTextValue(errValue.Resolve(), h.loc))
	}
	b.WriteByte('\n')
	if hasStack {
		writeTextStack(&b, stackValue)
	}

	_, err := io.WriteString(h.writer, b.String())
	return err
}

// writeTextStack 把错误调用栈缩进两格、按原样逐行写在日志记录之后。
// 栈是唯一允许多行的字段：不转义换行，便于在日志文件中直接阅读、复制和对照源码。
func writeTextStack(b *strings.Builder, stack string) {
	stack = strings.TrimRight(stack, "\r\n")
	if stack == "" {
		return
	}
	for line := range strings.SplitSeq(stack, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// textModule 返回最后一个非空模块标签（组合根接线时用 WithModule 注入）。
func textModule(fields []textField) (string, bool) {
	module := ""
	for _, field := range fields {
		if field.prefix != "" || field.attr.Key != ModuleAttr {
			continue
		}
		if value := field.attr.Value.Resolve().String(); value != "" {
			module = value
		}
	}
	return module, module != ""
}

// accessFields 读取访问日志压缩所需字段；缺少任一字段时不压缩，按普通日志输出。
func accessFields(fields []textField) (method, path string, status, latency int64, ok bool) {
	var hasStatus, hasLatency bool
	for _, field := range fields {
		if field.prefix != "" {
			continue
		}
		value := field.attr.Value.Resolve()
		switch field.attr.Key {
		case "method":
			method = value.String()
		case "path":
			path = value.String()
		case "status":
			status = value.Int64()
			hasStatus = true
		case "latency_ms":
			latency = value.Int64()
			hasLatency = true
		}
	}
	return method, path, status, latency, method != "" && path != "" && hasStatus && hasLatency
}

// writeAttr 递归输出分组字段：分组用点号连接，空键与空分组跳过。
func (h *textHandler) writeAttr(b *strings.Builder, prefix string, attr slog.Attr) {
	if attr.Key == "" {
		return
	}
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		groupPrefix := prefix + attr.Key + "."
		for _, child := range value.Group() {
			h.writeAttr(b, groupPrefix, child)
		}
		return
	}
	b.WriteByte(' ')
	b.WriteString(prefix)
	b.WriteString(attr.Key)
	b.WriteByte('=')
	b.WriteString(quoteTextValue(value, h.loc))
}

// quoteTextValue 把属性值渲染为单行文本：字符串按需加引号，数值/布尔/时长保持裸值。
func quoteTextValue(value slog.Value, loc *time.Location) string {
	switch value.Kind() {
	case slog.KindString:
		return quoteText(value.String())
	case slog.KindInt64:
		return strconv.FormatInt(value.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(value.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(value.Float64(), 'g', -1, 64)
	case slog.KindBool:
		return strconv.FormatBool(value.Bool())
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().In(loc).Format(time.RFC3339)
	default:
		return quoteText(value.String())
	}
}

// quoteText 在值含空格、等号、引号或控制字符时用双引号包裹，避免破坏行结构。
func quoteText(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == ' ' || r == '\t' || r == '=' || r == '"' {
			return strconv.Quote(s)
		}
	}
	return s
}

// oneLine 把消息中的换行转义为 \n，保证一条日志只占一行。
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", `\n`)
}

// padText 把 ASCII 前缀补齐到固定列宽；超长值原样输出（不截断）。
func padText(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	prefix := ""
	if len(h.groups) > 0 {
		prefix = strings.Join(h.groups, ".") + "."
	}
	next := &textHandler{
		writer: h.writer,
		level:  h.level,
		loc:    h.loc,
		groups: h.groups,
		fields: make([]textField, 0, len(h.fields)+len(attrs)),
	}
	next.fields = append(next.fields, h.fields...)
	for _, attr := range attrs {
		next.fields = append(next.fields, textField{prefix: prefix, attr: attr})
	}
	return next
}

func (h *textHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := make([]string, len(h.groups)+1)
	copy(groups, h.groups)
	groups[len(h.groups)] = name
	next := &textHandler{
		writer: h.writer,
		level:  h.level,
		loc:    h.loc,
		fields: append([]textField{}, h.fields...),
		groups: groups,
	}
	return next
}

// 编译期断言：textHandler 实现 slog.Handler。
var _ slog.Handler = (*textHandler)(nil)
