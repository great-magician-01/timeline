package http

// 本文件是 media/http 的无数据库测试脚手架：
//   - 内存 PostgreSQL 简单查询协议桩（与 app 包测试同构），让 Handler 背后的真实
//     app.Service 能走完读取/写入路径，而不需要真实数据库或网络；
//   - app.Deps 需要的假端口（Settings/Auth/Jobs/Audit/Content/IDs）；
//   - gin 测试上下文、multipart 请求、统一错误响应解码与文件存储夹具。
//
// 说明：Handler 只依赖具体 *app.Service，没有可注入的端口；因此用真实 Service +
// 协议桩才能覆盖成功路径（201 响应、文件流、Content-Disposition 等）。

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/modules/media/app"
	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/platform/storage"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

func init() { gin.SetMode(gin.TestMode) }

// testNow 是 http 测试统一使用的应用时间（北京时间）。
var testNow = time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)

// ===== 假端口 =====

// fakeSettings 是固定返回指定系统设置（或注入错误）的 SettingsReader。
type fakeSettings struct {
	values contracts.SystemSettings
	err    error
}

func (f *fakeSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	if f.err != nil {
		return contracts.SystemSettings{}, f.err
	}
	return f.values, nil
}

func (f *fakeSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	return contracts.WebSearchSettings{}, nil
}

// fakeAuth 记录头像/横幅回写与清理读取的头像引用附件。
type fakeAuth struct {
	contracts.AuthService

	setAvatarErr error
	avatarInputs []contracts.AvatarUpdateInput

	setBannerErr error
	bannerInputs []contracts.BannerUpdateInput

	avatarIDs    []int64
	avatarIDsErr error
}

func (f *fakeAuth) SetAvatar(_ context.Context, in contracts.AvatarUpdateInput) error {
	f.avatarInputs = append(f.avatarInputs, in)
	if f.setAvatarErr != nil {
		return f.setAvatarErr
	}
	return nil
}

func (f *fakeAuth) SetBanner(_ context.Context, in contracts.BannerUpdateInput) error {
	f.bannerInputs = append(f.bannerInputs, in)
	if f.setBannerErr != nil {
		return f.setBannerErr
	}
	return nil
}

func (f *fakeAuth) ListAvatarAttachmentIDs(context.Context) ([]int64, error) {
	return f.avatarIDs, f.avatarIDsErr
}

// fakeJobs 记录衍生文件作业入队。
type fakeJobs struct {
	contracts.JobsQueue

	enqueueErr error
	enqueued   []contracts.JobEnqueue
}

func (f *fakeJobs) Enqueue(_ context.Context, in contracts.JobEnqueue) error {
	f.enqueued = append(f.enqueued, in)
	return f.enqueueErr
}

// fakeAudit 记录管理审计条目。
type fakeAudit struct {
	contracts.AuditWriter

	err     error
	entries []contracts.AuditEntry
}

func (f *fakeAudit) Write(_ context.Context, entry contracts.AuditEntry) error {
	f.entries = append(f.entries, entry)
	return f.err
}

// fakeContent 是 media ContentPort 的假实现。
type fakeContent struct {
	app.ContentPort

	visiblePosts map[int64]model.Post
	visibleErr   error

	detached      bool
	detachErr     error
	referenced    bool
	referencedErr error

	activeDraft    []int64
	activeDraftErr error
}

func (f *fakeContent) GetVisiblePost(_ context.Context, _ model.Viewer, postID int64) (model.Post, error) {
	if f.visibleErr != nil {
		return model.Post{}, f.visibleErr
	}
	post, ok := f.visiblePosts[postID]
	if !ok {
		return model.Post{}, apperr.NotFound("内容不存在或无权访问")
	}
	return post, nil
}

func (f *fakeContent) DetachAttachmentFromDrafts(context.Context, int64, time.Time) (bool, error) {
	return f.detached, f.detachErr
}

func (f *fakeContent) AttachmentReferencedByVersion(context.Context, int64) (bool, error) {
	return f.referenced, f.referencedErr
}

func (f *fakeContent) ActiveDraftAttachmentIDs(context.Context, []int64) ([]int64, error) {
	return f.activeDraft, f.activeDraftErr
}

// fakeIDs 生成确定性的附件/衍生文件 ID。
type fakeIDs struct {
	next int64
	err  error
}

func (f *fakeIDs) Next() (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.next++
	return 100 + f.next, nil
}

// testPoolProvider 把测试连接池提供给 app.Service。
type testPoolProvider struct{ pool *pgxpool.Pool }

func (p testPoolProvider) Pool() *pgxpool.Pool { return p.pool }

// ===== 测试环境 =====

// testSystemSettings 返回一组默认上传设置。
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

