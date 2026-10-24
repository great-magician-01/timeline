// Package chat 收敛站内聊天类文本（1:1 私信与群聊消息）共享的纯限制与校验：
// 正文长度、来源体积、撤回时间窗与列表预览。无 IO、无 HTTP/数据库依赖；
// 私信与群聊各自的 domain 层引用同一组常量，避免两份规则漂移。
package chat

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

const (
	// RecallWindow 是发送者撤回自己消息的时间窗（应用侧 Clock 比较）。
	RecallWindow = 2 * time.Minute
	// PreviewRunes 是会话列表最后消息预览的最大字符数（按 rune 截断）。
	PreviewRunes = 50
	// RecalledPreview 是已撤回消息在会话列表中的预览文案。
	RecalledPreview = "消息已撤回"
	// MaxMessageRunes 是聊天正文的最大字符数（按清洗后纯文本的 rune 计）。
	MaxMessageRunes = 2000
	// MaxMessageSourceBytes 是 content_source 的原始体积上限（字节），
	// 在渲染清洗前快速拒绝超大请求，避免无谓的解析与清洗开销。
	MaxMessageSourceBytes = 64 << 10
)

// 校验错误（统一 validation 类；文案不带「私信/群聊」字样，由各域自行包装或直接复用）。
var (
	errTooLong = apperr.Validation("消息内容过长，最多 2000 字")
	errNoImage = apperr.Validation("消息不支持图片，请发送纯文本")
	errEmpty   = apperr.Validation("消息内容不能为空")
)

// ValidateSource 校验清洗前的原始正文体积：超过 MaxMessageSourceBytes 直接拒绝，
// 不把超大请求交给渲染清洗。
func ValidateSource(source string) error {
	if len(source) > MaxMessageSourceBytes {
		return errTooLong
	}
	return nil
}

// ValidateBody 校验清洗后的聊天正文：附件图片不允许出现，纯空白内容拒绝，
// 正文长度上限 MaxMessageRunes。附件图片检查优先，给出明确文案。
func ValidateBody(safeHTML, plain string) error {
	if strings.Contains(safeHTML, "<img") {
		return errNoImage
	}
	if strings.TrimSpace(plain) == "" {
		return errEmpty
	}
	if utf8.RuneCountInString(plain) > MaxMessageRunes {
		return errTooLong
	}
	return nil
}

// Preview 生成会话列表的最后消息预览：已撤回显示占位文案，其余按 rune 安全截断。
// recalled 由调用方传入消息撤回状态，避免本包依赖具体消息模型。
func Preview(recalled bool, plain string) string {
	if recalled {
		return RecalledPreview
	}
	text := strings.TrimSpace(plain)
	if utf8.RuneCountInString(text) <= PreviewRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:PreviewRunes]) + "…"
}
