// Package domain 实现群聊域的纯规则：群名校验、拉人资格、群主动作边界、
// 发言资格与成员视角判定。不访问数据库、不依赖 HTTP/Gin；错误统一使用 platform/apperr。
package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/chat"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

const (
	// MaxNameRunes 是群名的最大字符数（按 rune 计）。
	MaxNameRunes = 30
	// MaxMembers 是单个群的最大有效成员数（含群主与 AI）。
	MaxMembers = 200
	// MaxOwnedGroups 是单个用户可持有的群主名额上限（创建与受让同限）。
	MaxOwnedGroups = 20
	// MaxInvitesPerCall 是一次拉人请求允许追加的最大人数。
	MaxInvitesPerCall = 20
)

// ValidateName 校验群名：去除首尾空白后非空且不超过 MaxNameRunes。
func ValidateName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", apperr.Validation("群名称不能为空")
	}
	if utf8.RuneCountInString(trimmed) > MaxNameRunes {
		return "", apperr.Validation("群名称过长，最多 30 字")
	}
	return trimmed, nil
}

// CanBeInvited 判定 candidate 能否被拉入群（docs/REQUIREMENTS.md §10.8）：
// AI 账号允许被拉入（与私信不同）；停用账号拒绝；dm_policy=none（关闭私信）的
// 用户拒绝——接收策略同时约束「被拉入群」，避免关闭私信者被陌生人建群触达。
func CanBeInvited(candidate model.User) error {
	if candidate.IsSuspended() {
		return apperr.Forbidden("对方账号已停用，不能拉入群聊")
	}
	if !candidate.IsAI && candidate.DMPolicy == model.DMPolicyNone {
		return apperr.Forbidden("对方已关闭私信，不能拉入群聊")
	}
	return nil
}

// RequireOwner 校验操作者是现任群主；非群主按权限不足处理。
func RequireOwner(group model.GroupConversation, member model.GroupMember, viewerID int64) error {
	if !member.Active() || member.UserID != viewerID {
		return apperr.Forbidden("只有群主可以执行该操作")
	}
	if member.IsOwner() && group.OwnerID == viewerID {
		return nil
	}
	return apperr.Forbidden("只有群主可以执行该操作")
}

// RequireMember 校验 viewer 是群的有效成员；非成员按不存在处理，
// 不暴露群的存在性（与私信会话一致）。
func RequireMember(member model.GroupMember, viewerID int64) error {
	if !member.Active() || member.UserID != viewerID {
		return apperr.NotFound("群聊不存在")
	}
	return nil
}

// CanSendMessage 校验成员能否在群内发言：有效成员且群未解散。
// 已停用与 AI 的发送资格由 app 层的 requireWritable 前置（与私信同一入口）。
func CanSendMessage(group model.GroupConversation, member model.GroupMember, viewerID int64) error {
	if group.Dissolved() {
		return apperr.Forbidden("群已解散，不能再发送消息")
	}
	if err := RequireMember(member, viewerID); err != nil {
		return err
	}
	return nil
}

// CanRecall 校验发送者在时间窗内撤回自己的消息（窗口与私信共享 shared/chat）。
// 已撤回视为幂等成功；非发送者按不存在处理，不暴露消息存在性。
func CanRecall(msg model.GroupMessage, viewerID int64, now time.Time) error {
	if msg.SenderID != viewerID {
		return apperr.NotFound("消息不存在")
	}
	if msg.Recalled() {
		return nil
	}
	if now.Sub(msg.CreatedAt) > chat.RecallWindow {
		return apperr.Forbidden("超过撤回时间窗口，消息无法撤回")
	}
	return nil
}

// CanRemove 校验群主踢人：不能移除自己（群主离开走转让/解散），
// 目标必须是该群的有效普通成员。
func CanRemove(target model.GroupMember) error {
	if target.IsOwner() {
		return apperr.Validation("不能移除群主")
	}
	if !target.Active() {
		return apperr.NotFound("成员不存在")
	}
	return nil
}

// RequireTransferBeforeLeave 校验群主退出前置条件：必须先转让群主或解散群。
func RequireTransferBeforeLeave(member model.GroupMember) error {
	if member.IsOwner() {
		return apperr.Validation("群主不能直接退出，请先转让群主或解散群")
	}
	return nil
}
