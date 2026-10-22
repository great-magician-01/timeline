package app

// 本文件是 media/app 新增单元测试共用的无数据库脚手架：
//   - 假端口：Settings/Auth/Jobs/Audit 与 media 自声明的 ContentPort；
//   - 真实本地存储（t.TempDir）、假时钟（platform/clock.Fake）与确定性 ID 生成器；
//   - mediaTestEnv 把 Service 与所有假端口装配在一起，并暴露 stub 结果排期入口。
//
// 已有的 cleanup_test.go/generate_test.go/imaging_test.go 中的 fake/helper 被复用
// （cleanupSettings、fakeContentPort、discardLogger、fixedClock），本文件不重复声明。

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/platform/storage"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// testNow 是 app 测试统一使用的应用时间（北京时间）。
var testNow = time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)

// ===== 假端口 =====

// stubSettings 是固定返回指定系统设置（或注入错误）的 SettingsReader。
type stubSettings struct {
	values contracts.SystemSettings
	err    error
}

func (f *stubSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	if f.err != nil {
		return contracts.SystemSettings{}, f.err
	}
	return f.values, nil
}

func (f *stubSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	return contracts.WebSearchSettings{}, nil
}

// stubAuth 记录头像/横幅更新参数与清理流程读取的头像引用附件 ID。
type stubAuth struct {
	contracts.AuthService

	setAvatarErr error
	avatarInputs []contracts.AvatarUpdateInput

	setBannerErr error
	bannerInputs []contracts.BannerUpdateInput

	avatarIDs    []int64
	avatarIDsErr error
}

func (f *stubAuth) SetAvatar(_ context.Context, in contracts.AvatarUpdateInput) error {
	f.avatarInputs = append(f.avatarInputs, in)
	return f.setAvatarErr
}

func (f *stubAuth) SetBanner(_ context.Context, in contracts.BannerUpdateInput) error {
	f.bannerInputs = append(f.bannerInputs, in)
	return f.setBannerErr
}

func (f *stubAuth) ListAvatarAttachmentIDs(context.Context) ([]int64, error) {
	return f.avatarIDs, f.avatarIDsErr
}

// stubJobs 记录入队的衍生文件作业。
type stubJobs struct {
	contracts.JobsQueue

	enqueueErr error
	enqueued   []contracts.JobEnqueue
}

func (f *stubJobs) Enqueue(_ context.Context, in contracts.JobEnqueue) error {
	f.enqueued = append(f.enqueued, in)
	return f.enqueueErr
}

// stubAudit 记录管理审计条目。
type stubAudit struct {
	contracts.AuditWriter

	err     error
	entries []contracts.AuditEntry
}

func (f *stubAudit) Write(_ context.Context, entry contracts.AuditEntry) error {
	f.entries = append(f.entries, entry)
	return f.err
}

// stubContent 是 media 自己声明的 ContentPort 的假实现，可按用例注入可见性、
// 草稿关联与版本引用结果，并记录调用参数。
type stubContent struct {
	ContentPort

	visiblePosts map[int64]model.Post
	visibleErr   error

	detached      bool
	detachErr     error
	referenced    bool
	referencedErr error

	activeDraft    []int64
	activeDraftErr error

	detachedIDs   []int64
	detachedAt    time.Time
	versionChecks []int64
	draftQueries  [][]int64
}

func (f *stubContent) GetVisiblePost(_ context.Context, _ model.Viewer, postID int64) (model.Post, error) {
	if f.visibleErr != nil {
		return model.Post{}, f.visibleErr
	}
	post, ok := f.visiblePosts[postID]
	if !ok {
		return model.Post{}, apperr.NotFound("内容不存在或无权访问")
	}
	return post, nil
}

func (f *stubContent) DetachAttachmentFromDrafts(_ context.Context, attachmentID int64, at time.Time) (bool, error) {
	f.detachedIDs = append(f.detachedIDs, attachmentID)
	f.detachedAt = at
	return f.detached, f.detachErr
}

func (f *stubContent) AttachmentReferencedByVersion(_ context.Context, attachmentID int64) (bool, error) {
	f.versionChecks = append(f.versionChecks, attachmentID)
	return f.referenced, f.referencedErr
}

func (f *stubContent) ActiveDraftAttachmentIDs(_ context.Context, attachmentIDs []int64) ([]int64, error) {
	f.draftQueries = append(f.draftQueries, append([]int64(nil), attachmentIDs...))
	return f.activeDraft, f.activeDraftErr
}

// stubIDs 生成确定性的附件/衍生文件 ID。
type stubIDs struct {
	next int64
	err  error
}

func (f *stubIDs) Next() (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.next++
	return 100 + f.next, nil
}

// testPoolProvider 把构造好的测试连接池提供给 app.Service。
type testPoolProvider struct{ pool *pgxpool.Pool }

func (p testPoolProvider) Pool() *pgxpool.Pool { return p.pool }

// ===== 测试环境 =====

// testSystemSettings 返回一组能通过上传校验的默认系统设置。
func testSystemSettings() contracts.SystemSettings {
	return contracts.SystemSettings{
		MaxUploadBytes:          10 << 20,
		MaxRequestBytes:         12 << 20,
		StorageQuotaBytes:       1 << 30,
		UploadRateLimit:         100,
		UploadRateWindow:        time.Hour,
		TempAttachmentRetention: 7 * 24 * time.Hour,
	}
}

