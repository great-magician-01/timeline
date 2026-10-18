package app

// task.go 用例测试：RunTask 主流程、模型错误分类、动作执行分支、决策上下文组装、
// 记忆形成/沉淀触发、对账与纯函数。模型调用使用本地 httptest fake 服务，无真实外网。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== RunTask 夹具 =====

func newRunTaskFixture() *flowFixture {
	f := newFlowFixture()
	rt := f.enableAI(1)
	rt.Provider.BaseURL = "http://127.0.0.1:1/v1" // 仅用于不会被调用的模型路径
	f.ai.runtime = rt
	f.tasks.claimed = true
	f.tasks.claimAttempts = 1
	f.tasks.tasks[101] = repo.Task{
		ID:             101,
		AIID:           1,
		TriggerType:    domain.TriggerManual,
		TriggerRefType: domain.GenerationModeDirect,
		ActionType:     domain.ActionOriginalPost,
		Status:         domain.TaskStatusRunning,
	}
	return f
}

// attachDecisionServer 挂载返回固定决策文本的 OpenAI 兼容 fake 服务。
func attachDecisionServer(t *testing.T, f *flowFixture, content string) *chatServerState {
	t.Helper()
	state := &chatServerState{}
	chat := newChatServer(t, state, func(_ *chatServerState, _ map[string]any) map[string]any {
		return chatCompletionResponse(map[string]any{"role": "assistant", "content": content}, "stop")
	})
	rt := f.ai.runtime
	rt.Provider.BaseURL = chat.URL + "/v1"
	f.ai.runtime = rt
	return state
}

// attachModelStatusServer 挂载返回指定 HTTP 状态码的 fake 服务。
func attachModelStatusServer(t *testing.T, f *flowFixture, status int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"上游失败","type":"test"}}`))
	}))
	t.Cleanup(server.Close)
	rt := f.ai.runtime
	rt.Provider.BaseURL = server.URL + "/v1"
	f.ai.runtime = rt
}

func (f *flowFixture) setTask(task repo.Task) { f.tasks.tasks[task.ID] = task }

func TestRunTaskClaimNotAcquired(t *testing.T) {
	f := newRunTaskFixture()
	f.tasks.claimed = false
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("未领取到任务应直接返回 nil: %v", err)
	}
	if len(f.tasks.results) != 0 || len(f.tasks.statuses) != 0 {
		t.Fatalf("未领取任务不应写回结果: results=%+v statuses=%+v", f.tasks.results, f.tasks.statuses)
	}
}

func TestRunTaskClaimError(t *testing.T) {
	f := newRunTaskFixture()
	f.tasks.claimErr = errBoom
	if err := f.svc.RunTask(context.Background(), 101); !errors.Is(err, errBoom) {
		t.Fatalf("领取失败应透传错误: %v", err)
	}
}

// TestRunTaskUsesClaimedRow 确认 RunTask 直接使用 ClaimTask 的 UPDATE ... RETURNING 结果：
// 领取后不再调用 GetTask 二次读取（旧实现的「已领取却读不到任务」分支已不存在）。
func TestRunTaskUsesClaimedRow(t *testing.T) {
	f := newRunTaskFixture()
	f.settings.fakeSettings.values = contracts.SystemSettings{AIGlobalEnabled: false}
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("全局关闭应正常结束任务: %v", err)
	}
	if len(f.tasks.claims) != 1 || f.tasks.claims[0] != 101 {
		t.Fatalf("应领取一次任务: %+v", f.tasks.claims)
	}
	if len(f.tasks.results) != 1 || f.tasks.results[0].update.Status != domain.TaskStatusCanceled {
		t.Fatalf("应使用领取返回的任务行写回结果: %+v", f.tasks.results)
	}
	if f.tasks.getTaskCalls != 0 {
		t.Fatalf("领取后不应再调用 GetTask，实际 %d 次", f.tasks.getTaskCalls)
	}
}

func TestRunTaskCanceledWhenGlobalDisabled(t *testing.T) {
	f := newRunTaskFixture()
	f.settings.fakeSettings.values = contracts.SystemSettings{AIGlobalEnabled: false}
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("全局关闭应正常结束任务: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusCanceled || !strings.Contains(result.DecisionSummary, "全局 AI 开关") {
		t.Fatalf("全局关闭应取消任务: %+v", result)
	}
}

func TestRunTaskCanceledWhenAIUnavailable(t *testing.T) {
	cases := []struct {
		name       string
		prepare    func(f *flowFixture)
		wantPhrase string
	}{
		{
			name: "AI 账号不存在",
			prepare: func(f *flowFixture) {
				f.ai.runtimeErr = apperr.NotFound("AI 不存在")
			},
			wantPhrase: "AI 账号不存在",
		},
		{
			name: "AI 已停用",
			prepare: func(f *flowFixture) {
				rt := f.ai.runtime
				rt.Profile.IsEnabled = false
				f.ai.runtime = rt
			},
			wantPhrase: "该 AI 已停用",
		},
		{
			name: "账号行不存在",
			prepare: func(f *flowFixture) {
				delete(f.users.users, 1)
			},
			wantPhrase: "AI 账号不存在",
		},
		{
			name: "账号已停用",
			prepare: func(f *flowFixture) {
				f.users.users[1] = model.User{ID: 1, Username: "ai", Status: model.StatusSuspended, IsAI: true}
			},
			wantPhrase: "AI 账号已停用",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := newRunTaskFixture()
			testCase.prepare(f)
			if err := f.svc.RunTask(context.Background(), 101); err != nil {
				t.Fatalf("取消类结束不应返回错误: %v", err)
			}
			result := f.tasks.results[0].update
			if result.Status != domain.TaskStatusCanceled || !strings.Contains(result.DecisionSummary, testCase.wantPhrase) {
				t.Fatalf("期望取消原因含 %q，实际 %+v", testCase.wantPhrase, result)
			}
		})
	}
}

func TestRunTaskNoopOutsideActiveHours(t *testing.T) {
	f := newRunTaskFixture()
	rt := f.ai.runtime
	rt.Behavior.ActiveHoursStart = 13
	rt.Behavior.ActiveHoursEnd = 14
	f.ai.runtime = rt
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("非启用时段应正常结束任务: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusNoop || !strings.Contains(result.DecisionSummary, "启用时段") {
		t.Fatalf("非启用时段应 noop: %+v", result)
	}
}

func TestRunTaskRejectsInvisibleTriggerSource(t *testing.T) {
	f := newRunTaskFixture()
	commentID := int64(500)
	f.setTask(repo.Task{
		ID: 101, AIID: 1, TriggerType: domain.TriggerComment,
		TriggerRefType: domain.RefComment, TriggerRefID: &commentID,
	})
	f.social.comments[commentID] = model.Comment{
		ID: commentID, PostID: 200, AuthorID: 5, Status: model.CommentStatusDeleted,
	}
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("触发来源不可见应正常结束: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusRejected || result.Error != domain.ErrorTypeInvalidTarget+": 触发来源已不可见" {
		t.Fatalf("不可见来源应 rejected/invalid_target: %+v", result)
	}
}

func TestRunTaskProactiveNoCandidatesNoop(t *testing.T) {
	f := newRunTaskFixture()
	f.setTask(repo.Task{ID: 101, AIID: 1, TriggerType: domain.TriggerProactive, TriggerRefType: domain.RefAI})
	rt := f.ai.runtime
	rt.Behavior.CandidateScopeType = domain.ScopeGlobalPublic
	f.ai.runtime = rt
	state := attachDecisionServer(t, f, `{"action":"noop","content":"","reason":"无候选"}`)
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("无候选应正常结束: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusNoop || !strings.Contains(result.DecisionSummary, "没有合适的候选内容") {
		t.Fatalf("无候选应 noop: %+v", result)
	}
	if state.requests != 0 {
		t.Fatalf("无候选不应调用模型，实际 %d 次", state.requests)
	}
}

func TestRunTaskModelErrorClassification(t *testing.T) {
	// 只验证不可重试的鉴权失败；429/5xx 的可重试分类由 TestClassifyModelError 覆盖，
	// 且 aiclient 对 5xx 有库内退避重试，放在默认单测里会拖慢测试。
	f := newRunTaskFixture()
	attachModelStatusServer(t, f, http.StatusUnauthorized)
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("不可恢复失败不应返回错误: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusRejected || !strings.HasPrefix(result.Error, domain.ErrorTypeForbidden) {
		t.Fatalf("鉴权失败应 rejected/forbidden: %+v", result)
	}
}

func TestRunTaskContextCanceled(t *testing.T) {
	f := newRunTaskFixture()
	attachDecisionServer(t, f, `{"action":"noop","content":"","reason":"无"}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := f.svc.RunTask(ctx, 101)
	if err != nil {
		t.Fatalf("上下文取消不应触发作业重试: %v", err)
	}
	if f.tasks.results[0].update.Status != domain.TaskStatusCanceled {
		t.Fatalf("上下文取消应标记 canceled: %+v", f.tasks.results[0].update)
	}
}

