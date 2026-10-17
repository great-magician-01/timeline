package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// SessionRepo 是 auth_sessions 表的仓储实现。
type SessionRepo struct {
	pool *pgxpool.Pool
}

// NewSessionRepo 构造会话仓储；pool 在数据库就绪后由组合根注入。
func NewSessionRepo(pool *pgxpool.Pool) *SessionRepo { return &SessionRepo{pool: pool} }

func (r *SessionRepo) exec(ctx context.Context) db.Executor {
	return db.ExecutorFrom(ctx, r.pool)
}

// Insert 插入一条会话；created_at/expires_at 由 app 时钟显式传入。
func (r *SessionRepo) Insert(ctx context.Context, session model.Session, tokenHash string, createdAt time.Time) error {
	_, err := r.exec(ctx).Exec(ctx, `
		INSERT INTO auth_sessions (
			id, user_id, token_hash, csrf_token, token_version, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		session.ID, session.UserID, tokenHash, session.CSRFToken, session.TokenVersion,
		createdAt, session.ExpiresAt,
	)
	return err
}

// ByTokenHash 用一次 JOIN 查询同时读取会话与账号行；不存在返回领域 NotFound。
// 有效性（吊销/过期/token_version/停用/AI）由 domain.ValidateSession 在 app 侧判定。
func (r *SessionRepo) ByTokenHash(ctx context.Context, tokenHash string) (model.User, model.Session, error) {
	var (
		user    model.User
		session model.Session
	)
	err := r.exec(ctx).QueryRow(ctx, `
		SELECT s.id, s.user_id, s.csrf_token, s.token_version, s.expires_at, s.revoked_at,
		       `+userColumnsJoined+`
		FROM auth_sessions AS s
		JOIN users AS u ON u.id = s.user_id
		WHERE s.token_hash = $1`, tokenHash).Scan(
		&session.ID, &session.UserID, &session.CSRFToken, &session.TokenVersion,
		&session.ExpiresAt, &session.RevokedAt,
		&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.Nickname, &user.AvatarURL,
		&user.Bio, &user.Role, &user.IsSuperAdmin, &user.IsAI, &user.Status, &user.RequireFollowApproval,
		&user.TokenVersion, &user.SuspendedAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, model.Session{}, apperr.NotFound("会话不存在")
	}
	if err != nil {
		return model.User{}, model.Session{}, err
	}
	return user, session, nil
}

// Revoke 吊销单个会话；已吊销时幂等返回。
func (r *SessionRepo) Revoke(ctx context.Context, sessionID int64, at time.Time) error {
	_, err := r.exec(ctx).Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = $2
		WHERE id = $1 AND revoked_at IS NULL`, sessionID, at)
	return err
}

// RevokeAllForUserExcept 吊销某账号的全部有效会话，保留当前会话。
func (r *SessionRepo) RevokeAllForUserExcept(ctx context.Context, userID, exceptSessionID int64, at time.Time) error {
	_, err := r.exec(ctx).Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = $3
		WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL`, userID, exceptSessionID, at)
	return err
}

// RevokeAllForUser 吊销某账号的全部有效会话（停用、管理员重置密码）。
func (r *SessionRepo) RevokeAllForUser(ctx context.Context, userID int64, at time.Time) error {
	_, err := r.exec(ctx).Exec(ctx, `
		UPDATE auth_sessions SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL`, userID, at)
	return err
}

// UpdateTokenVersion 把当前会话的 token_version 同步为最新值（改密后保留当前会话）。
func (r *SessionRepo) UpdateTokenVersion(ctx context.Context, sessionID, tokenVersion int64) error {
	_, err := r.exec(ctx).Exec(ctx,
		`UPDATE auth_sessions SET token_version = $2 WHERE id = $1`, sessionID, tokenVersion)
	return err
}

// CountActive 统计账号当前有效会话数（管理后台用户详情）。
func (r *SessionRepo) CountActive(ctx context.Context, userID int64, now time.Time) (int64, error) {
	var count int64
	err := r.exec(ctx).QueryRow(ctx, `
		SELECT COUNT(*) FROM auth_sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2`, userID, now).Scan(&count)
	return count, err
}

// userColumnsJoined 是 JOIN 查询中带 u. 前缀的 users 列投影。
const userColumnsJoined = `u.id, u.username, COALESCE(u.email, ''), COALESCE(u.password_hash, ''),
	u.nickname, u.avatar_url, u.bio, u.role, u.is_super_admin, u.is_ai, u.status,
	u.require_follow_approval, u.token_version, u.suspended_at, u.created_at, u.updated_at`
