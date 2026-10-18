package http

// admin http 层的无数据库测试脚手架：用 app.New(假 Deps) 构造 *app.Service，再用
// gin.CreateTestContext + httptest 驱动 handler，只覆盖参数解析/鉴权/错误映射与
// 不依赖 ReadModel 的成功路径。
//
// Read 是具体 *repo.ReadModel（只读 DB、不可 fake），因此读取类接口只测参数校验
// 与「校验在触库前失败」的路径；触库成功路径由集成测试覆盖。

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/admin/app"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	platformsettings "github.com/great-magician-01/timeline/internal/platform/settings"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

func init() { gin.SetMode(gin.TestMode) }

// ===== 测试上下文与响应断言辅助 =====

func newTestContext(method, path string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, nil)
	return c, recorder
}

func newJSONContext(method, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

// withViewer 写入已认证访问者身份（路由层在真实请求中完成登录态解析）。
func withViewer(c *gin.Context, viewer model.Viewer) *gin.Context {
	httpx.SetViewer(c, viewer)
	return c
}

func adminViewer() model.Viewer {
	return model.Viewer{ID: 1, Authenticated: true, IsAdmin: true}
}

func setPathID(c *gin.Context, value string) {
	c.Params = gin.Params{{Key: "id", Value: value}}
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v，body=%s", err, recorder.Body.String())
	}
	return body.Error.Code
}

func errorDetails(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析错误响应失败: %v，body=%s", err, recorder.Body.String())
	}
	return body.Error.Details
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v，body=%s", err, recorder.Body.String())
	}
	return body
}

// ===== 假端口 =====

// fakeTx 直接执行回调，模拟已开启的事务。
type fakeTx struct{}

func (fakeTx) WithTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeAudit struct{ entries []contracts.AuditEntry }

func (f *fakeAudit) Write(_ context.Context, entry contracts.AuditEntry) error {
	f.entries = append(f.entries, entry)
	return nil
}

func (f *fakeAudit) actions() []string {
	actions := make([]string, 0, len(f.entries))
	for _, entry := range f.entries {
		actions = append(actions, entry.Action)
	}
	return actions
}

// fakeAuth 是 auth 端口的最小假实现：账号启停/重置密码会写入唯一一条审计。
type fakeAuth struct {
	contracts.AuthService

	audit *fakeAudit
	user  model.User

	getErr    error
	statusErr error
	resetErr  error

	statusInput *contracts.UserStatusInput
	resetInput  *contracts.ResetPasswordInput
}

