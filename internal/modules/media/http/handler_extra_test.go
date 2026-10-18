package http

// Handler 层用例：multipart 上传参数/大小/类型与错误映射、原文件与衍生文件下载的
// 状态码/响应头、逻辑删除鉴权与幂等、非法 ID 与未登录。成功路径由内存协议桩驱动
// 真实 app.Service，文件走 t.TempDir 的真实本地存储。

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/app"
	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== 上传 =====

// uploadSuccessResults 是图片上传成功所需的查询顺序：预检统计 + 上传统计 + 两条 INSERT。
func uploadSuccessResults() []pgResult {
	return []pgResult{
		sumSizeResult(0),
		sumSizeResult(0),
		commandResult("INSERT 0 1"),
		commandResult("INSERT 0 1"),
	}
}

// TestUploadSuccessJSON 覆盖上传成功：201 响应与 attachment DTO 关键字段。
func TestUploadSuccessJSON(t *testing.T) {
	env := newHTTPTestEnv(t)
	env.expect(uploadSuccessResults()...)

	c, recorder := newUploadRequest(t, humanViewer(7), "photo.png", domain.PurposePost, testPNG(t, 32, 16))
	env.handler.Upload(c)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("上传状态码 = %d，期望 201，body=%s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	attachment, ok := body["attachment"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 attachment 对象: %s", recorder.Body.String())
	}
	if attachment["id"] != "101" {
		t.Fatalf("附件 ID = %v，期望字符串 \"101\"", attachment["id"])
	}
	if attachment["kind"] != domain.KindImage || attachment["media_type"] != "image/png" {
		t.Fatalf("附件种类/媒体类型 = %v/%v", attachment["kind"], attachment["media_type"])
	}
	if attachment["original_filename"] != "photo.png" {
		t.Fatalf("原始文件名 = %v", attachment["original_filename"])
	}
	if attachment["width"] != float64(32) || attachment["height"] != float64(16) {
		t.Fatalf("图片尺寸 = %v/%v，期望 32/16", attachment["width"], attachment["height"])
	}
	if attachment["content_url"] != "/api/v1/attachments/101/content" {
		t.Fatalf("附件地址 = %v", attachment["content_url"])
	}
	derivatives, ok := attachment["derivatives"].(map[string]any)
	if !ok || derivatives[model.DerivativeThumbnail] != model.DerivativePending {
		t.Fatalf("衍生文件状态 = %v，期望缩略图 pending", attachment["derivatives"])
	}
	if attachment["status"] != model.AttachmentAvailable {
		t.Fatalf("附件状态 = %v", attachment["status"])
	}
	if attachment["created_at"] != "2026-10-18T12:00:00+08:00" {
		t.Fatalf("created_at = %v，期望应用时钟格式化时间", attachment["created_at"])
	}
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("上传应消费全部预设查询，剩余 %d", got)
	}
}

// TestUploadRateLimited 覆盖上传限频：第二次请求 429 并带 Retry-After，且不再读取请求体。
func TestUploadRateLimited(t *testing.T) {
	env := newHTTPTestEnv(t)
	env.settings.values.UploadRateLimit = 1
	env.settings.values.UploadRateWindow = time.Hour
	env.expect(uploadSuccessResults()...)

	first, firstRecorder := newUploadRequest(t, humanViewer(7), "photo.png", domain.PurposePost, testPNG(t, 8, 8))
	env.handler.Upload(first)
	if firstRecorder.Code != http.StatusCreated {
		t.Fatalf("首次上传状态码 = %d，期望 201", firstRecorder.Code)
	}

	secondBody := &countingReader{}
	second, secondRecorder := newTestContext(http.MethodPost, "/api/v1/attachments")
	second.Request.Body = io.NopCloser(secondBody)
	withViewer(second, humanViewer(7))
	env.handler.Upload(second)

	if secondRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("超频上传状态码 = %d，期望 429", secondRecorder.Code)
	}
	apiErr := decodeAPIError(t, secondRecorder)
	if apiErr.Error.Code != "rate_limited" {
		t.Fatalf("错误码 = %q，期望 rate_limited", apiErr.Error.Code)
	}
	if secondRecorder.Header().Get("Retry-After") == "" {
		t.Fatal("限频响应应带 Retry-After 头")
	}
	if secondBody.reads != 0 {
		t.Fatalf("预检失败后不应读取请求体，实际读取 %d 次", secondBody.reads)
	}
}

