package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/callable"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/aiclient"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ===== 公开端口 fake（嵌入接口，只实现测试用到的能力） =====

type fakeContent struct {
	contracts.ContentService
	posts map[int64]model.Post
}

func (f *fakeContent) GetVisiblePost(_ context.Context, _ model.Viewer, postID int64) (model.Post, error) {
	post, ok := f.posts[postID]
	if !ok {
		return model.Post{}, apperr.NotFound("帖子不存在")
	}
	return post, nil
}

type fakeSocial struct {
	contracts.SocialService
	comments      map[int64]model.Comment
	depths        map[int64]int
	likedPosts    map[int64]bool
	likedComments map[int64]bool
	following     map[int64]bool
}

func (f *fakeSocial) GetComment(_ context.Context, commentID int64) (model.Comment, error) {
	comment, ok := f.comments[commentID]
	if !ok {
		return model.Comment{}, apperr.NotFound("评论不存在")
	}
	return comment, nil
}

func (f *fakeSocial) CommentDepth(_ context.Context, commentID int64) (int, error) {
	return f.depths[commentID], nil
}

func (f *fakeSocial) IsFollowing(_ context.Context, followerID, followeeID int64) (bool, error) {
	return f.following[followeeID], nil
}

func (f *fakeSocial) IsPostLiked(_ context.Context, userID, postID int64) (bool, error) {
	return f.likedPosts[postID], nil
}

func (f *fakeSocial) IsCommentLiked(_ context.Context, userID, commentID int64) (bool, error) {
	return f.likedComments[commentID], nil
}

type fakeUsers struct {
	contracts.UserReader
	users map[int64]model.User
}

func (f *fakeUsers) GetUser(_ context.Context, userID int64) (model.User, error) {
	user, ok := f.users[userID]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return user, nil
}

type fakeAI struct {
	contracts.AIService
	recent       []contracts.Memory
	linked       []contracts.MemorySourceRef
	webSearch    contracts.WebSearchConfig
	webSearchErr error
	runtime      contracts.Runtime

	created              []contracts.CreateMemoryInput
	superseded           []contracts.SupersedeMemoryInput
	addedSources         []contracts.MemorySourceInput
	sourceMemoryIDs      []int64
	activeSourceRefs     []contracts.MemorySourceRef
	invalidated          []contracts.InvalidateBySourceInput
	invalidatedBySources []contracts.InvalidateMemoriesBySourcesInput
}

func (f *fakeAI) RecentMemories(_ context.Context, _ int64, _ int) ([]contracts.Memory, error) {
	return f.recent, nil
}

func (f *fakeAI) CreateMemory(_ context.Context, in contracts.CreateMemoryInput) (contracts.Memory, error) {
	f.created = append(f.created, in)
	return contracts.Memory{ID: 9000, AID: in.AIID, MemoryType: in.MemoryType}, nil
}

func (f *fakeAI) SupersedeMemories(_ context.Context, in contracts.SupersedeMemoryInput) error {
	f.superseded = append(f.superseded, in)
	return nil
}

func (f *fakeAI) AddMemorySources(_ context.Context, _ int64, sources []contracts.MemorySourceInput) error {
	f.addedSources = append(f.addedSources, sources...)
	return nil
}

func (f *fakeAI) ListMemorySources(_ context.Context, memoryIDs []int64) ([]contracts.MemorySourceRef, error) {
	f.sourceMemoryIDs = memoryIDs
	return f.linked, nil
}

func (f *fakeAI) WebSearchConfig(_ context.Context) (contracts.WebSearchConfig, error) {
	return f.webSearch, f.webSearchErr
}

func (f *fakeAI) RuntimeByAI(_ context.Context, aiID int64) (contracts.Runtime, error) {
	runtime := f.runtime
	runtime.AIID = aiID
	return runtime, nil
}

func (f *fakeAI) ListActiveMemorySourceRefs(_ context.Context, _ int64, _ int) ([]contracts.MemorySourceRef, error) {
	return f.activeSourceRefs, nil
}

func (f *fakeAI) InvalidateBySource(_ context.Context, in contracts.InvalidateBySourceInput) error {
	f.invalidated = append(f.invalidated, in)
	return nil
}

func (f *fakeAI) InvalidateMemoriesBySources(
	_ context.Context,
	in contracts.InvalidateMemoriesBySourcesInput,
) (int, error) {
	f.invalidatedBySources = append(f.invalidatedBySources, in)
	return 1, nil
}

// ===== 本模块 store/queue fake =====

type fakeTasks struct {
	TaskStore
	pending  []repo.Task
	running  []repo.Task
	tasks    map[int64]repo.Task
	statuses map[int64]string
}

func (f *fakeTasks) ListPendingTasks(_ context.Context, _ int) ([]repo.Task, error) {
	result := make([]repo.Task, 0, len(f.pending))
	for _, task := range f.pending {
		if f.statuses[task.ID] == domain.TaskStatusCanceled {
			continue
		}
		result = append(result, task)
	}
	return result, nil
}

