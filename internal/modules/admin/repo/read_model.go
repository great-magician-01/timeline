// Package repo 是 admin 编排模块的显式只读 read-model。
//
// 目标架构明确允许 admin 的 read-model 跨表 JOIN/聚合（审核列表、概览统计、审计列表、
// 任务列表、用户统计）：它只读、不写、不实现权限规则；可见性/权限判断由 app 层调用
// shared/access 完成，本包只接收 app 传入的过滤条件。
//
// SQL 约束：所有时间条件由 app 传入显式参数（例如活跃会话统计的 at），SQL 中不出现
// now()/CURRENT_DATE/::date/interval 等数据库时间函数；不执行任何写语句。
package repo

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ReadModel 是 admin 的只读查询入口；所有方法从 ctx 解析执行器（事务内复用事务）。
type ReadModel struct {
	pool *pgxpool.Pool
}

// NewReadModel 构造只读 read-model。
func NewReadModel(pool *pgxpool.Pool) *ReadModel {
	return &ReadModel{pool: pool}
}

func (r *ReadModel) executor(ctx context.Context) db.Executor {
	return db.ExecutorFrom(ctx, r.pool)
}

// ===== 行结构与分页参数 =====

// UserFilter 是用户列表的过滤条件；关键字由 app 预小写后传入。
type UserFilter struct {
	Status  string
	IsAI    *bool
	Keyword string
	Cursor  int64
	Limit   int
}

// PostFilter 是帖子审核列表的过滤条件。
type PostFilter struct {
	Visibility string
	Status     string
	Author     string
	Keyword    string // 纯文本搜索词（app 已小写）
	Cursor     int64
	Limit      int
}

// CommentFilter 是评论审核列表的过滤条件。
type CommentFilter struct {
	Author  string
	Keyword string
	Cursor  int64
	Limit   int
}

// AttachmentFilter 是附件审核列表的过滤条件。
type AttachmentFilter struct {
	UploaderID *int64
	PostID     *int64
	Purpose    string
	Status     string
	Keyword    string
	Cursor     int64
	Limit      int
}

// TaskFilter 是后台作业列表的过滤条件。
type TaskFilter struct {
	JobType string
	Status  string
	Cursor  int64
	Limit   int
}

// AuditFilter 是审计日志列表的过滤条件。
type AuditFilter struct {
	ActionType string
	Admin      string
	Cursor     int64
	Limit      int
}

// UserStats 是用户详情页的内容与关系统计（只计入未删除/仍有效的数据）。
type UserStats struct {
	Posts         int64
	Comments      int64
	Followers     int64
	Following     int64
	LikesGiven    int64
	LikesReceived int64
	Reposts       int64
}

// PostRow 是审核帖子列表/详情的基础行：帖子行 + 作者摘要 + 当前正文 + 附件数。
type PostRow struct {
	Post            model.Post
	Author          model.UserBrief
	ContentHTML     string
	AttachmentCount int
}

// VersionRow 是帖子历史版本行的审核投影。
type VersionRow struct {
	ID                    int64
	VersionNumber         int
	Visibility            string
	ContentHTML           string
	CreatedAt             time.Time
	RestoredFromVersionID *int64
}