func TestRunTaskRejectsInvalidModelOutput(t *testing.T) {
	f := newRunTaskFixture()
	attachDecisionServer(t, f, "这不是 JSON")
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("非法输出应正常结束: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusRejected || !strings.HasPrefix(result.Error, domain.ErrorTypeInvalidJSON) {
		t.Fatalf("非法输出应 rejected/invalid_json: %+v", result)
	}
}

func TestRunTaskNoopDecision(t *testing.T) {
	f := newRunTaskFixture()
	attachDecisionServer(t, f, `{"action":"noop","content":"","reason":""}`)
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("noop 应正常结束: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusNoop || !strings.Contains(result.DecisionSummary, "模型选择不执行任何操作") {
		t.Fatalf("noop 决策记录错误: %+v", result)
	}
}

func TestRunTaskActionGateBlocksAfterModel(t *testing.T) {
	f := newRunTaskFixture()
	attachDecisionServer(t, f, `{"action":"original_post","content":"内容","reason":"发帖"}`)
	settingsCalls := 0
	f.settings.settingsFn = func() contracts.SystemSettings {
		settingsCalls++
		if settingsCalls >= 2 {
			return contracts.SystemSettings{AIGlobalEnabled: false}
		}
		return contracts.SystemSettings{AIGlobalEnabled: true}
	}
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("停机拦截应正常结束任务: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusCanceled || !strings.Contains(result.DecisionSummary, "全局 AI 开关") {
		t.Fatalf("动作前二次校验应取消任务: %+v", result)
	}
	if len(f.content.createdPosts) != 0 {
		t.Fatalf("停机后不得执行动作: %+v", f.content.createdPosts)
	}
	if result.ProviderID == nil || result.Model != "test-model" {
		t.Fatalf("取消时仍应记录模型来源: %+v", result)
	}
}

func TestRunTaskRejectsActionNotAllowedForTrigger(t *testing.T) {
	f := newRunTaskFixture()
	rt := f.ai.runtime
	rt.Behavior.AllowOriginalPost = false
	rt.Behavior.AllowLike = true
	f.ai.runtime = rt
	attachDecisionServer(t, f, `{"action":"like","content":"","reason":"点赞","target_type":"post","target_id":"200"}`)
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("触发类型不允许动作应正常结束: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusRejected || !strings.HasPrefix(result.Error, domain.ErrorTypeInvalidTarget) {
		t.Fatalf("应 rejected/invalid_target: %+v", result)
	}
}

func TestRunTaskRejectsActionDisabledByBehavior(t *testing.T) {
	f := newRunTaskFixture()
	commentID := int64(500)
	f.setTask(repo.Task{
		ID: 101, AIID: 1, TriggerType: domain.TriggerMention,
		TriggerRefType: domain.RefComment, TriggerRefID: &commentID,
	})
	f.social.comments[commentID] = model.Comment{ID: commentID, PostID: 200, AuthorID: 5, Status: model.CommentStatusPublished, PlainText: "提到你"}
	f.content.posts[200] = publicPost(200, 5)
	rt := f.ai.runtime
	rt.Behavior.AllowPassiveReply = false
	f.ai.runtime = rt
	attachDecisionServer(t, f, `{"action":"reply","content":"回复内容","reason":"回复","target_id":"500"}`)
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("动作被策略禁止应正常结束: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusRejected || !strings.HasPrefix(result.Error, domain.ErrorTypeForbidden) {
		t.Fatalf("应 rejected/forbidden: %+v", result)
	}
}

func TestRunTaskRejectsRateLimitedAction(t *testing.T) {
	f := newRunTaskFixture()
	f.setTask(repo.Task{ID: 101, AIID: 1, TriggerType: domain.TriggerProactive, TriggerRefType: domain.RefAI})
	f.content.candidates = []contracts.PostCandidate{{Post: publicPost(100, 7), Summary: "候选内容"}}
	f.users.users[7] = activeHuman(7)
	rt := f.ai.runtime
	rt.Behavior.CandidateScopeType = domain.ScopeGlobalPublic
	rt.Behavior.RateLimits = []byte(`{"daily_like":1}`)
	f.ai.runtime = rt
	f.counters.actionCounts[domain.ActionLike] = 1
	attachDecisionServer(t, f, `{"action":"like","content":"","reason":"点赞","target_type":"post","target_id":"100"}`)
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("限频应正常结束: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusRejected || !strings.HasPrefix(result.Error, domain.ErrorTypeRateLimited) {
		t.Fatalf("应 rejected/rate_limited: %+v", result)
	}
}

func TestRunTaskRejectsInvalidTarget(t *testing.T) {
	f := newRunTaskFixture()
	commentID := int64(500)
	f.setTask(repo.Task{
		ID: 101, AIID: 1, TriggerType: domain.TriggerMention,
		TriggerRefType: domain.RefComment, TriggerRefID: &commentID,
	})
	f.social.comments[commentID] = model.Comment{ID: commentID, PostID: 200, AuthorID: 5, Status: model.CommentStatusPublished, PlainText: "提到你"}
	f.content.posts[200] = publicPost(200, 5)
	f.social.depths[commentID] = 5
	rt := f.ai.runtime
	rt.Behavior.AllowPassiveReply = true
	rt.Behavior.MaxReplyDepth = 2
	f.ai.runtime = rt
	attachDecisionServer(t, f, `{"action":"reply","content":"回复内容","reason":"回复","target_id":"500"}`)
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("目标校验失败应正常结束: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusRejected || !strings.Contains(result.Error, "回复深度超限") {
		t.Fatalf("应 rejected 且记录拒绝原因: %+v", result)
	}
}