func (f *fakeTasks) ListTasks(_ context.Context, filter repo.TaskFilter) ([]repo.Task, bool, error) {
	if filter.Status == domain.TaskStatusRunning {
		return f.running, false, nil
	}
	return nil, false, nil
}

func (f *fakeTasks) GetTask(_ context.Context, taskID int64) (repo.Task, error) {
	task, ok := f.tasks[taskID]
	if !ok {
		return repo.Task{}, apperr.NotFound("AI 任务不存在")
	}
	return task, nil
}

func (f *fakeTasks) SetTaskStatus(_ context.Context, taskID int64, status, _ string, _ *time.Time) error {
	if f.statuses == nil {
		f.statuses = map[int64]string{}
	}
	f.statuses[taskID] = status
	return nil
}

func (f *fakeTasks) UpdateTaskResult(_ context.Context, in repo.TaskResultUpdate) error {
	if f.statuses == nil {
		f.statuses = map[int64]string{}
	}
	f.statuses[in.TaskID] = in.Status
	return nil
}

type fakeJobs struct {
	contracts.JobsQueue
	byRef       []contracts.JobSnapshot
	reactivated []string
	enqueued    []string
	pausedAll   []contracts.PauseAllPendingInput
	canceled    []int64
}

func (f *fakeJobs) ListByRef(_ context.Context, _ contracts.JobRefQuery) ([]contracts.JobSnapshot, error) {
	return f.byRef, nil
}

func (f *fakeJobs) Reactivate(_ context.Context, idempotencyKey string, _, _ time.Time) (bool, error) {
	f.reactivated = append(f.reactivated, idempotencyKey)
	// 仅当存在引用该任务的作业（任意终态）时返回 true；否则由调用方回退 Enqueue。
	return len(f.byRef) > 0, nil
}

func (f *fakeJobs) Enqueue(_ context.Context, in contracts.JobEnqueue) error {
	f.enqueued = append(f.enqueued, in.IdempotencyKey)
	return nil
}

func (f *fakeJobs) PauseAllPending(_ context.Context, in contracts.PauseAllPendingInput) (int, error) {
	f.pausedAll = append(f.pausedAll, in)
	return 1, nil
}

func (f *fakeJobs) CancelJob(_ context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	f.canceled = append(f.canceled, in.JobID)
	return contracts.JobSnapshot{ID: in.JobID}, nil
}

type fakeCallLogs struct {
	CallLogStore
	inserted int
}

func (f *fakeCallLogs) InsertCallLog(_ context.Context, _ repo.CallLogInsert) error {
	f.inserted++
	return nil
}

type fakeIDs struct{ next int64 }

func (f *fakeIDs) Next() (int64, error) {
	f.next++
	return f.next, nil
}

// ===== 测试工具 =====

func testClock() *clock.Fake {
	return clock.NewFake(time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai))
}

func activeHuman(id int64) model.User {
	return model.User{ID: id, Username: fmt.Sprintf("u%d", id), Status: model.StatusActive}
}

func publicPost(id, authorID int64) model.Post {
	return model.Post{ID: id, AuthorID: authorID, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic}
}

func strPtr(value string) *string { return &value }

func newScopeService(content *fakeContent, social *fakeSocial, users *fakeUsers) *Service {
	return New(Deps{
		Clock:   testClock(),
		IDs:     &fakeIDs{next: 1000},
		Log:     testLogger(),
		Content: content,
		Social:  social,
		Users:   users,
	})
}

// ===== P0-2 候选范围与目标作者二次授权 =====

func TestBuildProactiveTargetScope(t *testing.T) {
	scope := buildProactiveTargetScope([]contracts.PostCandidate{
		{Post: publicPost(100, 7)},
		{Post: publicPost(200, 8)},
	})
	if !scope.allowsPost(100) || !scope.allowsPost(200) {
		t.Fatal("候选帖应被允许")
	}
	if scope.allowsPost(300) {
		t.Fatal("候选集合之外的帖子不应被允许")
	}
	if !scope.allowsUser(7) || !scope.allowsUser(8) {
		t.Fatal("候选帖作者应被允许")
	}
	if scope.allowsUser(9) {
		t.Fatal("候选集合之外的用户不应被允许")
	}
	var nilScope *proactiveTargetScope
	if !nilScope.allowsPost(999) || !nilScope.allowsUser(999) {
		t.Fatal("nil scope 不应限制目标")
	}
}

