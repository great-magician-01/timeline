package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// globalAIKey 是全局 AI 开关在 system_settings 中的键。
const globalAIKey = "ai.global_enabled"

// GlobalAIEnabled 读取全局 AI 自动行为开关；设置缺失时默认关闭，
// 避免未显式开启就产生自主行为（docs/REQUIREMENTS.md 12.3）。
func GlobalAIEnabled(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var enabled bool
	err := pool.QueryRow(ctx, `SELECT value FROM system_settings WHERE key = $1`, globalAIKey).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// Overview 是管理概览的数据形状（docs/PAGES_AND_PERMISSIONS.md A-01）。
type Overview struct {
	Users struct {
		Total     int64 `json:"total"`
		Suspended int64 `json:"suspended"`
		AI        int64 `json:"ai"`
	} `json:"users"`
	Content struct {
		Posts    int64 `json:"posts"`
		Comments int64 `json:"comments"`
		Drafts   int64 `json:"drafts"`
	} `json:"content"`
	AI struct {
		Enabled       int64 `json:"enabled"`
		Total         int64 `json:"total"`
		GlobalEnabled bool  `json:"global_enabled"`
	} `json:"ai"`
	Tasks struct {
		Pending int64 `json:"pending"`
		Failed  int64 `json:"failed"`
		Paused  int64 `json:"paused"`
	} `json:"tasks"`
}

// GetOverview 汇总账号、内容、AI 与后台任务的运行概况。
// 全部为聚合查询，不返回任何用户隐私字段。
func (s *Service) GetOverview(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var overview Overview

	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'suspended'),
		       COUNT(*) FILTER (WHERE is_ai)
		FROM users`,
	).Scan(&overview.Users.Total, &overview.Users.Suspended, &overview.Users.AI); err != nil {
		s.internalError(c, "统计账号概况失败", err)
		return
	}
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM posts WHERE status = 'published'),
			(SELECT COUNT(*) FROM comments WHERE status = 'published'),
			(SELECT COUNT(*) FROM posts WHERE status IN ('draft', 'scheduled'))`,
	).Scan(&overview.Content.Posts, &overview.Content.Comments, &overview.Content.Drafts); err != nil {
		s.internalError(c, "统计内容概况失败", err)
		return
	}
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE p.is_enabled), COUNT(*)
		FROM ai_profiles AS p
		JOIN users AS u ON u.id = p.user_id`,
	).Scan(&overview.AI.Enabled, &overview.AI.Total); err != nil {
		s.internalError(c, "统计 AI 概况失败", err)
		return
	}
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE status = 'pending'),
		       COUNT(*) FILTER (WHERE status = 'failed'),
		       COUNT(*) FILTER (WHERE status = 'paused')
		FROM background_jobs`,
	).Scan(&overview.Tasks.Pending, &overview.Tasks.Failed, &overview.Tasks.Paused); err != nil {
		s.internalError(c, "统计后台任务概况失败", err)
		return
	}
	globalEnabled, err := GlobalAIEnabled(ctx, pool)
	if err != nil {
		s.internalError(c, "读取全局 AI 开关失败", err)
		return
	}
	overview.AI.GlobalEnabled = globalEnabled

	c.JSON(http.StatusOK, overview)
}
