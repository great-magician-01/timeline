package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/content/app"
	"github.com/great-magician-01/timeline/internal/modules/content/domain"
)

// Timeline 返回全站时间线，按访问者权限过滤。
func (h *Handler) Timeline(c *gin.Context) {
	cursor, ok := timeCursor(c)
	if !ok {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := h.svc.Timeline(c.Request.Context(), currentViewer(c), c.Query("filter"), cursor, limit)
	if err != nil {
		h.fail(c, "读取时间线失败", err)
		return
	}
	h.respondTimeline(c, page)
}

// FollowingTimeline 返回关注时间线。
func (h *Handler) FollowingTimeline(c *gin.Context) {
	cursor, ok := timeCursor(c)
	if !ok {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := h.svc.FollowingTimeline(c.Request.Context(), currentViewer(c), c.Query("filter"), cursor, limit)
	if err != nil {
		h.fail(c, "读取关注时间线失败", err)
		return
	}
	h.respondTimeline(c, page)
}

// UserTimeline 返回个人主页时间线，支持 posts/replies/reposts 三种页签。
func (h *Handler) UserTimeline(c *gin.Context) {
	cursor, ok := timeCursor(c)
	if !ok {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := h.svc.UserTimeline(
		c.Request.Context(), currentViewer(c), c.Param("username"), c.Query("tab"), cursor, limit,
	)
	if err != nil {
		h.fail(c, "读取个人主页时间线失败", err)
		return
	}
	h.respondTimeline(c, page)
}

// respondTimeline 组装时间线类响应：回复页签输出 replies，其余输出 posts。
// 个人主页 posts 页签第一页额外返回 pinned_posts（按展示顺序的置顶帖，
// 普通流已排除），其余时间线不返回该字段。
func (h *Handler) respondTimeline(c *gin.Context, page app.TimelinePage) {
	if page.Kind == domain.TabReplies {
		c.JSON(http.StatusOK, timeListResponse(
			commentTimelineResponsesFor(page.Replies), page.Next, page.HasMore,
		))
		return
	}
	resp := timeListResponse(briefResponsesFor(page.Posts), page.Next, page.HasMore)
	if len(page.PinnedPosts) > 0 {
		resp["pinned_posts"] = briefResponsesFor(page.PinnedPosts)
	}
	c.JSON(http.StatusOK, resp)
}
