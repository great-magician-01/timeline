package http

import (
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/view"
)

// 本层复用 shared/view 的账号 DTO：公开资料不含邮箱/角色/超管标记等私有字段，
// 私有资料（本人或管理员）包含 is_super_admin。这里只做命名收敛与响应包装。

// userResponse 是账号 DTO 的别名，便于 handler 内引用。
type userResponse = view.UserResponse

// privateUser 组装私有账号 DTO（含 email/role/is_super_admin/status 等）。
func privateUser(u model.User) userResponse { return view.PrivateUserResponse(u) }

// publicUser 组装公开账号 DTO（不含私有字段）。
func publicUser(u model.User) userResponse { return view.PublicUserResponse(u) }

// csrfResponse 是 GET /auth/csrf 的响应。
type csrfResponse struct {
	CSRFToken string `json:"csrf_token"`
}

// configResponse 是 GET /auth/config 的响应。
type configResponse struct {
	RegistrationEnabled bool `json:"registration_enabled"`
	BootstrapRequired   bool `json:"bootstrap_required"`
}

// userEnvelope 是返回单个用户的统一响应包装。
type userEnvelope struct {
	User userResponse `json:"user"`
}

// loginResponse 是登录成功响应；会话令牌只通过 HttpOnly Cookie 传递。
type loginResponse struct {
	User      userResponse `json:"user"`
	CSRFToken string       `json:"csrf_token"`
}

// meResponse 是当前登录账号响应（含会话 CSRF 令牌）。
type meResponse struct {
	User      userResponse `json:"user"`
	CSRFToken string       `json:"csrf_token"`
}

// followSettingsResponse 是关注审批开关响应。
type followSettingsResponse struct {
	RequireFollowApproval bool `json:"require_follow_approval"`
}

// profileResponse 是个人主页响应（字段与旧 API 保持一致）。
type profileResponse struct {
	User           userResponse `json:"user"`
	Bio            string       `json:"bio"`
	PostCount      int64        `json:"post_count"`
	FollowingCount int64        `json:"following_count"`
	FollowerCount  int64        `json:"follower_count"`
	FollowState    string       `json:"follow_state"`
}