// TestUploadUnauthenticated 未登录上传返回 401，不触库。
func TestUploadUnauthenticated(t *testing.T) {
	env := newHTTPTestEnv(t)
	c, recorder := newUploadRequest(t, model.GuestViewer(), "photo.png", "", testPNG(t, 4, 4))
	env.handler.Upload(c)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录上传状态码 = %d，期望 401", recorder.Code)
	}
	if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != "unauthenticated" {
		t.Fatalf("错误码 = %q，期望 unauthenticated", apiErr.Error.Code)
	}
	if got := len(env.stub.recorded()); got != 0 {
		t.Fatalf("未登录上传不应触库，实际查询 %d 次", got)
	}
}

// TestUploadRequestTooLarge 请求体超过 MaxRequestBytes 时在读取阶段返回 413。
func TestUploadRequestTooLarge(t *testing.T) {
	env := newHTTPTestEnv(t)
	env.settings.values.MaxRequestBytes = 64
	env.expect(sumSizeResult(0))
	c, recorder := newUploadRequest(t, humanViewer(7), "photo.png", domain.PurposePost, testPNG(t, 64, 64))
	env.handler.Upload(c)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大请求状态码 = %d，期望 413，body=%s", recorder.Code, recorder.Body.String())
	}
	if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != "content_too_large" {
		t.Fatalf("错误码 = %q，期望 content_too_large", apiErr.Error.Code)
	}
}

// TestUploadMissingFile 缺少 file 字段时返回 400 invalid_request。
func TestUploadMissingFile(t *testing.T) {
	env := newHTTPTestEnv(t)
	env.expect(sumSizeResult(0))
	c, recorder := newUploadRequest(t, humanViewer(7), "", domain.PurposePost, nil)
	env.handler.Upload(c)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("缺少文件状态码 = %d，期望 400", recorder.Code)
	}
	if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != "invalid_request" {
		t.Fatalf("错误码 = %q，期望 invalid_request", apiErr.Error.Code)
	}
}

// TestUploadDomainRejections 覆盖大小、类型与用途校验的错误码/状态码映射。
func TestUploadDomainRejections(t *testing.T) {
	cases := []struct {
		name       string
		filename   string
		purpose    string
		content    []byte
		setup      func(*httpTestEnv)
		wantStatus int
		wantCode   string
	}{
		{
			name: "文件超过大小限制", filename: "a.png", purpose: domain.PurposePost,
			content: testPNG(t, 16, 16),
			setup: func(env *httpTestEnv) {
				env.settings.values.MaxUploadBytes = 8
				env.expect(sumSizeResult(0), sumSizeResult(0))
			},
			wantStatus: http.StatusRequestEntityTooLarge, wantCode: "content_too_large",
		},
		{
			name: "不支持的文件类型", filename: "evil.html", purpose: domain.PurposePost,
			content: testHTMLBytes(),
			setup: func(env *httpTestEnv) {
				env.expect(sumSizeResult(0), sumSizeResult(0))
			},
			wantStatus: http.StatusUnsupportedMediaType, wantCode: "unsupported_media_type",
		},
		{
			name: "非法用途", filename: "a.png", purpose: "video",
			content: testPNG(t, 8, 8),
			setup: func(env *httpTestEnv) {
				env.expect(sumSizeResult(0), sumSizeResult(0))
			},
			wantStatus: http.StatusBadRequest, wantCode: "invalid_request",
		},
		{
			name: "存储配额已满", filename: "a.png", purpose: domain.PurposePost,
			content: testPNG(t, 8, 8),
			setup: func(env *httpTestEnv) {
				env.settings.values.StorageQuotaBytes = 1024
				env.expect(sumSizeResult(2048))
			},
			wantStatus: http.StatusRequestEntityTooLarge, wantCode: "content_too_large",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newHTTPTestEnv(t)
			tc.setup(env)
			c, recorder := newUploadRequest(t, humanViewer(7), tc.filename, tc.purpose, tc.content)
			env.handler.Upload(c)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("状态码 = %d，期望 %d，body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != tc.wantCode {
				t.Fatalf("错误码 = %q，期望 %q", apiErr.Error.Code, tc.wantCode)
			}
		})
	}
}