// mediaTestEnv 汇总一次 media/app 用例测试所需的 Service 与全部假端口。
type mediaTestEnv struct {
	t        *testing.T
	stub     *pgStub
	svc      *Service
	store    *storage.Store
	clock    *clock.Fake
	settings *stubSettings
	auth     *stubAuth
	jobs     *stubJobs
	audit    *stubAudit
	content  *stubContent
	ids      *stubIDs
}

// newMediaTestEnv 构造使用内存协议桩与真实临时存储的 Service。
func newMediaTestEnv(t *testing.T) *mediaTestEnv {
	t.Helper()
	pool, stub := newPgStub(t)
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("创建临时存储失败: %v", err)
	}
	env := &mediaTestEnv{
		t:        t,
		stub:     stub,
		store:    store,
		clock:    clock.NewFake(testNow),
		settings: &stubSettings{values: testSystemSettings()},
		auth:     &stubAuth{},
		jobs:     &stubJobs{},
		audit:    &stubAudit{},
		content:  &stubContent{},
		ids:      &stubIDs{},
	}
	env.svc = New(Deps{
		Pools:    testPoolProvider{pool: pool},
		Tx:       db.NewTxManager(pool),
		Clock:    env.clock,
		IDs:      env.ids,
		Log:      discardLogger(),
		Storage:  store,
		Jobs:     env.jobs,
		Audit:    env.audit,
		Settings: env.settings,
		Auth:     env.auth,
		Content:  env.content,
	})
	return env
}

// expect 排期业务查询结果（BEGIN/COMMIT/ROLLBACK 由协议桩自动应答）。
func (e *mediaTestEnv) expect(results ...pgResult) { e.stub.expect(results...) }

// saveBytes 把内容写入真实临时存储并返回存储键（使用假时钟的日期目录）。
func (e *mediaTestEnv) saveBytes(payload []byte) string {
	e.t.Helper()
	key, _, _, err := e.store.Save(bytes.NewReader(payload), storage.DefaultMaxUploadBytes, e.clock.Now())
	if err != nil {
		e.t.Fatalf("保存测试文件到临时存储失败: %v", err)
	}
	return key
}

// ===== 通用夹具与断言辅助 =====

// testAttachmentRow 返回一条可用的帖子附件行。
func testAttachmentRow(id, uploaderID int64) repo.AttachmentRow {
	return repo.AttachmentRow{
		Attachment: model.Attachment{
			ID:               id,
			UploaderID:       uploaderID,
			Kind:             domain.KindImage,
			OriginalFilename: "photo.png",
			MediaType:        "image/png",
			SizeBytes:        128,
			StorageKey:       testStorageKey(id),
			Status:           model.AttachmentAvailable,
			CreatedAt:        testNow,
			UpdatedAt:        testNow,
		},
		Purpose: domain.PurposePost,
	}
}

// testStorageKey 生成符合 storage 安全键规则的确定性键。
func testStorageKey(id int64) string {
	return fmt.Sprintf("2026/10/18/%032x.png", id)
}

// testPNG 返回一张纯色 PNG 图片字节。
func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 40, G: 120, B: 200, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("编码测试 PNG 失败: %v", err)
	}
	return encoded.Bytes()
}

// testJPEG 返回一张纯色 JPEG 图片字节。
func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 200, G: 120, B: 40, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("编码测试 JPEG 失败: %v", err)
	}
	return encoded.Bytes()
}

// testTextBytes 返回会被识别为普通文本文件的字节。
func testTextBytes() []byte { return []byte("hello timeline\n") }

// testHTMLBytes 返回会被存储层拒绝的 HTML 内容（防存储型 XSS）。
func testHTMLBytes() []byte { return []byte("<html><body>x</body></html>") }

// testWebPBytes 返回 WebP 头部字节：可存为图片但标准库无法解码缩略图。
func testWebPBytes() []byte { return append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), 0x00) }

// testMP4Bytes 返回最小 MP4 头部字节（ftyp isom），会被识别为视频。
func testMP4Bytes() []byte { return append([]byte("\x00\x00\x00\x18ftypisom"), 0x00) }

// guestViewer 返回游客访问者。
func guestViewer() model.Viewer { return model.GuestViewer() }

// humanViewer 返回已登录人类用户访问者。
func humanViewer(id int64) model.Viewer {
	return model.Viewer{ID: id, Authenticated: true}
}

// adminViewer 返回管理员访问者。
func adminViewer(id int64) model.Viewer {
	return model.Viewer{ID: id, IsAdmin: true, Authenticated: true}
}

// aiViewer 返回 AI 访问者（不能写）。
func aiViewer(id int64) model.Viewer {
	return model.Viewer{ID: id, IsAI: true, Authenticated: true}
}

// requireKind 断言错误是期望 Kind 的领域错误。
func requireKind(t *testing.T, err error, want apperr.Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望领域错误 %s，实际 nil", want)
	}
	domainErr, ok := apperr.AsKind(err)
	if !ok {
		t.Fatalf("期望领域错误 %s，实际 %v", want, err)
	}
	if domainErr.Kind != want {
		t.Fatalf("期望 Kind=%s，实际 Kind=%s（%v）", want, domainErr.Kind, err)
	}
}
