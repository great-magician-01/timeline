package http

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/ai/app"
	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// Handler 是 ai 模块的管理 HTTP handler。
type Handler struct {
	service *app.Service
}

// New 构造 ai 模块 Handler；路由与鉴权由 transport/router 统一挂载。
func New(service *app.Service) *Handler { return &Handler{service: service} }

// writeError 把领域错误映射为统一 JSON 响应；未识别错误按 500 处理。
func (h *Handler) writeError(c *gin.Context, err error) {
	if httpx.Respond(c, err) {
		return
	}
	httpx.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
}

// adminID 读取当前管理员 ID；缺失时 RequireViewer 已写回 401。
func (h *Handler) adminID(c *gin.Context) (int64, bool) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return 0, false
	}
	return viewer.ID, true
}

// decode 解析请求体：限制大小、拒绝未知字段与多余 JSON 值。
func (h *Handler) decode(c *gin.Context, dst any) bool {
	if err := httpx.DecodeJSON(c, dst); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request",
			"请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return false
	}
	return true
}

// pageParams 解析通用分页参数；失败时已写回 400。
func (h *Handler) pageParams(c *gin.Context) (limit int, cursor int64, ok bool) {
	limit, err := httpx.PageLimit(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, 0, false
	}
	cursor, err = httpx.PageCursor(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return 0, 0, false
	}
	return limit, cursor, true
}

// pathID 解析路径参数中的雪花 ID；失败时已写回 400。
func (h *Handler) pathID(c *gin.Context, name, message string) (int64, bool) {
	id, err := httpx.ParseID(c.Param(name))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", message)
		return 0, false
	}
	return id, true
}

// listResponse 组装统一游标列表响应。
func listResponse(items any, lastID int64, hasMore bool) gin.H {
	return httpx.ListResponse(items, lastID, hasMore)
}

// behaviorInputDTO 是行为策略补丁的请求体（与旧 API 字段一致）。
type behaviorInputDTO struct {
	AllowOriginalPost    *bool           `json:"allow_original_post"`
	AllowActiveComment   *bool           `json:"allow_active_comment"`
	AllowPassiveReply    *bool           `json:"allow_passive_reply"`
	AllowLike            *bool           `json:"allow_like"`
	AllowFollow          *bool           `json:"allow_follow"`
	AllowUnfollow        *bool           `json:"allow_unfollow"`
	AllowRepost          *bool           `json:"allow_repost"`
	AllowQuote           *bool           `json:"allow_quote"`
	AllowAIToAI          *bool           `json:"allow_ai_to_ai"`
	AllowWebSearch       *bool           `json:"allow_web_search"`
	CandidateScopeType   *string         `json:"candidate_scope_type"`
	CandidateScopeParams json.RawMessage `json:"candidate_scope_params"`
	RateLimits           json.RawMessage `json:"rate_limits"`
	CooldownSeconds      *int            `json:"cooldown_seconds"`
	ActiveHoursStart     *int            `json:"active_hours_start"`
	ActiveHoursEnd       *int            `json:"active_hours_end"`
	Schedule             json.RawMessage `json:"schedule"`
	MaxReplyDepth        *int            `json:"max_reply_depth"`
}

// patch 转换为领域补丁。
func (in behaviorInputDTO) patch() domain.BehaviorPatch {
	return domain.BehaviorPatch{
		AllowOriginalPost:    in.AllowOriginalPost,
		AllowActiveComment:   in.AllowActiveComment,
		AllowPassiveReply:    in.AllowPassiveReply,
		AllowLike:            in.AllowLike,
		AllowFollow:          in.AllowFollow,
		AllowUnfollow:        in.AllowUnfollow,
		AllowRepost:          in.AllowRepost,
		AllowQuote:           in.AllowQuote,
		AllowAIToAI:          in.AllowAIToAI,
		AllowWebSearch:       in.AllowWebSearch,
		CandidateScopeType:   in.CandidateScopeType,
		CandidateScopeParams: in.CandidateScopeParams,
		RateLimits:           in.RateLimits,
		CooldownSeconds:      in.CooldownSeconds,
		ActiveHoursStart:     in.ActiveHoursStart,
		ActiveHoursEnd:       in.ActiveHoursEnd,
		Schedule:             in.Schedule,
		MaxReplyDepth:        in.MaxReplyDepth,
	}
}