func TestValidateTargetScopeAndAuthorChecks(t *testing.T) {
	content := &fakeContent{posts: map[int64]model.Post{
		100: publicPost(100, 7),
		200: publicPost(200, 8),
		300: publicPost(300, 9),
		400: publicPost(400, 10),
	}}
	users := &fakeUsers{users: map[int64]model.User{
		7:  activeHuman(7),
		8:  activeHuman(8),
		9:  {ID: 9, Username: "suspended", Status: model.StatusSuspended},
		10: {ID: 10, Username: "ai_other", Status: model.StatusActive, IsAI: true},
		11: activeHuman(11),
		12: activeHuman(12),
	}}
	social := &fakeSocial{
		comments: map[int64]model.Comment{
			500: {ID: 500, PostID: 200, AuthorID: 8, Status: model.CommentStatusPublished},
		},
		depths:        map[int64]int{500: 1},
		likedPosts:    map[int64]bool{},
		likedComments: map[int64]bool{},
		following:     map[int64]bool{},
	}
	svc := newScopeService(content, social, users)
	rt := contracts.Runtime{AIID: 1, Behavior: contracts.BehaviorSettings{
		AllowAIToAI: false, MaxReplyDepth: 2, AllowLike: true, AllowFollow: true,
	}}
	task := repo.Task{ID: 1, AIID: 1, TriggerType: domain.TriggerProactive}
	scope := buildProactiveTargetScope([]contracts.PostCandidate{
		{Post: publicPost(100, 7)},
	})

	cases := []struct {
		name       string
		decision   domain.Decision
		scope      *proactiveTargetScope
		wantSkip   string
		wantTarget int64
	}{
		{
			name:     "候选范围外的公开帖被拒绝",
			decision: domain.Decision{Action: domain.ActionLike, TargetType: domain.RefPost, TargetID: strPtr("200")},
			scope:    scope, wantSkip: "不在该 AI 的候选范围内",
		},
		{
			name:     "候选范围内的合法目标通过",
			decision: domain.Decision{Action: domain.ActionLike, TargetType: domain.RefPost, TargetID: strPtr("100")},
			scope:    scope, wantTarget: 100,
		},
		{
			name:     "停用作者被拒绝",
			decision: domain.Decision{Action: domain.ActionLike, TargetType: domain.RefPost, TargetID: strPtr("300")},
			wantSkip: "目标作者账号已停用",
		},
		{
			name:     "AI 间互动默认关闭时 AI 作者被拒绝",
			decision: domain.Decision{Action: domain.ActionLike, TargetType: domain.RefPost, TargetID: strPtr("400")},
			wantSkip: "AI 间互动未开启",
		},
		{
			name:     "候选范围外的评论目标被拒绝",
			decision: domain.Decision{Action: domain.ActionReply, TargetType: domain.RefComment, TargetID: strPtr("500")},
			scope:    scope, wantSkip: "不在该 AI 的候选范围内",
		},
		{
			name:     "取消点赞要求当前点赞关系",
			decision: domain.Decision{Action: domain.ActionUnlike, TargetType: domain.RefPost, TargetID: strPtr("100")},
			wantSkip: "不在当前点赞列表中",
		},
		{
			name:     "关注目标必须在候选范围",
			decision: domain.Decision{Action: domain.ActionFollow, TargetType: domain.RefUser, TargetID: strPtr("11")},
			scope:    scope, wantSkip: "不在该 AI 的候选范围内",
		},
		{
			name:     "取关要求当前关注关系",
			decision: domain.Decision{Action: domain.ActionUnfollow, TargetType: domain.RefUser, TargetID: strPtr("12")},
			wantSkip: "不在当前关注列表中",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			target, skip, err := svc.validateTarget(context.Background(), rt, task, testCase.decision, testCase.scope)
			if err != nil {
				t.Fatalf("validateTarget 不应返回基础设施错误: %v", err)
			}
			if testCase.wantSkip != "" {
				if !strings.Contains(skip, testCase.wantSkip) {
					t.Fatalf("期望拒绝原因包含 %q，实际 %q", testCase.wantSkip, skip)
				}
				return
			}
			if skip != "" {
				t.Fatalf("期望通过，实际被拒绝: %q", skip)
			}
			if target.ID != testCase.wantTarget {
				t.Fatalf("目标 ID 期望 %d，实际 %d", testCase.wantTarget, target.ID)
			}
		})
	}
}

func TestValidateTargetCannotFollowSelf(t *testing.T) {
	svc := newScopeService(&fakeContent{}, &fakeSocial{}, &fakeUsers{})
	rt := contracts.Runtime{AIID: 1}
	task := repo.Task{ID: 1, AIID: 1}
	_, skip, err := svc.validateTarget(context.Background(), rt, task, domain.Decision{
		Action: domain.ActionFollow, TargetID: strPtr("1"),
	}, nil)
	if err != nil || !strings.Contains(skip, "不能关注自己") {
		t.Fatalf("期望拒绝关注自己，实际 skip=%q err=%v", skip, err)
	}
}

// ===== P1-2 记忆沉淀约束 =====

