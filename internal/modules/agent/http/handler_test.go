package http

// agent/http Handler 的 httptest 测试：认证与参数校验、错误映射、DTO 组装。
// Handler 依赖 *app.Service，用 fake 端口构造后经 gin 路由驱动，不依赖数据库/网络。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/agent/app"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

var errBoom = errors.New("fake 基础设施错误")

// ===== fake 端口（嵌入接口，只实现 handler 会触达的方法） =====

type fakeContent struct {
	contracts.ContentService
	drafts        []contracts.DraftSummary
	draftsErr     error
	draftQueries  []contracts.DraftListQuery
	post          model.Post
	postErr       error
	updatedDrafts []contracts.UpdateDraftForActorInput
	updateErr     error
	published     []contracts.PublishPostForActorInput
	publishErr    error
}

func (f *fakeContent) ListDraftsByAuthor(_ context.Context, query contracts.DraftListQuery) ([]contracts.DraftSummary, error) {
	f.draftQueries = append(f.draftQueries, query)
	if f.draftsErr != nil {
		return nil, f.draftsErr
	}
	return f.drafts, nil
}

func (f *fakeContent) GetPost(_ context.Context, _ int64) (model.Post, error) {
	if f.postErr != nil {
		return model.Post{}, f.postErr
	}
	return f.post, nil
}

func (f *fakeContent) UpdateDraftForActor(_ context.Context, in contracts.UpdateDraftForActorInput) error {
	f.updatedDrafts = append(f.updatedDrafts, in)
	return f.updateErr
}

func (f *fakeContent) PublishPostForActor(_ context.Context, in contracts.PublishPostForActorInput) error {
	f.published = append(f.published, in)
	return f.publishErr
}

type statusRecord struct {
	taskID int64
	status string
}

type fakeTasks struct {
	app.TaskStore
	tasks       map[int64]repo.Task
	inserted    []repo.TaskInsert
	insertErr   error
	duplicate   bool
	taskList    []repo.Task
	listMore    bool
	listFilters []repo.TaskFilter
	listErr     error
	statuses    []statusRecord
	statusErr   error
	pending     []repo.Task
	pendingErr  error
	memoryRefs  map[int64][]repo.MemoryRef
	refsErr     error
	getTaskErr  error
}

func (f *fakeTasks) InsertTask(_ context.Context, in repo.TaskInsert) (bool, error) {
	f.inserted = append(f.inserted, in)
	if f.insertErr != nil {
		return false, f.insertErr
	}
	if f.duplicate {
		return false, nil
	}
	f.tasks[in.ID] = repo.Task{
		ID:             in.ID,
		AIID:           in.AIID,
		TriggerType:    in.TriggerType,
		TriggerRefType: in.TriggerRefType,
		ActionType:     in.ActionType,
		Status:         "pending",
		ScheduledAt:    in.ScheduledAt,
		CreatedAt:      in.CreatedAt,
	}
	return true, nil
}

func (f *fakeTasks) GetTask(_ context.Context, taskID int64) (repo.Task, error) {
	if f.getTaskErr != nil {
		return repo.Task{}, f.getTaskErr
	}
	task, ok := f.tasks[taskID]
	if !ok {
		return repo.Task{}, apperr.NotFound("AI 任务不存在")
	}
	return task, nil
}

func (f *fakeTasks) ListTasks(_ context.Context, filter repo.TaskFilter) ([]repo.Task, bool, error) {
	f.listFilters = append(f.listFilters, filter)
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	return f.taskList, f.listMore, nil
}

func (f *fakeTasks) SetTaskStatus(_ context.Context, taskID int64, status, _ string, _ *time.Time) error {
	f.statuses = append(f.statuses, statusRecord{taskID: taskID, status: status})
	if f.statusErr != nil {
		return f.statusErr
	}
	task := f.tasks[taskID]
	task.ID = taskID
	task.Status = status
	f.tasks[taskID] = task
	return nil
}

func (f *fakeTasks) ListPendingTasks(_ context.Context, _ int) ([]repo.Task, error) {
	if f.pendingErr != nil {
		return nil, f.pendingErr
	}
	result := make([]repo.Task, 0, len(f.pending))
	for _, task := range f.pending {
		// 已取消的任务不再出现在 pending 列表中，避免取消循环无法结束。
		if f.tasks[task.ID].Status == "canceled" {
			continue
		}
		result = append(result, task)
	}
	return result, nil
}

func (f *fakeTasks) ListMemoryRefs(_ context.Context, taskID int64) ([]repo.MemoryRef, error) {
	if f.refsErr != nil {
		return nil, f.refsErr
	}
	return f.memoryRefs[taskID], nil
}

