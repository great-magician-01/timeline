package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/ratelimit"
)

const (
	sessionCookieName = "timeline_session"
	guestCSRFCookie   = "timeline_csrf"
	csrfHeader        = "X-CSRF-Token"
	sessionTTL        = 30 * 24 * time.Hour
)

type PoolProvider interface {
	Pool() *pgxpool.Pool
}

type IDGenerator interface {
	Next() (int64, error)
}

type Service struct {
	pools        PoolProvider
	ids          IDGenerator
	log          *slog.Logger
	cookieSecure bool
	dummyHash    string

	loginAccountLimiter *ratelimit.Limiter
	loginPairLimiter    *ratelimit.Limiter
	loginIPLimiter      *ratelimit.Limiter
	registerLimiter     *ratelimit.Limiter
}

func NewService(pools PoolProvider, ids IDGenerator, log *slog.Logger, cookieSecure bool) *Service {
	// 用户不存在时也执行一次同成本比较，降低通过响应时间枚举账号的可能。
	dummy, _ := bcrypt.GenerateFromPassword([]byte("timeline-dummy-password"), passwordBcryptCost)
	return &Service{
		pools:               pools,
		ids:                 ids,
		log:                 log,
		cookieSecure:        cookieSecure,
		dummyHash:           string(dummy),
		loginAccountLimiter: ratelimit.New(30, 15*time.Minute),
		loginPairLimiter:    ratelimit.New(10, 5*time.Minute),
		loginIPLimiter:      ratelimit.New(30, 5*time.Minute),
		registerLimiter:     ratelimit.New(5, 10*time.Minute),
	}
}

func (s *Service) pool(c *gin.Context) (*pgxpool.Pool, bool) {
	pool := s.pools.Pool()
	if pool == nil {
		api.Error(c, http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪，请稍后重试")
		return nil, false
	}
	return pool, true
}

func (s *Service) internalError(c *gin.Context, message string, err error) {
	s.log.Error(message, "err", err, "path", c.Request.URL.Path)
	api.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
}

func (s *Service) setSessionCookie(c *gin.Context, token string, expires time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) setGuestCSRFCookie(c *gin.Context, token string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     guestCSRFCookie,
		Value:    token,
		Path:     "/api/v1/auth",
		MaxAge:   86400,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// RegistrationEnabled 读取开放注册设置；设置行缺失时使用需求中的默认开放值。
func RegistrationEnabled(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var enabled bool
	err := pool.QueryRow(ctx,
		`SELECT value FROM system_settings WHERE key = 'registration.enabled'`,
	).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	return enabled, err
}