func TestConsolidateMemoriesProducesValidMemory(t *testing.T) {
	recent := make([]contracts.Memory, 0, consolidationThreshold)
	for index := 0; index < consolidationThreshold; index++ {
		recent = append(recent, contracts.Memory{
			ID:      int64(100 + index),
			AID:     1,
			Content: strings.Repeat("近期经历内容", 20), // 每条 120 字符，10 条超过 1000 上限
		})
	}
	postID := int64(123)
	commentID := int64(456)
	recent[0].SourceRefType = domain.RefPost
	recent[0].SourceRefID = &postID
	recent[1].SourceRefType = domain.RefComment
	recent[1].SourceRefID = &commentID
	ai := &fakeAI{
		recent: recent,
		linked: []contracts.MemorySourceRef{
			{SourceType: domain.RefPost, SourceID: postID}, // 与直接引用重复
			{SourceType: domain.RefPost, SourceID: 999},    // 只来自 ai_memory_sources
			{SourceType: "consolidation", SourceID: 1},     // 非法来源类型必须被过滤
		},
	}
	svc := New(Deps{Clock: testClock(), Log: testLogger(), AI: ai})
	if err := svc.consolidateMemories(context.Background(), 1); err != nil {
		t.Fatalf("记忆沉淀不应失败: %v", err)
	}
	if len(ai.created) != 1 {
		t.Fatalf("期望写入 1 条长期记忆，实际 %d", len(ai.created))
	}
	created := ai.created[0]
	if created.Category != "event" {
		t.Fatalf("沉淀分类必须是 ai_memories 允许的取值，实际 %q", created.Category)
	}
	if created.MemoryType != "long_term" || created.InferenceLevel != "summary" {
		t.Fatalf("沉淀记忆类型/推断级别错误: %+v", created)
	}
	if runes := len([]rune(created.Content)); runes > memoryContentRuneLimit {
		t.Fatalf("沉淀正文超出 ai 模块上限: %d > %d", runes, memoryContentRuneLimit)
	}
	if len(ai.superseded) != 1 || len(ai.superseded[0].MemoryIDs) != consolidationThreshold {
		t.Fatalf("应把全部近期记忆标记为被取代: %+v", ai.superseded)
	}
	seen := map[string]bool{}
	for _, source := range ai.addedSources {
		if source.SourceType != domain.RefPost && source.SourceType != domain.RefComment {
			t.Fatalf("来源类型必须只是 post/comment（0007 CHECK），实际 %q", source.SourceType)
		}
		key := source.SourceType + ":" + apiID(source.SourceID)
		if seen[key] {
			t.Fatalf("来源关联出现重复: %s", key)
		}
		seen[key] = true
	}
	if !seen["post:123"] || !seen["comment:456"] || !seen["post:999"] {
		t.Fatalf("来源关联聚合错误: %+v", ai.addedSources)
	}
}

func TestConsolidateMemoriesSkipsWhenNoSource(t *testing.T) {
	recent := make([]contracts.Memory, 0, consolidationThreshold)
	for index := 0; index < consolidationThreshold; index++ {
		recent = append(recent, contracts.Memory{ID: int64(100 + index), AID: 1, Content: "普通近期记忆"})
	}
	ai := &fakeAI{recent: recent}
	svc := New(Deps{Clock: testClock(), Log: testLogger(), AI: ai})
	if err := svc.consolidateMemories(context.Background(), 1); err != nil {
		t.Fatalf("无来源的沉淀不应失败: %v", err)
	}
	if len(ai.addedSources) != 0 {
		t.Fatalf("无来源时不应写入来源关联: %+v", ai.addedSources)
	}
}

// ===== P1-1 / P1-14 任务重试与 reconcile =====

func TestFinishTaskReturnsRetryableError(t *testing.T) {
	tasks := &fakeTasks{tasks: map[int64]repo.Task{1: {ID: 1, AIID: 1}}}
	svc := New(Deps{Clock: testClock(), Log: testLogger(), Tasks: tasks})
	result := taskResult{
		Status:       domain.TaskStatusFailed,
		ErrorType:    domain.ErrorTypeModel,
		ErrorMessage: "上游 503",
		Retryable:    true,
	}
	err := svc.finishTask(context.Background(), repo.Task{ID: 1, AIID: 1}, result, 1)
	if err == nil {
		t.Fatal("可恢复失败应返回错误，交给作业层重试")
	}
	if tasks.statuses[1] != domain.TaskStatusFailed {
		t.Fatalf("任务状态应写回 failed，实际 %q", tasks.statuses[1])
	}
}

func TestReconcileReactivatesTerminalJob(t *testing.T) {
	tasks := &fakeTasks{pending: []repo.Task{{ID: 55, AIID: 1}}}
	refID := int64(55)
	jobs := &fakeJobs{byRef: []contracts.JobSnapshot{{ID: 900, RefID: &refID, Status: contracts.JobStatusFailed}}}
	svc := New(Deps{Clock: testClock(), Log: testLogger(), Tasks: tasks, Jobs: jobs})
	if err := svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile 不应失败: %v", err)
	}
	if len(jobs.reactivated) != 1 || jobs.reactivated[0] != "ai_task:55" {
		t.Fatalf("应重激活任务 55 的作业，实际 %+v", jobs.reactivated)
	}
	if len(jobs.enqueued) != 0 {
		t.Fatalf("重激活后不应重复入队: %+v", jobs.enqueued)
	}
}

func TestReconcileEnqueuesMissingJob(t *testing.T) {
	tasks := &fakeTasks{
		pending: []repo.Task{{ID: 56, AIID: 1}},
		tasks:   map[int64]repo.Task{56: {ID: 56, AIID: 1}},
	}
	jobs := &fakeJobs{} // ListByRef 为空、Reactivate 返回 false
	svc := New(Deps{Clock: testClock(), Log: testLogger(), Tasks: tasks, Jobs: jobs})
	if err := svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile 不应失败: %v", err)
	}
	if len(jobs.enqueued) != 1 || jobs.enqueued[0] != "ai_task:56" {
		t.Fatalf("作业缺失时应按幂等键补投，实际 %+v", jobs.enqueued)
	}
}