// AttachmentRow 是附件审核列表行；purpose 只存在于 attachments 表，不在 shared/model 中。
type AttachmentRow struct {
	ID               int64
	Uploader         model.UserBrief
	PostID           *int64
	Kind             string
	Purpose          string
	OriginalFilename string
	MediaType        string
	SizeBytes        int64
	Width            *int
	Height           *int
	DurationMS       *int64
	Status           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// CommentRow 是评论审核列表/帖子评论上下文的行。
type CommentRow struct {
	ID          int64
	PostID      int64
	Author      model.UserBrief
	ContentHTML string
	Status      string
	CreatedAt   time.Time
}

// TaskRow 是后台作业列表行（时间字段均为数据库已存值，不在 SQL 内计算）。
type TaskRow struct {
	ID          int64
	JobType     string
	RefType     string
	RefID       *int64
	Status      string
	RunAt       time.Time
	Attempts    int
	MaxAttempts int
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AuditRow 是审计日志列表行（含操作管理员摘要）。
type AuditRow struct {
	ID         int64
	AdminID    int64
	Username   string
	Nickname   string
	ActionType string
	TargetType string
	TargetID   *int64
	Detail     []byte
	CreatedAt  time.Time
}

// OverviewCounts 是管理概览的聚合结果。
type OverviewCounts struct {
	UsersTotal          int64
	UsersSuspended      int64
	UsersAI             int64
	PostsPublished      int64
	CommentsPublished   int64
	PostsDraftScheduled int64
	AIEnabled           int64
	AITotal             int64
	JobsPending         int64
	JobsFailed          int64
	JobsPaused          int64
}

// ===== 动态过滤构建 =====

// filterBuilder 以静态片段 + $n 占位符构建 WHERE 条件。
type filterBuilder struct {
	conds []string
	args  []any
}

func (b *filterBuilder) add(value any) string {
	b.args = append(b.args, value)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *filterBuilder) where() string {
	if len(b.conds) == 0 {
		return "TRUE"
	}
	return strings.Join(b.conds, " AND ")
}

// ===== 账号 =====

const userColumns = `id, username, COALESCE(email, ''), COALESCE(password_hash, ''),
	nickname, avatar_url, bio, role, is_super_admin, is_ai, status,
	require_follow_approval, token_version, suspended_at, created_at, updated_at`

func scanUser(row pgx.Row) (model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Nickname, &u.AvatarURL,
		&u.Bio, &u.Role, &u.IsSuperAdmin, &u.IsAI, &u.Status, &u.RequireFollowApproval,
		&u.TokenVersion, &u.SuspendedAt, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// GetUser 读取管理端可见的完整账号行；不存在返回领域 NotFound。
func (r *ReadModel) GetUser(ctx context.Context, userID int64) (model.User, error) {
	user, err := scanUser(r.executor(ctx).QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return user, err
}

// ListUsers 按过滤条件分页读取账号行（按 id 倒序）；返回最多 limit+1 条供上层判断 has_more。
func (r *ReadModel) ListUsers(ctx context.Context, f UserFilter) ([]model.User, error) {
	b := &filterBuilder{}
	if f.Status != "" {
		b.conds = append(b.conds, "status = "+b.add(f.Status))
	}
	if f.IsAI != nil {
		b.conds = append(b.conds, "is_ai = "+b.add(*f.IsAI))
	}
	if f.Keyword != "" {
		placeholder := b.add(f.Keyword)
		b.conds = append(b.conds, "(STRPOS(LOWER(username), "+placeholder+") > 0 OR "+
			"STRPOS(LOWER(nickname), "+placeholder+") > 0 OR "+
			"STRPOS(LOWER(COALESCE(email, '')), "+placeholder+") > 0)")
	}
	if f.Cursor > 0 {
		b.conds = append(b.conds, "id < "+b.add(f.Cursor))
	}
	limitPlaceholder := b.add(f.Limit + 1)
	rows, err := r.executor(ctx).Query(ctx,
		`SELECT `+userColumns+` FROM users WHERE `+b.where()+
			` ORDER BY id DESC LIMIT `+limitPlaceholder, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]model.User, 0, f.Limit+1)
	for rows.Next() {
		u, scanErr := scanUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CountActiveSessions 统计指定时刻仍然有效的会话数；at 由 app 用 clock 传入。
func (r *ReadModel) CountActiveSessions(ctx context.Context, userID int64, at time.Time) (int64, error) {
	var count int64
	err := r.executor(ctx).QueryRow(ctx, `
		SELECT COUNT(*) FROM auth_sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2`, userID, at).Scan(&count)
	return count, err
}

// userStatsRestSelect 是用户内容与关系统计中与帖子计数口径无关的 6 项只读计数
// （评论、粉丝、关注、点赞送出/收到、转发）；供管理后台与个人主页两套口径复用。
const userStatsRestSelect = `
	(SELECT COUNT(*) FROM comments WHERE author_id = $1 AND status = 'published'),
	(SELECT COUNT(*) FROM follows WHERE followee_id = $1 AND is_active),
	(SELECT COUNT(*) FROM follows WHERE follower_id = $1 AND is_active),
	(SELECT COUNT(*) FROM likes WHERE user_id = $1 AND is_active),
	(SELECT COUNT(*) FROM likes l WHERE l.is_active AND (
		(l.target_type = 'post' AND EXISTS (
			SELECT 1 FROM posts p WHERE p.id = l.target_id AND p.author_id = $1
		))
		OR (l.target_type = 'comment' AND EXISTS (
			SELECT 1 FROM comments c WHERE c.id = l.target_id AND c.author_id = $1
		))
	)),
	(SELECT COUNT(*) FROM reposts WHERE user_id = $1 AND is_active)`

// 两套用户统计查询只差帖子计数口径：管理后台计「未删除」，公开个人主页只计「已发布」。
const (
	sqlUserStats = `SELECT
			(SELECT COUNT(*) FROM posts WHERE author_id = $1 AND status <> 'deleted'),` +
		userStatsRestSelect
	sqlUserProfileStats = `SELECT
			(SELECT COUNT(*) FROM posts WHERE author_id = $1 AND status = 'published'),` +
		userStatsRestSelect
)

// UserStats 统计管理后台用户详情页的内容与关系；帖子按「未删除」口径统计。
func (r *ReadModel) UserStats(ctx context.Context, userID int64) (UserStats, error) {
	var stats UserStats
	err := r.executor(ctx).QueryRow(ctx, sqlUserStats, userID).
		Scan(&stats.Posts, &stats.Comments, &stats.Followers, &stats.Following,
			&stats.LikesGiven, &stats.LikesReceived, &stats.Reposts)
	return stats, err
}

// UserProfileStats 统计公开个人主页的计数：帖子只计已发布（个人主页 post_count 契约），
// 其余评论/关注/点赞/转发口径与 UserStats 一致。供组合根的个人主页只读适配器使用。
func (r *ReadModel) UserProfileStats(ctx context.Context, userID int64) (UserStats, error) {
	var stats UserStats
	err := r.executor(ctx).QueryRow(ctx, sqlUserProfileStats, userID).
		Scan(&stats.Posts, &stats.Comments, &stats.Followers, &stats.Following,
			&stats.LikesGiven, &stats.LikesReceived, &stats.Reposts)
	return stats, err
}

// ===== 概览 =====

// Overview 汇总账号、内容、AI 与后台任务概况；所有语句均为只读聚合。
func (r *ReadModel) Overview(ctx context.Context) (OverviewCounts, error) {
	var counts OverviewCounts
	exec := r.executor(ctx)
	if err := exec.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'suspended'),
		       COUNT(*) FILTER (WHERE is_ai)
		FROM users`,
	).Scan(&counts.UsersTotal, &counts.UsersSuspended, &counts.UsersAI); err != nil {
		return counts, err
	}
	if err := exec.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM posts WHERE status = 'published'),
			(SELECT COUNT(*) FROM comments WHERE status = 'published'),
			(SELECT COUNT(*) FROM posts WHERE status IN ('draft', 'scheduled'))`,
	).Scan(&counts.PostsPublished, &counts.CommentsPublished, &counts.PostsDraftScheduled); err != nil {
		return counts, err
	}
	if err := exec.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE p.is_enabled), COUNT(*)
		FROM ai_profiles AS p
		JOIN users AS u ON u.id = p.user_id`,
	).Scan(&counts.AIEnabled, &counts.AITotal); err != nil {
		return counts, err
	}
	if err := exec.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE status = 'pending'),
		       COUNT(*) FILTER (WHERE status = 'failed'),
		       COUNT(*) FILTER (WHERE status = 'paused')
		FROM background_jobs`,
	).Scan(&counts.JobsPending, &counts.JobsFailed, &counts.JobsPaused); err != nil {
		return counts, err
	}
	return counts, nil
}

// ===== 帖子审核 =====

const postSelect = `SELECT p.id, p.author_id, p.status, p.visibility, p.current_version_id,
	p.quoted_post_id, p.scheduled_at, p.published_at, p.deleted_at,
	p.like_count, p.comment_count, p.repost_count, p.created_at, p.updated_at,
	COALESCE(v.content_html, ''),
	u.username, u.nickname, u.avatar_url, u.is_ai, u.status,
	(SELECT COUNT(*) FROM attachments AS a
	 WHERE a.post_id = p.id AND a.status = 'available')
	FROM posts AS p
	JOIN users AS u ON u.id = p.author_id
	LEFT JOIN post_versions AS v ON v.id = p.current_version_id`

func scanPostRow(row pgx.Row) (PostRow, error) {
	var item PostRow
	var authorStatus string
	err := row.Scan(
		&item.Post.ID, &item.Post.AuthorID, &item.Post.Status, &item.Post.Visibility,
		&item.Post.CurrentVersionID, &item.Post.QuotedPostID, &item.Post.ScheduledAt,
		&item.Post.PublishedAt, &item.Post.DeletedAt, &item.Post.LikeCount,
		&item.Post.CommentCount, &item.Post.RepostCount, &item.Post.CreatedAt, &item.Post.UpdatedAt,
		&item.ContentHTML,
		&item.Author.Username, &item.Author.Nickname, &item.Author.AvatarURL,
		&item.Author.IsAI, &authorStatus, &item.AttachmentCount,
	)
	if err != nil {
		return item, err
	}
	item.Author.ID = item.Post.AuthorID
	item.Author.IsSuspended = authorStatus != model.StatusActive
	return item, nil
}

// ListPosts 分页读取全部可见性级别的帖子（可按可见性/状态/作者/纯文本过滤）。
func (r *ReadModel) ListPosts(ctx context.Context, f PostFilter) ([]PostRow, error) {
	b := &filterBuilder{}
	searchJoin := ""
	if f.Visibility != "" {
		b.conds = append(b.conds, "p.visibility = "+b.add(f.Visibility))
	}
	if f.Status != "" {
		b.conds = append(b.conds, "p.status = "+b.add(f.Status))
	}
	if f.Author != "" {
		b.conds = append(b.conds, "LOWER(u.username) = LOWER("+b.add(f.Author)+")")
	}
	if f.Keyword != "" {
		searchJoin = " LEFT JOIN post_search AS ps ON ps.post_id = p.id"
		b.conds = append(b.conds,
			"STRPOS(LOWER(COALESCE(ps.plain_text, '')), "+b.add(f.Keyword)+") > 0")
	}
	if f.Cursor > 0 {
		b.conds = append(b.conds, "p.id < "+b.add(f.Cursor))
	}
	limitPlaceholder := b.add(f.Limit + 1)
	query := postSelect + searchJoin + " WHERE " + b.where() +
		" ORDER BY p.id DESC LIMIT " + limitPlaceholder
	rows, err := r.executor(ctx).Query(ctx, query, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]PostRow, 0, f.Limit+1)
	for rows.Next() {
		item, scanErr := scanPostRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetPost 读取单条帖子审核行；不存在返回领域 NotFound。
func (r *ReadModel) GetPost(ctx context.Context, postID int64) (PostRow, error) {
	item, err := scanPostRow(r.executor(ctx).QueryRow(ctx, postSelect+" WHERE p.id = $1", postID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PostRow{}, apperr.NotFound("帖子不存在")
	}
	return item, err
}

// PostContentHTML 读取审核详情正文：已发布取当前版本，未发布回退草稿工作区。
func (r *ReadModel) PostContentHTML(ctx context.Context, post model.Post) (string, error) {
	exec := r.executor(ctx)
	if post.CurrentVersionID != nil {
		var html string
		err := exec.QueryRow(ctx,
			`SELECT content_html FROM post_versions WHERE id = $1`, *post.CurrentVersionID).Scan(&html)
		if err == nil {
			return html, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	if post.Status != model.PostStatusDraft && post.Status != model.PostStatusScheduled {
		return "", nil
	}
	var html string
	err := exec.QueryRow(ctx,
		`SELECT content_html FROM post_drafts WHERE post_id = $1`, post.ID).Scan(&html)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return html, err
}

// PostVersions 读取帖子的全部历史版本（按版本号倒序）。
func (r *ReadModel) PostVersions(ctx context.Context, postID int64) ([]VersionRow, error) {
	rows, err := r.executor(ctx).Query(ctx, `
		SELECT id, version_number, visibility, created_at, restored_from_version_id, content_html
		FROM post_versions WHERE post_id = $1 ORDER BY version_number DESC`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]VersionRow, 0)
	for rows.Next() {
		var item VersionRow
		if err := rows.Scan(&item.ID, &item.VersionNumber, &item.Visibility, &item.CreatedAt,
			&item.RestoredFromVersionID, &item.ContentHTML); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// PostAttachments 读取帖子关联的全部附件（含逻辑失效记录，供审核）。
func (r *ReadModel) PostAttachments(ctx context.Context, postID int64) ([]AttachmentRow, error) {
	rows, err := r.executor(ctx).Query(ctx, `
		SELECT a.id, a.uploader_id, a.post_id, a.kind, a.purpose, a.original_filename, a.media_type,
		       a.size_bytes, a.width, a.height, a.duration_ms, a.status, a.created_at, a.updated_at,
		       u.username, u.nickname, u.avatar_url, u.is_ai, u.status
		FROM attachments AS a
		JOIN users AS u ON u.id = a.uploader_id
		WHERE a.post_id = $1 ORDER BY a.id`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AttachmentRow, 0)
	for rows.Next() {
		var item AttachmentRow
		var uploaderID int64
		var uploaderStatus string
		if err := rows.Scan(&item.ID, &uploaderID, &item.PostID, &item.Kind, &item.Purpose,
			&item.OriginalFilename, &item.MediaType, &item.SizeBytes, &item.Width, &item.Height,
			&item.DurationMS, &item.Status, &item.CreatedAt, &item.UpdatedAt,
			&item.Uploader.Username, &item.Uploader.Nickname, &item.Uploader.AvatarURL,
			&item.Uploader.IsAI, &uploaderStatus); err != nil {
			return nil, err
		}
		item.Uploader.ID = uploaderID
		item.Uploader.IsSuspended = uploaderStatus != model.StatusActive
		items = append(items, item)
	}
	return items, rows.Err()
}

// PostCommentContext 返回帖子顶层评论（最多 limit 条）与已发布评论总数。
func (r *ReadModel) PostCommentContext(ctx context.Context, postID int64, limit int) ([]CommentRow, int64, error) {
	exec := r.executor(ctx)
	var total int64
	if err := exec.QueryRow(ctx, `
		SELECT COUNT(*) FROM comments WHERE post_id = $1 AND status = 'published'`,
		postID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := exec.Query(ctx, `
		SELECT c.id, c.post_id, c.content_html, c.status, c.created_at,
		       u.id, u.username, u.nickname, u.avatar_url, u.is_ai, u.status
		FROM comments AS c
		JOIN users AS u ON u.id = c.author_id
		WHERE c.post_id = $1 AND c.parent_comment_id IS NULL
		ORDER BY c.id ASC
		LIMIT $2`, postID, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]CommentRow, 0, limit)
	for rows.Next() {
		var item CommentRow
		var authorStatus string
		if err := rows.Scan(&item.ID, &item.PostID, &item.ContentHTML, &item.Status, &item.CreatedAt,
			&item.Author.ID, &item.Author.Username, &item.Author.Nickname, &item.Author.AvatarURL,
			&item.Author.IsAI, &authorStatus); err != nil {
			return nil, 0, err
		}
		item.Author.IsSuspended = authorStatus != model.StatusActive
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// ===== 评论审核 =====

// ListComments 分页读取评论审核列表（可按作者与纯文本过滤）。
func (r *ReadModel) ListComments(ctx context.Context, f CommentFilter) ([]CommentRow, error) {
	b := &filterBuilder{}
	if f.Author != "" {
		b.conds = append(b.conds, "LOWER(u.username) = LOWER("+b.add(f.Author)+")")
	}
	if f.Keyword != "" {
		b.conds = append(b.conds, "STRPOS(LOWER(c.plain_text), "+b.add(f.Keyword)+") > 0")
	}
	if f.Cursor > 0 {
		b.conds = append(b.conds, "c.id < "+b.add(f.Cursor))
	}
	limitPlaceholder := b.add(f.Limit + 1)
	rows, err := r.executor(ctx).Query(ctx, `
		SELECT c.id, c.post_id, c.content_html, c.status, c.created_at,
		       u.id, u.username, u.nickname, u.avatar_url, u.is_ai, u.status
		FROM comments AS c
		JOIN users AS u ON u.id = c.author_id
		WHERE `+b.where()+`
		ORDER BY c.id DESC LIMIT `+limitPlaceholder, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]CommentRow, 0, f.Limit+1)
	for rows.Next() {
		var item CommentRow
		var authorStatus string
		if err := rows.Scan(&item.ID, &item.PostID, &item.ContentHTML, &item.Status, &item.CreatedAt,
			&item.Author.ID, &item.Author.Username, &item.Author.Nickname, &item.Author.AvatarURL,
			&item.Author.IsAI, &authorStatus); err != nil {
			return nil, err
		}
		item.Author.IsSuspended = authorStatus != model.StatusActive
		items = append(items, item)
	}
	return items, rows.Err()
}

// ===== 附件审核 =====

const attachmentAdminSelect = `
	SELECT a.id, a.uploader_id, a.post_id, a.kind, a.purpose, a.original_filename, a.media_type,
	       a.size_bytes, a.width, a.height, a.duration_ms, a.status, a.created_at, a.updated_at,
	       u.username, u.nickname, u.avatar_url, u.is_ai, u.status
	FROM attachments AS a
	JOIN users AS u ON u.id = a.uploader_id`

func scanAttachmentAdminRow(row pgx.Row) (AttachmentRow, error) {
	var item AttachmentRow
	var uploaderID int64
	var uploaderStatus string
	err := row.Scan(&item.ID, &uploaderID, &item.PostID, &item.Kind, &item.Purpose,
		&item.OriginalFilename, &item.MediaType, &item.SizeBytes, &item.Width, &item.Height,
		&item.DurationMS, &item.Status, &item.CreatedAt, &item.UpdatedAt,
		&item.Uploader.Username, &item.Uploader.Nickname, &item.Uploader.AvatarURL,
		&item.Uploader.IsAI, &uploaderStatus)
	item.Uploader.ID = uploaderID
	item.Uploader.IsSuspended = uploaderStatus != model.StatusActive
	return item, err
}

// ListAttachments 分页读取附件审核列表（上传者/帖子/用途/状态/文件名过滤）。
func (r *ReadModel) ListAttachments(ctx context.Context, f AttachmentFilter) ([]AttachmentRow, error) {
	b := &filterBuilder{}
	if f.UploaderID != nil {
		b.conds = append(b.conds, "a.uploader_id = "+b.add(*f.UploaderID))
	}
	if f.PostID != nil {
		b.conds = append(b.conds, "a.post_id = "+b.add(*f.PostID))
	}
	if f.Purpose != "" {
		b.conds = append(b.conds, "a.purpose = "+b.add(f.Purpose))
	}
	if f.Status != "" {
		b.conds = append(b.conds, "a.status = "+b.add(f.Status))
	}
	if f.Keyword != "" {
		b.conds = append(b.conds, "STRPOS(LOWER(a.original_filename), "+b.add(f.Keyword)+") > 0")
	}
	if f.Cursor > 0 {
		b.conds = append(b.conds, "a.id < "+b.add(f.Cursor))
	}
	limitPlaceholder := b.add(f.Limit + 1)
	rows, err := r.executor(ctx).Query(ctx, attachmentAdminSelect+
		" WHERE "+b.where()+" ORDER BY a.id DESC LIMIT "+limitPlaceholder, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AttachmentRow, 0, f.Limit+1)
	for rows.Next() {
		item, scanErr := scanAttachmentAdminRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ===== 后台作业 =====

const taskSelect = `SELECT id, job_type, COALESCE(ref_type, ''), ref_id, status, run_at,
	attempts, max_attempts, last_error, created_at, updated_at FROM background_jobs`

func scanTaskRow(row pgx.Row) (TaskRow, error) {
	var item TaskRow
	err := row.Scan(&item.ID, &item.JobType, &item.RefType, &item.RefID, &item.Status,
		&item.RunAt, &item.Attempts, &item.MaxAttempts, &item.LastError,
		&item.CreatedAt, &item.UpdatedAt)
	return item, err
}

// ListTasks 分页读取后台作业列表（类型/状态过滤）。
func (r *ReadModel) ListTasks(ctx context.Context, f TaskFilter) ([]TaskRow, error) {
	b := &filterBuilder{}
	if f.JobType != "" {
		b.conds = append(b.conds, "job_type = "+b.add(f.JobType))
	}
	if f.Status != "" {
		b.conds = append(b.conds, "status = "+b.add(f.Status))
	}
	if f.Cursor > 0 {
		b.conds = append(b.conds, "id < "+b.add(f.Cursor))
	}
	limitPlaceholder := b.add(f.Limit + 1)
	rows, err := r.executor(ctx).Query(ctx, taskSelect+
		" WHERE "+b.where()+" ORDER BY id DESC LIMIT "+limitPlaceholder, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TaskRow, 0, f.Limit+1)
	for rows.Next() {
		item, scanErr := scanTaskRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ===== 审计日志 =====

// ListAuditLogs 分页读取管理审计日志（动作类型/管理员过滤）。
func (r *ReadModel) ListAuditLogs(ctx context.Context, f AuditFilter) ([]AuditRow, error) {
	b := &filterBuilder{}
	if f.ActionType != "" {
		b.conds = append(b.conds, "l.action_type = "+b.add(f.ActionType))
	}
	if f.Admin != "" {
		b.conds = append(b.conds, "LOWER(u.username) = LOWER("+b.add(f.Admin)+")")
	}
	if f.Cursor > 0 {
		b.conds = append(b.conds, "l.id < "+b.add(f.Cursor))
	}
	limitPlaceholder := b.add(f.Limit + 1)
	rows, err := r.executor(ctx).Query(ctx, `
		SELECT l.id, l.admin_id, u.username, u.nickname, l.action_type, l.target_type,
		       l.target_id, l.detail, l.created_at
		FROM admin_audit_logs AS l
		JOIN users AS u ON u.id = l.admin_id
		WHERE `+b.where()+`
		ORDER BY l.id DESC LIMIT `+limitPlaceholder, b.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AuditRow, 0, f.Limit+1)
	for rows.Next() {
		var item AuditRow
		if err := rows.Scan(&item.ID, &item.AdminID, &item.Username, &item.Nickname,
			&item.ActionType, &item.TargetType, &item.TargetID, &item.Detail,
			&item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
