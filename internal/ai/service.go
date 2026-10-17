// Package ai 实现模型接口预设（含 API Key 加密）、AI 身份与配置、行为策略与分层记忆。
//
// 管理接口统一挂在 /api/v1/admin 下，路由与鉴权（RequireAuth + RequireAdmin）由
// internal/server 注册；本包 Handler 只负责参数校验、事务一致性、审计与响应组装。
// API Key 只以 AES-256-GCM 密文落库，任何响应、日志与审计明细都不得出现明文或密文。
package ai

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/ratelimit"
)

// PoolProvider 与 auth.PoolProvider 同形，避免包之间相互依赖。
type PoolProvider interface {
	Pool() *pgxpool.Pool
}

// IDGenerator 与雪花 ID 生成器同形。
type IDGenerator interface {
	Next() (int64, error)
}

// Service 承载预设、AI 配置与记忆的全部管理操作。
type Service struct {
	pools PoolProvider
	ids   IDGenerator
	log   *slog.Logger
	// secretKey 是 32 字节对称密钥，来自 config.Config.SecretKey。
	secretKey []byte
	// providerTestLimiter 限制管理员的预设连接测试频率（会真实出站请求）。
	providerTestLimiter *ratelimit.Limiter
}

func NewService(pools PoolProvider, ids IDGenerator, log *slog.Logger, secretKey []byte) *Service {
	return &Service{
		pools:     pools,
		ids:       ids,
		log:       log,
		secretKey: secretKey,

		providerTestLimiter: ratelimit.New(providerTestRateLimit, providerTestRateWindow),
	}
}

// 预设连接测试的限频阈值：单个管理员每分钟最多 10 次。
const (
	providerTestRateLimit  = 10
	providerTestRateWindow = time.Minute
)

// consumeProviderTestQuota 消耗一次连接测试额度，超限时写回 429 与 Retry-After。
func (s *Service) consumeProviderTestQuota(c *gin.Context, adminID int64) bool {
	allowed, retryAfter := s.providerTestLimiter.Consume("admin:" + api.FormatID(adminID))
	if allowed {
		return true
	}
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	if !api.Respond(c, api.ErrRateLimited()) {
		s.internalError(c, "连接测试限频响应失败", errors.New("rate limited"))
	}
	return false
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

// beginTx 开启事务；失败时已经写出 500 响应。
func (s *Service) beginTx(c *gin.Context, message string) (pgx.Tx, bool) {
	pool, ok := s.pool(c)
	if !ok {
		return nil, false
	}
	tx, err := pool.Begin(c.Request.Context())
	if err != nil {
		s.internalError(c, message, err)
		return nil, false
	}
	return tx, true
}

// commitTx 提交事务；失败时已经写出 500 响应。
func (s *Service) commitTx(c *gin.Context, tx pgx.Tx, message string) bool {
	if err := tx.Commit(c.Request.Context()); err != nil {
		s.internalError(c, message, err)
		return false
	}
	return true
}

// writeAudit 统一走 audit.Write，保证审计与业务变更处于同一事务。
func (s *Service) writeAudit(
	ctx context.Context,
	q access.Querier,
	adminID int64,
	action, targetType string,
	targetID *int64,
	detail any,
) error {
	return audit.Write(ctx, q, s.ids, adminID, action, targetType, targetID, detail)
}

// parsePathID 解析路径参数中的雪花 ID；失败时已经写出 400 响应。
func parsePathID(c *gin.Context, name, message string) (int64, bool) {
	id, err := api.ParseID(c.Param(name))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", message)
		return 0, false
	}
	return id, true
}

// firstDetail 取字段校验详情中的第一条作为错误消息；map 顺序不保证，仅用于提示。
func firstDetail(details map[string]string) string {
	for _, message := range details {
		return message
	}
	return "请求参数不符合要求"
}

// isUniqueViolation 判断 PostgreSQL 唯一索引冲突（并发下的兜底映射为 409）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// truncateRunes 按字符截断，避免把超长错误信息写入响应或日志。
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// 审计目标类型。
const (
	targetModelProvider = "model_provider"
	targetAI            = "ai"
	targetAIMemory      = "ai_memory"
	targetSystemSetting = "system_settings"
)