func TestReconcileRecoversStaleRunningTask(t *testing.T) {
	startedAt := testClock().Now().Add(-30 * time.Minute)
	tasks := &fakeTasks{
		running: []repo.Task{{ID: 57, AIID: 1, Status: domain.TaskStatusRunning, StartedAt: &startedAt}},
		tasks:   map[int64]repo.Task{57: {ID: 57, AIID: 1, StartedAt: &startedAt}},
	}
	jobs := &fakeJobs{}
	svc := New(Deps{Clock: testClock(), Log: testLogger(), Tasks: tasks, Jobs: jobs})
	if err := svc.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile 不应失败: %v", err)
	}
	if tasks.statuses[57] != domain.TaskStatusFailed {
		t.Fatalf("超时 running 任务应标记 failed，实际 %q", tasks.statuses[57])
	}
	if len(jobs.reactivated) != 1 || jobs.reactivated[0] != "ai_task:57" {
		t.Fatalf("应重新投递超时任务作业，实际 %+v", jobs.reactivated)
	}
}

// ===== P1-3 模型覆盖 =====

func TestModelForRuntimePrefersOverride(t *testing.T) {
	rt := contracts.Runtime{Model: "override-model", Provider: contracts.ProviderConfig{DefaultModel: "default-model"}}
	if got := modelForRuntime(rt); got != "override-model" {
		t.Fatalf("应优先使用 model_override，实际 %q", got)
	}
	rt.Model = "  "
	if got := modelForRuntime(rt); got != "default-model" {
		t.Fatalf("model_override 为空时应回退预设默认模型，实际 %q", got)
	}
}

// ===== P1-4 联网搜索工具 =====

func TestWebSearchTopicSanitizes(t *testing.T) {
	rt := contracts.Runtime{Profile: contracts.Profile{Topics: "科技,编程"}}
	prompt := promptInput{Event: "第一行\n第二行\x00控制字符"}
	topic := webSearchTopic(rt, prompt)
	if strings.ContainsAny(topic, "\n\r\t\x00") {
		t.Fatalf("搜索主题必须是单行无控制字符: %q", topic)
	}
	if runes := len([]rune(topic)); runes > maxWebSearchTopicRunes {
		t.Fatalf("搜索主题超长: %d", runes)
	}
	// 主动互动/定时任务没有事件时，用记忆或兴趣标签生成主题。
	fallback := webSearchTopic(rt, promptInput{Recent: []contracts.Memory{{Content: "最近在关注 Go 与数据库"}}})
	if fallback == "" {
		t.Fatal("没有事件时应从记忆生成搜索主题")
	}
}

func TestWebSearchToolValidationAndBudget(t *testing.T) {
	tool := &webSearchTool{}
	cases := []string{
		`{"query":""}`,
		`{"query":"含\n换行"}`,
		`{"query":"` + strings.Repeat("长", 130) + `"}`,
		`{"query":"x","extra":1}`,
	}
	for _, raw := range cases {
		result, _ := tool.run(context.Background(), raw)
		if !result.IsError {
			t.Fatalf("非法搜索参数应返回错误结果: %s", raw)
		}
	}
	tool = &webSearchTool{calls: maxWebSearchCalls}
	result, _ := tool.run(context.Background(), `{"query":"ok"}`)
	if !result.IsError || !strings.Contains(result.Content, "web_search_budget_exhausted") {
		t.Fatalf("搜索次数达到上限后应拒绝: %s", result.Content)
	}
	// 查询包含全局密钥时应拒绝。
	tool = &webSearchTool{apiKey: "global-secret"}
	result, _ = tool.run(context.Background(), `{"query":"global-secret"}`)
	if !result.IsError || !strings.Contains(result.Content, "invalid_query") {
		t.Fatalf("包含密钥的查询应被拒绝: %s", result.Content)
	}
}

func TestNormalizeAssistantToolCallIDs(t *testing.T) {
	message := callableMessageWithToolCall("", "web_search", `{"query":"x"}`)
	normalized := normalizeAssistantToolCallIDs(message)
	calls := normalized.ToolCalls()
	if len(calls) != 1 || calls[0].ID == "" {
		t.Fatalf("缺失 ID 的工具调用应补齐稳定 ID: %+v", calls)
	}
}

// ===== 模型调用与联网搜索降级/轮次上限（httptest fake 服务） =====

type chatServerState struct {
	requests int
	hasTools []bool
	models   []string
}

func newChatServer(t *testing.T, state *chatServerState, handler func(state *chatServerState, request map[string]any) map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.requests++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求体失败: %v", err)
		}
		request := map[string]any{}
		_ = json.Unmarshal(body, &request)
		_, hasTools := request["tools"]
		state.hasTools = append(state.hasTools, hasTools)
		if model, ok := request["model"].(string); ok {
			state.models = append(state.models, model)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(handler(state, request))
	}))
	t.Cleanup(server.Close)
	return server
}

