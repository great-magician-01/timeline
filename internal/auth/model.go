package auth

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/api"
)

const (
	RoleUser  = "user"
	RoleAdmin = "admin"

	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// User 是数据库账号模型。PasswordHash 永远不会直接序列化到响应。
type User struct {
	ID                    int64
	Username              string
	Email                 string
	PasswordHash          string
	Nickname              string
	AvatarURL             string
	Bio                   string
	Role                  string
	IsAI                  bool
	Status                string
	RequireFollowApproval bool
	TokenVersion          int64
	SuspendedAt           *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (u User) IsSuspended() bool { return u.Status == StatusSuspended }

// UserColumns 供账号相关查询复用；不带表名或别名，JOIN 查询需自行列全字段。
const UserColumns = `id, username, COALESCE(email, ''), COALESCE(password_hash, ''),
	nickname, avatar_url, bio, role, is_ai, status, require_follow_approval,
	token_version, suspended_at, created_at, updated_at`

type Row interface {
	Scan(dest ...any) error
}

func ScanUser(row Row) (User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Nickname, &u.AvatarURL,
		&u.Bio, &u.Role, &u.IsAI, &u.Status, &u.RequireFollowApproval,
		&u.TokenVersion, &u.SuspendedAt, &u.CreatedAt, &u.UpdatedAt,
	)
	return u, err
}

type QueryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func UserByID(ctx context.Context, db QueryRower, id int64, forUpdate bool) (User, error) {
	query := `SELECT ` + UserColumns + ` FROM users WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	return ScanUser(db.QueryRow(ctx, query, id))
}

func UserByUsername(ctx context.Context, db QueryRower, username string) (User, error) {
	return ScanUser(db.QueryRow(ctx,
		`SELECT `+UserColumns+` FROM users WHERE LOWER(username) = LOWER($1)`, username,
	))
}

// UserResponse 是账号接口的安全 DTO。私有字段只在本人或管理员响应中填充。
type UserResponse struct {
	ID                    string `json:"id"`
	Username              string `json:"username"`
	Nickname              string `json:"nickname"`
	AvatarURL             string `json:"avatar_url"`
	Bio                   string `json:"bio"`
	IsAI                  bool   `json:"is_ai"`
	IsSuspended           bool   `json:"is_suspended"`
	CreatedAt             string `json:"created_at"`
	Email                 string `json:"email,omitempty"`
	Role                  string `json:"role,omitempty"`
	Status                string `json:"status,omitempty"`
	RequireFollowApproval *bool  `json:"require_follow_approval,omitempty"`
	UpdatedAt             string `json:"updated_at,omitempty"`
}

func PublicUser(u User) UserResponse {
	return UserResponse{
		ID:          api.FormatID(u.ID),
		Username:    u.Username,
		Nickname:    u.Nickname,
		AvatarURL:   u.AvatarURL,
		Bio:         u.Bio,
		IsAI:        u.IsAI,
		IsSuspended: u.IsSuspended(),
		CreatedAt:   api.FormatTime(u.CreatedAt),
	}
}

func PrivateUser(u User) UserResponse {
	resp := PublicUser(u)
	resp.Email = u.Email
	resp.Role = u.Role
	resp.Status = u.Status
	resp.RequireFollowApproval = &u.RequireFollowApproval
	resp.UpdatedAt = api.FormatTime(u.UpdatedAt)
	return resp
}

type Session struct {
	ID           int64
	UserID       int64
	CSRFToken    string
	TokenVersion int64
	ExpiresAt    time.Time
	RevokedAt    *time.Time
}

const (
	userContextKey    = "timeline.auth.user"
	sessionContextKey = "timeline.auth.session"
)

func CurrentUser(c *gin.Context) (User, bool) {
	value, ok := c.Get(userContextKey)
	if !ok {
		return User{}, false
	}
	u, ok := value.(User)
	return u, ok
}

func CurrentSession(c *gin.Context) (Session, bool) {
	value, ok := c.Get(sessionContextKey)
	if !ok {
		return Session{}, false
	}
	s, ok := value.(Session)
	return s, ok
}
