// Package domain 实现私信域的纯规则：会话双方规范化、接收策略判定、撤回时间窗、
// 正文校验与列表预览。不访问数据库、不依赖 HTTP/Gin；错误统一使用 platform/apperr。
package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/chat"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 聊天文本限制与私信/群聊共享（shared/chat 单一来源），本包保留同名导出供既有调用方
// 与测试使用；校验文案保持私信域的具体表述。
const (
	// RecallWindow 是发送者撤回自己消息的时间窗（应用侧 Clock 比较）。
	RecallWindow = chat.RecallWindow
	// PreviewRunes 是会话列表最后消息预览的最大字符数（按 rune 截断）。
	PreviewRunes = chat.PreviewRunes
	// RecalledPreview 是已撤回消息在会话列表中的预览文案。
	RecalledPreview = chat.RecalledPreview
	// MaxMessageRunes 是私信正文的最大字符数（按清洗后纯文本的 rune 计）。
	MaxMessageRunes = chat.MaxMessageRunes
	// MaxMessageSourceBytes 是私信 content_source 的原始体积上限（字节），
	// 在渲染清洗前快速拒绝超大请求，避免无谓的解析与清洗开销。
	MaxMessageSourceBytes = chat.MaxMessageSourceBytes
)

// NormalizePair 把任意方向的两位成员 ID 规范化为 (user_a_id, user_b_id) = (较小, 较大)，
// 与 dm_conversations_pair_unique 及 CHECK (user_a_id < user_b_id) 一致。
func NormalizePair(a, b int64) (int64, int64, error) {
	if a <= 0 || b <= 0 {
		return 0, 0, apperr.Validation("会话成员无效")
	}
	if a == b {
		return 0, 0, apperr.Validation("不能给自己发私信")
	}
	if a > b {
		return b, a, nil
	}
	return a, b, nil
}

// PeerID 返回会话中 userID 的对方 ID；非会话成员按不存在处理，不暴露会话存在性。
func PeerID(conv model.DMConversation, userID int64) (int64, error) {
	peerID, ok := conv.PeerIDFor(userID)
	if !ok {
		return 0, apperr.NotFound("会话不存在")
	}
	return peerID, nil
}

// CanSendTo 判定 sender 能否给 recipient 发送私信（docs/REQUIREMENTS.md §10.7）：
//   - 不能给自己发送；
//   - AI 账号双向不参与私信；
//   - 停用账号不能产生新互动；
//   - 接收策略 all 放行；following 要求接收者关注发送者（“仅我关注的人”）；
//     mutual 要求互相关注；none 关闭；未知取值按关闭处理（失败关闭）。
//
// 关注关系由 app 在发送时刻加载后传入，本函数不做 IO。
func CanSendTo(recipient model.User, senderID int64, senderFollowsRecipient, recipientFollowsSender bool) error {
	if recipient.ID == senderID {
		return apperr.Validation("不能给自己发私信")
	}
	if recipient.IsAI {
		return apperr.Forbidden("AI 账号不参与私信")
	}
	if recipient.IsSuspended() {
		return apperr.Forbidden("对方账号已停用，不能产生新的互动")
	}
	switch recipient.DMPolicy {
	case "", model.DMPolicyAll:
		return nil
	case model.DMPolicyFollowing:
		if !recipientFollowsSender {
			return apperr.Forbidden("对方仅接受其关注的人发来的私信")
		}
		return nil
	case model.DMPolicyMutual:
		if !senderFollowsRecipient || !recipientFollowsSender {
			return apperr.Forbidden("对方仅接受互相关注用户的私信")
		}
		return nil
	default: // model.DMPolicyNone 及未知取值一律关闭。
		return apperr.Forbidden("对方已关闭私信")
	}
}

// CanRecall 校验发送者在时间窗内撤回自己的消息。已撤回视为幂等成功（repo 不重复变更）；
// 非发送者按不存在处理，不暴露消息存在性。
func CanRecall(msg model.DMMessage, viewer model.Viewer, now time.Time) error {
	if msg.SenderID != viewer.ID {
		return apperr.NotFound("消息不存在")
	}
	if msg.Recalled() {
		return nil
	}
	if now.Sub(msg.CreatedAt) > RecallWindow {
		return apperr.Forbidden("超过撤回时间窗口，消息无法撤回")
	}
	return nil
}

// ValidateMessageSource 校验清洗前的原始正文体积：超过 MaxMessageSourceBytes 直接拒绝，
// 不把超大请求交给渲染清洗。
func ValidateMessageSource(source string) error {
	if len(source) > MaxMessageSourceBytes {
		return apperr.Validation("私信内容过长")
	}
	return nil
}

// ValidateMessageBody 校验清洗后的私信正文：附件图片不允许出现在私信中，
// 纯空白内容拒绝，正文长度上限 MaxMessageRunes。附件图片检查优先，给出明确文案。
func ValidateMessageBody(safeHTML, plain string) error {
	if strings.Contains(safeHTML, "<img") {
		return apperr.Validation("私信不支持图片，请发送纯文本")
	}
	if strings.TrimSpace(plain) == "" {
		return apperr.Validation("私信内容不能为空")
	}
	if utf8.RuneCountInString(plain) > MaxMessageRunes {
		return apperr.Validation("私信内容过长，最多 2000 字")
	}
	return nil
}

// Preview 生成会话列表的最后消息预览：已撤回显示占位文案，其余按 rune 安全截断。
func Preview(msg model.DMMessage) string {
	if msg.Recalled() {
		return RecalledPreview
	}
	text := strings.TrimSpace(msg.PlainText)
	if utf8.RuneCountInString(text) <= PreviewRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:PreviewRunes]) + "…"
}
