package media

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/storage"
)

// Content 处理 GET /api/v1/attachments/:id/content。
// 原文件（含视频）支持 Range 请求；UPLOAD_DIR 本身从不作为静态目录暴露。
func (s *Service) Content(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	att, ok := s.attachmentFromPath(c, pool)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := s.RequireAttachmentAccess(ctx, pool, viewerFromContext(c), att); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验附件访问权限失败", err)
		}
		return
	}
	file, err := s.store.Open(att.StorageKey)
	if err != nil {
		// 物理文件缺失时不泄露存储路径与内部错误，按不存在处理。
		s.log.Error("附件物理文件缺失", "attachment_id", att.ID, "err", err)
		api.Error(c, http.StatusNotFound, "not_found", "附件不存在")
		return
	}
	defer file.Close()

	c.Header("Content-Type", att.MediaType)
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", contentDisposition(att.Kind, att.OriginalFilename))
	// ServeContent 负责 Content-Length、Range 与条件请求，客户端不能借此访问别的文件。
	http.ServeContent(c.Writer, c.Request, att.OriginalFilename, att.CreatedAt, file)
}

// Derivative 处理 GET /api/v1/attachments/:id/derivatives/:type。
// 只有就绪且物理文件存在的衍生文件才返回内容，否则统一 404，由前端回退到原图或占位
// （docs/REQUIREMENTS.md 7.1.6）。
func (s *Service) Derivative(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	derivativeType := c.Param("type")
	if derivativeType != model.DerivativeThumbnail && derivativeType != model.DerivativeVideoCover {
		api.Error(c, http.StatusNotFound, "not_found", "衍生文件不存在")
		return
	}
	att, ok := s.attachmentFromPath(c, pool)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	// 衍生文件继承原文件与其所属内容的访问权限，不存在绕开鉴权的公开地址。
	if err := s.RequireAttachmentAccess(ctx, pool, viewerFromContext(c), att); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验衍生文件访问权限失败", err)
		}
		return
	}

	var status, key string
	err := pool.QueryRow(ctx, `
		SELECT status, storage_key FROM attachment_derivatives
		WHERE attachment_id = $1 AND derivative_type = $2`,
		att.ID, derivativeType,
	).Scan(&status, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "衍生文件尚未生成")
		return
	}
	if err != nil {
		s.internalError(c, "读取衍生文件记录失败", err)
		return
	}
	if status != model.DerivativeReady || key == "" {
		api.Error(c, http.StatusNotFound, "not_found", "衍生文件尚未生成")
		return
	}
	file, err := s.store.Open(key)
	if err != nil {
		s.log.Error("衍生文件物理文件缺失", "attachment_id", att.ID, "type", derivativeType, "err", err)
		api.Error(c, http.StatusNotFound, "not_found", "衍生文件不存在")
		return
	}
	defer file.Close()

	// 缩略图与视频封面统一编码为 JPEG。
	c.Header("Content-Type", "image/jpeg")
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "inline")
	http.ServeContent(c.Writer, c.Request, derivativeType+".jpg", att.CreatedAt, file)
}

// attachmentFromPath 解析路径中的附件 ID 并读取记录；异常时直接写出响应。
func (s *Service) attachmentFromPath(c *gin.Context, q access.Querier) (model.Attachment, bool) {
	id, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusNotFound, "not_found", "附件不存在")
		return model.Attachment{}, false
	}
	att, err := s.AttachmentByID(c.Request.Context(), q, id)
	if err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "读取附件失败", err)
		}
		return model.Attachment{}, false
	}
	if !att.Available() {
		api.Error(c, http.StatusNotFound, "not_found", "附件不存在或已失效")
		return model.Attachment{}, false
	}
	return att, true
}

// viewerFromContext 组装访问者身份；未登录按游客处理。
func viewerFromContext(c *gin.Context) model.Viewer {
	if u, ok := auth.CurrentUser(c); ok {
		return model.ViewerFrom(u)
	}
	return model.GuestViewer()
}

// contentDisposition 组装 Content-Disposition：图片与视频内联展示，其他文件强制下载。
// 文件名同时给出 ASCII 回退与 RFC 5987 的 filename* 百分号编码形式。
func contentDisposition(kind, filename string) string {
	if kind != storage.KindFile {
		return "inline"
	}
	if asciiFilenameSafe(filename) {
		return `attachment; filename="` + filename + `"`
	}
	return `attachment; filename="download"; filename*=UTF-8''` + encodeRFC5987(filename)
}

// asciiFilenameSafe 判断文件名能否直接放进 filename="..."（排除引号、反斜杠与分隔符）。
func asciiFilenameSafe(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' || r == ';' {
			return false
		}
	}
	return true
}

// encodeRFC5987 按 RFC 5987 对文件名做百分号编码：attr-char 之外的字符按 UTF-8 字节编码。
func encodeRFC5987(name string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isAttrChar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperhex[c>>4])
		b.WriteByte(upperhex[c&0x0f])
	}
	return b.String()
}

func isAttrChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '&', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
