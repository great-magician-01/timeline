// Package access 集中实现跨上下文复用的可见性、只读、互动与角色授权纯策略。
//
// 本包不访问数据库、不依赖 HTTP：关注集合、作者账号状态等数据由 app 用例通过端口
// 加载后作为参数传入；同一套策略同时被 HTTP 中间件（角色部分）与 app 用例复用。
// 不可见场景一律返回 not_found，避免暴露资源存在性（docs/PAGES_AND_PERMISSIONS.md §8）。
package access

import (
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== 帖子可见性 =====

// CanViewPostWithFollowing 判断访问者能否查看帖子，following 为该访问者的生效关注集合
// （可空，仅影响 followers 可见性）。供列表/会话内批量过滤使用，不需要预先加载关注集合
// 的单条场景由调用方按需传入。
func CanViewPostWithFollowing(viewer model.Viewer, post model.Post, following map[int64]bool) bool {
	switch post.Status {
	case model.PostStatusDraft, model.PostStatusScheduled:
		// 草稿与定时未发布只有作者与管理员可见。
		return viewer.IsAdmin || post.SelfPosted(viewer)
	case model.PostStatusDeleted:
		// 已删除内容只有管理员可见。
		return viewer.IsAdmin
	case model.PostStatusPublished:
	default:
		return false
	}
	if viewer.IsAdmin {
		return true
	}
	switch post.Visibility {
	case model.VisibilityPublic:
		return true
	case model.VisibilityFollowers:
		// AI 与会话身份不能借关注关系查看 followers 内容；作者本人可见。
		if !viewer.Authenticated || viewer.IsAI {
			return false
		}
		if post.SelfPosted(viewer) {
			return true
		}
		return following[post.AuthorID]
	case model.VisibilityPrivate:
		return post.SelfPosted(viewer)
	default:
		return false
	}
}

// CanViewPost 是 CanViewPostWithFollowing 的标准入口（旧名保留用于平滑迁移）。
func CanViewPost(viewer model.Viewer, post model.Post, following map[int64]bool) bool {
	return CanViewPostWithFollowing(viewer, post, following)
}

// RequireVisiblePost 在不可见时返回 not_found，不暴露资源存在性。
func RequireVisiblePost(viewer model.Viewer, post model.Post, following map[int64]bool) error {
	if !CanViewPostWithFollowing(viewer, post, following) {
		return apperr.NotFound("内容不存在或无权访问")
	}
	return nil
}

// ===== 写操作与账号状态 =====

// CanWrite 表示该访问者可以执行写操作（已登录、非 AI、未停用）。
func CanWrite(viewer model.Viewer) bool {
	return viewer.Authenticated && !viewer.IsAI && !viewer.IsSuspended
}

// RequireWritable 检查写操作的最低条件（登录、未停用、非 AI）。
func RequireWritable(viewer model.Viewer) error {
	if !viewer.Authenticated {
		return apperr.Unauthenticated("请先登录")
	}
	if viewer.IsSuspended {
		return apperr.Suspended("账号已停用，不能执行此操作")
	}
	if viewer.IsAI {
		return apperr.Forbidden("AI 账号不能通过此接口操作")
	}
	return nil
}

// IsHumanActive 表示账号是人类身份且处于正常状态。
func IsHumanActive(u model.User) bool { return !u.IsAI && u.IsActive() }

// RequireActorWritable 供 AI/后台任务内部执行动作时校验账号状态：
// AI 身份是合法的动作主体，因此这里只拒绝停用账号。
func RequireActorWritable(u model.User) error {
	if u.IsSuspended() {
		return apperr.Suspended("账号已停用，不能执行此操作")
	}
	return nil
}

// ===== 互动 =====

// CanInteract 判断访问者在内容可见的前提下能否产生新互动（点赞/评论/转发等）：
// 帖子必须已发布，且作者账号未停用（作者本人例外）。不包含登录态与可见性检查，
// 调用方应先用 RequireWritable/RequireVisiblePost 完成这些判断。
func CanInteract(viewer model.Viewer, post model.Post, authorSuspended bool) error {
	if post.Status != model.PostStatusPublished {
		return apperr.Forbidden("该内容当前不可互动")
	}
	if authorSuspended && !post.SelfPosted(viewer) {
		return apperr.Forbidden("对方账号已停用，不能产生新的互动")
	}
	return nil
}

// RequireInteractivePost 在 RequireWritable + RequireVisiblePost 基础上检查互动条件。
// following 为访问者的生效关注集合，authorSuspended 为帖子作者账号是否停用。
func RequireInteractivePost(
	viewer model.Viewer,
	post model.Post,
	following map[int64]bool,
	authorSuspended bool,
) error {
	if err := RequireWritable(viewer); err != nil {
		return err
	}
	if err := RequireVisiblePost(viewer, post, following); err != nil {
		return err
	}
	return CanInteract(viewer, post, authorSuspended)
}

// ===== 关注关系 =====

// ClassifyFollowState 把三组布尔关系分类为面向视图的四态：
// following（已关注）/ pending（我已申请）/ incoming_pending（对方申请了我）/ none。
func ClassifyFollowState(following, pending, incomingPending bool) string {
	switch {
	case following:
		return model.FollowStateFollowing
	case pending:
		return model.FollowStatePending
	case incomingPending:
		return model.FollowStateIncomingPending
	default:
		return model.FollowStateNone
	}
}

// ===== 私信接收策略 =====

// ValidateDMPolicy 校验用户提交的私信接收策略；空串按默认 all 处理。
// 取值与 users.dm_policy 的 CHECK 约束一致（需求 §10.7）。
func ValidateDMPolicy(raw string) (string, error) {
	switch raw {
	case "":
		return model.DMPolicyAll, nil
	case model.DMPolicyAll, model.DMPolicyFollowing, model.DMPolicyMutual, model.DMPolicyNone:
		return raw, nil
	default:
		return "", apperr.Validation("私信接收策略无效")
	}
}

// ===== 管理员与超管保护 =====

// CanManageAdmins 表示访问者可以授予/撤销管理员角色：仅超级管理员本人。
func CanManageAdmins(viewer model.Viewer) bool {
	return viewer.Authenticated && !viewer.IsAI && viewer.IsAdmin && viewer.IsSuperAdmin
}

// CanChangeAdminRole 判断操作者能否修改目标账号的管理员角色（授权与撤销共用）：
// 只有超级管理员可以操作，且不能操作自己、其他超级管理员或 AI 账号。
// 授权方向额外的「已停用账号不能授权」由 CanGrantAdminRole 补充（撤销方向不受限）。
func CanChangeAdminRole(actor, target model.User) error {
	if actor.IsAI || actor.Role != model.RoleAdmin || !actor.IsSuperAdmin {
		return apperr.Forbidden("需要超级管理员权限")
	}
	if target.IsAI {
		return apperr.Validation("AI 账号不能成为管理员")
	}
	if target.ID == actor.ID {
		return apperr.Validation("不能修改自己的管理员权限")
	}
	if target.IsSuperAdmin {
		return apperr.Validation("不能修改超级管理员的管理员权限")
	}
	return nil
}

// CanGrantAdminRole 在 CanChangeAdminRole 之上补充授权方向的校验：
// 已停用账号不能授权为管理员（撤销方向不要求账号处于启用状态）。
func CanGrantAdminRole(actor, target model.User) error {
	if err := CanChangeAdminRole(actor, target); err != nil {
		return err
	}
	if !target.IsActive() {
		return apperr.Validation("已停用账号不能授权为管理员")
	}
	return nil
}

// CanModerateUser 判断管理员能否处置目标账号（停用/启用/重置密码等）：
// 操作者必须是管理员会计且非 AI；超级管理员账号只能由超级管理员本人处置。
// 停用自己账号等方向性限制属于具体用例，由调用方在拿到 action 后再判断。
func CanModerateUser(actor model.Viewer, target model.User) error {
	if !actor.Authenticated || actor.IsAI || !actor.IsAdmin {
		return apperr.Forbidden("需要管理员权限")
	}
	if target.IsSuperAdmin && !actor.IsSuperAdmin {
		return apperr.Forbidden("不能操作超级管理员账号")
	}
	return nil
}