func (f *fakeTasks) ListMemoryRefsByTaskIDs(_ context.Context, taskIDs []int64) (map[int64][]repo.MemoryRef, error) {
	if f.refsErr != nil {
		return nil, f.refsErr
	}
	refs := make(map[int64][]repo.MemoryRef, len(taskIDs))
	for _, taskID := range taskIDs {
		if taskRefs, ok := f.memoryRefs[taskID]; ok {
			refs[taskID] = taskRefs
		}
	}
	return refs, nil
}

type fakeAI struct {
	contracts.AIService
	runtime    contracts.Runtime
	runtimeErr error
}

func (f *fakeAI) RuntimeByAI(_ context.Context, aiID int64) (contracts.Runtime, error) {
	if f.runtimeErr != nil {
		return contracts.Runtime{}, f.runtimeErr
	}
	runtime := f.runtime
	runtime.AIID = aiID
	return runtime, nil
}

func (f *fakeAI) ProfileEnabled(_ context.Context, _ int64) (bool, error) {
	return f.runtime.Profile.IsEnabled, nil
}

type fakeJobs struct {
	contracts.JobsQueue
	enqueued    []contracts.JobEnqueue
	enqueueErr  error
	byRef       []contracts.JobSnapshot
	refErr      error
	pausedAll   []contracts.PauseAllPendingInput
	pauseErr    error
	canceled    []int64
	cancelErr   error
	retried     []int64
	retryErr    error
	reactivated []string
	reactivate  bool
}

func (f *fakeJobs) Enqueue(_ context.Context, in contracts.JobEnqueue) error {
	f.enqueued = append(f.enqueued, in)
	return f.enqueueErr
}

func (f *fakeJobs) ListByRef(_ context.Context, _ contracts.JobRefQuery) ([]contracts.JobSnapshot, error) {
	if f.refErr != nil {
		return nil, f.refErr
	}
	return f.byRef, nil
}

func (f *fakeJobs) PauseAllPending(_ context.Context, in contracts.PauseAllPendingInput) (int, error) {
	f.pausedAll = append(f.pausedAll, in)
	return 1, f.pauseErr
}

func (f *fakeJobs) CancelJob(_ context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	f.canceled = append(f.canceled, in.JobID)
	return contracts.JobSnapshot{ID: in.JobID}, f.cancelErr
}

func (f *fakeJobs) RetryJob(_ context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	f.retried = append(f.retried, in.JobID)
	return contracts.JobSnapshot{ID: in.JobID, Status: contracts.JobStatusPending}, f.retryErr
}

func (f *fakeJobs) Reactivate(_ context.Context, idempotencyKey string, _, _ time.Time) (bool, error) {
	f.reactivated = append(f.reactivated, idempotencyKey)
	return f.reactivate, nil
}

type fakeCallLogs struct {
	app.CallLogStore
	listResult  []repo.CallLog
	listMore    bool
	listFilters []repo.CallLogFilter
	listErr     error
	getResult   repo.CallLog
	getErr      error
}

func (f *fakeCallLogs) ListCallLogs(_ context.Context, filter repo.CallLogFilter) ([]repo.CallLog, bool, error) {
	f.listFilters = append(f.listFilters, filter)
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	return f.listResult, f.listMore, nil
}

func (f *fakeCallLogs) GetCallLog(_ context.Context, _ int64) (repo.CallLog, error) {
	if f.getErr != nil {
		return repo.CallLog{}, f.getErr
	}
	return f.getResult, nil
}

type fakeSettings struct {
	contracts.SettingsReader
	contracts.SettingsWriter
	values contracts.SystemSettings
	writes []contracts.SettingsWrite
	setErr error
}

func (f *fakeSettings) Settings(_ context.Context) (contracts.SystemSettings, error) {
	return f.values, nil
}

func (f *fakeSettings) Set(_ context.Context, in contracts.SettingsWrite) error {
	f.writes = append(f.writes, in)
	return f.setErr
}

type fakeAudit struct {
	contracts.AuditWriter
	entries []contracts.AuditEntry
	err     error
}

func (f *fakeAudit) Write(_ context.Context, entry contracts.AuditEntry) error {
	f.entries = append(f.entries, entry)
	return f.err
}

type fakeIDs struct{ next int64 }

func (f *fakeIDs) Next() (int64, error) {
	f.next++
	return f.next, nil
}

// ===== 夹具与请求工具 =====

type handlerFixture struct {
	clk      *clock.Fake
	content  *fakeContent
	ai       *fakeAI
	tasks    *fakeTasks
	jobs     *fakeJobs
	callLogs *fakeCallLogs
	settings *fakeSettings
	audit    *fakeAudit
	handler  *Handler
	router   *gin.Engine
}

