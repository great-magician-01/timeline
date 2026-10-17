// Package media 实现附件上传、鉴权访问、图片缩略图与视频封面衍生文件，以及失效清理
// （docs/REQUIREMENTS.md §7、docs/WORKFLOWS.md §6、docs/DATA_MODEL.md §6.9-6.10）。
//
// 硬性约束：
//   - 所有内容（原文件与衍生文件）都只能通过带鉴权的接口访问，UPLOAD_DIR 绝不作为静态目录暴露；
//   - 附件与衍生文件一律逻辑删除，物理文件永不删除；唯一例外是视频封面任务自己生成的
//     ffmpeg 临时输出文件（见 generateVideoCover）；
//   - 存储键、绝对路径与内部错误细节不得出现在响应中。
package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/ratelimit"
	"github.com/great-magician-01/timeline/internal/settings"
	"github.com/great-magician-01/timeline/internal/storage"
)

// PoolProvider 提供运行期连接池（与 auth.PoolProvider 同形）。
type PoolProvider interface {
	Pool() *pgxpool.Pool
}

// IDGenerator 生成雪花 ID（与 auth.IDGenerator 同形）。
type IDGenerator interface {
	Next() (int64, error)
}

// 附件用途（与 attachments.purpose 约束一致）。
const (
	purposePost   = "post"
	purposeAvatar = "avatar"
)

// Service 提供附件上传、访问、衍生文件与失效清理能力。
type Service struct {
	pools PoolProvider
	ids   IDGenerator
	log   *slog.Logger
	store *storage.Store
	jobs  *jobs.Runner

	// uploadLimiter 限制单用户的上传频率；实际阈值与窗口每次请求从系统设置读取，
	// 这里的构造参数只作为未读取设置时的兜底，属于安全保护而非业务配额。
	uploadLimiter *ratelimit.Limiter
}