// TestUploadInternalError 后端未装配作业队列等内部错误统一 500，不泄露细节。
func TestUploadInternalError(t *testing.T) {
	env := newHTTPTestEnv(t, func(deps *app.Deps) { deps.Jobs = nil })
	env.expect(sumSizeResult(0))
	c, recorder := newUploadRequest(t, humanViewer(7), "a.png", domain.PurposePost, testPNG(t, 8, 8))
	env.handler.Upload(c)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("内部错误状态码 = %d，期望 500", recorder.Code)
	}
	apiErr := decodeAPIError(t, recorder)
	if apiErr.Error.Code != "internal_error" {
		t.Fatalf("错误码 = %q，期望 internal_error", apiErr.Error.Code)
	}
	if apiErr.Error.Message != "服务暂时异常，请稍后重试" {
		t.Fatalf("内部错误文案 = %q", apiErr.Error.Message)
	}
}

// ===== 原文件下载 =====

// TestContentInvalidID 非法/非正 ID 按 404 处理，不触库。
func TestContentInvalidID(t *testing.T) {
	env := newHTTPTestEnv(t)
	for _, raw := range []string{"abc", "0", "-3", ""} {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/"+raw+"/content")
		withPathParams(c, map[string]string{"id": raw})
		env.handler.Content(c)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("非法 ID %q 状态码 = %d，期望 404", raw, recorder.Code)
		}
		if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != "not_found" {
			t.Fatalf("非法 ID 错误码 = %q，期望 not_found", apiErr.Error.Code)
		}
	}
	if got := len(env.stub.recorded()); got != 0 {
		t.Fatalf("非法 ID 不应触库，实际查询 %d 次", got)
	}
}

// TestContentNotFoundAndDatabaseError 覆盖附件不存在与数据库错误。
func TestContentNotFoundAndDatabaseError(t *testing.T) {
	t.Run("附件不存在", func(t *testing.T) {
		env := newHTTPTestEnv(t)
		env.expect(noAttachmentResult())
		c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/42/content")
		withPathParams(c, map[string]string{"id": "42"})
		env.handler.Content(c)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d，期望 404", recorder.Code)
		}
	})
	t.Run("数据库错误", func(t *testing.T) {
		env := newHTTPTestEnv(t)
		env.expect(dbErrorResult("boom"))
		c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/42/content")
		withPathParams(c, map[string]string{"id": "42"})
		env.handler.Content(c)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("状态码 = %d，期望 500", recorder.Code)
		}
		if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != "internal_error" {
			t.Fatalf("错误码 = %q", apiErr.Error.Code)
		}
	})
}

// TestContentDeniedByPostVisibility 附件继承帖子可见性，不可见时 404。
func TestContentDeniedByPostVisibility(t *testing.T) {
	env := newHTTPTestEnv(t)
	postID := int64(9)
	row := testAttachmentRow(42, 7)
	row.PostID = &postID
	env.expect(attachmentResult(row))
	c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/42/content")
	withPathParams(c, map[string]string{"id": "42"})
	env.handler.Content(c)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("不可见附件状态码 = %d，期望 404，body=%s", recorder.Code, recorder.Body.String())
	}
}