func TestRunTaskSuccessOriginalPost(t *testing.T) {
	f := newRunTaskFixture()
	f.ai.recent = []contracts.Memory{{ID: 900, AID: 1, MemoryType: "recent", Content: "最近在关注 Go"}}
	f.ai.longTerm = []contracts.Memory{{ID: 901, AID: 1, MemoryType: "long_term", Content: "长期记忆"}}
	state := attachDecisionServer(t, f, `{"action":"original_post","content":"今天聊聊 Go","reason":"想发帖"}`)

	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("任务执行不应失败: %v", err)
	}
	if state.requests != 1 {
		t.Fatalf("应调用 1 次模型，实际 %d", state.requests)
	}
	if f.tx.calls != 1 {
		t.Fatalf("动作应在 1 个事务内执行，实际 %d", f.tx.calls)
	}
	if len(f.tasks.results) != 1 {
		t.Fatalf("应写回 1 次任务结果，实际 %+v", f.tasks.results)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusSucceeded || result.ActionType != domain.ActionOriginalPost {
		t.Fatalf("任务终态错误: %+v", result)
	}
	if result.ResultRefType != domain.RefPost || result.ResultRefID == nil || *result.ResultRefID != 7001 {
		t.Fatalf("结果引用应为新帖 7001: %+v", result)
	}
	if result.ProviderID == nil || *result.ProviderID != 1 || result.Model != "test-model" {
		t.Fatalf("任务应记录模型来源: %+v", result)
	}
	if len(f.content.createdPosts) != 1 {
		t.Fatalf("应创建 1 条帖子，实际 %+v", f.content.createdPosts)
	}
	post := f.content.createdPosts[0]
	if post.AuthorID != 1 || post.ContentFormat != "markdown" || post.ContentSource != "今天聊聊 Go" ||
		post.Visibility != model.VisibilityPublic || post.AsDraft || post.ScheduledAt != nil {
		t.Fatalf("发帖参数错误: %+v", post)
	}
	if !post.AuthorIsAI {
		t.Fatalf("AI 发帖必须写入 author_is_ai=true 快照: %+v", post)
	}
	if !post.At.Equal(f.clk.Now()) {
		t.Fatalf("发帖时间应来自注入时钟: %v", post.At)
	}
	if len(f.counters.bumps) != 1 || f.counters.bumps[0].action != domain.ActionOriginalPost {
		t.Fatalf("成功动作应累加计数: %+v", f.counters.bumps)
	}
	if len(f.callLogs.inserts) != 1 || f.callLogs.inserts[0].Status != "success" {
		t.Fatalf("应写 1 条成功调用记录: %+v", f.callLogs.inserts)
	}
	if len(f.ai.created) != 1 {
		t.Fatalf("应形成 1 条近期记忆，实际 %+v", f.ai.created)
	}
	memory := f.ai.created[0]
	if memory.MemoryType != "recent" || memory.SourceType != "action" ||
		memory.SourceRefType != domain.RefPost || memory.SourceRefID == nil || *memory.SourceRefID != 7001 {
		t.Fatalf("记忆形成参数错误: %+v", memory)
	}
	if len(f.tasks.addedRefs[101]) != 2 {
		t.Fatalf("应写入 2 条注入记忆引用，实际 %+v", f.tasks.addedRefs[101])
	}
	if len(f.ai.touched) != 1 || len(f.ai.touched[0]) != 2 {
		t.Fatalf("应更新注入记忆使用时间: %+v", f.ai.touched)
	}
	if len(f.ai.recentLimit) != 1 || f.ai.recentLimit[0] != maxRecentMemoryCount {
		t.Fatalf("近期记忆注入上限错误: %+v", f.ai.recentLimit)
	}
	if len(f.ai.longTermInputs) != 1 || f.ai.longTermInputs[0].Limit != maxLongTermMemoryCount {
		t.Fatalf("长期记忆检索参数错误: %+v", f.ai.longTermInputs)
	}
	if len(f.jobs.enqueueCalls) != 0 {
		t.Fatalf("近期记忆未达阈值不应投递沉淀作业: %+v", f.jobs.enqueueCalls)
	}
}

func TestRunTaskSuccessLikeAction(t *testing.T) {
	f := newRunTaskFixture()
	f.setTask(repo.Task{ID: 101, AIID: 1, TriggerType: domain.TriggerProactive, TriggerRefType: domain.RefAI})
	f.content.candidates = []contracts.PostCandidate{{Post: publicPost(100, 7), Summary: "候选内容"}}
	f.content.posts[100] = publicPost(100, 7)
	f.users.users[7] = activeHuman(7)
	rt := f.ai.runtime
	rt.Behavior.CandidateScopeType = domain.ScopeGlobalPublic
	f.ai.runtime = rt
	f.social.likeActive = true
	f.social.likeCount = 5
	attachDecisionServer(t, f, `{"action":"like","content":"","reason":"点赞","target_type":"post","target_id":"100"}`)
	if err := f.svc.RunTask(context.Background(), 101); err != nil {
		t.Fatalf("点赞任务不应失败: %v", err)
	}
	result := f.tasks.results[0].update
	if result.Status != domain.TaskStatusSucceeded || result.ActionType != domain.ActionLike ||
		result.TargetType != domain.RefPost || result.TargetID == nil || *result.TargetID != 100 {
		t.Fatalf("点赞任务结果错误: %+v", result)
	}
	if len(f.social.likeInputs) != 1 || f.social.likeInputs[0].TargetType != domain.RefPost || f.social.likeInputs[0].TargetID != 100 {
		t.Fatalf("点赞调用参数错误: %+v", f.social.likeInputs)
	}
	if len(f.counters.bumps) != 1 || f.counters.bumps[0].action != domain.ActionLike {
		t.Fatalf("点赞应累加计数: %+v", f.counters.bumps)
	}
}

// ===== executeAction 各动作分支 =====