func (f *fakeAuth) GetUser(_ context.Context, userID int64) (model.User, error) {
	if f.getErr != nil {
		return model.User{}, f.getErr
	}
	if f.user.ID == 0 || f.user.ID != userID {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return f.user, nil
}

func (f *fakeAuth) SetUserStatus(_ context.Context, in contracts.UserStatusInput) (model.User, error) {
	f.statusInput = &in
	if f.statusErr != nil {
		return model.User{}, f.statusErr
	}
	updated := f.user
	action := contracts.AuditActionUserUnsuspend
	if in.Suspend {
		updated.Status = model.StatusSuspended
		action = contracts.AuditActionUserSuspend
	} else {
		updated.Status = model.StatusActive
	}
	f.user = updated
	targetID := in.TargetID
	return updated, f.audit.Write(context.Background(), contracts.AuditEntry{
		AdminID:    in.ActorID,
		Action:     action,
		TargetType: "user",
		TargetID:   &targetID,
		At:         in.At,
	})
}

func (f *fakeAuth) ResetPassword(_ context.Context, in contracts.ResetPasswordInput) error {
	f.resetInput = &in
	if f.resetErr != nil {
		return f.resetErr
	}
	targetID := in.TargetID
	return f.audit.Write(context.Background(), contracts.AuditEntry{
		AdminID:    in.ActorID,
		Action:     contracts.AuditActionUserResetPwd,
		TargetType: "user",
		TargetID:   &targetID,
		At:         in.At,
	})
}

// fakeSettings 是系统设置端口的可注入错误内存实现。
type fakeSettings struct {
	values    contracts.SystemSettings
	webSearch contracts.WebSearchSettings
	sets      []contracts.SettingsWrite

	settingsErr  error
	webSearchErr error
	setErr       error
}

func (f *fakeSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	if f.settingsErr != nil {
		return contracts.SystemSettings{}, f.settingsErr
	}
	return f.values, nil
}

func (f *fakeSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	if f.webSearchErr != nil {
		return contracts.WebSearchSettings{}, f.webSearchErr
	}
	return f.webSearch, nil
}

func (f *fakeSettings) Set(_ context.Context, in contracts.SettingsWrite) error {
	f.sets = append(f.sets, in)
	if f.setErr != nil {
		return f.setErr
	}
	switch in.Key {
	case contracts.SettingKeyAIGlobalEnabled:
		if value, ok := in.Value.(bool); ok {
			f.values.AIGlobalEnabled = value
		}
	case contracts.SettingKeyRegistrationEnabled:
		if value, ok := in.Value.(bool); ok {
			f.values.RegistrationEnabled = value
		}
	case contracts.SettingKeyWebSearchBaseURL:
		if value, ok := in.Value.(string); ok {
			f.webSearch.BaseURL = value
		}
	case contracts.SettingKeyWebSearchAPIKeyCipher:
		if value, ok := in.Value.(string); ok {
			f.webSearch.APIKeyCipher = value
		}
	}
	return nil
}

// fakeGlobalAI 模拟 agent SetGlobal：记录调用并写唯一一条全局开关审计。
type fakeGlobalAI struct {
	audit       *fakeAudit
	haltCalls   int
	enableCalls int
}

func (f *fakeGlobalAI) HaltGlobalAI(_ context.Context, actorID int64, at time.Time) error {
	f.haltCalls++
	return f.audit.Write(context.Background(), contracts.AuditEntry{
		AdminID: actorID, Action: contracts.AuditActionGlobalAIStop, TargetType: "system_settings", At: at,
	})
}

func (f *fakeGlobalAI) EnableGlobalAI(_ context.Context, actorID int64, at time.Time) error {
	f.enableCalls++
	return f.audit.Write(context.Background(), contracts.AuditEntry{
		AdminID: actorID, Action: contracts.AuditActionGlobalAIStart, TargetType: "system_settings", At: at,
	})
}

// fakeContent/social/media 只实现 admin 处置实际调用的删除方法。
type fakeContent struct {
	contracts.ContentService

	deleteErr error
	deleted   []contracts.DeletePostInput
}

func (f *fakeContent) ListPostIDsByAuthor(context.Context, int64) ([]int64, error) {
	// 停用账号编排会查询该账号帖子以暂停定时发布作业；http 层测试不关心该细节。
	return nil, nil
}

func (f *fakeContent) DeletePostAsAdmin(_ context.Context, in contracts.DeletePostInput) error {
	f.deleted = append(f.deleted, in)
	return f.deleteErr
}

type fakeSocial struct {
	contracts.SocialService

	deleteErr error
	deleted   []contracts.DeleteCommentInput
}

func (f *fakeSocial) DeleteCommentAsAdmin(_ context.Context, in contracts.DeleteCommentInput) error {
	f.deleted = append(f.deleted, in)
	return f.deleteErr
}

type fakeMedia struct {
	contracts.MediaService

	deleteErr error
	deleted   []contracts.DeleteAttachmentInput
}

func (f *fakeMedia) DeleteAttachmentAsAdmin(_ context.Context, in contracts.DeleteAttachmentInput) error {
	f.deleted = append(f.deleted, in)
	return f.deleteErr
}

// fakeJobs 只实现任务列表失败恢复与取消所需的作业端口方法。
type fakeJobs struct {
	contracts.JobsQueue

	job       contracts.JobSnapshot
	getErr    error
	retryErr  error
	cancelErr error

	retried  []contracts.JobActionInput
	canceled []contracts.JobActionInput
}

func (f *fakeJobs) GetJob(_ context.Context, jobID int64) (contracts.JobSnapshot, error) {
	if f.getErr != nil {
		return contracts.JobSnapshot{}, f.getErr
	}
	current := f.job
	current.ID = jobID
	return current, nil
}

func (f *fakeJobs) RetryJob(_ context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	f.retried = append(f.retried, in)
	if f.retryErr != nil {
		return contracts.JobSnapshot{}, f.retryErr
	}
	updated := f.job
	updated.ID = in.JobID
	updated.Status = contracts.JobStatusPending
	return updated, nil
}

func (f *fakeJobs) CancelJob(_ context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	f.canceled = append(f.canceled, in)
	if f.cancelErr != nil {
		return contracts.JobSnapshot{}, f.cancelErr
	}
	updated := f.job
	updated.ID = in.JobID
	updated.Status = contracts.JobStatusCanceled
	return updated, nil
}

// fakeCipher 是测试用敏感配置加密器。
type fakeCipher struct {
	err        error
	canDecrypt bool
}

func (f fakeCipher) EncryptSecret(plain string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "v1:" + plain, nil
}

func (f fakeCipher) CanDecryptSecret(encoded string) bool {
	if encoded == "" {
		return true
	}
	return f.canDecrypt
}

// ===== 测试环境 =====

// validSystemSettings 返回一份能通过 app.validateSettings 的基线设置。
func validSystemSettings() contracts.SystemSettings {
	return contracts.SystemSettings{
		RegistrationEnabled:     true,
		AIGlobalEnabled:         false,
		MaxUploadBytes:          100 << 20,
		MaxRequestBytes:         100<<20 + platformsettings.DefaultUploadOverhead,
		StorageQuotaBytes:       5 << 30,
		PostRateLimit:           30,
		PostRateWindow:          10 * time.Minute,
		UploadRateLimit:         60,
		UploadRateWindow:        10 * time.Minute,
		TempAttachmentRetention: 7 * 24 * time.Hour,
		CallLogMaxBytes:         64 << 10,
		CallLogRetentionDays:    90,
	}
}

// testEnv 是一次 handler 测试的 handler 与全部假端口。
type testEnv struct {
	handler  *Handler
	audit    *fakeAudit
	auth     *fakeAuth
	settings *fakeSettings
	globalAI *fakeGlobalAI
	content  *fakeContent
	social   *fakeSocial
	media    *fakeMedia
	jobs     *fakeJobs
	clock    *clock.Fake
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	audit := &fakeAudit{}
	auth := &fakeAuth{
		audit: audit,
		user: model.User{
			ID: 2, Username: "target", Role: model.RoleUser,
			Status: model.StatusActive, TokenVersion: 1,
		},
	}
	settings := &fakeSettings{values: validSystemSettings()}
	globalAI := &fakeGlobalAI{audit: audit}
	content := &fakeContent{}
	social := &fakeSocial{}
	media := &fakeMedia{}
	jobs := &fakeJobs{}
	clk := clock.NewFake()
	service := app.New(app.Deps{
		Tx:       fakeTx{},
		Clock:    clk,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit:    audit,
		Auth:     auth,
		Content:  content,
		Social:   social,
		Media:    media,
		Jobs:     jobs,
		Settings: settings,
		Cipher:   fakeCipher{canDecrypt: true},
		GlobalAI: globalAI,
	})
	return &testEnv{
		handler: New(service), audit: audit, auth: auth, settings: settings,
		globalAI: globalAI, content: content, social: social, media: media,
		jobs: jobs, clock: clk,
	}
}