// TestContentImageSuccess 覆盖头像附件（公开）成功下载：响应头与文件流。
func TestContentImageSuccess(t *testing.T) {
	env := newHTTPTestEnv(t)
	payload := testPNG(t, 8, 8)
	row := testAttachmentRow(42, 7)
	row.Purpose = domain.PurposeAvatar
	row.StorageKey = env.saveBytes(payload)
	env.expect(attachmentResult(row))

	c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/42/content")
	withPathParams(c, map[string]string{"id": "42"})
	env.handler.Content(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q，期望 image/png", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, max-age=300" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != "inline" {
		t.Fatalf("Content-Disposition = %q，期望 inline", got)
	}
	if !bytesEqual(recorder.Body.Bytes(), payload) {
		t.Fatalf("下载内容长度 = %d，期望 %d", recorder.Body.Len(), len(payload))
	}
}

// TestContentFileDisposition 普通文件强制下载并使用 ASCII 文件名头。
func TestContentFileDisposition(t *testing.T) {
	env := newHTTPTestEnv(t)
	payload := []byte("hello timeline\n")
	row := testAttachmentRow(42, 7)
	row.Purpose = domain.PurposeAvatar
	row.Kind = domain.KindFile
	row.MediaType = "text/plain; charset=utf-8"
	row.OriginalFilename = "report.txt"
	row.StorageKey = env.saveBytes(payload)
	env.expect(attachmentResult(row))

	c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/42/content")
	withPathParams(c, map[string]string{"id": "42"})
	env.handler.Content(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Disposition"); got != `attachment; filename="report.txt"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
}

// TestContentPhysicalFileMissing 物理文件缺失按不存在处理（不泄露存储路径）。
func TestContentPhysicalFileMissing(t *testing.T) {
	env := newHTTPTestEnv(t)
	row := testAttachmentRow(42, 7)
	row.Purpose = domain.PurposeAvatar
	env.expect(attachmentResult(row))
	c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/42/content")
	withPathParams(c, map[string]string{"id": "42"})
	env.handler.Content(c)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("物理文件缺失状态码 = %d，期望 404", recorder.Code)
	}
}

// ===== 衍生文件下载 =====

// TestDerivativeInvalidParams 非法衍生类型或 ID 均按 404 处理且不触库。
func TestDerivativeInvalidParams(t *testing.T) {
	env := newHTTPTestEnv(t)
	cases := []struct{ id, derivativeType string }{
		{"42", "poster"},
		{"42", ""},
		{"abc", model.DerivativeThumbnail},
		{"0", model.DerivativeThumbnail},
	}
	for _, tc := range cases {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/"+tc.id+"/derivatives/"+tc.derivativeType)
		withPathParams(c, map[string]string{"id": tc.id, "type": tc.derivativeType})
		env.handler.Derivative(c)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("参数 id=%q type=%q 状态码 = %d，期望 404", tc.id, tc.derivativeType, recorder.Code)
		}
		if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != "not_found" {
			t.Fatalf("错误码 = %q，期望 not_found", apiErr.Error.Code)
		}
	}
	if got := len(env.stub.recorded()); got != 0 {
		t.Fatalf("非法参数不应触库，实际查询 %d 次", got)
	}
}

// TestDerivativePendingThenSuccess 覆盖未就绪 404 与就绪后的 JPEG 流式响应。
func TestDerivativePendingThenSuccess(t *testing.T) {
	t.Run("尚未生成", func(t *testing.T) {
		env := newHTTPTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), derivativeResult(repo.Derivative{
			ID: 5, AttachmentID: 42, DerivativeType: model.DerivativeThumbnail,
			Status: model.DerivativePending, CreatedAt: testNow, UpdatedAt: testNow,
		}))
		c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/42/derivatives/thumbnail")
		withPathParams(c, map[string]string{"id": "42", "type": model.DerivativeThumbnail})
		env.handler.Derivative(c)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("未就绪状态码 = %d，期望 404", recorder.Code)
		}
	})
	t.Run("就绪返回图片流", func(t *testing.T) {
		env := newHTTPTestEnv(t)
		payload := []byte("fake-jpeg-bytes")
		row := testAttachmentRow(42, 7)
		row.Purpose = domain.PurposeAvatar
		key := env.saveBytes(payload)
		env.expect(attachmentResult(row), derivativeResult(repo.Derivative{
			ID: 5, AttachmentID: 42, DerivativeType: model.DerivativeThumbnail,
			Status: model.DerivativeReady, StorageKey: key,
			CreatedAt: testNow, UpdatedAt: testNow,
		}))
		c, recorder := newTestContext(http.MethodGet, "/api/v1/attachments/42/derivatives/thumbnail")
		withPathParams(c, map[string]string{"id": "42", "type": model.DerivativeThumbnail})
		env.handler.Derivative(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Content-Type"); got != "image/jpeg" {
			t.Fatalf("Content-Type = %q，期望 image/jpeg", got)
		}
		if got := recorder.Header().Get("Content-Disposition"); got != "inline" {
			t.Fatalf("Content-Disposition = %q，期望 inline", got)
		}
		if !bytesEqual(recorder.Body.Bytes(), payload) {
			t.Fatalf("衍生文件内容 = %q，期望 %q", recorder.Body.String(), payload)
		}
	})
}

