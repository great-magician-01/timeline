// Package repo 是 content 模块唯一的 SQL 层：帖子、草稿工作区、历史版本、标签、
// 搜索投影与时间线候选查询。所有写入时间由 app 从 clock 取时后显式传入，
// SQL 中不出现 now()/CURRENT_DATE/::date/interval 等数据库时间运算。
package repo

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// itoa 是占位符序号的数字格式化。
func itoa(value int) string { return strconv.Itoa(value) }

// aliasColumns 给逗号分隔的列清单统一加上表别名前缀。
func aliasColumns(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = alias + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}

// Repo 持有连接池提供者；每次查询通过 db.ExecutorFrom 自动加入 app 编排的事务。
type Repo struct {
	pools db.PoolProvider
}

// New 构造 content 仓储。pools 在数据库就绪后返回非 nil 连接池。
func New(pools db.PoolProvider) *Repo {
	return &Repo{pools: pools}
}

// ex 返回当前上下文对应的执行器（事务优先）。
func (r *Repo) ex(ctx context.Context) db.Executor {
	if r.pools == nil {
		return nil
	}
	return db.ExecutorFrom(ctx, r.pools.Pool())
}

// scanner 同时覆盖 pgx.Row 与 pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// postColumns 是 posts 的完整列清单（只在本包使用，不向业务层导出）。
const postColumns = `id, author_id, status, visibility, current_version_id, quoted_post_id,
	scheduled_at, published_at, deleted_at, like_count, comment_count, repost_count,
	created_at, updated_at, pinned_at, pinned_sort`

func scanPost(row scanner) (model.Post, error) {
	var post model.Post
	err := row.Scan(
		&post.ID, &post.AuthorID, &post.Status, &post.Visibility, &post.CurrentVersionID,
		&post.QuotedPostID, &post.ScheduledAt, &post.PublishedAt, &post.DeletedAt,
		&post.LikeCount, &post.CommentCount, &post.RepostCount, &post.CreatedAt, &post.UpdatedAt,
		&post.PinnedAt, &post.PinnedSort,
	)
	return post, err
}

// versionColumns 是 post_versions 的完整列清单。
const versionColumns = `id, post_id, version_number, content_format, content_source,
	content_html, plain_text, visibility, restored_from_version_id, created_by, created_at`

// postColumnsP 是带 posts 表别名 p. 的列清单，供多表 JOIN 查询使用。
var postColumnsP = aliasColumns("p.", postColumns)

func scanVersion(row scanner) (model.PostVersion, error) {
	var version model.PostVersion
	err := row.Scan(
		&version.ID, &version.PostID, &version.VersionNumber, &version.ContentFormat,
		&version.ContentSource, &version.ContentHTML, &version.PlainText, &version.Visibility,
		&version.RestoredFromVersionID, &version.CreatedBy, &version.CreatedAt,
	)
	return version, err
}

// notFoundIfNoRows 把 pgx.ErrNoRows 映射为领域 NotFound，避免业务层 import pgx。
func notFoundIfNoRows(err error, message string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound(message)
	}
	return err
}

// touchTime 统一处理可选应用时间：零值时使用 fallback，防止写出零值时间。
func touchTime(at, fallback time.Time) time.Time {
	if at.IsZero() {
		return fallback
	}
	return at
}
