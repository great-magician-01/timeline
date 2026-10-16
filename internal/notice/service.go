package notice

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/api"
)

// PoolProvider 提供运行期连接池（与 auth.PoolProvider 同形）。
type PoolProvider interface {
	Pool() *pgxpool.Pool
}

// Service 提供通知中心接口。
type Service struct {
	pools PoolProvider
	ids   IDGenerator
	log   *slog.Logger
}

func NewService(pools PoolProvider, ids IDGenerator, log *slog.Logger) *Service {
	return &Service{pools: pools, ids: ids, log: log}
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
