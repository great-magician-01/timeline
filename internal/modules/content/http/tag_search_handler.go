package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SuggestTags 按前缀匹配标签（只统计访问者可见的已发布帖子）。
func (h *Handler) SuggestTags(c *gin.Context) {
	items, err := h.svc.SuggestTags(c.Request.Context(), currentViewer(c), c.Query("q"))
	if err != nil {
		h.fail(c, "读取标签补全失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": tagSuggestionResponsesFor(items)})
}

// TagTimeline 返回标签聚合时间线；标签不存在时返回空列表而不是 404。
func (h *Handler) TagTimeline(c *gin.Context) {
	cursor, ok := timeCursor(c)
	if !ok {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := h.svc.TagTimeline(c.Request.Context(), currentViewer(c), c.Param("name"), cursor, limit)
	if err != nil {
		h.fail(c, "读取标签时间线失败", err)
		return
	}
	h.respondTimeline(c, page)
}

// SearchPosts 搜索帖子（按访问者权限过滤）。
func (h *Handler) SearchPosts(c *gin.Context) {
	cursor, ok := timeCursor(c)
	if !ok {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := h.svc.SearchPosts(c.Request.Context(), currentViewer(c), c.Query("q"), cursor, limit)
	if err != nil {
		h.fail(c, "搜索帖子失败", err)
		return
	}
	h.respondTimeline(c, page)
}
