package http

import (
	stdhttp "net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/auth/app"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// UpdateProfile 处理 POST /api/v1/users/me：更新昵称/简介（头像只能通过附件上传设置）。
func (h *Handler) UpdateProfile(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	var in struct {
		Nickname  *string `json:"nickname"`
		AvatarURL *string `json:"avatar_url"`
		Bio       *string `json:"bio"`
	}
	if !h.decodeJSON(c, &in) {
		return
	}
	user, err := h.svc.UpdateProfile(c.Request.Context(), app.UpdateProfileInput{
		UserID:    viewer.ID,
		Nickname:  in.Nickname,
		Bio:       in.Bio,
		AvatarURL: in.AvatarURL,
	})
	if err != nil {
		h.fail(c, "更新用户资料失败", err)
		return
	}
	c.JSON(stdhttp.StatusOK, userEnvelope{User: privateUser(user)})
}

// DeleteBanner 处理 POST /api/v1/users/me/banner/delete：移除主页横幅，回退默认占位。
func (h *Handler) DeleteBanner(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	user, err := h.svc.ClearBanner(c.Request.Context(), viewer.ID)
	if err != nil {
		h.fail(c, "移除主页横幅失败", err)
		return
	}
	c.JSON(stdhttp.StatusOK, userEnvelope{User: privateUser(user)})
}

// FollowSettings 处理 GET /api/v1/users/me/follow-settings：读取关注审批开关。
func (h *Handler) FollowSettings(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	enabled, err := h.svc.FollowSettings(c.Request.Context(), viewer.ID)
	if err != nil {
		h.fail(c, "读取关注审批设置失败", err)
		return
	}
	c.JSON(stdhttp.StatusOK, followSettingsResponse{RequireFollowApproval: enabled})
}

// UpdateFollowSettings 处理 POST /api/v1/users/me/follow-settings：更新关注审批开关。
func (h *Handler) UpdateFollowSettings(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	var in struct {
		RequireFollowApproval *bool `json:"require_follow_approval"`
	}
	if !h.decodeJSON(c, &in) {
		return
	}
	if in.RequireFollowApproval == nil {
		httpx.ErrorWithDetails(c, stdhttp.StatusBadRequest, "validation_failed", "缺少关注审批设置",
			map[string]string{"require_follow_approval": "该字段不能为空"})
		return
	}
	enabled, err := h.svc.UpdateFollowSettings(c.Request.Context(), viewer.ID, *in.RequireFollowApproval)
	if err != nil {
		h.fail(c, "更新关注审批设置失败", err)
		return
	}
	c.JSON(stdhttp.StatusOK, followSettingsResponse{RequireFollowApproval: enabled})
}

// DMSettings 处理 GET /api/v1/users/me/dm-settings：读取私信接收策略。
func (h *Handler) DMSettings(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	policy, err := h.svc.DMSettings(c.Request.Context(), viewer.ID)
	if err != nil {
		h.fail(c, "读取私信接收设置失败", err)
		return
	}
	c.JSON(stdhttp.StatusOK, dmSettingsResponse{DMPolicy: policy})
}

// UpdateDMSettings 处理 POST /api/v1/users/me/dm-settings：更新私信接收策略。
func (h *Handler) UpdateDMSettings(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	var in struct {
		DMPolicy *string `json:"dm_policy"`
	}
	if !h.decodeJSON(c, &in) {
		return
	}
	if in.DMPolicy == nil {
		httpx.ErrorWithDetails(c, stdhttp.StatusBadRequest, "validation_failed", "缺少私信接收设置",
			map[string]string{"dm_policy": "该字段不能为空"})
		return
	}
	policy, err := h.svc.UpdateDMSettings(c.Request.Context(), viewer.ID, *in.DMPolicy)
	if err != nil {
		h.fail(c, "更新私信接收设置失败", err)
		return
	}
	c.JSON(stdhttp.StatusOK, dmSettingsResponse{DMPolicy: policy})
}

// SearchUsers 处理 GET /api/v1/users/search：只返回公开资料，ID 游标分页。
func (h *Handler) SearchUsers(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("q"))
	limit, err := httpx.PageLimit(c)
	if err != nil {
		httpx.Error(c, stdhttp.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := httpx.PageCursor(c)
	if err != nil {
		httpx.Error(c, stdhttp.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := h.svc.SearchUsers(c.Request.Context(), keyword, cursor, limit)
	if err != nil {
		h.fail(c, "搜索用户失败", err)
		return
	}
	items := make([]userResponse, 0, len(result.Users))
	for _, user := range result.Users {
		items = append(items, publicUser(user))
	}
	c.JSON(stdhttp.StatusOK, httpx.ListResponse(items, result.LastID, result.HasMore))
}

// GetProfile 处理 GET /api/v1/users/:username：公开资料、计数与关注状态。
func (h *Handler) GetProfile(c *gin.Context) {
	// 公开读：未登录或无有效会话时按游客继续。
	viewer, _ := httpx.Viewer(c)
	if !viewer.Authenticated {
		viewer = model.GuestViewer()
	}
	result, err := h.svc.GetProfile(c.Request.Context(), viewer, c.Param("username"))
	if err != nil {
		h.fail(c, "读取用户资料失败", err)
		return
	}
	c.JSON(stdhttp.StatusOK, profileResponse{
		User:           publicUser(result.User),
		Bio:            result.User.Bio,
		PostCount:      result.Stats.Posts,
		FollowingCount: result.Stats.Following,
		FollowerCount:  result.Stats.Followers,
		FollowState:    result.FollowState,
	})
}
