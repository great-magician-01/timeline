// Package http 是 media 模块的 Gin 传输层：multipart 上传、附件原文件与衍生文件的
// 鉴权读取、逻辑删除。HTTP 语义（状态码/错误码/缓存头）与旧实现保持一致。
package http

import (
	"errors"
	stdhttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/media/app"
	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/view"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// Handler 是 media 模块的 HTTP 处理器；构造只依赖 app.Service。
type Handler struct {
	svc *app.Service
}

// New 构造 media HTTP 处理器。
func New(service *app.Service) *Handler {
	return &Handler{svc: service}
}

// Upload 处理 POST /api/v1/attachments（multipart/form-data，字段 file 与 purpose）。
func (h *Handler) Upload(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	// 即使客户端伪造 Content-Length，超限请求也会在读取时被中断。
	maxRequestBytes, _ := h.svc.UploadLimits(ctx)
	c.Request.Body = stdhttp.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBytes)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxErr *stdhttp.MaxBytesError
		if errors.As(err, &maxErr) {
			httpx.Error(c, stdhttp.StatusRequestEntityTooLarge, "content_too_large", "文件超过大小限制")
			return
		}
		httpx.Error(c, stdhttp.StatusBadRequest, "invalid_request", "请选择要上传的文件")
		return
	}
	source, err := fileHeader.Open()
	if err != nil {
		httpx.Error(c, stdhttp.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
		return
	}
	defer source.Close()

	result, err := h.svc.Upload(ctx, app.UploadInput{
		Viewer:   viewer,
		Filename: fileHeader.Filename,
		Purpose:  c.PostForm("purpose"),
		SizeHint: fileHeader.Size,
		Content:  source,
	})
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(stdhttp.StatusCreated, gin.H{
		"attachment": view.AttachmentResponseFor(result.Attachment, result.Derivatives),
	})
}

// Content 处理 GET /api/v1/attachments/:id/content。
// 原文件（含视频）支持 Range 请求；UPLOAD_DIR 本身从不作为静态目录暴露。
func (h *Handler) Content(c *gin.Context) {
	id, ok := h.attachmentID(c)
	if !ok {
		return
	}
	result, err := h.svc.OpenContent(c.Request.Context(), viewerFromContext(c), id)
	if err != nil {
		h.respondError(c, err)
		return
	}
	defer result.File.Close()

	attachment := result.Attachment
	c.Header("Content-Type", attachment.MediaType)
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", contentDisposition(attachment.Kind, attachment.OriginalFilename))
	// ServeContent 负责 Content-Length、Range 与条件请求，客户端不能借此访问别的文件。
	stdhttp.ServeContent(c.Writer, c.Request, attachment.OriginalFilename, attachment.CreatedAt, result.File)
}

// Derivative 处理 GET /api/v1/attachments/:id/derivatives/:type。
// 只有就绪且物理文件存在的衍生文件才返回内容，否则统一 404，由前端回退到原图或占位。
func (h *Handler) Derivative(c *gin.Context) {
	derivativeType := c.Param("type")
	if !domain.ValidDerivativeType(derivativeType) {
		httpx.Error(c, stdhttp.StatusNotFound, "not_found", "衍生文件不存在")
		return
	}
	id, ok := h.attachmentID(c)
	if !ok {
		return
	}
	result, err := h.svc.OpenDerivative(c.Request.Context(), viewerFromContext(c), id, derivativeType)
	if err != nil {
		h.respondError(c, err)
		return
	}
	defer result.File.Close()

	// 缩略图与视频封面统一编码为 JPEG。
	c.Header("Content-Type", "image/jpeg")
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "inline")
	stdhttp.ServeContent(c.Writer, c.Request, result.DerivativeType+".jpg", result.Attachment.CreatedAt, result.File)
}

// Delete 处理 POST /api/v1/attachments/:id/delete（仅上传者本人）。
// 已被历史版本引用时只解除当前草稿关联，否则把附件整体置为失效；
// 数据库记录与物理文件始终保留。
func (h *Handler) Delete(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	id, ok := h.attachmentID(c)
	if !ok {
		return
	}
	result, err := h.svc.Delete(c.Request.Context(), viewer.ID, id)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(stdhttp.StatusOK, gin.H{"detached": result.Detached, "deleted": result.Deleted})
}

// attachmentID 解析路径中的附件 ID；无效 ID 按 404 处理（不暴露资源存在性）。
func (h *Handler) attachmentID(c *gin.Context) (int64, bool) {
	id, err := httpx.ParseID(c.Param("id"))
	if err != nil {
		httpx.Error(c, stdhttp.StatusNotFound, "not_found", "附件不存在")
		return 0, false
	}
	return id, true
}

// respondError 统一映射领域错误；413/415 无法由 apperr.Kind 表达，单独映射。
func (h *Handler) respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrContentTooLarge):
		httpx.Error(c, stdhttp.StatusRequestEntityTooLarge, "content_too_large", "文件超过大小限制")
	case errors.Is(err, domain.ErrStorageQuotaExhausted):
		httpx.Error(c, stdhttp.StatusRequestEntityTooLarge, "content_too_large", "存储空间已用完，无法继续上传")
	case errors.Is(err, domain.ErrStorageQuotaExceeded):
		httpx.Error(c, stdhttp.StatusRequestEntityTooLarge, "content_too_large", "附件总大小超过存储配额")
	case errors.Is(err, domain.ErrUnsupportedMediaType):
		httpx.Error(c, stdhttp.StatusUnsupportedMediaType, "unsupported_media_type", "不支持的文件类型")
	default:
		if !httpx.Respond(c, err) {
			httpx.Error(c, stdhttp.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
		}
	}
}

// viewerFromContext 组装访问者身份；未登录按游客处理（附件可见性由 app 判定）。
func viewerFromContext(c *gin.Context) model.Viewer {
	if viewer, ok := httpx.Viewer(c); ok {
		return viewer
	}
	return model.GuestViewer()
}

// contentDisposition 组装 Content-Disposition：图片与视频内联展示，其他文件强制下载。
// 文件名同时给出 ASCII 回退与 RFC 5987 的 filename* 百分号编码形式。
func contentDisposition(kind, filename string) string {
	if kind != domain.KindFile {
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
	var b []byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isAttrChar(c) {
			b = append(b, c)
			continue
		}
		b = append(b, '%', upperhex[c>>4], upperhex[c&0x0f])
	}
	return string(b)
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