// ===== 删除 =====

// TestDeleteUnauthenticated 未登录删除返回 401。
func TestDeleteUnauthenticated(t *testing.T) {
	env := newHTTPTestEnv(t)
	c, recorder := newTestContext(http.MethodPost, "/api/v1/attachments/42/delete")
	withPathParams(c, map[string]string{"id": "42"})
	env.handler.Delete(c)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录删除状态码 = %d，期望 401", recorder.Code)
	}
	if got := len(env.stub.recorded()); got != 0 {
		t.Fatalf("未登录删除不应触库，实际查询 %d 次", got)
	}
}

// TestDeleteInvalidID 非法 ID 返回 404。
func TestDeleteInvalidID(t *testing.T) {
	env := newHTTPTestEnv(t)
	c, recorder := newTestContext(http.MethodPost, "/api/v1/attachments/abc/delete")
	withPathParams(c, map[string]string{"id": "abc"})
	withViewer(c, humanViewer(7))
	env.handler.Delete(c)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("非法 ID 状态码 = %d，期望 404", recorder.Code)
	}
	if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != "not_found" {
		t.Fatalf("错误码 = %q，期望 not_found", apiErr.Error.Code)
	}
}

// TestDeleteSuccessJSON 覆盖删除成功的 JSON 结果与幂等字段。
func TestDeleteSuccessJSON(t *testing.T) {
	env := newHTTPTestEnv(t)
	env.content.detached = true
	env.expect(
		attachmentResult(testAttachmentRow(42, 7)),
		commandResult("UPDATE 1"),
		commandResult("UPDATE 0"),
	)
	c, recorder := newTestContext(http.MethodPost, "/api/v1/attachments/42/delete")
	withPathParams(c, map[string]string{"id": "42"})
	withViewer(c, humanViewer(7))
	env.handler.Delete(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("删除状态码 = %d，期望 200，body=%s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	if body["detached"] != true || body["deleted"] != true {
		t.Fatalf("删除响应 = %v，期望 detached/deleted 均为 true", body)
	}
}

// TestDeleteReferencedByVersion 仍被历史版本引用时只解除草稿关联。
func TestDeleteReferencedByVersion(t *testing.T) {
	env := newHTTPTestEnv(t)
	env.content.detached = true
	env.content.referenced = true
	env.expect(attachmentResult(testAttachmentRow(42, 7)))
	c, recorder := newTestContext(http.MethodPost, "/api/v1/attachments/42/delete")
	withPathParams(c, map[string]string{"id": "42"})
	withViewer(c, humanViewer(7))
	env.handler.Delete(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", recorder.Code)
	}
	body := decodeBody(t, recorder)
	if body["detached"] != true || body["deleted"] != false {
		t.Fatalf("删除响应 = %v，期望 detached=true、deleted=false", body)
	}
}

// TestDeleteNotFound 非上传者或记录不存在（含数据库错误）的错误映射。
func TestDeleteNotFound(t *testing.T) {
	t.Run("附件不存在", func(t *testing.T) {
		env := newHTTPTestEnv(t)
		env.expect(noAttachmentResult())
		c, recorder := newTestContext(http.MethodPost, "/api/v1/attachments/42/delete")
		withPathParams(c, map[string]string{"id": "42"})
		withViewer(c, humanViewer(7))
		env.handler.Delete(c)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d，期望 404", recorder.Code)
		}
	})
	t.Run("数据库错误", func(t *testing.T) {
		env := newHTTPTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), dbErrorResult("update 失败"))
		c, recorder := newTestContext(http.MethodPost, "/api/v1/attachments/42/delete")
		withPathParams(c, map[string]string{"id": "42"})
		withViewer(c, humanViewer(7))
		env.handler.Delete(c)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("状态码 = %d，期望 500", recorder.Code)
		}
		if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != "internal_error" {
			t.Fatalf("错误码 = %q", apiErr.Error.Code)
		}
	})
}