func newHandlerFixture() *handlerFixture {
	gin.SetMode(gin.TestMode)
	f := &handlerFixture{
		clk:     clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)),
		content: &fakeContent{},
		ai: &fakeAI{runtime: contracts.Runtime{
			Profile:  contracts.Profile{IsEnabled: true},
			Provider: contracts.ProviderConfig{ID: 1, DefaultModel: "test-model", IsEnabled: true},
			Model:    "test-model",
			Behavior: contracts.BehaviorSettings{AllowOriginalPost: true},
		}},
		tasks:    &fakeTasks{tasks: map[int64]repo.Task{}, memoryRefs: map[int64][]repo.MemoryRef{}},
		jobs:     &fakeJobs{},
		callLogs: &fakeCallLogs{},
		settings: &fakeSettings{values: contracts.SystemSettings{AIGlobalEnabled: true}},
		audit:    &fakeAudit{},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.handler = New(app.New(app.Deps{
		Clock:          f.clk,
		IDs:            &fakeIDs{next: 1000},
		Log:            logger,
		Tasks:          f.tasks,
		CallLogs:       f.callLogs,
		Jobs:           f.jobs,
		AI:             f.ai,
		Content:        f.content,
		Audit:          f.audit,
		Settings:       f.settings,
		SettingsWriter: f.settings,
	}), logger)
	f.router = f.newRouter(true)
	return f
}

// newRouter 构造注册全部 handler 的 gin 引擎；auth=false 时不注入登录身份。
func (f *handlerFixture) newRouter(auth bool) *gin.Engine {
	engine := gin.New()
	if auth {
		engine.Use(func(c *gin.Context) {
			httpx.SetViewer(c, model.Viewer{ID: 100, Authenticated: true, IsAdmin: true})
			c.Next()
		})
	}
	h := f.handler
	engine.POST("/ai/global/start", h.GlobalStart)
	engine.POST("/ai/global/stop", h.GlobalStop)
	engine.POST("/ai/:id/generate", h.GenerateDraft)
	engine.POST("/ai/:id/publish-direct", h.PublishDirect)
	engine.GET("/ai/:id/drafts", h.ListAIDrafts)
	engine.POST("/ai/:id/drafts/:post_id/update", h.UpdateAIDraft)
	engine.POST("/ai/:id/drafts/:post_id/publish", h.PublishAIDraft)
	engine.GET("/ai/:id/tasks", h.ListAITasks)
	engine.POST("/ai/tasks/:id/retry", h.RetryAITask)
	engine.POST("/ai/tasks/:id/cancel", h.CancelAITask)
	engine.GET("/ai/call-logs", h.ListCallLogs)
	engine.GET("/ai/call-logs/:id", h.GetCallLog)
	return engine
}

func doRequest(t *testing.T, engine *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是合法 JSON: %v body=%s", err, recorder.Body.String())
	}
	return payload
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	payload := decodeBody(t, recorder)
	errorBody, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 error 对象: %s", recorder.Body.String())
	}
	code, _ := errorBody["code"].(string)
	return code
}

func TestHandlerNewFillsDefaultLogger(t *testing.T) {
	handler := New(nil, nil)
	if handler.log == nil {
		t.Fatal("nil logger 应回退默认 logger")
	}
}

