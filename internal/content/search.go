package content

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/access"
)

// SearchPosts 按纯文本投影搜索已发布帖子；结果仍按访问者权限过滤。
func (s *Service) SearchPosts(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	keyword, err := normalizeSearchKeyword(c.Query("q"))
	if err != nil {
		s.respond(c, "校验搜索关键词失败", err)
		return
	}
	ctx := c.Request.Context()
	following, err := access.FollowingSet(ctx, pool, viewer.ID)
	if err != nil {
		s.internalError(c, "读取关注集合失败", err)
		return
	}
	// post_search 只保存已发布内容的纯文本，大小写不敏感匹配在 SQL 内完成。
	s.timelineList(c, pool, viewer, following, timelineParams{
		includePosts: true,
		searchText:   strings.ToLower(keyword),
		filter:       filterAll,
	})
}
