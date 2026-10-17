// Package admin 实现管理员账号管理、注册设置与审计查询。
package admin

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/content"
	"github.com/great-magician-01/timeline/internal/social"
)

// AttachmentAdmin 是管理后台处置附件所需的最小能力（由 internal/media.Service 实现）。
// 用接口而不是直接依赖 internal/media，避免 admin 与 media 之间形成循环依赖。
type AttachmentAdmin interface {
	DeleteAsAdmin(ctx context.Context, q access.Querier, adminID, attachmentID int64) error
}

// SecretCipher 是管理后台保存全局敏感设置所需的最小加密能力（由 internal/ai.Service 实现）。
// 管理后台不直接持有主密钥，只通过该接口写入密文。
type SecretCipher interface {
	EncryptSecret(plain string) (string, error)
	CanDecryptSecret(encoded string) bool
}

type Service struct {
	pools        auth.PoolProvider
	ids          auth.IDGenerator
	log          *slog.Logger
	content      *content.Service
	social       *social.Service
	media        AttachmentAdmin
	secretCipher SecretCipher
}

// NewService 构造管理服务；内容审核复用 content/social 的逻辑删除流程，
// 避免管理后台出现第二套删除实现。mediaServices 可选：传入后启用附件处置接口。
func NewService(
	pools auth.PoolProvider,
	ids auth.IDGenerator,
	log *slog.Logger,
	contentService *content.Service,
	socialService *social.Service,
	secretCipher SecretCipher,
	mediaServices ...AttachmentAdmin,
) *Service {
	service := &Service{
		pools: pools, ids: ids, log: log, content: contentService,
		social: socialService, secretCipher: secretCipher,
	}
	if len(mediaServices) > 0 {
		service.media = mediaServices[0]
	}
	return service
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