// httpTestEnv 汇总一次 handler 测试的 Handler 与全部假端口。
type httpTestEnv struct {
	t        *testing.T
	stub     *pgStub
	handler  *Handler
	store    *storage.Store
	clock    *clock.Fake
	settings *fakeSettings
	auth     *fakeAuth
	jobs     *fakeJobs
	audit    *fakeAudit
	content  *fakeContent
	ids      *fakeIDs
}

// newHTTPTestEnv 构造 Handler；options 可在组装 app.Service 前调整依赖（例如置空 Jobs）。
func newHTTPTestEnv(t *testing.T, options ...func(*app.Deps)) *httpTestEnv {
	t.Helper()
	pool, stub := newPgStub(t)
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("创建临时存储失败: %v", err)
	}
	env := &httpTestEnv{
		t:        t,
		stub:     stub,
		store:    store,
		clock:    clock.NewFake(testNow),
		settings: &fakeSettings{values: testSystemSettings()},
		auth:     &fakeAuth{},
		jobs:     &fakeJobs{},
		audit:    &fakeAudit{},
		content:  &fakeContent{},
		ids:      &fakeIDs{},
	}
	deps := app.Deps{
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
	}
	for _, option := range options {
		option(&deps)
	}
	env.handler = New(app.New(deps))
	return env
}

// expect 排期业务查询结果（BEGIN/COMMIT/ROLLBACK 由协议桩自动应答）。
func (e *httpTestEnv) expect(results ...pgResult) { e.stub.expect(results...) }

// saveBytes 把内容写入真实临时存储并返回存储键。
func (e *httpTestEnv) saveBytes(payload []byte) string {
	e.t.Helper()
	key, _, _, err := e.store.Save(bytes.NewReader(payload), storage.DefaultMaxUploadBytes, e.clock.Now())
	if err != nil {
		e.t.Fatalf("保存测试文件失败: %v", err)
	}
	return key
}

// ===== gin/HTTP 请求辅助 =====

// newTestContext 构造 gin 测试上下文。
func newTestContext(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, recorder
}

// withViewer 写入已认证访问者身份。
func withViewer(c *gin.Context, viewer model.Viewer) *gin.Context {
	httpx.SetViewer(c, viewer)
	return c
}

// withPathParams 写入路径参数。
func withPathParams(c *gin.Context, params map[string]string) *gin.Context {
	for key, value := range params {
		c.Params = append(c.Params, gin.Param{Key: key, Value: value})
	}
	return c
}

// newUploadRequest 构造 multipart 上传请求；filename 为空表示不附带 file 字段。
func newUploadRequest(t *testing.T, viewer model.Viewer, filename, purpose string, content []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if filename != "" {
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("构造 multipart 文件字段失败: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("写入 multipart 内容失败: %v", err)
		}
	}
	if purpose != "" {
		if err := writer.WriteField("purpose", purpose); err != nil {
			t.Fatalf("写入 multipart purpose 字段失败: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart body 失败: %v", err)
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/attachments", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	if viewer.Authenticated || viewer.ID != 0 {
		httpx.SetViewer(c, viewer)
	}
	return c, recorder
}

// apiError 是统一错误响应结构。
type apiError struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// decodeAPIError 解析统一错误响应。
func decodeAPIError(t *testing.T, recorder *httptest.ResponseRecorder) apiError {
	t.Helper()
	var body apiError
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v，body=%s", err, recorder.Body.String())
	}
	return body
}

// decodeBody 解析 JSON 响应为 map。
func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v，body=%s", err, recorder.Body.String())
	}
	return body
}

// ===== 通用夹具 =====

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

// testStorageKey 生成符合 storage 键规则的确定性键。
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

// testHTMLBytes 返回会被存储层拒绝的 HTML 内容。
func testHTMLBytes() []byte { return []byte("<html><body>x</body></html>") }

// humanViewer 返回已登录人类用户访问者。
func humanViewer(id int64) model.Viewer { return model.Viewer{ID: id, Authenticated: true} }

// guestViewer 返回游客访问者。
func guestViewer() model.Viewer { return model.GuestViewer() }

// discardLogger 返回丢弃输出的日志器。
// 说明：app 包测试中的同名 helper 不在 http 包内，这里必须独立声明。
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// ===== 内存 PostgreSQL 协议桩（与 app 包测试同构，按包隔离复制） =====

const (
	oidInt8        = 20
	oidInt4        = 23
	oidText        = 25
	oidTimestamptz = 1184
)

type pgColumn struct {
	name string
	oid  uint32
}

type pgResult struct {
	columns []pgColumn
	rows    [][]any
	tag     string
	err     string
}

