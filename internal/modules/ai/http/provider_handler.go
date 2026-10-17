package http

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/ai/app"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// ListProviders 返回预设列表：支持名称模糊搜索与启停状态过滤，游标倒序分页。
func (h *Handler) ListProviders(c *gin.Context) {
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
	result, err := h.service.ListProviders(c.Request.Context(), app.ProviderFilter{
		Query:  query,
		Status: status,
		Cursor: cursor,
		Limit:  limit,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	items := make([]providerListItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, providerListView(item))
	}
	c.JSON(http.StatusOK, listResponse(items, result.LastID, result.HasMore))
}

// CreateProvider 创建模型接口预设；密钥加密存储，审计与写入同事务。
func (h *Handler) CreateProvider(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	var in struct {
		Name         string `json:"name"`
		BaseURL      string `json:"base_url"`
		APIKey       string `json:"api_key"`
		DefaultModel string `json:"default_model"`
	}
	if !h.decode(c, &in) {
		return
	}
	item, err := h.service.CreateProvider(c.Request.Context(), adminID, app.CreateProviderInput{
		Name:         in.Name,
		BaseURL:      in.BaseURL,
		APIKey:       in.APIKey,
		DefaultModel: in.DefaultModel,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"provider": providerListView(item)})
}

// UpdateProvider 更新预设；api_key 缺省或为空表示不变更密钥。
func (h *Handler) UpdateProvider(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	providerID, ok := h.pathID(c, "id", "无效的模型接口预设 ID")
	if !ok {
		return
	}
	var in struct {
		Name         *string `json:"name"`
		BaseURL      *string `json:"base_url"`
		APIKey       *string `json:"api_key"`
		DefaultModel *string `json:"default_model"`
		IsEnabled    *bool   `json:"is_enabled"`
	}
	if !h.decode(c, &in) {
		return
	}
	item, err := h.service.UpdateProvider(c.Request.Context(), adminID, providerID, app.UpdateProviderInput{
		Name:         in.Name,
		BaseURL:      in.BaseURL,
		APIKey:       in.APIKey,
		DefaultModel: in.DefaultModel,
		IsEnabled:    in.IsEnabled,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"provider": providerListView(item)})
}

// DeleteProvider 逻辑删除预设；仍有关联 AI 时返回 409 duplicate。
func (h *Handler) DeleteProvider(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	providerID, ok := h.pathID(c, "id", "无效的模型接口预设 ID")
	if !ok {
		return
	}
	if err := h.service.DeleteProvider(c.Request.Context(), adminID, providerID); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// TestProvider 用 ListModels 做连接测试；无论成功失败 HTTP 都为 200，错误信息经脱敏。
func (h *Handler) TestProvider(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	providerID, ok := h.pathID(c, "id", "无效的模型接口预设 ID")
	if !ok {
		return
	}
	result, err := h.service.TestProvider(c.Request.Context(), adminID, providerID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	if !result.OK {
		c.JSON(http.StatusOK, gin.H{
			"ok":         false,
			"latency_ms": result.LatencyMS,
			"error":      result.Error,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"latency_ms": result.LatencyMS,
		"models":     result.Models,
	})
}
