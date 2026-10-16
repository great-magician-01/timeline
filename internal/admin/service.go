// Package admin 实现管理员账号管理、注册设置与审计查询。
package admin

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/content"
	"github.com/great-magician-01/timeline/internal/social"
)

type Service struct {
	pools   auth.PoolProvider
	ids     auth.IDGenerator
	log     *slog.Logger
	content *content.Service
	social  *social.Service
}

// NewService 构造管理服务；内容审核复用 content/social 的逻辑删除流程，
// 避免管理后台出现第二套删除实现。
func NewService(
	pools auth.PoolProvider,
	ids auth.IDGenerator,
	log *slog.Logger,
	contentService *content.Service,
	socialService *social.Service,
) *Service {
	return &Service{pools: pools, ids: ids, log: log, content: contentService, social: socialService}
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
