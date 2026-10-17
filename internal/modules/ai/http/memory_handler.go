package http

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/ai/app"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// ListMemories 返回某个 AI 的记忆列表：type 必填，status 默认 active。
func (h *Handler) ListMemories(c *gin.Context) {
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	limit, cursor, ok := h.pageParams(c)
	if !ok {
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	if utf8.RuneCountInString(query) > 100 {
		h.writeError(c, apperr.Invalid("搜索关键词过长"))
		return
	}
	result, err := h.service.ListMemories(c.Request.Context(), aiID, app.MemoryFilter{
		MemoryType: strings.TrimSpace(c.Query("type")),
		Status:     strings.TrimSpace(c.Query("status")),
		Query:      query,
		Cursor:     cursor,
		Limit:      limit,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	items := make([]memoryResponse, 0, len(result.Items))
	for _, record := range result.Items {
		items = append(items, memoryView(record))
	}
	c.JSON(http.StatusOK, listResponse(items, result.LastID, result.HasMore))
}

// CreateMemory 手工添加记忆（含分类、关键词、重要性）。
func (h *Handler) CreateMemory(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in struct {
		MemoryType     string   `json:"memory_type"`
		Content        string   `json:"content"`
		Category       string   `json:"category"`
		Keywords       []string `json:"keywords"`
		Importance     *int     `json:"importance"`
		InferenceLevel string   `json:"inference_level"`
	}
	if !h.decode(c, &in) {
		return
	}
	record, err := h.service.CreateAIMemory(c.Request.Context(), adminID, aiID, app.CreateMemoryRequest{
		MemoryType:     in.MemoryType,
		Content:        in.Content,
		Category:       in.Category,
		Keywords:       in.Keywords,
		Importance:     in.Importance,
		InferenceLevel: in.InferenceLevel,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"memory": memoryView(record)})
}

// UpdateMemory 编辑记忆正文、分类、标签、重要性与状态。
func (h *Handler) UpdateMemory(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	memoryID, ok := h.pathID(c, "memory_id", "无效的记忆 ID")
	if !ok {
		return
	}
	var in struct {
		Content    *string   `json:"content"`
		Category   *string   `json:"category"`
		Keywords   *[]string `json:"keywords"`
		Importance *int      `json:"importance"`
		Status     *string   `json:"status"`
	}
	if !h.decode(c, &in) {
		return
	}
	record, err := h.service.UpdateMemory(c.Request.Context(), adminID, aiID, memoryID,
		app.UpdateMemoryRequest{
			Content:    in.Content,
			Category:   in.Category,
			Keywords:   in.Keywords,
			Importance: in.Importance,
			Status:     in.Status,
		})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"memory": memoryView(record)})
}

// DeleteMemory 逻辑删除单条记忆。
func (h *Handler) DeleteMemory(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	memoryID, ok := h.pathID(c, "memory_id", "无效的记忆 ID")
	if !ok {
		return
	}
	if err := h.service.DeleteMemory(c.Request.Context(), adminID, aiID, memoryID); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ClearMemories 清空某类记忆：type=recent|long_term，两类互不影响。
func (h *Handler) ClearMemories(c *gin.Context) {
	adminID, ok := h.adminID(c)
	if !ok {
		return
	}
	aiID, ok := h.pathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in struct {
		Type string `json:"type"`
	}
	if !h.decode(c, &in) {
		return
	}
	updated, err := h.service.ClearMemories(c.Request.Context(), adminID, aiID, in.Type)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": updated})
}
