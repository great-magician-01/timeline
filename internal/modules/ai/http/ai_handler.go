package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/ai/app"
	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/view"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// ListAIs 返回 AI 用户列表；支持名称模糊搜索与启停状态过滤，游标倒序分页。
func (h *Handler) ListAIs(c *gin.Context) {
	limit, cursor, ok := h.pageParams(c)
	if !ok {
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	if utf8.RuneCountInString(query) > 100 {
		h.writeError(c, apperr.Invalid("搜索关键词过长"))
		return
	}
	status := strings.TrimSpace(c.Query("status"))
	if status != "" && status != "enabled" && status != "disabled" {
		h.writeError(c, apperr.Invalid("status 只能是 enabled 或 disabled"))
		return
	}
	result, err := h.service.ListAIs(c.Request.Context(), app.AIFilter{
		Query:  query,
		Status: status,
		Cursor: cursor,
		Limit:  limit,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	items := make([]aiListItem, 0, len(result.Items))
	for _, item := range result.Items {
		var providerID *string
		if item.ProviderID != nil {
			value := httpx.FormatID(*item.ProviderID)
			providerID = &value
		}
		items = append(items, aiListItem{
			User:         view.UserBriefResponseFor(item.User),
			IsEnabled:    item.IsEnabled,
			ProviderID:   providerID,
			ProviderName: item.ProviderName,
			Model:        item.Model,
			TodayActions: item.TodayActions,
			CreatedAt:    httpx.FormatTime(item.CreatedAt),
		})
	}
	c.JSON(http.StatusOK, listResponse(items, result.LastID, result.HasMore))
}

// CreateAI 创建 AI 用户：资料、身份、预设、初始行为策略与审计同事务完成。
func (h *Handler) CreateAI(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	var in struct {
		Username         string           `json:"username"`
		Nickname         string           `json:"nickname"`
		Bio              string           `json:"bio"`
		Background       string           `json:"background"`
		Personality      string           `json:"personality"`
		CoreValues       string           `json:"core_values"`
		LanguageStyle    string           `json:"language_style"`
		Topics           string           `json:"topics"`
		TargetLanguage   string           `json:"target_language"`
		Taboos           string           `json:"taboos"`
		SystemPrompt     string           `json:"system_prompt"`
		ProviderID       string           `json:"provider_id"`
		ModelOverride    string           `json:"model_override"`
		GenerationParams json.RawMessage  `json:"generation_params"`
		Behavior         behaviorInputDTO `json:"behavior"`
		IsEnabled        *bool            `json:"is_enabled"`
	}
	if !h.decode(c, &in) {
		return
	}
	detail, err := h.service.CreateAI(c.Request.Context(), adminID, app.CreateAIInput{
		Identity: domain.CreateInput{
			Username:       in.Username,
			Nickname:       in.Nickname,
			Bio:            in.Bio,
			Background:     in.Background,
			Personality:    in.Personality,
			CoreValues:     in.CoreValues,
			LanguageStyle:  in.LanguageStyle,
			Topics:         in.Topics,
			TargetLanguage: in.TargetLanguage,
			Taboos:         in.Taboos,
			SystemPrompt:   in.SystemPrompt,
		},
		ProviderID:       in.ProviderID,
		ModelOverride:    in.ModelOverride,
		GenerationParams: in.GenerationParams,
		Behavior:         in.Behavior.patch(),
		IsEnabled:        in.IsEnabled,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ai": detailView(detail)})
}

// GetAI 返回 AI 详情；非 AI 用户 id 返回 404。
func (h *Handler) GetAI(c *gin.Context) {
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	detail, err := h.service.GetAI(c.Request.Context(), aiID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ai": detailView(detail)})
}

// UpdateAI 更新 AI 资料与身份字段（不含模型与行为策略）。
func (h *Handler) UpdateAI(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in struct {
		Username       *string `json:"username"`
		Nickname       *string `json:"nickname"`
		Bio            *string `json:"bio"`
		Background     *string `json:"background"`
		Personality    *string `json:"personality"`
		CoreValues     *string `json:"core_values"`
		LanguageStyle  *string `json:"language_style"`
		Topics         *string `json:"topics"`
		TargetLanguage *string `json:"target_language"`
		Taboos         *string `json:"taboos"`
		SystemPrompt   *string `json:"system_prompt"`
	}
	if !h.decode(c, &in) {
		return
	}
	detail, err := h.service.UpdateAI(c.Request.Context(), adminID, aiID, app.UpdateAIInput{
		Identity: domain.UpdateInput{
			Username:       in.Username,
			Nickname:       in.Nickname,
			Bio:            in.Bio,
			Background:     in.Background,
			Personality:    in.Personality,
			CoreValues:     in.CoreValues,
			LanguageStyle:  in.LanguageStyle,
			Topics:         in.Topics,
			TargetLanguage: in.TargetLanguage,
			Taboos:         in.Taboos,
			SystemPrompt:   in.SystemPrompt,
		},
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ai": detailView(detail)})
}

// UpdateAIModel 更新接口预设、模型覆盖、生成参数与请求超时。
func (h *Handler) UpdateAIModel(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in struct {
		ProviderID            *string         `json:"provider_id"`
		ModelOverride         *string         `json:"model_override"`
		GenerationParams      json.RawMessage `json:"generation_params"`
		RequestTimeoutSeconds *int            `json:"request_timeout_seconds"`
	}
	if !h.decode(c, &in) {
		return
	}
	result, err := h.service.UpdateAIModel(c.Request.Context(), adminID, aiID, app.UpdateAIModelInput{
		ProviderID:            in.ProviderID,
		ModelOverride:         in.ModelOverride,
		GenerationParams:      in.GenerationParams,
		RequestTimeoutSeconds: in.RequestTimeoutSeconds,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"model": modelView(result.Settings)})
}

// UpdateAIBehavior 更新行为开关、候选范围、AI 间互动、频率与冷却。
func (h *Handler) UpdateAIBehavior(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in behaviorInputDTO
	if !h.decode(c, &in) {
		return
	}
	settings, updatedAt, err := h.service.UpdateAIBehavior(c.Request.Context(), adminID, aiID, in.patch())
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"behavior": behaviorView(settings, updatedAt)})
}

// EnableAI 启用单个 AI。
func (h *Handler) EnableAI(c *gin.Context) {
	h.setAIEnabled(c, true)
}

// DisableAI 停用单个 AI：待执行的 AI 任务经 agent 端口取消。
func (h *Handler) DisableAI(c *gin.Context) {
	h.setAIEnabled(c, false)
}

// setAIEnabled 是启用/停用的共用实现。
func (h *Handler) setAIEnabled(c *gin.Context, enabled bool) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	if err := h.service.SetAIEnabled(c.Request.Context(), adminID, aiID, enabled); err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": httpx.FormatID(aiID), "is_enabled": enabled})
}