type pgStub struct {
	mu      sync.Mutex
	queue   []pgResult
	queries []string
	conns   map[net.Conn]struct{}
	wg      sync.WaitGroup
}

func newPgStub(t *testing.T) (*pgxpool.Pool, *pgStub) {
	t.Helper()
	stub := &pgStub{conns: map[net.Conn]struct{}{}}
	cfg, err := pgxpool.ParseConfig("postgres://timeline:secret@pgstub:5432/timeline?sslmode=disable")
	if err != nil {
		t.Fatalf("解析测试 DSN 失败: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.LookupFunc = func(context.Context, string) ([]string, error) {
		return []string{"127.0.0.1"}, nil
	}
	cfg.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		stub.mu.Lock()
		stub.conns[server] = struct{}{}
		stub.mu.Unlock()
		stub.wg.Add(1)
		go stub.serve(server)
		return client, nil
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("创建测试连接池失败: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		stub.mu.Lock()
		for conn := range stub.conns {
			_ = conn.Close()
		}
		stub.mu.Unlock()
		stub.wg.Wait()
	})
	return pool, stub
}

func (s *pgStub) expect(results ...pgResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, results...)
}

func (s *pgStub) pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *pgStub) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func (s *pgStub) next() (pgResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return pgResult{}, false
	}
	result := s.queue[0]
	s.queue = s.queue[1:]
	return result, true
}

func (s *pgStub) serve(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	var header [8]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return
	}
	length := int(binary.BigEndian.Uint32(header[0:4]))
	if length < 8 || length > 1<<20 {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, length-8)); err != nil {
		return
	}
	writeStartup(conn)

	for {
		typ, payload, err := readMessage(conn)
		if err != nil {
			return
		}
		switch typ {
		case 'Q':
			s.handleQuery(conn, payload)
		case 'X':
			return
		default:
			writeErrorResponse(conn, fmt.Sprintf("测试协议桩不支持消息 %q，只实现简单查询协议", typ))
			writeReadyForQuery(conn)
		}
	}
}

func (s *pgStub) handleQuery(conn net.Conn, payload []byte) {
	query := strings.TrimSpace(strings.TrimRight(string(payload), "\x00"))
	s.mu.Lock()
	s.queries = append(s.queries, query)
	s.mu.Unlock()

	switch strings.ToLower(query) {
	case "begin", "commit", "rollback":
		writeCommandComplete(conn, strings.ToUpper(strings.ToLower(query)))
		writeReadyForQuery(conn)
		return
	}

	result, ok := s.next()
	if !ok {
		writeErrorResponse(conn, "测试协议桩没有为该查询准备结果: "+query)
		writeReadyForQuery(conn)
		return
	}
	if result.err != "" {
		writeErrorResponse(conn, result.err)
		writeReadyForQuery(conn)
		return
	}
	if len(result.columns) > 0 {
		writeRowDescription(conn, result.columns)
		for _, row := range result.rows {
			writeDataRow(conn, row)
		}
		writeCommandComplete(conn, "SELECT "+strconv.Itoa(len(result.rows)))
	} else {
		tag := result.tag
		if tag == "" {
			tag = "OK"
		}
		writeCommandComplete(conn, tag)
	}
	writeReadyForQuery(conn)
}

func writeStartup(w io.Writer) {
	writeInt32Payload(w, 'R', 0)
	writeParameterStatus(w, "server_version", "16.0")
	writeParameterStatus(w, "client_encoding", "UTF8")
	writeParameterStatus(w, "DateStyle", "ISO, MDY")
	writeParameterStatus(w, "standard_conforming_strings", "on")
	writeInt32Payload(w, 'K', 1, 2)
	writeReadyForQuery(w)
}

func writeParameterStatus(w io.Writer, key, value string) {
	payload := append([]byte(key), 0)
	payload = append(payload, []byte(value)...)
	payload = append(payload, 0)
	writePayload(w, 'S', payload)
}

func writeInt32Payload(w io.Writer, typ byte, values ...int32) {
	payload := make([]byte, len(values)*4)
	for i, value := range values {
		binary.BigEndian.PutUint32(payload[i*4:], uint32(value))
	}
	writePayload(w, typ, payload)
}

func writeReadyForQuery(w io.Writer) { writePayload(w, 'Z', []byte{'I'}) }

func writePayload(w io.Writer, typ byte, payload []byte) {
	message := make([]byte, 5+len(payload))
	message[0] = typ
	binary.BigEndian.PutUint32(message[1:5], uint32(4+len(payload)))
	copy(message[5:], payload)
	_, _ = w.Write(message)
}

