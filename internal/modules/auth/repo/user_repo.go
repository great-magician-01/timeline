// Package repo 实现 auth 模块唯一的数据访问层：users 与 auth_sessions 的全部 SQL。
// 只依赖 platform/db 的 Executor 原语，自动加入 app 编排的事务；所有时间列由调用方
// 显式传入，SQL 不依赖数据库 now()。
package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// userColumns 是 users 表的完整行投影，字段顺序与 scanUser 一致。
const userColumns = `id, username, COALESCE(email, ''), COALESCE(password_hash, ''),
	nickname, avatar_url, bio, role, is_super_admin, is_ai, status, require_follow_approval,
	token_version, suspended_at, created_at, updated_at`

// rowScanner 抽象 pgx.Row 与 pgx.Rows 的 Scan，便于复用同一列投影。
type rowScanner interface {
	Scan(dest ...any) error
}

// UserRepo 是 users 表的仓储实现。
type UserRepo struct {
	pool *pgxpool.Pool
}

// NewUserRepo 构造 users 仓储；pool 在数据库就绪后由组合根注入。
func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

func (r *UserRepo) exec(ctx context.Context) db.Executor {
	return db.ExecutorFrom(ctx, r.pool)
}

// Insert 插入一个账号行（人类或 AI），时间列使用传入值。
func (r *UserRepo) Insert(ctx context.Context, u model.User) error {
	_, err := r.exec(ctx).Exec(ctx, `
		INSERT INTO users (
			id, username, email, password_hash, nickname, avatar_url, bio,
			role, is_super_admin, is_ai, status, require_follow_approval,
			token_version, suspended_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		u.ID, u.Username, nullableText(u.Email), nullableText(u.PasswordHash), u.Nickname,
		u.AvatarURL, u.Bio, u.Role, u.IsSuperAdmin, u.IsAI, u.Status, u.RequireFollowApproval,
		u.TokenVersion, u.SuspendedAt, u.CreatedAt, u.UpdatedAt,
	)
	return mapUserWriteError(err)
}

// ByID 按 ID 读取账号；不存在返回领域 NotFound。
func (r *UserRepo) ByID(ctx context.Context, id int64) (model.User, error) {
	return r.queryUser(ctx, r.exec(ctx).QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// ByIDForUpdate 在调用方事务内按 ID 读取账号并加行锁。
func (r *UserRepo) ByIDForUpdate(ctx context.Context, id int64) (model.User, error) {
	return r.queryUser(ctx, r.exec(ctx).QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1 FOR UPDATE`, id))
}