func TestHandlerGlobalSwitchError(t *testing.T) {
	f := newHandlerFixture()
	f.settings.setErr = errBoom
	recorder := doRequest(t, f.router, stdhttp.MethodPost, "/ai/global/start", nil)
	if recorder.Code != stdhttp.StatusInternalServerError {
		t.Fatalf("设置写入失败应返回 500，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerRequiresLogin(t *testing.T) {
	f := newHandlerFixture()
	engine := f.newRouter(false)
	requests := []struct {
		method string
		path   string
	}{
		{stdhttp.MethodPost, "/ai/global/stop"},
		{stdhttp.MethodPost, "/ai/1/generate"},
		{stdhttp.MethodPost, "/ai/1/drafts/2/update"},
		{stdhttp.MethodPost, "/ai/1/drafts/2/publish"},
		{stdhttp.MethodPost, "/ai/tasks/1/retry"},
		{stdhttp.MethodPost, "/ai/tasks/1/cancel"},
	}
	for _, item := range requests {
		recorder := doRequest(t, engine, item.method, item.path, nil)
		if recorder.Code != stdhttp.StatusUnauthorized {
			t.Fatalf("%s %s 未登录应返回 401，实际 %d", item.method, item.path, recorder.Code)
		}
		if code := errorCode(t, recorder); code != "unauthenticated" {
			t.Fatalf("错误码错误: %s", code)
		}
	}
}

func TestHandlerGlobalSwitch(t *testing.T) {
	f := newHandlerFixture()
	recorder := doRequest(t, f.router, stdhttp.MethodPost, "/ai/global/start", nil)
	if recorder.Code != stdhttp.StatusOK {
		t.Fatalf("打开全局开关应返回 200，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeBody(t, recorder)
	if payload["ai_global_enabled"] != true {
		t.Fatalf("响应状态错误: %+v", payload)
	}
	if len(f.settings.writes) != 1 || f.settings.writes[0].Key != contracts.SettingKeyAIGlobalEnabled {
		t.Fatalf("应写全局开关设置: %+v", f.settings.writes)
	}
	if len(f.audit.entries) != 1 || f.audit.entries[0].Action != contracts.AuditActionGlobalAIStart {
		t.Fatalf("应写全局开启审计: %+v", f.audit.entries)
	}

	f.tasks.pending = []repo.Task{{ID: 1}, {ID: 2}}
	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/global/stop", nil)
	if recorder.Code != stdhttp.StatusOK {
		t.Fatalf("关闭全局开关应返回 200，实际 %d", recorder.Code)
	}
	if len(f.jobs.pausedAll) != 2 {
		t.Fatalf("关闭开关应暂停两类作业: %+v", f.jobs.pausedAll)
	}
	if len(f.tasks.statuses) != 2 {
		t.Fatalf("关闭开关应取消 pending 任务: %+v", f.tasks.statuses)
	}
}

func TestHandlerGenerateDraftAndPublishDirect(t *testing.T) {
	f := newHandlerFixture()
	f.tasks.memoryRefs[1001] = []repo.MemoryRef{{MemoryID: 77, MemoryType: "recent"}}
	recorder := doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/generate", []byte(`{}`))
	if recorder.Code != stdhttp.StatusCreated {
		t.Fatalf("生成草稿应返回 201，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeBody(t, recorder)
	task, ok := payload["task"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 task: %+v", payload)
	}
	if task["id"] != "1001" || task["trigger_ref_type"] != "draft" || task["trigger_type"] != "manual" {
		t.Fatalf("任务响应字段错误: %+v", task)
	}
	refs, ok := task["memory_refs"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("任务响应记忆引用错误: %+v", task["memory_refs"])
	}
	if len(f.tasks.inserted) != 1 || f.tasks.inserted[0].TriggerRefType != "draft" {
		t.Fatalf("生成草稿参数错误: %+v", f.tasks.inserted)
	}

	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/publish-direct", nil)
	if recorder.Code != stdhttp.StatusCreated {
		t.Fatalf("立即发布应返回 201，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	if f.tasks.inserted[1].TriggerRefType != "direct" {
		t.Fatalf("立即发布模式错误: %+v", f.tasks.inserted[1])
	}
}

func TestHandlerGenerateValidation(t *testing.T) {
	f := newHandlerFixture()
	cases := []struct {
		name   string
		path   string
		body   []byte
		status int
		code   string
	}{
		{"路径 ID 非法", "/ai/abc/generate", []byte(`{}`), stdhttp.StatusBadRequest, "invalid_request"},
		{"路径 ID 为零", "/ai/0/generate", []byte(`{}`), stdhttp.StatusBadRequest, "invalid_request"},
		{"请求体非法 JSON", "/ai/1/generate", []byte(`not-json`), stdhttp.StatusBadRequest, "invalid_request"},
		{"未知字段", "/ai/1/generate", []byte(`{"unknown":1}`), stdhttp.StatusBadRequest, "invalid_request"},
		{"定时时间格式非法", "/ai/1/generate", []byte(`{"scheduled_at":"not-a-time"}`), stdhttp.StatusBadRequest, "validation_failed"},
		{"定时时间早于当前", "/ai/1/generate", []byte(`{"scheduled_at":"2020-01-01T00:00:00+08:00"}`), stdhttp.StatusBadRequest, "validation_failed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := doRequest(t, f.router, stdhttp.MethodPost, testCase.path, testCase.body)
			if recorder.Code != testCase.status {
				t.Fatalf("期望 %d，实际 %d body=%s", testCase.status, recorder.Code, recorder.Body.String())
			}
			if code := errorCode(t, recorder); code != testCase.code {
				t.Fatalf("期望错误码 %q，实际 %q", testCase.code, code)
			}
		})
	}

	// 合法定时时间应被接受。
	future := f.clk.Now().Add(time.Hour).Format(time.RFC3339)
	recorder := doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/generate",
		[]byte(`{"scheduled_at":"`+future+`","instruction":"写点东西"}`))
	if recorder.Code != stdhttp.StatusCreated {
		t.Fatalf("合法定时时间应创建任务，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	if f.tasks.inserted[0].ScheduledAt == nil {
		t.Fatalf("定时时间未写入任务: %+v", f.tasks.inserted[0])
	}
}

func TestHandlerGenerateErrorMapping(t *testing.T) {
	f := newHandlerFixture()
	f.ai.runtimeErr = apperr.NotFound("AI 不存在")
	recorder := doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/generate", nil)
	if recorder.Code != stdhttp.StatusNotFound {
		t.Fatalf("AI 不存在应返回 404，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	if code := errorCode(t, recorder); code != "not_found" {
		t.Fatalf("错误码错误: %s", code)
	}

	f = newHandlerFixture()
	f.audit.err = apperr.Unavailable("审计不可用")
	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/generate", nil)
	if recorder.Code != stdhttp.StatusServiceUnavailable {
		t.Fatalf("依赖不可用应返回 503，实际 %d", recorder.Code)
	}

	f = newHandlerFixture()
	f.jobs.enqueueErr = errors.New("未知内部错误")
	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/generate", nil)
	if recorder.Code != stdhttp.StatusInternalServerError {
		t.Fatalf("未知错误应返回 500，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerListAIDrafts(t *testing.T) {
	f := newHandlerFixture()
	postID := int64(10)
	f.content.drafts = []contracts.DraftSummary{
		{Post: model.Post{ID: postID, AuthorID: 1, Status: model.PostStatusDraft, UpdatedAt: f.clk.Now()}, ContentHTML: "<p>正文</p>"},
		{Post: model.Post{ID: 9, AuthorID: 1, Status: model.PostStatusDraft}, ContentHTML: "<p>旧</p>"},
	}
	f.jobs.byRef = []contracts.JobSnapshot{{
		ID: 1, RefID: &postID, Status: contracts.JobStatusPending, RunAt: f.clk.Now().Add(time.Hour),
	}}
	recorder := doRequest(t, f.router, stdhttp.MethodGet, "/ai/1/drafts?limit=1", nil)
	if recorder.Code != stdhttp.StatusOK {
		t.Fatalf("读取草稿应返回 200，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeBody(t, recorder)
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("草稿列表条目错误: %+v", payload)
	}
	if payload["has_more"] != true || payload["next_cursor"] == nil {
		t.Fatalf("hasMore 时应返回 next_cursor: %+v", payload)
	}
	item := items[0].(map[string]any)
	if item["id"] != "10" || item["publish_status"] != contracts.JobStatusPending ||
		item["content_html"] != "<p>正文</p>" || item["scheduled_at"] == nil {
		t.Fatalf("草稿条目字段错误: %+v", item)
	}
	if f.content.draftQueries[0].Limit != 2 {
		t.Fatalf("应多取一条判断 hasMore，查询参数 %+v", f.content.draftQueries[0])
	}
	cursor := payload["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("游标不应为空")
	}

	for _, path := range []string{"/ai/abc/drafts", "/ai/1/drafts?limit=abc", "/ai/1/drafts?cursor=bad"} {
		recorder = doRequest(t, f.router, stdhttp.MethodGet, path, nil)
		if recorder.Code != stdhttp.StatusBadRequest {
			t.Fatalf("非法参数 %s 应返回 400，实际 %d", path, recorder.Code)
		}
	}

	f.content.draftsErr = errBoom
	recorder = doRequest(t, f.router, stdhttp.MethodGet, "/ai/1/drafts", nil)
	if recorder.Code != stdhttp.StatusInternalServerError {
		t.Fatalf("读取失败应返回 500，实际 %d", recorder.Code)
	}
}

func TestHandlerUpdateAndPublishAIDraft(t *testing.T) {
	f := newHandlerFixture()
	f.content.post = model.Post{ID: 10, AuthorID: 1, Status: model.PostStatusDraft}
	recorder := doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/drafts/10/update",
		[]byte(`{"content_format":" markdown ","content_source":"正文内容"}`))
	if recorder.Code != stdhttp.StatusNoContent {
		t.Fatalf("编辑草稿应返回 204，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(f.content.updatedDrafts) != 1 || f.content.updatedDrafts[0].ContentFormat != "markdown" {
		t.Fatalf("编辑参数错误: %+v", f.content.updatedDrafts)
	}
	if f.content.updatedDrafts[0].ContentSource != "正文内容" {
		t.Fatalf("正文未被透传: %+v", f.content.updatedDrafts[0])
	}

	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/drafts/10/publish", nil)
	if recorder.Code != stdhttp.StatusNoContent {
		t.Fatalf("发布草稿应返回 204，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(f.content.published) != 1 || f.content.published[0].ActorID != 100 || f.content.published[0].PostID != 10 {
		t.Fatalf("发布参数错误: %+v", f.content.published)
	}

	// 校验与错误映射。
	cases := []struct {
		name   string
		path   string
		body   []byte
		status int
	}{
		{"编辑：路径 ID 非法", "/ai/1/drafts/abc/update", []byte(`{}`), stdhttp.StatusBadRequest},
		{"编辑：请求体非法", "/ai/1/drafts/10/update", []byte(`not-json`), stdhttp.StatusBadRequest},
		{"发布：post ID 非法", "/ai/1/drafts/0/publish", nil, stdhttp.StatusBadRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := doRequest(t, f.router, stdhttp.MethodPost, testCase.path, testCase.body)
			if recorder.Code != testCase.status {
				t.Fatalf("期望 %d，实际 %d body=%s", testCase.status, recorder.Code, recorder.Body.String())
			}
		})
	}

	f.content.post = model.Post{ID: 10, AuthorID: 2, Status: model.PostStatusDraft}
	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/drafts/10/update", []byte(`{}`))
	if recorder.Code != stdhttp.StatusNotFound {
		t.Fatalf("非该 AI 草稿应返回 404，实际 %d", recorder.Code)
	}
	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/1/drafts/10/publish", nil)
	if recorder.Code != stdhttp.StatusNotFound {
		t.Fatalf("非该 AI 草稿发布应返回 404，实际 %d", recorder.Code)
	}
}

func TestHandlerListAITasks(t *testing.T) {
	f := newHandlerFixture()
	f.tasks.taskList = []repo.Task{{ID: 5, AIID: 1, Status: "failed"}, {ID: 4, AIID: 1}}
	f.tasks.listMore = true
	f.tasks.memoryRefs[5] = []repo.MemoryRef{{MemoryID: 88, MemoryType: "long_term"}}
	recorder := doRequest(t, f.router, stdhttp.MethodGet,
		"/ai/1/tasks?limit=2&trigger_type=manual&action_type=original_post&status=failed", nil)
	if recorder.Code != stdhttp.StatusOK {
		t.Fatalf("读取任务列表应返回 200，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	filter := f.tasks.listFilters[0]
	if filter.AIID != 1 || filter.TriggerType != "manual" || filter.ActionType != "original_post" ||
		filter.Status != "failed" || filter.Limit != 2 {
		t.Fatalf("过滤参数未透传: %+v", filter)
	}
	payload := decodeBody(t, recorder)
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 2 || payload["has_more"] != true {
		t.Fatalf("任务列表响应错误: %+v", payload)
	}
	first := items[0].(map[string]any)
	if first["id"] != "5" {
		t.Fatalf("任务 ID 格式化错误: %+v", first)
	}
	refs, ok := first["memory_refs"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("任务记忆引用错误: %+v", first["memory_refs"])
	}

	for _, path := range []string{"/ai/abc/tasks", "/ai/1/tasks?limit=0", "/ai/1/tasks?limit=51", "/ai/1/tasks?cursor=bad"} {
		recorder = doRequest(t, f.router, stdhttp.MethodGet, path, nil)
		if recorder.Code != stdhttp.StatusBadRequest {
			t.Fatalf("非法参数 %s 应返回 400，实际 %d", path, recorder.Code)
		}
	}

	f.tasks.listErr = errBoom
	recorder = doRequest(t, f.router, stdhttp.MethodGet, "/ai/1/tasks", nil)
	if recorder.Code != stdhttp.StatusInternalServerError {
		t.Fatalf("读取失败应返回 500，实际 %d", recorder.Code)
	}
}

func TestHandlerRetryAndCancelAITask(t *testing.T) {
	f := newHandlerFixture()
	f.tasks.tasks[5] = repo.Task{ID: 5, AIID: 1, Status: "failed"}
	recorder := doRequest(t, f.router, stdhttp.MethodPost, "/ai/tasks/5/retry", nil)
	if recorder.Code != stdhttp.StatusOK {
		t.Fatalf("重试任务应返回 200，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeBody(t, recorder)
	task, ok := payload["task"].(map[string]any)
	if !ok || task["id"] != "5" || task["status"] != "pending" {
		t.Fatalf("重试任务响应错误: %+v", payload)
	}
	if len(f.jobs.enqueued) != 1 {
		t.Fatalf("应补投执行作业: %+v", f.jobs.enqueued)
	}

	f.tasks.tasks[6] = repo.Task{ID: 6, AIID: 1, Status: "running"}
	f.jobs.byRef = []contracts.JobSnapshot{{ID: 88, Status: contracts.JobStatusPending}}
	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/tasks/6/cancel", nil)
	if recorder.Code != stdhttp.StatusOK {
		t.Fatalf("取消任务应返回 200，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(f.jobs.canceled) != 1 || f.jobs.canceled[0] != 88 {
		t.Fatalf("应取消任务作业: %+v", f.jobs.canceled)
	}

	for _, path := range []string{"/ai/tasks/abc/retry", "/ai/tasks/abc/cancel"} {
		recorder = doRequest(t, f.router, stdhttp.MethodPost, path, nil)
		if recorder.Code != stdhttp.StatusBadRequest {
			t.Fatalf("非法任务 ID %s 应返回 400，实际 %d", path, recorder.Code)
		}
	}
	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/tasks/404/retry", nil)
	if recorder.Code != stdhttp.StatusNotFound {
		t.Fatalf("任务不存在应返回 404，实际 %d", recorder.Code)
	}

	f.tasks.tasks[7] = repo.Task{ID: 7, AIID: 1, Status: "succeeded"}
	recorder = doRequest(t, f.router, stdhttp.MethodPost, "/ai/tasks/7/cancel", nil)
	if recorder.Code != stdhttp.StatusBadRequest {
		t.Fatalf("成功任务不可取消应返回 400，实际 %d", recorder.Code)
	}
}

func TestHandlerListCallLogs(t *testing.T) {
	f := newHandlerFixture()
	f.callLogs.listResult = []repo.CallLog{{
		ID: 3, TaskID: int64Ptr(5), AIID: int64Ptr(1), ProviderID: int64Ptr(2), Model: "test-model",
		Params: []byte(`{"max_tokens":1}`), Status: "success", Attempt: 1,
		StartedAt: f.clk.Now(), FinishedAt: f.clk.Now(), CreatedAt: f.clk.Now(),
	}}
	f.callLogs.listMore = true
	from := f.clk.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := f.clk.Now().UTC().Format(time.RFC3339)
	path := "/ai/call-logs?limit=10&ai_id=1&provider_id=2&task_id=5&status=success&error_type=network_error&from=" +
		from + "&to=" + to
	recorder := doRequest(t, f.router, stdhttp.MethodGet, path, nil)
	if recorder.Code != stdhttp.StatusOK {
		t.Fatalf("读取调用记录应返回 200，实际 %d body=%s", recorder.Code, recorder.Body.String())
	}
	filter := f.callLogs.listFilters[0]
	if filter.AIID != 1 || filter.ProviderID != 2 || filter.TaskID != 5 ||
		filter.Status != "success" || filter.ErrorType != "network_error" ||
		filter.From == nil || filter.To == nil || filter.Limit != 10 {
		t.Fatalf("调用记录过滤参数错误: %+v", filter)
	}
	payload := decodeBody(t, recorder)
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 1 || payload["has_more"] != true {
		t.Fatalf("调用记录列表响应错误: %+v", payload)
	}
	item := items[0].(map[string]any)
	if item["id"] != "3" || item["task_id"] != "5" || item["params"] != `{"max_tokens":1}` ||
		item["started_at"] == "" {
		t.Fatalf("调用记录字段错误: %+v", item)
	}

	invalid := []string{
		"/ai/call-logs?limit=abc",
		"/ai/call-logs?cursor=bad",
		"/ai/call-logs?ai_id=abc",
		"/ai/call-logs?provider_id=abc",
		"/ai/call-logs?task_id=abc",
		"/ai/call-logs?from=bad",
		"/ai/call-logs?to=bad",
	}
	for _, path := range invalid {
		recorder = doRequest(t, f.router, stdhttp.MethodGet, path, nil)
		if recorder.Code != stdhttp.StatusBadRequest {
			t.Fatalf("非法参数 %s 应返回 400，实际 %d", path, recorder.Code)
		}
	}

	f.callLogs.listErr = errBoom
	recorder = doRequest(t, f.router, stdhttp.MethodGet, "/ai/call-logs", nil)
	if recorder.Code != stdhttp.StatusInternalServerError {
		t.Fatalf("读取失败应返回 500，实际 %d", recorder.Code)
	}
}

func TestHandlerGetCallLog(t *testing.T) {
	f := newHandlerFixture()
	f.callLogs.getResult = repo.CallLog{
		ID: 7, Model: "test-model", Status: "error", HTTPStatus: intPtr(429),
		ErrorType: "rate_limited", ErrorMessage: "限频", Attempt: 1,
		StartedAt: f.clk.Now(), FinishedAt: f.clk.Now(), CreatedAt: f.clk.Now(),
	}
	recorder := doRequest(t, f.router, stdhttp.MethodGet, "/ai/call-logs/7", nil)
	if recorder.Code != stdhttp.StatusOK {
		t.Fatalf("读取调用记录详情应返回 200，实际 %d", recorder.Code)
	}
	payload := decodeBody(t, recorder)
	entry, ok := payload["call_log"].(map[string]any)
	if !ok || entry["id"] != "7" || entry["http_status"] != float64(429) {
		t.Fatalf("调用记录详情字段错误: %+v", payload)
	}

	recorder = doRequest(t, f.router, stdhttp.MethodGet, "/ai/call-logs/abc", nil)
	if recorder.Code != stdhttp.StatusBadRequest {
		t.Fatalf("非法 ID 应返回 400，实际 %d", recorder.Code)
	}
	f.callLogs.getErr = apperr.NotFound("调用记录不存在")
	recorder = doRequest(t, f.router, stdhttp.MethodGet, "/ai/call-logs/9", nil)
	if recorder.Code != stdhttp.StatusNotFound {
		t.Fatalf("调用记录不存在应返回 404，实际 %d", recorder.Code)
	}
}

func TestTaskResponseFormatting(t *testing.T) {
	createdAt := time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)
	triggerRefID := int64(5)
	task := repo.Task{
		ID: 1, AIID: 2, TriggerType: "manual", TriggerRefType: "draft", TriggerRefID: &triggerRefID,
		ActionType: "original_post", Status: "pending", DecisionSummary: "摘要",
		TargetType: "post", TargetID: int64Ptr(9), ResultRefType: "post", ResultRefID: int64Ptr(10),
		ProviderID: int64Ptr(3), Model: "test-model", Error: "", Attempts: 1, CreatedAt: createdAt,
	}
	response := taskResponse(app.TaskView{Task: task, MemoryRefs: []repo.MemoryRef{{MemoryID: 77, MemoryType: "recent"}}})
	formatIDValue := func(key string) string {
		value, ok := response[key].(*string)
		if !ok || value == nil {
			t.Fatalf("字段 %s 应是格式化 ID 指针: %+v", key, response[key])
		}
		return *value
	}
	if response["id"] != "1" || response["ai_id"] != "2" || formatIDValue("trigger_ref_id") != "5" ||
		formatIDValue("target_id") != "9" || formatIDValue("result_ref_id") != "10" || formatIDValue("provider_id") != "3" {
		t.Fatalf("任务响应 ID 格式化错误: %+v", response)
	}
	isNilTime := func(key string) bool {
		value, ok := response[key].(*string)
		return ok && value == nil
	}
	if !isNilTime("scheduled_at") || !isNilTime("started_at") || !isNilTime("finished_at") {
		t.Fatalf("空时间应为 nil 指针: %+v", response)
	}
	if response["created_at"] != httpx.FormatTime(createdAt) {
		t.Fatalf("创建时间格式化错误: %+v", response["created_at"])
	}
	refs, ok := response["memory_refs"].([]gin.H)
	if !ok || len(refs) != 1 || refs[0]["memory_id"] != "77" {
		t.Fatalf("记忆引用格式错误: %+v", response["memory_refs"])
	}

	callLog := callLogResponse(repo.CallLog{
		ID: 7, Params: []byte("{}"), Status: "success",
		StartedAt: createdAt, FinishedAt: createdAt, CreatedAt: createdAt,
	})
	if callLog["id"] != "7" || callLog["params"] != "{}" || callLog["started_at"] != httpx.FormatTime(createdAt) {
		t.Fatalf("调用记录响应格式错误: %+v", callLog)
	}
	if taskID, ok := callLog["task_id"].(*string); !ok || taskID != nil {
		t.Fatalf("空任务 ID 应为 nil 指针: %+v", callLog["task_id"])
	}
	if aiID, ok := callLog["ai_id"].(*string); !ok || aiID != nil {
		t.Fatalf("空 AI ID 应为 nil 指针: %+v", callLog["ai_id"])
	}
}

func TestParseTimeRange(t *testing.T) {
	from, to, err := parseTimeRange("", "")
	if from != nil || to != nil || err != nil {
		t.Fatalf("空范围应返回 nil: %v %v %v", from, to, err)
	}
	from, to, err = parseTimeRange("2026-10-18T00:00:00+08:00", "2026-10-18T12:00:00+08:00")
	if err != nil || from == nil || to == nil || !from.Before(*to) {
		t.Fatalf("范围解析错误: %v %v %v", from, to, err)
	}
	if _, _, err := parseTimeRange("bad", ""); err == nil {
		t.Fatal("非法 from 应返回错误")
	}
	if _, _, err := parseTimeRange("", "bad"); err == nil {
		t.Fatal("非法 to 应返回错误")
	}
}

func intPtr(value int) *int { return &value }

func int64Ptr(value int64) *int64 { return &value }