func readMessage(r io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	length := int(binary.BigEndian.Uint32(header[1:5])) - 4
	if length < 0 || length > 1<<20 {
		return 0, nil, errors.New("非法的协议消息长度")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

func writeRowDescription(w io.Writer, columns []pgColumn) {
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, uint16(len(columns)))
	for _, column := range columns {
		payload = append(payload, []byte(column.name)...)
		payload = append(payload, 0)
		field := make([]byte, 18)
		binary.BigEndian.PutUint32(field[6:10], column.oid)
		binary.BigEndian.PutUint16(field[10:12], 0xFFFF)
		binary.BigEndian.PutUint32(field[12:16], 0xFFFFFFFF)
		payload = append(payload, field...)
	}
	writePayload(w, 'T', payload)
}

func writeDataRow(w io.Writer, row []any) {
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, uint16(len(row)))
	for _, value := range row {
		if value == nil {
			payload = append(payload, 0xFF, 0xFF, 0xFF, 0xFF)
			continue
		}
		text := encodeTextValue(value)
		size := make([]byte, 4)
		binary.BigEndian.PutUint32(size, uint32(len(text)))
		payload = append(payload, size...)
		payload = append(payload, text...)
	}
	writePayload(w, 'D', payload)
}

func writeCommandComplete(w io.Writer, tag string) {
	writePayload(w, 'C', append([]byte(tag), 0))
}

func writeErrorResponse(w io.Writer, message string) {
	payload := []byte{'S'}
	payload = append(payload, "ERROR"...)
	payload = append(payload, 0, 'V')
	payload = append(payload, "ERROR"...)
	payload = append(payload, 0, 'C')
	payload = append(payload, "XX000"...)
	payload = append(payload, 0, 'M')
	payload = append(payload, []byte(message)...)
	payload = append(payload, 0, 0)
	writePayload(w, 'E', payload)
}

func encodeTextValue(value any) []byte {
	switch typed := value.(type) {
	case string:
		return []byte(typed)
	case int64:
		return []byte(strconv.FormatInt(typed, 10))
	case int:
		return []byte(strconv.Itoa(typed))
	case bool:
		if typed {
			return []byte("t")
		}
		return []byte("f")
	case time.Time:
		return []byte(typed.UTC().Format("2006-01-02 15:04:05.999999-07"))
	default:
		return []byte(fmt.Sprint(typed))
	}
}

var attachmentRowColumns = []pgColumn{
	{"id", oidInt8}, {"uploader_id", oidInt8}, {"post_id", oidInt8},
	{"kind", oidText}, {"purpose", oidText}, {"original_filename", oidText},
	{"media_type", oidText}, {"size_bytes", oidInt8}, {"storage_key", oidText},
	{"status", oidText}, {"width", oidInt4}, {"height", oidInt4},
	{"duration_ms", oidInt8}, {"created_at", oidTimestamptz}, {"updated_at", oidTimestamptz},
}

var derivativeRowColumns = []pgColumn{
	{"id", oidInt8}, {"attachment_id", oidInt8}, {"derivative_type", oidText},
	{"status", oidText}, {"storage_key", oidText}, {"last_error", oidText},
	{"attempts", oidInt4}, {"created_at", oidTimestamptz}, {"updated_at", oidTimestamptz},
}

var usageColumn = []pgColumn{{"used_bytes", oidInt8}}

func attachmentResult(row repo.AttachmentRow) pgResult {
	return pgResult{columns: attachmentRowColumns, rows: [][]any{attachmentRowValues(row)}}
}

func noAttachmentResult() pgResult { return pgResult{columns: attachmentRowColumns} }

func derivativeResult(row repo.Derivative) pgResult {
	return pgResult{
		columns: derivativeRowColumns,
		rows: [][]any{{
			row.ID, row.AttachmentID, row.DerivativeType, row.Status, row.StorageKey,
			row.LastError, int(row.Attempts), row.CreatedAt, row.UpdatedAt,
		}},
	}
}

// usageResult 返回上传者已用存储空间（模拟 repo.Usage 的 attachment_usage 点查）。
func usageResult(total int64) pgResult {
	return pgResult{columns: usageColumn, rows: [][]any{{total}}}
}

func commandResult(tag string) pgResult { return pgResult{tag: tag} }

func dbErrorResult(message string) pgResult { return pgResult{err: message} }

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func attachmentRowValues(row repo.AttachmentRow) []any {
	return []any{
		row.ID, row.UploaderID, nullableInt64(row.PostID), row.Kind, row.Purpose,
		row.OriginalFilename, row.MediaType, row.SizeBytes, row.StorageKey,
		row.Status, nullableInt(row.Width), nullableInt(row.Height),
		nullableInt64(row.DurationMS), row.CreatedAt, row.UpdatedAt,
	}
}