func TestExecuteActionBranches(t *testing.T) {
	now := testClock().Now()
	postID := int64(7001)

	t.Run("原创帖", func(t *testing.T) {
		f := newFlowFixture()
		f.content.createdPost = model.Post{ID: postID, AuthorID: 1, Status: model.PostStatusPublished}
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1, TriggerRefType: domain.GenerationModeDirect},
			domain.Decision{Action: domain.ActionOriginalPost, Content: "正文"},
			targetRef{}, now)
		if err != nil {
			t.Fatalf("原创帖动作不应失败: %v", err)
		}
		if outcome.ResultRefType != domain.RefPost || outcome.ResultRefID == nil || *outcome.ResultRefID != postID {
			t.Fatalf("原创帖结果引用错误: %+v", outcome)
		}
		if !strings.Contains(outcome.Summary, "发布了原创帖") || !strings.Contains(outcome.MemoryNote, "正文") {
			t.Fatalf("原创帖摘要/记忆错误: %+v", outcome)
		}
		if f.content.createdPosts[0].AsDraft || f.content.createdPosts[0].QuotedPostID != nil {
			t.Fatalf("原创帖输入不应是草稿/引用: %+v", f.content.createdPosts[0])
		}
	})

	t.Run("AI 草稿", func(t *testing.T) {
		f := newFlowFixture()
		f.content.createdPost = model.Post{ID: postID, AuthorID: 1}
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1, TriggerRefType: domain.GenerationModeDraft},
			domain.Decision{Action: domain.ActionOriginalPost, Content: "草稿正文"},
			targetRef{}, now)
		if err != nil {
			t.Fatalf("草稿动作不应失败: %v", err)
		}
		if !f.content.createdPosts[0].AsDraft || !strings.Contains(outcome.Summary, "草稿") {
			t.Fatalf("草稿动作参数错误: %+v %+v", f.content.createdPosts[0], outcome)
		}
	})

	t.Run("定时原创", func(t *testing.T) {
		f := newFlowFixture()
		scheduled := now.Add(time.Hour)
		f.content.createdPost = model.Post{ID: postID, AuthorID: 1}
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1, ScheduledAt: &scheduled},
			domain.Decision{Action: domain.ActionOriginalPost, Content: "定时正文"},
			targetRef{}, now)
		if err != nil {
			t.Fatalf("定时原创不应失败: %v", err)
		}
		if !strings.Contains(outcome.Summary, "定时") || f.content.createdPosts[0].ScheduledAt == nil {
			t.Fatalf("定时原创参数错误: %+v %+v", f.content.createdPosts[0], outcome)
		}
	})

	t.Run("引用转发", func(t *testing.T) {
		f := newFlowFixture()
		f.content.createdPost = model.Post{ID: postID, AuthorID: 1}
		target := targetRef{Type: domain.RefPost, ID: 100, PostID: 100}
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1},
			domain.Decision{Action: domain.ActionQuote, Content: "引用理由"},
			target, now)
		if err != nil {
			t.Fatalf("引用动作不应失败: %v", err)
		}
		if f.content.createdPosts[0].QuotedPostID == nil || *f.content.createdPosts[0].QuotedPostID != 100 {
			t.Fatalf("引用目标错误: %+v", f.content.createdPosts[0])
		}
		if !strings.Contains(outcome.Summary, "引用转发") || !strings.Contains(outcome.MemoryNote, "引用转发") {
			t.Fatalf("引用摘要错误: %+v", outcome)
		}
	})

	t.Run("评论", func(t *testing.T) {
		f := newFlowFixture()
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1},
			domain.Decision{Action: domain.ActionComment, Content: "评论内容"},
			targetRef{Type: domain.RefPost, ID: 9, PostID: 9}, now)
		if err != nil {
			t.Fatalf("评论动作不应失败: %v", err)
		}
		input := f.social.commentInputs[0]
		if input.ActorID != 1 || !input.IsAI || input.PostID != 9 || input.ParentID != nil {
			t.Fatalf("评论输入错误: %+v", input)
		}
		if outcome.ResultRefType != domain.RefComment || outcome.TargetType != domain.RefPost ||
			outcome.TargetID == nil || *outcome.TargetID != 9 {
			t.Fatalf("评论结果错误: %+v", outcome)
		}
	})

	t.Run("回复", func(t *testing.T) {
		f := newFlowFixture()
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1},
			domain.Decision{Action: domain.ActionReply, Content: "回复内容"},
			targetRef{Type: domain.RefComment, ID: 55, PostID: 9}, now)
		if err != nil {
			t.Fatalf("回复动作不应失败: %v", err)
		}
		if f.social.commentInputs[0].ParentID == nil || *f.social.commentInputs[0].ParentID != 55 ||
			f.social.commentInputs[0].PostID != 9 {
			t.Fatalf("回复输入错误: %+v", f.social.commentInputs[0])
		}
		if outcome.ResultRefType != domain.RefComment {
			t.Fatalf("回复结果错误: %+v", outcome)
		}
	})

	t.Run("点赞生效", func(t *testing.T) {
		f := newFlowFixture()
		f.social.likeActive = true
		f.social.likeCount = 3
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1},
			domain.Decision{Action: domain.ActionLike, TargetType: domain.RefPost},
			targetRef{Type: domain.RefPost, ID: 100}, now)
		if err != nil {
			t.Fatalf("点赞不应失败: %v", err)
		}
		if outcome.Skipped {
			t.Fatalf("点赞生效不应跳过: %+v", outcome)
		}
		if !strings.Contains(outcome.Summary, "3") || outcome.TargetID == nil || *outcome.TargetID != 100 {
			t.Fatalf("点赞结果错误: %+v", outcome)
		}
	})

	t.Run("点赞未生效时标记跳过", func(t *testing.T) {
		f := newFlowFixture()
		f.social.likeActive = false
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1},
			domain.Decision{Action: domain.ActionLike, TargetType: domain.RefPost},
			targetRef{Type: domain.RefPost, ID: 100}, now)
		if err != nil {
			t.Fatalf("点赞不应失败: %v", err)
		}
		if !outcome.Skipped {
			t.Fatalf("未生效的点赞应标记 skipped: %+v", outcome)
		}
	})

	t.Run("取消点赞", func(t *testing.T) {
		f := newFlowFixture()
		f.social.unlikeActive = false
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1},
			domain.Decision{Action: domain.ActionUnlike, TargetType: domain.RefComment},
			targetRef{Type: domain.RefComment, ID: 55}, now)
		if err != nil {
			t.Fatalf("取消点赞不应失败: %v", err)
		}
		if outcome.Skipped {
			t.Fatal("取消成功的动作不应跳过")
		}
		if len(f.social.likeInputs) != 1 || f.social.likeInputs[0].TargetType != domain.RefComment {
			t.Fatalf("取消点赞目标类型错误: %+v", f.social.likeInputs)
		}
	})

	t.Run("关注", func(t *testing.T) {
		f := newFlowFixture()
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1},
			domain.Decision{Action: domain.ActionFollow},
			targetRef{Type: domain.RefUser, ID: 7}, now)
		if err != nil {
			t.Fatalf("关注不应失败: %v", err)
		}
		if outcome.TargetType != domain.RefUser || outcome.TargetID == nil || *outcome.TargetID != 7 ||
			!strings.Contains(outcome.Summary, "following") {
			t.Fatalf("关注结果错误: %+v", outcome)
		}
	})

	t.Run("取关", func(t *testing.T) {
		f := newFlowFixture()
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1},
			domain.Decision{Action: domain.ActionUnfollow},
			targetRef{Type: domain.RefUser, ID: 7}, now)
		if err != nil {
			t.Fatalf("取关不应失败: %v", err)
		}
		if !strings.Contains(outcome.Summary, "none") {
			t.Fatalf("取关结果错误: %+v", outcome)
		}
	})

	t.Run("转发", func(t *testing.T) {
		f := newFlowFixture()
		f.social.repostCount = 8
		outcome, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1},
			domain.Decision{Action: domain.ActionRepost},
			targetRef{Type: domain.RefPost, ID: 100, PostID: 100}, now)
		if err != nil {
			t.Fatalf("转发不应失败: %v", err)
		}
		if outcome.TargetType != domain.RefPost || !strings.Contains(outcome.Summary, "8") {
			t.Fatalf("转发结果错误: %+v", outcome)
		}
	})

	t.Run("不支持的动作", func(t *testing.T) {
		f := newFlowFixture()
		_, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
			repo.Task{ID: 1}, domain.Decision{Action: "magic"}, targetRef{}, now)
		if !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("不支持的动作应返回 Validation: %v", err)
		}
	})
}