// ===== 错误映射与辅助函数 =====

// TestRespondErrorMapping respondError 是 413/415 与领域错误的唯一映射点。
func TestRespondErrorMapping(t *testing.T) {
	env := newHTTPTestEnv(t)
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantHeader string
	}{
		{"内容过大", domain.ErrContentTooLarge, http.StatusRequestEntityTooLarge, "content_too_large", ""},
		{"配额已满", domain.ErrStorageQuotaExhausted, http.StatusRequestEntityTooLarge, "content_too_large", ""},
		{"超过配额", domain.ErrStorageQuotaExceeded, http.StatusRequestEntityTooLarge, "content_too_large", ""},
		{"类型不支持", domain.ErrUnsupportedMediaType, http.StatusUnsupportedMediaType, "unsupported_media_type", ""},
		{"领域 NotFound", apperr.NotFound("附件不存在"), http.StatusNotFound, "not_found", ""},
		{"领域 Validation", apperr.Validation("校验失败"), http.StatusBadRequest, "validation_failed", ""},
		{"领域未登录", apperr.Unauthenticated("请先登录"), http.StatusUnauthorized, "unauthenticated", ""},
		{"限频带重试时间", apperr.RateLimited(2 * time.Second), http.StatusTooManyRequests, "rate_limited", "2"},
		{"未知错误", errors.New("boom"), http.StatusInternalServerError, "internal_error", ""},
		{"nil 错误", nil, http.StatusInternalServerError, "internal_error", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newTestContext(http.MethodPost, "/api/v1/attachments")
			env.handler.respondError(c, tc.err)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("状态码 = %d，期望 %d，body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if apiErr := decodeAPIError(t, recorder); apiErr.Error.Code != tc.wantCode {
				t.Fatalf("错误码 = %q，期望 %q", apiErr.Error.Code, tc.wantCode)
			}
			if tc.wantHeader != "" {
				if got := recorder.Header().Get("Retry-After"); got != tc.wantHeader {
					t.Fatalf("Retry-After = %q，期望 %q", got, tc.wantHeader)
				}
			}
		})
	}
}

// TestViewerFromContext 未解析登录态按游客处理。
func TestViewerFromContext(t *testing.T) {
	c, _ := newTestContext(http.MethodGet, "/api/v1/attachments/42/content")
	if viewer := viewerFromContext(c); viewer != model.GuestViewer() {
		t.Fatalf("未写入手身份时应为游客，实际 %+v", viewer)
	}
	withViewer(c, humanViewer(7))
	if viewer := viewerFromContext(c); viewer.ID != 7 || !viewer.Authenticated {
		t.Fatalf("已写入手身份 = %+v，期望 ID=7", viewer)
	}
}

// TestAttachmentIDParsing 路径 ID 解析：非法返回 false 并写 404。
func TestAttachmentIDParsing(t *testing.T) {
	env := newHTTPTestEnv(t)
	for _, raw := range []string{"abc", "0", "-1", ""} {
		c, recorder := newTestContext(http.MethodGet, "/")
		withPathParams(c, map[string]string{"id": raw})
		if _, ok := env.handler.attachmentID(c); ok {
			t.Fatalf("非法 ID %q 应解析失败", raw)
		}
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("非法 ID %q 状态码 = %d，期望 404", raw, recorder.Code)
		}
	}
	c, _ := newTestContext(http.MethodGet, "/")
	withPathParams(c, map[string]string{"id": "42"})
	if id, ok := env.handler.attachmentID(c); !ok || id != 42 {
		t.Fatalf("合法 ID 解析 = %d/%v，期望 42/true", id, ok)
	}
}

// bytesEqual 比较字节切片内容。
func bytesEqual(got, want []byte) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
