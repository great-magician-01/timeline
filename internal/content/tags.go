package content

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/model"
)

// tagSuggestion 是标签自动补全项。
type tagSuggestion struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	PostCount   int    `json:"post_count"`
}

// TagTimeline 返回标签聚合：只包含当前访问者有权查看的已发布帖子。
// 标签不存在时返回空列表，而不是 404，避免暴露标签是否被非公开内容使用。
func (s *Service) TagTimeline(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	name := strings.ToLower(strings.TrimSpace(c.Param("name")))
	ctx := c.Request.Context()

	if name == "" || utf8.RuneCountInString(name) > maxTagRunes {
		c.JSON(http.StatusOK, timeListResponse([]PostBrief{}, time.Time{}, 0, false))
		return
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tags WHERE name = $1)`, name).Scan(&exists); err != nil {
		s.internalError(c, "读取标签失败", err)
		return
	}
	if !exists {
		c.JSON(http.StatusOK, timeListResponse([]PostBrief{}, time.Time{}, 0, false))
		return
	}
	following, err := access.FollowingSet(ctx, pool, viewer.ID)
	if err != nil {
		s.internalError(c, "读取关注集合失败", err)
		return
	}
	s.timelineList(c, pool, viewer, following, timelineParams{
		includePosts: true,
		tagName:      name,
		filter:       filterAll,
	})
}

// SuggestTags 按前缀匹配标签，且只统计当前访问者可见的已发布帖子。
func (s *Service) SuggestTags(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	keyword := strings.ToLower(strings.TrimSpace(c.Query("q")))
	items := []tagSuggestion{}
	if keyword == "" || utf8.RuneCountInString(keyword) > maxTagRunes {
		c.JSON(http.StatusOK, gin.H{"items": items})
		return
	}
	ctx := c.Request.Context()
	following, err := access.FollowingSet(ctx, pool, viewer.ID)
	if err != nil {
		s.internalError(c, "读取关注集合失败", err)
		return
	}
	rows, err := pool.Query(ctx, `
		SELECT tg.name, tg.display_name, COUNT(DISTINCT p.id) AS post_count
		FROM post_tags pt
		JOIN tags tg ON tg.id = pt.tag_id
		JOIN posts p ON p.id = pt.post_id
		WHERE pt.is_active AND p.status = 'published'
		  AND (p.visibility = 'public' OR p.author_id = $1
		       OR (p.visibility = 'followers' AND NOT $3::boolean AND p.author_id = ANY($2::bigint[])))
		  AND STRPOS(tg.name, $4) = 1
		GROUP BY tg.name, tg.display_name
		ORDER BY post_count DESC, tg.name
		LIMIT $5`, viewer.ID, sortedIDs(following), viewer.IsAI, keyword, maxSuggestCount)
	if err != nil {
		s.internalError(c, "读取标签补全失败", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var item tagSuggestion
		if err := rows.Scan(&item.Name, &item.DisplayName, &item.PostCount); err != nil {
			s.internalError(c, "读取标签补全失败", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "读取标签补全失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// tagPostFilter 供标签与搜索复用的可见性判断（保留在内容包内，便于单元测试覆盖）。
func tagPostFilter(viewer model.Viewer, post model.Post, following map[int64]bool) bool {
	return post.Status == model.PostStatusPublished &&
		access.CanViewPostWithFollowing(viewer, post, following)
}