func TestExecuteActionPropagatesPortErrors(t *testing.T) {
	now := testClock().Now()
	f := newFlowFixture()
	f.content.createPostErr = apperr.Unavailable("数据库未就绪")
	if _, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
		repo.Task{ID: 1}, domain.Decision{Action: domain.ActionOriginalPost, Content: "x"}, targetRef{}, now); !errors.Is(err, f.content.createPostErr) {
		t.Fatalf("应透传内容端口错误: %v", err)
	}

	f = newFlowFixture()
	f.social.commentErr = errBoom
	if _, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
		repo.Task{ID: 1}, domain.Decision{Action: domain.ActionComment, Content: "x"},
		targetRef{Type: domain.RefPost, ID: 9, PostID: 9}, now); !errors.Is(err, errBoom) {
		t.Fatalf("应透传社交端口错误: %v", err)
	}

	f = newFlowFixture()
	f.social.likeErr = errBoom
	if _, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
		repo.Task{ID: 1}, domain.Decision{Action: domain.ActionLike, TargetType: domain.RefPost},
		targetRef{Type: domain.RefPost, ID: 9}, now); !errors.Is(err, errBoom) {
		t.Fatalf("应透传点赞错误: %v", err)
	}

	f = newFlowFixture()
	f.social.followErr = errBoom
	if _, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
		repo.Task{ID: 1}, domain.Decision{Action: domain.ActionFollow},
		targetRef{Type: domain.RefUser, ID: 9}, now); !errors.Is(err, errBoom) {
		t.Fatalf("应透传关注错误: %v", err)
	}

	f = newFlowFixture()
	f.social.repostErr = errBoom
	if _, err := f.svc.executeAction(context.Background(), contracts.Runtime{AIID: 1},
		repo.Task{ID: 1}, domain.Decision{Action: domain.ActionRepost},
		targetRef{Type: domain.RefPost, ID: 9, PostID: 9}, now); !errors.Is(err, errBoom) {
		t.Fatalf("应透传转发错误: %v", err)
	}
}

func TestTaskActionAllowed(t *testing.T) {
	cases := []struct {
		trigger string
		action  string
		want    bool
	}{
		{domain.TriggerManual, domain.ActionOriginalPost, true},
		{domain.TriggerManual, domain.ActionQuote, true},
		{domain.TriggerManual, domain.ActionLike, false},
		{domain.TriggerSchedule, domain.ActionOriginalPost, true},
		{domain.TriggerSchedule, domain.ActionComment, false},
		{domain.TriggerMention, domain.ActionReply, true},
		{domain.TriggerMention, domain.ActionComment, true},
		{domain.TriggerMention, domain.ActionLike, true},
		{domain.TriggerMention, domain.ActionFollow, false},
		{domain.TriggerComment, domain.ActionReply, true},
		{domain.TriggerProactive, domain.ActionFollow, true},
		{domain.TriggerRetry, domain.ActionLike, true},
	}
	for _, testCase := range cases {
		task := repo.Task{TriggerType: testCase.trigger}
		if got := taskActionAllowed(task, testCase.action); got != testCase.want {
			t.Fatalf("taskActionAllowed(%s,%s) = %v，期望 %v", testCase.trigger, testCase.action, got, testCase.want)
		}
	}
}

// ===== buildPrompt / assemblePrompts =====

func TestBuildPromptCommentTrigger(t *testing.T) {
	f := newFlowFixture()
	commentID := int64(500)
	f.social.comments[commentID] = model.Comment{
		ID: commentID, PostID: 200, AuthorID: 5, Status: model.CommentStatusPublished, PlainText: "你怎么看？",
	}
	f.content.posts[200] = publicPost(200, 5)
	f.social.recentComments = []contracts.CommentBrief{{ID: 1, AuthorName: "小明", Content: "讨论内容"}}
	f.ai.recent = []contracts.Memory{{ID: 900, AID: 1, MemoryType: "recent", Content: "近期记忆"}}
	f.ai.longTerm = []contracts.Memory{{ID: 901, AID: 1, MemoryType: "long_term", Content: "长期记忆"}}
	task := repo.Task{ID: 1, AIID: 1, TriggerType: domain.TriggerMention, TriggerRefType: domain.RefComment, TriggerRefID: &commentID}
	prompt, refs, err := f.svc.buildPrompt(context.Background(), task, contracts.Runtime{AIID: 1})
	if err != nil {
		t.Fatalf("组装上下文不应失败: %v", err)
	}
	if !strings.Contains(prompt.Event, "comment_id=500") || !strings.Contains(prompt.Event, "你怎么看？") {
		t.Fatalf("评论事件上下文错误: %q", prompt.Event)
	}
	if len(prompt.Thread) != 1 {
		t.Fatalf("应注入最近讨论: %+v", prompt.Thread)
	}
	if len(refs) != 2 || len(f.ai.touched) != 1 || len(f.ai.touched[0]) != 2 {
		t.Fatalf("记忆引用/使用时间错误: refs=%+v touched=%+v", refs, f.ai.touched)
	}
	if f.ai.longTermInputs[0].AIID != 1 || f.ai.longTermInputs[0].Limit != maxLongTermMemoryCount {
		t.Fatalf("长期记忆检索参数错误: %+v", f.ai.longTermInputs)
	}
}

func TestBuildPromptPostTrigger(t *testing.T) {
	f := newFlowFixture()
	postID := int64(300)
	f.content.posts[postID] = publicPost(postID, 5)
	f.content.excerpts[postID] = "帖子摘要内容"
	task := repo.Task{ID: 1, AIID: 1, TriggerType: domain.TriggerMention, TriggerRefType: domain.RefPost, TriggerRefID: &postID}
	prompt, _, err := f.svc.buildPrompt(context.Background(), task, contracts.Runtime{AIID: 1})
	if err != nil {
		t.Fatalf("组装上下文不应失败: %v", err)
	}
	if !strings.Contains(prompt.Event, "post_id=300") || !strings.Contains(prompt.Event, "帖子摘要内容") {
		t.Fatalf("帖子事件上下文错误: %q", prompt.Event)
	}
}

