// Package domain 实现社交域的纯规则：评论线程位置、状态迁移、计数增量与删除权限。
// 不访问数据库、不依赖 HTTP/Gin；错误统一使用 platform/apperr。
package domain

import (
	"strings"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

const (
	// ReplyPreviewSize 是顶层评论在列表中携带的回复预览条数。
	ReplyPreviewSize = 2
	// MaxCommentDepth 是计算回复深度时的防环上限。
	MaxCommentDepth = 256

	// TargetPost / TargetComment 是互动与点赞的目标类型（与数据库 CHECK 约束一致）。
	TargetPost    = "post"
	TargetComment = "comment"

	// ActionApprove / ActionReject / ActionCancel 是关注申请的处理动作。
	ActionApprove = "approve"
	ActionReject  = "reject"
	ActionCancel  = "cancel"

	// DirectionIncoming / DirectionOutgoing 是关注申请列表方向。
	DirectionIncoming = "incoming"
	DirectionOutgoing = "outgoing"
)

// CountDelta 返回状态翻转对应的计数增量：失效→生效 +1，生效→失效 -1，
// 状态未变化时为 0（幂等操作不重复计数）。
func CountDelta(wasActive, nowActive bool) int {
	switch {
	case !wasActive && nowActive:
		return 1
	case wasActive && !nowActive:
		return -1
	default:
		return 0
	}
}

// ThreadPosition 推导回复的 parent / root：顶层评论两者都为空；
// 父评论本身是顶层时 root 就是父评论 ID。
func ThreadPosition(parentID int64, parentRootID *int64) (parent, root *int64) {
	parent = &parentID
	if parentRootID != nil {
		return parent, parentRootID
	}
	return parent, parent
}

// ValidateCommentBody 校验渲染后的正文非空；只包含本站附件图片的正文视为非空。
func ValidateCommentBody(safeHTML, plain string) error {
	if strings.TrimSpace(plain) != "" {
		return nil
	}
	if strings.Contains(safeHTML, "<img") {
		return nil
	}
	return apperr.Validation("评论内容不能为空")
}

// CanDeleteComment 判断访问者能否删除评论：评论作者、帖子作者或管理员；
// 已删除评论不再可删除。
func CanDeleteComment(comment model.Comment, postAuthorID int64, viewer model.Viewer) bool {
	if comment.Deleted() {
		return false
	}
	if viewer.IsAdmin {
		return true
	}
	if !viewer.Authenticated {
		return false
	}
	return viewer.ID == comment.AuthorID || viewer.ID == postAuthorID
}

// FollowRequestTransition 计算关注申请状态机的下一步：
// pending 时按动作迁移；已处于目标状态时幂等返回；其余情况视为冲突。
func FollowRequestTransition(current, action string) (next string, changed bool, err error) {
	var target string
	switch action {
	case ActionApprove:
		target = model.FollowRequestApproved
	case ActionReject:
		target = model.FollowRequestRejected
	case ActionCancel:
		target = model.FollowRequestCanceled
	default:
		return "", false, apperr.Validation("未知的申请操作")
	}
	switch current {
	case target:
		return current, false, nil
	case model.FollowRequestPending:
		return target, true, nil
	default:
		return "", false, apperr.Conflict("request_resolved", "该申请已处理，无法再次操作")
	}
}

// NormalizeDirection 校验关注申请列表方向；空串默认 incoming。
func NormalizeDirection(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	switch value {
	case "":
		return DirectionIncoming, nil
	case DirectionIncoming, DirectionOutgoing:
		return value, nil
	default:
		return "", apperr.Invalid("direction 只支持 incoming 或 outgoing")
	}
}

// ValidateTargetType 校验互动目标类型。
func ValidateTargetType(targetType string) error {
	if targetType != TargetPost && targetType != TargetComment {
		return apperr.Validation("互动目标类型无效")
	}
	return nil
}