func chatCompletionResponse(message map[string]any, finishReason string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1,
		"model":   "test-model",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	}
}

func toolCallMessage(id, name, args string) map[string]any {
	return map[string]any{
		"role":    "assistant",
		"content": "",
		"tool_calls": []any{map[string]any{
			"id":       id,
			"type":     "function",
			"function": map[string]any{"name": name, "arguments": args},
		}},
	}
}

func newModelTestService(t *testing.T, ai *fakeAI) (*Service, *fakeCallLogs) {
	t.Helper()
	logs := &fakeCallLogs{}
	svc := New(Deps{
		Clock:    testClock(),
		IDs:      &fakeIDs{next: 1000},
		Log:      testLogger(),
		AI:       ai,
		CallLogs: logs,
	})
	return svc, logs
}

func TestCallDecisionModelDegradesWithoutSearchConfig(t *testing.T) {
	state := &chatServerState{}
	chat := newChatServer(t, state, func(_ *chatServerState, _ map[string]any) map[string]any {
		return chatCompletionResponse(map[string]any{
			"role": "assistant", "content": `{"action":"noop","content":"","reason":"无"}`,
		}, "stop")
	})
	ai := &fakeAI{webSearchErr: fmt.Errorf("搜索配置不可用")}
	svc, logs := newModelTestService(t, ai)
	rt := contracts.Runtime{
		AIID: 1,
		Provider: contracts.ProviderConfig{
			ID: 1, BaseURL: chat.URL + "/v1", APIKey: "k", DefaultModel: "test-model", IsEnabled: true,
		},
		Model:    "override-model", // model_override 必须真实用于请求
		Timeout:  5 * time.Second,
		Behavior: contracts.BehaviorSettings{AllowWebSearch: true},
	}
	resp, err := svc.callDecisionModel(context.Background(), rt, 1, promptInput{}, "system", "user", decisionFormat())
	if err != nil {
		t.Fatalf("降级决策不应失败: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Text, "noop") {
		t.Fatalf("应返回普通决策结果: %+v", resp)
	}
	if state.requests != 1 {
		t.Fatalf("降级后应只调用一次模型，实际 %d", state.requests)
	}
	if len(state.hasTools) != 1 || state.hasTools[0] {
		t.Fatalf("降级调用不应声明工具: %+v", state.hasTools)
	}
	if len(state.models) != 1 || state.models[0] != "override-model" {
		t.Fatalf("请求必须使用 AI 的 model_override，实际 %+v", state.models)
	}
	if logs.inserted != 1 {
		t.Fatalf("每次模型调用应写一条调用记录，实际 %d", logs.inserted)
	}
}

func TestCallDecisionModelWebSearchLimitedRounds(t *testing.T) {
	searchCalls := 0
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchCalls++
		if r.URL.Path != "/v1/tools/search" {
			t.Errorf("搜索端点必须固定为 /v1/tools/search，实际 %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{map[string]any{
				"title": "公开资料", "url": "https://example.com/a", "snippet": "摘要",
			}},
		})
	}))
	t.Cleanup(search.Close)

	state := &chatServerState{}
	chat := newChatServer(t, state, func(current *chatServerState, _ map[string]any) map[string]any {
		// 前两轮模型持续请求搜索，验证最多 2 轮工具调用；最后一轮强制无工具。
		if current.requests <= maxWebSearchRounds {
			return chatCompletionResponse(toolCallMessage(
				fmt.Sprintf("call_%d", current.requests), webSearchToolName,
				fmt.Sprintf(`{"query":"公开主题 %d"}`, current.requests),
			), "tool_calls")
		}
		return chatCompletionResponse(map[string]any{
			"role": "assistant", "content": `{"action":"noop","content":"","reason":"搜索后不动作"}`,
		}, "stop")
	})
	ai := &fakeAI{webSearch: contracts.WebSearchConfig{BaseURL: search.URL, APIKey: "search-key"}}
	svc, logs := newModelTestService(t, ai)
	rt := contracts.Runtime{
		AIID: 1,
		Provider: contracts.ProviderConfig{
			ID: 1, BaseURL: chat.URL + "/v1", APIKey: "k", DefaultModel: "test-model", IsEnabled: true,
		},
		Model:    "test-model",
		Timeout:  5 * time.Second,
		Behavior: contracts.BehaviorSettings{AllowWebSearch: true},
	}
	prompt := promptInput{Event: "今天有什么科技新闻"}
	resp, err := svc.callDecisionModel(context.Background(), rt, 1, prompt, "system", "user", decisionFormat())
	if err != nil {
		t.Fatalf("联网搜索决策不应失败: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Text, "noop") {
		t.Fatalf("最终轮应返回结构化决策: %+v", resp)
	}
	if state.requests != maxWebSearchRounds+1 {
		t.Fatalf("最多 %d 轮工具调用 + 1 轮最终决策，实际 %d 轮", maxWebSearchRounds, state.requests)
	}
	if searchCalls != maxWebSearchCalls {
		t.Fatalf("单次决策最多 %d 次搜索，实际 %d", maxWebSearchCalls, searchCalls)
	}
	if len(state.hasTools) != maxWebSearchRounds+1 {
		t.Fatalf("工具轮次统计错误: %+v", state.hasTools)
	}
	for round := 0; round < maxWebSearchRounds; round++ {
		if !state.hasTools[round] {
			t.Fatalf("第 %d 轮应声明工具", round+1)
		}
	}
	if state.hasTools[maxWebSearchRounds] {
		t.Fatal("最终轮必须强制无工具")
	}
	if logs.inserted != maxWebSearchRounds+1 {
		t.Fatalf("工具轮与最终轮都应写调用记录，实际 %d", logs.inserted)
	}
}