func TestBuildPromptProactiveCandidates(t *testing.T) {
	f := newFlowFixture()
	f.content.candidates = []contracts.PostCandidate{
		{Post: publicPost(100, 7), AuthorName: "作者七", Summary: "候选一"},
	}
	f.social.likedRefs = []contracts.LikedPostRef{{PostID: 88, Summary: "已点赞"}}
	f.social.followeeRefs = []contracts.FolloweeRef{{UserID: 9, Username: "someone"}}
	rt := contracts.Runtime{
		AIID: 1,
		Behavior: contracts.BehaviorSettings{
			AllowAIToAI:          true,
			AllowLike:            true,
			AllowUnfollow:        true,
			CandidateScopeType:   domain.ScopeTags,
			CandidateScopeParams: []byte(`{"tags":["Go","数据库"],"usernames":["alice"]}`),
		},
	}
	task := repo.Task{ID: 1, AIID: 1, TriggerType: domain.TriggerProactive}
	prompt, _, err := f.svc.buildPrompt(context.Background(), task, rt)
	if err != nil {
		t.Fatalf("组装上下文不应失败: %v", err)
	}
	if len(prompt.Candidates) != 1 || len(prompt.Liked) != 1 || len(prompt.Followees) != 1 {
		t.Fatalf("主动互动上下文错误: %+v", prompt)
	}
	if len(f.content.candidateQueries) != 1 {
		t.Fatalf("应查询候选内容: %+v", f.content.candidateQueries)
	}
	query := f.content.candidateQueries[0]
	if query.ExcludeAuthorID != 1 || !query.AllowAIToAI || query.ScopeType != domain.ScopeTags ||
		query.Limit != maxCandidateCount || len(query.ScopeTags) != 2 || len(query.ScopeUsernames) != 1 {
		t.Fatalf("候选查询参数错误: %+v", query)
	}
}

func TestBuildPromptErrors(t *testing.T) {
	t.Run("评论不存在", func(t *testing.T) {
		f := newFlowFixture()
		commentID := int64(500)
		f.social.comments[commentID] = model.Comment{ID: commentID, Status: model.CommentStatusDeleted}
		task := repo.Task{ID: 1, AIID: 1, TriggerType: domain.TriggerComment, TriggerRefType: domain.RefComment, TriggerRefID: &commentID}
		_, _, err := f.svc.buildPrompt(context.Background(), task, contracts.Runtime{AIID: 1})
		if !isNotFound(err) {
			t.Fatalf("已删除评论应返回 NotFound: %v", err)
		}
	})
	t.Run("可见帖不存在", func(t *testing.T) {
		f := newFlowFixture()
		postID := int64(300)
		task := repo.Task{ID: 1, AIID: 1, TriggerType: domain.TriggerMention, TriggerRefType: domain.RefPost, TriggerRefID: &postID}
		_, _, err := f.svc.buildPrompt(context.Background(), task, contracts.Runtime{AIID: 1})
		if !isNotFound(err) {
			t.Fatalf("不可见帖子应返回 NotFound: %v", err)
		}
	})
	t.Run("近期记忆读取失败", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.recentErr = errBoom
		if _, _, err := f.svc.buildPrompt(context.Background(), repo.Task{ID: 1, AIID: 1}, contracts.Runtime{AIID: 1}); !errors.Is(err, errBoom) {
			t.Fatalf("应透传记忆读取错误: %v", err)
		}
	})
	t.Run("长期记忆检索失败", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.longTermErr = errBoom
		if _, _, err := f.svc.buildPrompt(context.Background(), repo.Task{ID: 1, AIID: 1}, contracts.Runtime{AIID: 1}); !errors.Is(err, errBoom) {
			t.Fatalf("应透传长期记忆错误: %v", err)
		}
	})
	t.Run("候选内容读取失败", func(t *testing.T) {
		f := newFlowFixture()
		f.content.candidateErr = errBoom
		rt := contracts.Runtime{AIID: 1, Behavior: contracts.BehaviorSettings{CandidateScopeType: domain.ScopeGlobalPublic}}
		task := repo.Task{ID: 1, AIID: 1, TriggerType: domain.TriggerProactive}
		if _, _, err := f.svc.buildPrompt(context.Background(), task, rt); !errors.Is(err, errBoom) {
			t.Fatalf("应透传候选内容错误: %v", err)
		}
	})
}

func TestAssemblePromptsSections(t *testing.T) {
	f := newFlowFixture()
	rt := contracts.Runtime{
		Username: "ai_1",
		Nickname: "小助手",
		Profile: contracts.Profile{
			SystemPrompt: "角色设定内容",
			Personality:  "温和",
		},
		Behavior: contracts.BehaviorSettings{AllowOriginalPost: true, AllowLike: true},
	}
	prompt := promptInput{
		Event:      "触发事件内容",
		Thread:     []contracts.CommentBrief{{AuthorName: "小明", Content: "讨论"}},
		Candidates: []contracts.PostCandidate{{Post: publicPost(100, 7), AuthorName: "作者七", Summary: "候选摘要"}},
		Recent:     []contracts.Memory{{ID: 1, Content: "近期记忆内容"}},
		LongTerm:   []contracts.Memory{{ID: 2, Content: "长期记忆内容"}},
		Liked:      []contracts.LikedPostRef{{PostID: 88, Summary: "点赞摘要"}},
		Followees:  []contracts.FolloweeRef{{UserID: 9, Username: "someone"}},
	}
	system, user := f.svc.assemblePrompts(rt, prompt)
	for _, want := range []string{"小助手", "角色设定内容", "温和", "可用动作", domain.ActionOriginalPost, domain.ActionLike} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt 缺少 %q: %s", want, system)
		}
	}
	for _, want := range []string{
		"【触发事件】", "触发事件内容", "【最近讨论】", "小明", "【候选公开内容】", "post_id=100",
		"【你的记忆】", "近期记忆内容", "长期记忆内容", "【可取消的关系】", "post_id=88", "user_id=9",
	} {
		if !strings.Contains(user, want) {
			t.Fatalf("user prompt 缺少 %q: %s", want, user)
		}
	}
	if !strings.HasSuffix(user, "请输出一个 JSON 决策对象。") {
		t.Fatalf("user prompt 结尾错误: %s", user)
	}

	// 昵称为空时回退用户名。
	system, _ = f.svc.assemblePrompts(contracts.Runtime{Username: "fallback_user"}, promptInput{})
	if !strings.Contains(system, "fallback_user") {
		t.Fatalf("昵称为空应回退用户名: %s", system)
	}
}

func TestInjectMemoryRefs(t *testing.T) {
	f := newFlowFixture()
	f.svc.injectMemoryRefs(context.Background(), repo.Task{ID: 1}, nil)
	if len(f.tasks.addedRefs) != 0 {
		t.Fatalf("无记忆引用不应写库: %+v", f.tasks.addedRefs)
	}

	f.svc = New(Deps{Clock: f.clk, IDs: &stuckIDs{err: errBoom}, Log: testLogger(), Tasks: f.tasks})
	f.svc.injectMemoryRefs(context.Background(), repo.Task{ID: 1}, []repo.MemoryRef{{MemoryID: 9, MemoryType: "recent"}})
	if len(f.tasks.addedRefs) != 0 {
		t.Fatalf("ID 生成失败不应写库: %+v", f.tasks.addedRefs)
	}

	f.tasks.addRefsErr = errBoom
	f.svc.injectMemoryRefs(context.Background(), repo.Task{ID: 1}, []repo.MemoryRef{{MemoryID: 9, MemoryType: "recent"}})
}

// ===== 记忆形成与沉淀 =====