// ByUsername 按用户名（大小写不敏感）读取账号。
func (r *UserRepo) ByUsername(ctx context.Context, username string) (model.User, error) {
	return r.queryUser(ctx, r.exec(ctx).QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE LOWER(username) = LOWER($1)`, username))
}

// ByLoginIdentifier 按用户名或邮箱（大小写不敏感）读取账号。
func (r *UserRepo) ByLoginIdentifier(ctx context.Context, identifier string) (model.User, error) {
	return r.queryUser(ctx, r.exec(ctx).QueryRow(ctx,
		`SELECT `+userColumns+` FROM users
		 WHERE LOWER(username) = LOWER($1) OR LOWER(COALESCE(email, '')) = LOWER($1)`, identifier))
}

// UpdateProfile 按字段更新资料；nil 指针表示不修改。返回更新后的账号行。
func (r *UserRepo) UpdateProfile(
	ctx context.Context, id int64, nickname, bio, avatarURL *string, at time.Time,
) (model.User, error) {
	return r.queryUser(ctx, r.exec(ctx).QueryRow(ctx, `
		UPDATE users
		SET nickname = COALESCE($2::text, nickname),
		    bio = COALESCE($3::text, bio),
		    avatar_url = COALESCE($4::text, avatar_url),
		    updated_at = $5
		WHERE id = $1
		RETURNING `+userColumns, id, nickname, bio, avatarURL, at))
}

// UpdateProfileFields 按字段更新用户名/昵称/简介（nil 表示不修改），用于 AI 身份与
// 账号资料维护；唯一约束冲突返回领域 Conflict。返回更新后的账号行。
func (r *UserRepo) UpdateProfileFields(
	ctx context.Context, id int64, username, nickname, bio *string, at time.Time,
) (model.User, error) {
	u, err := r.queryUser(ctx, r.exec(ctx).QueryRow(ctx, `
		UPDATE users
		SET username = COALESCE($2::text, username),
		    nickname = COALESCE($3::text, nickname),
		    bio = COALESCE($4::text, bio),
		    updated_at = $5
		WHERE id = $1
		RETURNING `+userColumns, id, username, nickname, bio, at))
	if err != nil {
		return model.User{}, mapUserWriteError(err)
	}
	return u, nil
}

// UpdateFollowApproval 更新关注审批开关并返回最新值；AI 账号不可修改。
func (r *UserRepo) UpdateFollowApproval(ctx context.Context, id int64, enabled bool, at time.Time) (bool, error) {
	var updated bool
	err := r.exec(ctx).QueryRow(ctx, `
		UPDATE users SET require_follow_approval = $2, updated_at = $3
		WHERE id = $1 AND NOT is_ai
		RETURNING require_follow_approval`, id, enabled, at).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, apperr.NotFound("用户不存在")
	}
	return updated, err
}

// UpdateRole 更新角色并返回更新后的账号行（审计由 app 在同一事务内写入）。
func (r *UserRepo) UpdateRole(ctx context.Context, id int64, role string, at time.Time) (model.User, error) {
	return r.queryUser(ctx, r.exec(ctx).QueryRow(ctx,
		`UPDATE users SET role = $2, updated_at = $3 WHERE id = $1 RETURNING `+userColumns,
		id, role, at))
}

// UpdateStatus 更新账号状态与 token_version；suspendedAt 为空表示恢复启用。
func (r *UserRepo) UpdateStatus(
	ctx context.Context, id int64, status string, suspendedAt *time.Time, tokenVersion int64, at time.Time,
) (model.User, error) {
	return r.queryUser(ctx, r.exec(ctx).QueryRow(ctx, `
		UPDATE users
		SET status = $2, suspended_at = $3, token_version = $4, updated_at = $5
		WHERE id = $1
		RETURNING `+userColumns, id, status, suspendedAt, tokenVersion, at))
}

// UpdatePassword 更新密码哈希与 token_version（会话失效由调用方在同一事务内处理）。
func (r *UserRepo) UpdatePassword(ctx context.Context, id int64, passwordHash string, tokenVersion int64, at time.Time) error {
	tag, err := r.exec(ctx).Exec(ctx, `
		UPDATE users SET password_hash = $2, token_version = $3, updated_at = $4 WHERE id = $1`,
		id, passwordHash, tokenVersion, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("用户不存在")
	}
	return nil
}

// UpdateAvatar 更新头像附件引用与展示地址。
func (r *UserRepo) UpdateAvatar(ctx context.Context, id int64, attachmentID *int64, avatarURL string, at time.Time) error {
	tag, err := r.exec(ctx).Exec(ctx, `
		UPDATE users
		SET avatar_attachment_id = $2, avatar_url = $3, updated_at = $4
		WHERE id = $1`, id, attachmentID, avatarURL, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("用户不存在")
	}
	return nil
}

// Search 按用户名或昵称搜索账号，ID 游标倒序；只返回公开可展示的账号行。
func (r *UserRepo) Search(ctx context.Context, keyword string, cursor int64, limit int) ([]model.User, error) {
	where := []string{"(STRPOS(LOWER(username), $1) > 0 OR STRPOS(LOWER(nickname), $1) > 0)"}
	args := []any{strings.ToLower(keyword)}
	if cursor > 0 {
		args = append(args, cursor)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}
	args = append(args, limit+1)
	rows, err := r.exec(ctx).Query(ctx,
		`SELECT `+userColumns+` FROM users WHERE `+strings.Join(where, " AND ")+
			` ORDER BY id DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]model.User, 0, limit+1)
	for rows.Next() {
		u, scanErr := scanUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CountHumanUsers 统计人类账号数（AI 不计入），用于初始化判定。
func (r *UserRepo) CountHumanUsers(ctx context.Context) (int64, error) {
	var count int64
	err := r.exec(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE NOT is_ai`).Scan(&count)
	return count, err
}

// CountSuperAdmins 统计超级管理员数量，供启动日志与引导检查使用。
func (r *UserRepo) CountSuperAdmins(ctx context.Context) (int64, error) {
	var count int64
	err := r.exec(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE is_super_admin`).Scan(&count)
	return count, err
}

// GetUsers 批量读取账号行，返回 id → User；缺失 ID 不出现在结果中。
func (r *UserRepo) GetUsers(ctx context.Context, ids []int64) (map[int64]model.User, error) {
	result := make(map[int64]model.User, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.exec(ctx).Query(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, scanErr := scanUser(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result[u.ID] = u
	}
	return result, rows.Err()
}

// GetUserBriefs 批量读取作者摘要，返回 id → UserBrief。
func (r *UserRepo) GetUserBriefs(ctx context.Context, ids []int64) (map[int64]model.UserBrief, error) {
	result := make(map[int64]model.UserBrief, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.exec(ctx).Query(ctx, `
		SELECT id, username, nickname, avatar_url, is_ai, status
		FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		brief, scanErr := scanUserBrief(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result[brief.ID] = brief
	}
	return result, rows.Err()
}

// FindActiveUsersByUsernames 按用户名（大小写不敏感）解析生效账号，用于 @提及。
func (r *UserRepo) FindActiveUsersByUsernames(ctx context.Context, usernames []string) ([]model.UserBrief, error) {
	if len(usernames) == 0 {
		return nil, nil
	}
	lowered := make([]string, 0, len(usernames))
	for _, name := range usernames {
		lowered = append(lowered, strings.ToLower(strings.TrimSpace(name)))
	}
	rows, err := r.exec(ctx).Query(ctx, `
		SELECT id, username, nickname, avatar_url, is_ai, status
		FROM users WHERE LOWER(username) = ANY($1) AND status = 'active'`, lowered)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	briefs := make([]model.UserBrief, 0, len(lowered))
	for rows.Next() {
		brief, scanErr := scanUserBrief(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		briefs = append(briefs, brief)
	}
	return briefs, rows.Err()
}

// ListUserIDsByAIFlag 列出全部 AI 或人类账号 ID（时间线按作者类型过滤）。
func (r *UserRepo) ListUserIDsByAIFlag(ctx context.Context, isAI bool) ([]int64, error) {
	rows, err := r.exec(ctx).Query(ctx, `SELECT id FROM users WHERE is_ai = $1 ORDER BY id DESC`, isAI)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListAvatarAttachmentIDs 列出全部用户当前头像引用的附件 ID（媒体清理防误删）。
func (r *UserRepo) ListAvatarAttachmentIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.exec(ctx).Query(ctx,
		`SELECT avatar_attachment_id FROM users WHERE avatar_attachment_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *UserRepo) queryUser(ctx context.Context, row pgx.Row) (model.User, error) {
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return u, err
}

func scanUser(row rowScanner) (model.User, error) {
	var u model.User
	err := row.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Nickname, &u.AvatarURL,
		&u.Bio, &u.Role, &u.IsSuperAdmin, &u.IsAI, &u.Status, &u.RequireFollowApproval,
		&u.TokenVersion, &u.SuspendedAt, &u.CreatedAt, &u.UpdatedAt,
	)
	return u, err
}

func scanUserBrief(row rowScanner) (model.UserBrief, error) {
	var brief model.UserBrief
	var status string
	err := row.Scan(&brief.ID, &brief.Username, &brief.Nickname, &brief.AvatarURL, &brief.IsAI, &status)
	brief.IsSuspended = status != model.StatusActive
	return brief, err
}

// nullableText 把空字符串写成 NULL：users 表对 AI 账号要求 email/password_hash 为 NULL。
func nullableText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// mapUserWriteError 把唯一约束冲突映射为领域错误（app 层不 import pgx）。
func mapUserWriteError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return err
	}
	switch pgErr.ConstraintName {
	case "users_username_unique":
		return apperr.Conflict("duplicate", "用户名或邮箱已被使用").
			WithDetails(map[string]string{"username": "用户名已存在"})
	case "users_email_unique":
		return apperr.Conflict("duplicate", "用户名或邮箱已被使用").
			WithDetails(map[string]string{"email": "邮箱已存在"})
	case "users_single_super_admin":
		return apperr.Conflict("duplicate", "系统已存在超级管理员")
	default:
		return apperr.Conflict("duplicate", "用户名或邮箱已被使用")
	}
}