func TestClassifyModelError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus string
		wantRetry  bool
	}{
		{"传输层失败可重试", &aiclient.APIError{StatusCode: 0}, domain.TaskStatusFailed, true},
		{"429 可重试", &aiclient.APIError{StatusCode: 429}, domain.TaskStatusFailed, true},
		{"5xx 可重试", &aiclient.APIError{StatusCode: 503}, domain.TaskStatusFailed, true},
		{"401 不重试", &aiclient.APIError{StatusCode: 401}, domain.TaskStatusRejected, false},
		{"400 不重试", &aiclient.APIError{StatusCode: 400}, domain.TaskStatusRejected, false},
		{"超时可重试", context.DeadlineExceeded, domain.TaskStatusFailed, true},
		{"上下文取消按 canceled", context.Canceled, domain.TaskStatusCanceled, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, _, retryable := classifyModelError(testCase.err)
			if status != testCase.wantStatus || retryable != testCase.wantRetry {
				t.Fatalf("classifyModelError(%v) = (%s,%v)，期望 (%s,%v)",
					testCase.err, status, retryable, testCase.wantStatus, testCase.wantRetry)
			}
		})
	}
}

func TestClassifyActionError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus string
		wantRetry  bool
	}{
		{"目标不存在按 rejected", apperr.NotFound("评论已删除"), domain.TaskStatusRejected, false},
		{"无权互动按 rejected", apperr.Forbidden("不能互动"), domain.TaskStatusRejected, false},
		{"限频按 rejected", apperr.RateLimited(), domain.TaskStatusRejected, false},
		{"依赖不可用可重试", apperr.Unavailable("数据库未就绪"), domain.TaskStatusFailed, true},
		{"未知错误可重试", fmt.Errorf("连接中断"), domain.TaskStatusFailed, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, _, retryable := classifyActionError(testCase.err)
			if status != testCase.wantStatus || retryable != testCase.wantRetry {
				t.Fatalf("classifyActionError(%v) = (%s,%v)，期望 (%s,%v)",
					testCase.err, status, retryable, testCase.wantStatus, testCase.wantRetry)
			}
		})
	}
}

// callableMessageWithToolCall 构造一条带工具调用的 assistant 消息（测试 normalizeAssistantToolCallIDs）。
func callableMessageWithToolCall(id, name, args string) callable.Message {
	message := callable.Message{Role: callable.RoleAssistant}
	message.Parts = append(message.Parts, callable.ToolCallPart{ID: id, Name: name, Arguments: args})
	return message
}

// ===== 停机二次校验、全局暂停范围、来源失效复核、管理取消任务 =====

type fakeSettings struct {
	contracts.SettingsReader
	contracts.SettingsWriter
	values contracts.SystemSettings
	writes []contracts.SettingsWrite
}

func (f *fakeSettings) Settings(_ context.Context) (contracts.SystemSettings, error) {
	return f.values, nil
}

func (f *fakeSettings) Set(_ context.Context, in contracts.SettingsWrite) error {
	f.writes = append(f.writes, in)
	return nil
}

type fakeAudit struct {
	contracts.AuditWriter
	actions []string
}

func (f *fakeAudit) Write(_ context.Context, entry contracts.AuditEntry) error {
	f.actions = append(f.actions, entry.Action)
	return nil
}

func TestActionGateBlocksAfterModelTimeStop(t *testing.T) {
	settings := &fakeSettings{values: contracts.SystemSettings{AIGlobalEnabled: false}}
	svc := New(Deps{Clock: testClock(), Log: testLogger(), Settings: settings, AI: &fakeAI{}, Users: &fakeUsers{}})
	gate, blocked, err := svc.actionGate(context.Background(), 1)
	if err != nil {
		t.Fatalf("actionGate 不应失败: %v", err)
	}
	if !blocked || gate.Status != domain.TaskStatusCanceled || !strings.Contains(gate.Decision, "全局 AI 开关") {
		t.Fatalf("全局开关关闭时应取消任务: %+v blocked=%v", gate, blocked)
	}

	// 全局开关开启但 AI 在等待模型期间被停用：同样必须阻止执行动作。
	settings.values.AIGlobalEnabled = true
	ai := &fakeAI{}
	ai.runtime = contracts.Runtime{AIID: 1, Profile: contracts.Profile{IsEnabled: false}}
	svc = New(Deps{Clock: testClock(), Log: testLogger(), Settings: settings, AI: ai, Users: &fakeUsers{}})
	gate, blocked, err = svc.actionGate(context.Background(), 1)
	if err != nil || !blocked || !strings.Contains(gate.Decision, "已停用") {
		t.Fatalf("AI 停用后应取消任务: %+v blocked=%v err=%v", gate, blocked, err)
	}
}