func TestFormMemory(t *testing.T) {
	f := newFlowFixture()
	rt := contracts.Runtime{AIID: 1}
	task := repo.Task{ID: 1, AIID: 1}
	if err := f.svc.formMemory(context.Background(), task, rt, taskResult{}, f.clk.Now()); err != nil {
		t.Fatalf("无动作结果不应形成记忆: %v", err)
	}
	if len(f.ai.created) != 0 {
		t.Fatalf("无动作结果不应写记忆: %+v", f.ai.created)
	}

	refID := int64(7001)
	result := taskResult{Outcome: &actionOutcome{
		MemoryNote:    strings.Repeat("记忆正文", 200), // 800 字符，未超上限
		ResultRefType: domain.RefPost,
		ResultRefID:   &refID,
	}}
	if err := f.svc.formMemory(context.Background(), task, rt, result, f.clk.Now()); err != nil {
		t.Fatalf("形成记忆不应失败: %v", err)
	}
	if len(f.ai.created) != 1 {
		t.Fatalf("应写入 1 条记忆，实际 %+v", f.ai.created)
	}
	created := f.ai.created[0]
	if created.AIID != 1 || created.MemoryType != "recent" || created.Category != "event" ||
		created.SourceType != "action" || created.InferenceLevel != "fact" ||
		created.SourceRefType != domain.RefPost || created.SourceRefID == nil || *created.SourceRefID != refID {
		t.Fatalf("记忆形成参数错误: %+v", created)
	}
	if created.Content != result.Outcome.MemoryNote {
		t.Fatalf("未超上限的记忆正文不应被截断: %q", created.Content)
	}
	if len(f.ai.addedSources) != 1 || f.ai.addedSources[0].SourceID != refID {
		t.Fatalf("应写入记忆来源关联: %+v", f.ai.addedSources)
	}

	f.ai.createMemoryErr = errBoom
	if err := f.svc.formMemory(context.Background(), task, rt, result, f.clk.Now()); !errors.Is(err, errBoom) {
		t.Fatalf("记忆写入失败应透传: %v", err)
	}
	f.ai.createMemoryErr = nil
	f.ai.addSourcesErr = errBoom
	if err := f.svc.formMemory(context.Background(), task, rt, result, f.clk.Now()); !errors.Is(err, errBoom) {
		t.Fatalf("来源写入失败应透传: %v", err)
	}
}

func TestMaybeEnqueueConsolidation(t *testing.T) {
	f := newFlowFixture()
	at := f.clk.Now()
	if err := f.svc.maybeEnqueueConsolidation(context.Background(), 1, at); err != nil {
		t.Fatalf("未达阈值不应失败: %v", err)
	}
	if len(f.jobs.enqueueCalls) != 0 {
		t.Fatalf("未达阈值不应投递作业: %+v", f.jobs.enqueueCalls)
	}

	f.ai.recentCount = consolidationThreshold
	if err := f.svc.maybeEnqueueConsolidation(context.Background(), 1, at); err != nil {
		t.Fatalf("投递沉淀作业不应失败: %v", err)
	}
	if len(f.jobs.enqueueCalls) != 1 {
		t.Fatalf("达阈值应投递 1 个作业，实际 %+v", f.jobs.enqueueCalls)
	}
	call := f.jobs.enqueueCalls[0]
	if call.Type != contracts.JobTypeMemoryConsolidate || call.RefType != domain.RefAI ||
		call.IdempotencyKey != "memory_consolidation:1:20261018" || call.MaxAttempts != 3 {
		t.Fatalf("沉淀作业参数错误: %+v", call)
	}
	payload, ok := call.Payload.(jobPayload)
	if !ok || payload.AIID != 1 {
		t.Fatalf("沉淀作业负载错误: %+v", call.Payload)
	}

	f.ai.recentCountErr = errBoom
	if err := f.svc.maybeEnqueueConsolidation(context.Background(), 1, at); !errors.Is(err, errBoom) {
		t.Fatalf("计数失败应透传: %v", err)
	}
}

func TestAfterAction(t *testing.T) {
	f := newFlowFixture()
	f.ai.recentCount = consolidationThreshold
	f.content.posts[200] = publicPost(200, 5)
	f.ai.activeSourceRefs = []contracts.MemorySourceRef{{SourceType: domain.RefPost, SourceID: 200}}
	f.svc.afterAction(context.Background(), repo.Task{ID: 1, AIID: 1}, taskResult{Status: domain.TaskStatusSucceeded})
	if len(f.jobs.enqueueCalls) != 1 {
		t.Fatalf("成功动作后应投递沉淀作业: %+v", f.jobs.enqueueCalls)
	}
	if len(f.ai.invalidated) != 0 {
		t.Fatalf("来源仍公开不应失效记忆: %+v", f.ai.invalidated)
	}

	f = newFlowFixture()
	f.ai.activeRefsErr = errBoom
	f.svc.afterAction(context.Background(), repo.Task{ID: 1, AIID: 1}, taskResult{Status: domain.TaskStatusCanceled})
}

func TestConsolidateMemoriesErrorPaths(t *testing.T) {
	t.Run("读取近期记忆失败", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.recentErr = errBoom
		if err := f.svc.consolidateMemories(context.Background(), 1); !errors.Is(err, errBoom) {
			t.Fatalf("应透传读取错误: %v", err)
		}
	})
	t.Run("近期记忆为空直接返回", func(t *testing.T) {
		f := newFlowFixture()
		if err := f.svc.consolidateMemories(context.Background(), 1); err != nil {
			t.Fatalf("空记忆不应失败: %v", err)
		}
		if len(f.ai.created) != 0 {
			t.Fatalf("空记忆不应写沉淀: %+v", f.ai.created)
		}
	})
	t.Run("写入长期记忆失败", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.recent = []contracts.Memory{{ID: 1, Content: "近期"}}
		f.ai.createMemoryErr = errBoom
		if err := f.svc.consolidateMemories(context.Background(), 1); !errors.Is(err, errBoom) {
			t.Fatalf("应透传写入错误: %v", err)
		}
	})
	t.Run("来源关联失败只告警", func(t *testing.T) {
		f := newFlowFixture()
		refID := int64(200)
		f.ai.recent = []contracts.Memory{{
			ID: 1, Content: "近期", SourceRefType: domain.RefPost, SourceRefID: &refID,
		}}
		f.ai.addSourcesErr = errBoom
		if err := f.svc.consolidateMemories(context.Background(), 1); err != nil {
			t.Fatalf("来源关联失败不应让沉淀失败: %v", err)
		}
		if len(f.ai.created) != 1 || len(f.ai.superseded) != 1 {
			t.Fatalf("长期记忆与取代关系仍应写入: created=%+v superseded=%+v", f.ai.created, f.ai.superseded)
		}
	})
	t.Run("读取记忆来源失败时只用直接引用", func(t *testing.T) {
		f := newFlowFixture()
		refID := int64(200)
		f.ai.recent = []contracts.Memory{{
			ID: 1, Content: "近期", SourceRefType: domain.RefPost, SourceRefID: &refID,
		}}
		f.ai.listSourcesErr = errBoom
		if err := f.svc.consolidateMemories(context.Background(), 1); err != nil {
			t.Fatalf("沉淀不应失败: %v", err)
		}
		if len(f.ai.addedSources) != 1 || f.ai.addedSources[0].SourceID != refID {
			t.Fatalf("应只用直接引用来源: %+v", f.ai.addedSources)
		}
	})
}

