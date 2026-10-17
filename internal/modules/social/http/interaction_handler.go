package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/social/app"
)

// Repost 纯转发。
func (h *Handler) Repost(c *gin.Context) { h.setRepost(c, true) }

// Unrepost 取消纯转发。
func (h *Handler) Unrepost(c *gin.Context) { h.setRepost(c, false) }

func (h *Handler) setRepost(c *gin.Context, active bool) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var result app.RepostResult
	var err error
	if active {
		result, err = h.svc.Repost(c.Request.Context(), viewer, postID)
	} else {
		result, err = h.svc.Unrepost(c.Request.Context(), viewer, postID)
	}
	if err != nil {
		h.fail(c, "更新转发状态失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"reposted": result.Reposted, "repost_count": result.Count})
}

// LikePost 点赞帖子。
func (h *Handler) LikePost(c *gin.Context) { h.setPostLike(c, true) }

// UnlikePost 取消点赞帖子。
func (h *Handler) UnlikePost(c *gin.Context) { h.setPostLike(c, false) }

func (h *Handler) setPostLike(c *gin.Context, active bool) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	postID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var result app.LikeResult
	var err error
	if active {
		result, err = h.svc.LikePost(c.Request.Context(), viewer, postID)
	} else {
		result, err = h.svc.UnlikePost(c.Request.Context(), viewer, postID)
	}
	if err != nil {
		h.fail(c, "更新点赞状态失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"liked": result.Liked, "like_count": result.Count})
}

// LikeComment 点赞评论。
func (h *Handler) LikeComment(c *gin.Context) { h.setCommentLike(c, true) }

// UnlikeComment 取消点赞评论。
func (h *Handler) UnlikeComment(c *gin.Context) { h.setCommentLike(c, false) }

func (h *Handler) setCommentLike(c *gin.Context, active bool) {
	viewer, ok := h.requireViewer(c)
	if !ok {
		return
	}
	commentID, ok := idParam(c, "id")
	if !ok {
		return
	}
	var result app.LikeResult
	var err error
	if active {
		result, err = h.svc.LikeComment(c.Request.Context(), viewer, commentID)
	} else {
		result, err = h.svc.UnlikeComment(c.Request.Context(), viewer, commentID)
	}
	if err != nil {
		h.fail(c, "更新点赞状态失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"liked": result.Liked, "like_count": result.Count})
}