func TestSetGlobalCancelsTasksAndPausesCorrectJobs(t *testing.T) {
	settings := &fakeSettings{}
	audit := &fakeAudit{}
	tasks := &fakeTasks{pending: []repo.Task{{ID: 1, AIID: 8}, {ID: 2, AIID: 9}}}
	jobs := &fakeJobs{}
	svc := New(Deps{
		Clock: testClock(), Log: testLogger(), Tasks: tasks, Jobs: jobs,
		Settings: settings, SettingsWriter: settings, Audit: audit,
	})
	if err := svc.SetGlobal(context.Background(), 100, false); err != nil {
		t.Fatalf("关闭全局 AI 开关不应失败: %v", err)
	}
	if tasks.statuses[1] != domain.TaskStatusCanceled || tasks.statuses[2] != domain.TaskStatusCanceled {
		t.Fatalf("全部 pending 任务都应按批取消: %+v", tasks.statuses)
	}
	if len(jobs.pausedAll) != 2 {
		t.Fatalf("应暂停 ai_task 与 ref_type='ai'（调度/记忆沉淀）两类作业，实际 %+v", jobs.pausedAll)
	}
	want := map[string]bool{
		contracts.JobTypeAITask + "|" + domain.RefTask: true,
		"|" + domain.RefAI: true,
	}
	for _, pause := range jobs.pausedAll {
		key := pause.JobType + "|" + pause.RefType
		if !want[key] {
			t.Fatalf("暂停作业使用了错误的 ref_type: %+v", pause)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("缺少作业类型暂停: %+v", jobs.pausedAll)
	}
	if len(settings.writes) != 1 || settings.writes[0].Key != contracts.SettingKeyAIGlobalEnabled {
		t.Fatalf("应写全局 AI 开关设置: %+v", settings.writes)
	}
	if len(audit.actions) != 1 || audit.actions[0] != contracts.AuditActionGlobalAIStop {
		t.Fatalf("应写全局停止审计: %+v", audit.actions)
	}
}

func TestRecheckSourceMemoriesInvalidatesUnavailableSources(t *testing.T) {
	content := &fakeContent{posts: map[int64]model.Post{
		200: publicPost(200, 7), // 仍公开
		300: {ID: 300, AuthorID: 8, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
	}}
	social := &fakeSocial{comments: map[int64]model.Comment{
		500: {ID: 500, PostID: 300, AuthorID: 8, Status: model.CommentStatusPublished}, // 所在帖已转仅关注者
	}}
	ai := &fakeAI{activeSourceRefs: []contracts.MemorySourceRef{
		{SourceType: domain.RefPost, SourceID: 100},    // 已删除
		{SourceType: domain.RefComment, SourceID: 500}, // 所在帖非公开
		{SourceType: domain.RefPost, SourceID: 200},    // 仍公开
	}}
	svc := New(Deps{Clock: testClock(), Log: testLogger(), AI: ai, Content: content, Social: social})
	if err := svc.recheckSourceMemories(context.Background(), 1); err != nil {
		t.Fatalf("来源复核不应失败: %v", err)
	}
	if len(ai.invalidated) != 2 {
		t.Fatalf("应失效 2 个不可用来源，实际 %+v", ai.invalidated)
	}
	got := map[string]bool{}
	for _, in := range ai.invalidated {
		got[in.SourceType+":"+apiID(in.SourceID)] = true
	}
	if !got["post:100"] || !got["comment:500"] || got["post:200"] {
		t.Fatalf("来源失效范围错误: %+v", got)
	}
	if len(ai.invalidatedBySources) != 2 {
		t.Fatalf("沉淀来源关联记忆也应失效: %+v", ai.invalidatedBySources)
	}
}

func TestCancelAITaskCancelsBackingJob(t *testing.T) {
	tasks := &fakeTasks{tasks: map[int64]repo.Task{55: {ID: 55, AIID: 1, Status: domain.TaskStatusRunning}}}
	refID := int64(55)
	jobs := &fakeJobs{byRef: []contracts.JobSnapshot{{ID: 77, RefID: &refID, Status: contracts.JobStatusPending}}}
	audit := &fakeAudit{}
	svc := New(Deps{Clock: testClock(), Log: testLogger(), Tasks: tasks, Jobs: jobs, Audit: audit})
	if _, err := svc.CancelAITask(context.Background(), 100, 55); err != nil {
		t.Fatalf("取消任务不应失败: %v", err)
	}
	if tasks.statuses[55] != domain.TaskStatusCanceled {
		t.Fatalf("任务应标记 canceled: %+v", tasks.statuses)
	}
	if len(jobs.canceled) != 1 || jobs.canceled[0] != 77 {
		t.Fatalf("应按任务 ID 查到作业 77 并取消，实际 %+v", jobs.canceled)
	}
}