// NewService 组装附件服务；store 与 runner 由进程启动时统一创建。
func NewService(pools PoolProvider, ids IDGenerator, log *slog.Logger, store *storage.Store, runner *jobs.Runner) *Service {
	defaults := settings.Defaults()
	return &Service{
		pools:         pools,
		ids:           ids,
		log:           log,
		store:         store,
		jobs:          runner,
		uploadLimiter: ratelimit.New(defaults.UploadRateLimit, defaults.UploadRateWindow),
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

// AttachmentByID 读取附件记录。已逻辑删除的记录仍会返回（调用方按 Available 判断可用性，
// 例如后台审核详情需要展示历史附件）；记录不存在时返回 404 not_found。
func (s *Service) AttachmentByID(ctx context.Context, q access.Querier, id int64) (model.Attachment, error) {
	if id <= 0 {
		return model.Attachment{}, api.ErrNotFound("附件不存在")
	}
	att, err := model.ScanAttachment(q.QueryRow(ctx,
		`SELECT `+model.AttachmentColumns+` FROM attachments WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Attachment{}, api.ErrNotFound("附件不存在")
	}
	if err != nil {
		return model.Attachment{}, err
	}
	return att, nil
}

// RequireAttachmentAccess 校验访问者能否读取附件内容（原文件与衍生文件共用）。
// 规则：purpose='avatar' 的头像附件公开可读；其余附件继承所属帖子的可见性；
// 尚未关联任何帖子的附件只有上传者本人与管理员可查看。不满足时返回 404 not_found，
// 不暴露资源存在性（docs/PAGES_AND_PERMISSIONS.md §8）。
func (s *Service) RequireAttachmentAccess(ctx context.Context, q access.Querier, viewer model.Viewer, att model.Attachment) error {
	if !att.Available() {
		return api.ErrNotFound("附件不存在或已失效")
	}
	// model.Attachment 未包含 purpose 列，头像附件需要单独读取用途。
	purpose, err := s.attachmentPurpose(ctx, q, att.ID)
	if err != nil {
		return err
	}
	if purpose == purposeAvatar {
		return nil
	}
	if att.PostID == nil {
		if viewer.IsAdmin || (viewer.Authenticated && viewer.ID == att.UploaderID) {
			return nil
		}
		return api.ErrNotFound("附件不存在或无权访问")
	}
	post, err := model.ScanPost(q.QueryRow(ctx,
		`SELECT `+model.PostColumns+` FROM posts WHERE id = $1`, *att.PostID))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ErrNotFound("附件不存在或无权访问")
	}
	if err != nil {
		return err
	}
	return access.RequireVisiblePost(ctx, q, viewer, post)
}

func (s *Service) attachmentPurpose(ctx context.Context, q access.Querier, id int64) (string, error) {
	var purpose string
	err := q.QueryRow(ctx, `SELECT purpose FROM attachments WHERE id = $1`, id).Scan(&purpose)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", api.ErrNotFound("附件不存在")
	}
	if err != nil {
		return "", fmt.Errorf("读取附件用途失败: %w", err)
	}
	return purpose, nil
}

// DerivativesFor 批量读取衍生文件状态：attachment_id -> derivative_type -> status。
func (s *Service) DerivativesFor(ctx context.Context, q access.Querier, attachmentIDs []int64) (map[int64]map[string]string, error) {
	result := make(map[int64]map[string]string, len(attachmentIDs))
	if len(attachmentIDs) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, `
		SELECT attachment_id, derivative_type, status
		FROM attachment_derivatives
		WHERE attachment_id = ANY($1)`, attachmentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var attachmentID int64
		var derivativeType, status string
		if err := rows.Scan(&attachmentID, &derivativeType, &status); err != nil {
			return nil, err
		}
		if result[attachmentID] == nil {
			result[attachmentID] = map[string]string{}
		}
		result[attachmentID][derivativeType] = status
	}
	return result, rows.Err()
}

// AttachmentsByIDs 批量读取附件记录（供帖子详情与版本快照使用）。
// 与 AttachmentByID 一致，已逻辑删除的记录也会返回。
func (s *Service) AttachmentsByIDs(ctx context.Context, q access.Querier, ids []int64) (map[int64]model.Attachment, error) {
	result := make(map[int64]model.Attachment, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx,
		`SELECT `+model.AttachmentColumns+` FROM attachments WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		att, err := model.ScanAttachment(rows)
		if err != nil {
			return nil, err
		}
		result[att.ID] = att
	}
	return result, rows.Err()
}

// AttachmentResponses 按传入顺序组装附件 DTO，跳过已失效的附件，并附带衍生文件状态。
func (s *Service) AttachmentResponses(ctx context.Context, q access.Querier, attachments []model.Attachment) ([]model.AttachmentResponse, error) {
	ids := make([]int64, 0, len(attachments))
	for _, att := range attachments {
		if att.Available() {
			ids = append(ids, att.ID)
		}
	}
	derivatives, err := s.DerivativesFor(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	responses := make([]model.AttachmentResponse, 0, len(ids))
	for _, att := range attachments {
		if !att.Available() {
			continue
		}
		responses = append(responses, model.AttachmentResponseFor(att, derivatives[att.ID]))
	}
	return responses, nil
}

// SetAvatar 把附件设为用户头像：校验附件归属与用途后更新 users.avatar_url。
// 旧头像附件不做任何物理处理，仅由头像地址切换决定展示。
func (s *Service) SetAvatar(ctx context.Context, pool *pgxpool.Pool, userID, attachmentID int64) error {
	att, err := s.AttachmentByID(ctx, pool, attachmentID)
	if err != nil {
		return err
	}
	if !att.Available() {
		return api.ErrNotFound("附件不存在或已失效")
	}
	if att.UploaderID != userID {
		return api.ErrForbidden("只能使用自己上传的头像附件")
	}
	purpose, err := s.attachmentPurpose(ctx, pool, att.ID)
	if err != nil {
		return err
	}
	if purpose != purposeAvatar {
		return api.ErrValidation("该附件不是头像用途，无法设为头像")
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET avatar_url = $2, updated_at = now() WHERE id = $1`,
		userID, att.ContentURL(),
	); err != nil {
		return fmt.Errorf("更新头像失败: %w", err)
	}
	return nil
}