func TestReconcileErrorBranches(t *testing.T) {
	t.Run("读取 pending 任务失败", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.pendingErr = errBoom
		if err := f.svc.Reconcile(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("应透传错误: %v", err)
		}
	})
	t.Run("查询作业失败", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.pending = []repo.Task{{ID: 1, AIID: 1}}
		f.jobs.refErr = errBoom
		if err := f.svc.Reconcile(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("应透传作业查询错误: %v", err)
		}
	})
	t.Run("已有活跃作业不重复投递", func(t *testing.T) {
		f := newFlowFixture()
		refID := int64(1)
		f.tasks.pending = []repo.Task{{ID: 1, AIID: 1}}
		f.jobs.byRef = []contracts.JobSnapshot{{ID: 9, RefID: &refID, Status: contracts.JobStatusRunning}}
		if err := f.svc.Reconcile(context.Background()); err != nil {
			t.Fatalf("reconcile 不应失败: %v", err)
		}
		if len(f.jobs.reactivated) != 0 || len(f.jobs.enqueueCalls) != 0 {
			t.Fatalf("已有活跃作业不应重投: reactivated=%+v enqueued=%+v", f.jobs.reactivated, f.jobs.enqueueCalls)
		}
	})
	t.Run("未超租约的 running 任务跳过", func(t *testing.T) {
		f := newFlowFixture()
		startedAt := f.clk.Now().Add(-time.Minute)
		f.tasks.running = []repo.Task{{ID: 2, AIID: 1, StartedAt: &startedAt}}
		if err := f.svc.Reconcile(context.Background()); err != nil {
			t.Fatalf("reconcile 不应失败: %v", err)
		}
		if len(f.tasks.statuses) != 0 || len(f.jobs.reactivated) != 0 {
			t.Fatalf("未超租约不应处置: statuses=%+v reactivated=%+v", f.tasks.statuses, f.jobs.reactivated)
		}
	})
	t.Run("超时任务写状态失败返回错误", func(t *testing.T) {
		f := newFlowFixture()
		startedAt := f.clk.Now().Add(-30 * time.Minute)
		f.tasks.running = []repo.Task{{ID: 3, AIID: 1, StartedAt: &startedAt}}
		f.tasks.statusErrs = errBoom
		if err := f.svc.Reconcile(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("应返回状态写入错误: %v", err)
		}
	})
	t.Run("running 任务查询失败", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.listErr = errBoom
		if err := f.svc.Reconcile(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("应透传查询错误: %v", err)
		}
	})
}

// ===== 纯函数 =====

func TestParseStringList(t *testing.T) {
	if got := parseStringList(nil, "tags"); got != nil {
		t.Fatalf("空参数应返回 nil: %+v", got)
	}
	if got := parseStringList([]byte("not-json"), "tags"); got != nil {
		t.Fatalf("非法 JSON 应返回 nil: %+v", got)
	}
	raw := []byte(`{"tags":["Go","数据库"],"usernames":["alice"]}`)
	if got := parseStringList(raw, "tags"); len(got) != 2 || got[0] != "Go" {
		t.Fatalf("tags 解析错误: %+v", got)
	}
	if got := parseStringList(raw, "usernames"); len(got) != 1 || got[0] != "alice" {
		t.Fatalf("usernames 解析错误: %+v", got)
	}
	if got := parseStringList(raw, "missing"); got != nil {
		t.Fatalf("缺失键应返回 nil: %+v", got)
	}
}

func TestTopicKeywords(t *testing.T) {
	prompt := promptInput{
		Event: "Go 与数据库",
		Candidates: []contracts.PostCandidate{
			{Summary: "Go 并发"},
			{Summary: "数据库 索引"},
		},
	}
	keywords := topicKeywords(prompt)
	if len(keywords) == 0 {
		t.Fatal("应从事件与候选提取关键词")
	}
	seen := map[string]bool{}
	for _, keyword := range keywords {
		if seen[keyword] {
			t.Fatalf("关键词重复: %v", keywords)
		}
		seen[keyword] = true
	}
}

func TestDomainKeywords(t *testing.T) {
	keywords := domainKeywords("Go，数据库。Go；并发 单字 a 这是 这是一个超过三十二个字符的非常长的片段用来验证长度上限会被过滤掉")
	if len(keywords) == 0 {
		t.Fatal("应提取关键词")
	}
	if len(keywords) > 8 {
		t.Fatalf("关键词最多 8 个，实际 %d", len(keywords))
	}
	for _, keyword := range keywords {
		if len([]rune(keyword)) < 2 || len([]rune(keyword)) > 32 {
			t.Fatalf("关键词长度非法: %q", keyword)
		}
	}
	if len(domainKeywords("")) != 0 {
		t.Fatalf("空文本应返回空: %v", domainKeywords(""))
	}
}

func TestActionCatalog(t *testing.T) {
	all := actionCatalog(contracts.BehaviorSettings{
		AllowOriginalPost: true, AllowActiveComment: true, AllowPassiveReply: true,
		AllowLike: true, AllowFollow: true, AllowUnfollow: true, AllowRepost: true, AllowQuote: true,
	})
	for _, action := range []string{
		domain.ActionOriginalPost, domain.ActionComment, domain.ActionReply, domain.ActionLike,
		domain.ActionUnlike, domain.ActionFollow, domain.ActionUnfollow, domain.ActionRepost,
		domain.ActionQuote, domain.ActionNoop,
	} {
		if !strings.Contains(all, action) {
			t.Fatalf("动作目录缺少 %q: %s", action, all)
		}
	}
	if got := actionCatalog(contracts.BehaviorSettings{}); got != domain.ActionNoop {
		t.Fatalf("全部关闭时动作目录应只有 noop，实际 %q", got)
	}
}

func TestMemoryIDs(t *testing.T) {
	ids := memoryIDs([]repo.MemoryRef{{MemoryID: 1}, {MemoryID: 2}})
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("记忆 ID 提取错误: %+v", ids)
	}
	if len(memoryIDs(nil)) != 0 {
		t.Fatal("nil 引用应返回空切片")
	}
}

func TestFirstNonEmptyAndIsNotFound(t *testing.T) {
	if firstNonEmpty("", " ", "第二个") != "第二个" {
		t.Fatal("firstNonEmpty 应跳过空白")
	}
	if firstNonEmpty("  ", "") != "" {
		t.Fatal("全空白应返回空串")
	}
	if isNotFound(errBoom) {
		t.Fatal("普通错误不是 NotFound")
	}
	if !isNotFound(apperr.NotFound("不存在")) {
		t.Fatal("NotFound 应被识别")
	}
}

func TestJSONUnmarshal(t *testing.T) {
	var target map[string]int
	if err := jsonUnmarshal([]byte(`{"a":1}`), &target); err != nil || target["a"] != 1 {
		t.Fatalf("JSON 解析错误: %v %+v", err, target)
	}
	if err := jsonUnmarshal([]byte("bad"), &target); err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
}
