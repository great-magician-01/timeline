package app

// admin.go / read.go 用例测试：管理员生成与草稿管理、任务与调用记录读取、人工重试/取消、
// 只读摘要，以及目标授权（target.go）与来源记忆复核（memory_recheck.go）的边界分支。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== 管理端生成 =====

func TestAdminGenerationValidation(t *testing.T) {
	f := newFlowFixture()
	if _, err := f.svc.GenerateDraft(context.Background(), 0, 1, GenerationInput{}); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("缺少管理员应返回 Unauthenticated: %v", err)
	}
	if _, err := f.svc.PublishDirect(context.Background(), 100, 0, GenerationInput{}); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("缺少 AI 账号应返回 Validation: %v", err)
	}

	f = newFlowFixture()
	f.enableAI(1)
	past := f.clk.Now().Add(-time.Minute)
	if _, err := f.svc.GenerateDraft(context.Background(), 100, 1, GenerationInput{ScheduledAt: &past}); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("定时时间早于当前应返回 Validation: %v", err)
	}

	f.ai.runtimeErr = errBoom
	if _, err := f.svc.GenerateDraft(context.Background(), 100, 1, GenerationInput{}); !errors.Is(err, errBoom) {
		t.Fatalf("应透传运行时读取错误: %v", err)
	}
	if len(f.tasks.inserted) != 0 {
		t.Fatalf("校验失败不应写入任务: %+v", f.tasks.inserted)
	}
}

func TestAdminGenerateDraftSuccess(t *testing.T) {
	f := newFlowFixture()
	f.enableAI(1)
	f.tasks.memoryRefs[1001] = []repo.MemoryRef{{MemoryID: 77, MemoryType: "recent"}}
	view, err := f.svc.GenerateDraft(context.Background(), 100, 1, GenerationInput{})
	if err != nil {
		t.Fatalf("生成草稿任务不应失败: %v", err)
	}
	if view.Task.ID != 1001 || view.Task.TriggerType != domain.TriggerManual {
		t.Fatalf("返回任务视图错误: %+v", view.Task)
	}
	if len(view.MemoryRefs) != 1 {
		t.Fatalf("任务视图应带注入记忆: %+v", view.MemoryRefs)
	}
	if len(f.tasks.inserted) != 1 {
		t.Fatalf("应写入 1 条任务，实际 %+v", f.tasks.inserted)
	}
	insert := f.tasks.inserted[0]
	if insert.TriggerRefType != domain.GenerationModeDraft || insert.ActionType != domain.ActionOriginalPost ||
		insert.AIID != 1 || !strings.HasPrefix(insert.IdempotencyKey, "ai_generate:draft:1:") {
		t.Fatalf("草稿任务参数错误: %+v", insert)
	}
	if len(f.jobs.enqueueCalls) != 1 {
		t.Fatalf("应投递执行作业: %+v", f.jobs.enqueueCalls)
	}
	if len(f.audit.entries) != 1 {
		t.Fatalf("应写 1 条审计，实际 %+v", f.audit.entries)
	}
	entry := f.audit.entries[0]
	detail, _ := entry.Detail.(map[string]any)
	if entry.Action != contracts.AuditActionAIGenerate || entry.AdminID != 100 || entry.TargetType != domain.RefAI ||
		entry.TargetID == nil || *entry.TargetID != 1 || detail["mode"] != domain.GenerationModeDraft ||
		detail["task_id"] != "1001" {
		t.Fatalf("生成审计参数错误: %+v", entry)
	}
}

func TestAdminPublishDirectSuccess(t *testing.T) {
	f := newFlowFixture()
	f.enableAI(1)
	now := f.clk.Now()
	scheduled := now.Add(2 * time.Hour)
	if _, err := f.svc.PublishDirect(context.Background(), 100, 1, GenerationInput{ScheduledAt: &scheduled}); err != nil {
		t.Fatalf("立即发布任务不应失败: %v", err)
	}
	insert := f.tasks.inserted[0]
	if insert.TriggerRefType != domain.GenerationModeDirect ||
		!strings.HasPrefix(insert.IdempotencyKey, "ai_generate:direct:1:") {
		t.Fatalf("立即发布任务参数错误: %+v", insert)
	}
	if insert.ScheduledAt == nil || !insert.ScheduledAt.Equal(scheduled) {
		t.Fatalf("定时时间未写入任务: %+v", insert)
	}
	if len(f.jobs.enqueueCalls) != 1 || !f.jobs.enqueueCalls[0].RunAt.Equal(scheduled) {
		t.Fatalf("作业计划时间应为定时时间: %+v", f.jobs.enqueueCalls)
	}
	detail, _ := f.audit.entries[0].Detail.(map[string]any)
	if detail["mode"] != domain.GenerationModeDirect {
		t.Fatalf("审计模式错误: %+v", detail)
	}
}

func TestAdminGenerationErrors(t *testing.T) {
	t.Run("幂等键冲突", func(t *testing.T) {
		f := newFlowFixture()
		f.enableAI(1)
		f.tasks.duplicate = true
		if _, err := f.svc.GenerateDraft(context.Background(), 100, 1, GenerationInput{}); !apperr.IsKind(err, apperr.KindConflict) {
			t.Fatalf("重复生成应返回 Conflict: %v", err)
		}
	})
	t.Run("审计写入失败", func(t *testing.T) {
		f := newFlowFixture()
		f.enableAI(1)
		f.audit.err = errBoom
		if _, err := f.svc.GenerateDraft(context.Background(), 100, 1, GenerationInput{}); !errors.Is(err, errBoom) {
			t.Fatalf("应透传审计错误: %v", err)
		}
	})
	t.Run("作业投递失败", func(t *testing.T) {
		f := newFlowFixture()
		f.enableAI(1)
		f.jobs.enqueueErr = errBoom
		if _, err := f.svc.GenerateDraft(context.Background(), 100, 1, GenerationInput{}); !errors.Is(err, errBoom) {
			t.Fatalf("应透传作业投递错误: %v", err)
		}
	})
}

// ===== AI 草稿列表 / 编辑 / 发布 =====

func TestAdminListAIDrafts(t *testing.T) {
	f := newFlowFixture()
	if _, _, err := f.svc.ListAIDrafts(context.Background(), 0, 0, 0); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("缺少 AI 账号应返回 Validation: %v", err)
	}
	f.content.draftErr = errBoom
	if _, _, err := f.svc.ListAIDrafts(context.Background(), 1, 0, 0); !errors.Is(err, errBoom) {
		t.Fatalf("应透传草稿读取错误: %v", err)
	}

	f = newFlowFixture()
	t0 := f.clk.Now()
	runAt := t0.Add(time.Hour)
	postID := int64(10)
	f.content.draftList = []contracts.DraftSummary{
		{Post: model.Post{ID: postID, AuthorID: 1, Status: model.PostStatusDraft, UpdatedAt: t0}, ContentHTML: "<p>正文</p>"},
		{Post: model.Post{ID: 9, AuthorID: 1, Status: model.PostStatusDraft}, ContentHTML: "<p>旧</p>"},
	}
	f.jobs.byRef = []contracts.JobSnapshot{{
		ID: 8, RefID: &postID, Status: contracts.JobStatusPending, LastError: "", RunAt: runAt,
	}}
	views, hasMore, err := f.svc.ListAIDrafts(context.Background(), 1, 0, 5)
	if err != nil {
		t.Fatalf("读取草稿列表不应失败: %v", err)
	}
	if hasMore {
		t.Fatal("两条草稿不应有下一页")
	}
	if len(views) != 2 {
		t.Fatalf("应返回 2 条草稿，实际 %d", len(views))
	}
	if f.content.draftQueries[0].Limit != 21 || f.content.draftQueries[0].Cursor != 5 {
		t.Fatalf("草稿查询参数错误: %+v", f.content.draftQueries[0])
	}
	if views[0].ContentHTML != "<p>正文</p>" || views[0].PublishStatus != contracts.JobStatusPending ||
		views[0].PendingRunAt == nil || !views[0].PendingRunAt.Equal(runAt) {
		t.Fatalf("草稿发布状态补充错误: %+v", views[0])
	}
	if views[1].PublishStatus != "" || views[1].PendingRunAt != nil {
		t.Fatalf("无作业草稿不应有发布状态: %+v", views[1])
	}

	// 多取一条用于判定 hasMore。
	f = newFlowFixture()
	f.content.draftList = []contracts.DraftSummary{
		{Post: model.Post{ID: 10, AuthorID: 1}},
		{Post: model.Post{ID: 9, AuthorID: 1}},
	}
	views, hasMore, err = f.svc.ListAIDrafts(context.Background(), 1, 1, 0)
	if err != nil || !hasMore || len(views) != 1 {
		t.Fatalf("hasMore 判定错误: views=%d hasMore=%v err=%v", len(views), hasMore, err)
	}
	if f.content.draftQueries[0].Limit != 2 {
		t.Fatalf("limit=1 时应多取一条，查询参数 %+v", f.content.draftQueries[0])
	}

	f = newFlowFixture()
	f.content.draftList = []contracts.DraftSummary{{Post: model.Post{ID: 10, AuthorID: 1}}}
	f.jobs.refErr = errBoom
	if _, _, err := f.svc.ListAIDrafts(context.Background(), 1, 0, 0); !errors.Is(err, errBoom) {
		t.Fatalf("应透传作业查询错误: %v", err)
	}
}

func TestAdminUpdateAIDraft(t *testing.T) {
	f := newFlowFixture()
	postID := int64(10)
	if err := f.svc.UpdateAIDraft(context.Background(), 0, 1, postID, "markdown", "正文"); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("缺少管理员应返回 Unauthenticated: %v", err)
	}

	f.content.getPostErr = errBoom
	if err := f.svc.UpdateAIDraft(context.Background(), 100, 1, postID, "markdown", "正文"); !errors.Is(err, errBoom) {
		t.Fatalf("应透传帖子读取错误: %v", err)
	}

	f = newFlowFixture()
	f.content.getPost[postID] = model.Post{ID: postID, AuthorID: 2}
	if err := f.svc.UpdateAIDraft(context.Background(), 100, 1, postID, "markdown", "正文"); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非该 AI 草稿应返回 NotFound: %v", err)
	}

	f = newFlowFixture()
	f.content.getPost[postID] = model.Post{ID: postID, AuthorID: 1, Status: model.PostStatusDeleted}
	if err := f.svc.UpdateAIDraft(context.Background(), 100, 1, postID, "markdown", "正文"); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("已删除草稿应返回 NotFound: %v", err)
	}

	f = newFlowFixture()
	f.content.getPost[postID] = model.Post{ID: postID, AuthorID: 1, Status: model.PostStatusDraft}
	if err := f.svc.UpdateAIDraft(context.Background(), 100, 1, postID, "markdown", "正文内容"); err != nil {
		t.Fatalf("编辑草稿不应失败: %v", err)
	}
	if len(f.content.updatedDrafts) != 1 {
		t.Fatalf("应更新 1 次草稿，实际 %+v", f.content.updatedDrafts)
	}
	update := f.content.updatedDrafts[0]
	if update.PostID != postID || update.ContentFormat != "markdown" || update.ContentSource != "正文内容" ||
		!update.At.Equal(f.clk.Now()) {
		t.Fatalf("草稿更新参数错误: %+v", update)
	}
	detail, _ := f.audit.entries[0].Detail.(map[string]any)
	if f.audit.entries[0].Action != contracts.AuditActionAIGenerate || detail["action"] != "update_draft" {
		t.Fatalf("草稿编辑审计错误: %+v", f.audit.entries[0])
	}

	f.content.updateDraftErr = errBoom
	if err := f.svc.UpdateAIDraft(context.Background(), 100, 1, postID, "markdown", "正文"); !errors.Is(err, errBoom) {
		t.Fatalf("应透传更新错误: %v", err)
	}
	f = newFlowFixture()
	f.content.getPost[postID] = model.Post{ID: postID, AuthorID: 1, Status: model.PostStatusDraft}
	f.audit.err = errBoom
	if err := f.svc.UpdateAIDraft(context.Background(), 100, 1, postID, "markdown", "正文"); !errors.Is(err, errBoom) {
		t.Fatalf("应透传审计错误: %v", err)
	}
}

func TestAdminPublishAIDraft(t *testing.T) {
	f := newFlowFixture()
	postID := int64(10)
	if err := f.svc.PublishAIDraft(context.Background(), 0, 1, postID); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("缺少管理员应返回 Unauthenticated: %v", err)
	}
	f.content.getPostErr = errBoom
	if err := f.svc.PublishAIDraft(context.Background(), 100, 1, postID); !errors.Is(err, errBoom) {
		t.Fatalf("应透传帖子读取错误: %v", err)
	}

	f = newFlowFixture()
	f.content.getPost[postID] = model.Post{ID: postID, AuthorID: 2}
	if err := f.svc.PublishAIDraft(context.Background(), 100, 1, postID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非该 AI 草稿应返回 NotFound: %v", err)
	}

	f = newFlowFixture()
	f.content.getPost[postID] = model.Post{ID: postID, AuthorID: 1, Status: model.PostStatusDraft}
	if err := f.svc.PublishAIDraft(context.Background(), 100, 1, postID); err != nil {
		t.Fatalf("发布草稿不应失败: %v", err)
	}
	if len(f.content.publishedDrafts) != 1 {
		t.Fatalf("应发布 1 次草稿，实际 %+v", f.content.publishedDrafts)
	}
	publish := f.content.publishedDrafts[0]
	if publish.ActorID != 100 || publish.PostID != postID || !publish.At.Equal(f.clk.Now()) {
		t.Fatalf("发布参数错误: %+v", publish)
	}
	if f.audit.entries[0].Action != contracts.AuditActionAIPublish || f.audit.entries[0].TargetID == nil ||
		*f.audit.entries[0].TargetID != postID {
		t.Fatalf("发布审计错误: %+v", f.audit.entries[0])
	}

	f.content.publishErr = errBoom
	if err := f.svc.PublishAIDraft(context.Background(), 100, 1, postID); !errors.Is(err, errBoom) {
		t.Fatalf("应透传发布错误: %v", err)
	}
	f = newFlowFixture()
	f.content.getPost[postID] = model.Post{ID: postID, AuthorID: 1, Status: model.PostStatusDraft}
	f.audit.err = errBoom
	if err := f.svc.PublishAIDraft(context.Background(), 100, 1, postID); !errors.Is(err, errBoom) {
		t.Fatalf("应透传审计错误: %v", err)
	}
}

// ===== 任务读取 =====

func TestAdminListAndGetTasks(t *testing.T) {
	f := newFlowFixture()
	f.tasks.listErr = errBoom
	if _, _, err := f.svc.ListAITasks(context.Background(), repo.TaskFilter{}); !errors.Is(err, errBoom) {
		t.Fatalf("应透传任务列表错误: %v", err)
	}

	f = newFlowFixture()
	f.tasks.listResult = []repo.Task{{ID: 5, AIID: 1}, {ID: 4, AIID: 1}}
	f.tasks.memoryRefs[5] = []repo.MemoryRef{{MemoryID: 88, MemoryType: "long_term"}}
	views, _, err := f.svc.ListAITasks(context.Background(), repo.TaskFilter{AIID: 1})
	if err != nil {
		t.Fatalf("读取任务列表不应失败: %v", err)
	}
	if len(views) != 2 || len(views[0].MemoryRefs) != 1 || views[0].MemoryRefs[0].MemoryID != 88 {
		t.Fatalf("任务列表记忆引用错误: %+v", views)
	}

	f.tasks.memoryRefsErr = errBoom
	if _, _, err := f.svc.ListAITasks(context.Background(), repo.TaskFilter{AIID: 1}); !errors.Is(err, errBoom) {
		t.Fatalf("应透传记忆引用读取错误: %v", err)
	}

	// GetTask：不存在与成功。
	f = newFlowFixture()
	if _, err := f.svc.GetTask(context.Background(), 404); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("任务不存在应返回 NotFound: %v", err)
	}
	f.tasks.tasks[5] = repo.Task{ID: 5, AIID: 1}
	f.tasks.memoryRefs[5] = []repo.MemoryRef{{MemoryID: 66, MemoryType: "recent"}}
	view, err := f.svc.GetTask(context.Background(), 5)
	if err != nil || view.Task.ID != 5 || len(view.MemoryRefs) != 1 {
		t.Fatalf("任务详情错误: %+v err=%v", view, err)
	}
}

// ===== 人工重试 / 取消 =====

func newRetryFixture() (*flowFixture, int64) {
	f := newFlowFixture()
	taskID := int64(5)
	f.tasks.tasks[taskID] = repo.Task{ID: taskID, AIID: 1, Status: domain.TaskStatusFailed}
	return f, taskID
}

func TestAdminRetryAITaskBranches(t *testing.T) {
	t.Run("任务不存在", func(t *testing.T) {
		f, _ := newRetryFixture()
		if _, err := f.svc.RetryAITask(context.Background(), 100, 404); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("应返回 NotFound: %v", err)
		}
	})
	t.Run("状态不允许重试", func(t *testing.T) {
		f, taskID := newRetryFixture()
		f.tasks.tasks[taskID] = repo.Task{ID: taskID, AIID: 1, Status: domain.TaskStatusSucceeded}
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("成功任务不可重试: %v", err)
		}
	})
	t.Run("没有旧作业时按幂等键补投", func(t *testing.T) {
		f, taskID := newRetryFixture()
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); err != nil {
			t.Fatalf("重试不应失败: %v", err)
		}
		if len(f.tasks.statuses) != 1 || f.tasks.statuses[0].status != domain.TaskStatusPending {
			t.Fatalf("应先把任务置回 pending: %+v", f.tasks.statuses)
		}
		if len(f.jobs.enqueueCalls) != 1 || f.jobs.enqueueCalls[0].IdempotencyKey != "ai_task:5" {
			t.Fatalf("应补投执行作业: %+v", f.jobs.enqueueCalls)
		}
		detail, _ := f.audit.entries[0].Detail.(map[string]any)
		if f.audit.entries[0].Action != contracts.AuditActionTaskRetry || detail["previous_status"] != domain.TaskStatusFailed {
			t.Fatalf("重试审计错误: %+v", f.audit.entries[0])
		}
	})
	t.Run("failed 作业人工重试", func(t *testing.T) {
		f, taskID := newRetryFixture()
		f.jobs.byRef = []contracts.JobSnapshot{{ID: 9, Status: contracts.JobStatusFailed}}
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); err != nil {
			t.Fatalf("重试不应失败: %v", err)
		}
		if len(f.jobs.retried) != 1 || f.jobs.retried[0] != 9 {
			t.Fatalf("应人工重试 failed 作业: %+v", f.jobs.retried)
		}
	})
	t.Run("pending 作业直接复用", func(t *testing.T) {
		f, taskID := newRetryFixture()
		f.jobs.byRef = []contracts.JobSnapshot{{ID: 9, Status: contracts.JobStatusPending}}
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); err != nil {
			t.Fatalf("重试不应失败: %v", err)
		}
		if len(f.jobs.retried) != 0 || len(f.jobs.enqueueCalls) != 0 || len(f.jobs.reactivated) != 0 {
			t.Fatalf("pending 作业不应重复处置: %+v", f.jobs)
		}
	})
	t.Run("canceled 作业重新激活", func(t *testing.T) {
		f, taskID := newRetryFixture()
		f.jobs.byRef = []contracts.JobSnapshot{{ID: 9, Status: contracts.JobStatusCanceled}}
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); err != nil {
			t.Fatalf("重试不应失败: %v", err)
		}
		if len(f.jobs.reactivated) != 1 || f.jobs.reactivated[0] != "ai_task:5" {
			t.Fatalf("应重激活 canceled 作业: %+v", f.jobs.reactivated)
		}
	})
	t.Run("succeeded 作业用一次性幂等键补投", func(t *testing.T) {
		f, taskID := newRetryAIDiFixture()
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); err != nil {
			t.Fatalf("重试不应失败: %v", err)
		}
		if len(f.jobs.enqueueCalls) != 1 || !strings.Contains(f.jobs.enqueueCalls[0].IdempotencyKey, ":retry:") {
			t.Fatalf("应使用一次性幂等键补投: %+v", f.jobs.enqueueCalls)
		}
	})
	t.Run("状态写回失败", func(t *testing.T) {
		f, taskID := newRetryFixture()
		f.tasks.statusErrs = errBoom
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); !errors.Is(err, errBoom) {
			t.Fatalf("应透传状态写入错误: %v", err)
		}
	})
	t.Run("作业查询失败", func(t *testing.T) {
		f, taskID := newRetryFixture()
		f.jobs.refErr = errBoom
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); !errors.Is(err, errBoom) {
			t.Fatalf("应透传作业查询错误: %v", err)
		}
	})
	t.Run("作业人工重试失败", func(t *testing.T) {
		f, taskID := newRetryFixture()
		f.jobs.byRef = []contracts.JobSnapshot{{ID: 9, Status: contracts.JobStatusFailed}}
		f.jobs.retryErr = errBoom
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); !errors.Is(err, errBoom) {
			t.Fatalf("应透传作业重试错误: %v", err)
		}
	})
	t.Run("审计写入失败", func(t *testing.T) {
		f, taskID := newRetryFixture()
		f.audit.err = errBoom
		if _, err := f.svc.RetryAITask(context.Background(), 100, taskID); !errors.Is(err, errBoom) {
			t.Fatalf("应透传审计错误: %v", err)
		}
	})
}

// newRetryAIDiFixture 构造旧作业已 succeeded 的重试夹具。
func newRetryAIDiFixture() (*flowFixture, int64) {
	f, taskID := newRetryFixture()
	f.jobs.byRef = []contracts.JobSnapshot{{ID: 9, Status: contracts.JobStatusSucceeded}}
	return f, taskID
}

func TestAdminCancelAITaskBranches(t *testing.T) {
	t.Run("状态不允许取消", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.tasks[5] = repo.Task{ID: 5, AIID: 1, Status: domain.TaskStatusSucceeded}
		if _, err := f.svc.CancelAITask(context.Background(), 100, 5); !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("成功任务不可取消: %v", err)
		}
	})
	t.Run("任务不存在", func(t *testing.T) {
		f := newFlowFixture()
		if _, err := f.svc.CancelAITask(context.Background(), 100, 404); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("应返回 NotFound: %v", err)
		}
	})
	t.Run("取消 running 任务并取消 pending 作业", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.tasks[5] = repo.Task{ID: 5, AIID: 1, Status: domain.TaskStatusRunning}
		f.jobs.byRef = []contracts.JobSnapshot{
			{ID: 77, Status: contracts.JobStatusPending},
			{ID: 78, Status: contracts.JobStatusSucceeded},
		}
		if _, err := f.svc.CancelAITask(context.Background(), 100, 5); err != nil {
			t.Fatalf("取消任务不应失败: %v", err)
		}
		if f.tasks.statuses[0].status != domain.TaskStatusCanceled {
			t.Fatalf("任务应置为 canceled: %+v", f.tasks.statuses)
		}
		if len(f.jobs.canceled) != 1 || f.jobs.canceled[0] != 77 {
			t.Fatalf("只应取消 pending 作业: %+v", f.jobs.canceled)
		}
	})
	t.Run("作业查询失败仍完成取消", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.tasks[5] = repo.Task{ID: 5, AIID: 1, Status: domain.TaskStatusPending}
		f.jobs.refErr = errBoom
		if _, err := f.svc.CancelAITask(context.Background(), 100, 5); err != nil {
			t.Fatalf("作业查询失败不应阻塞取消: %v", err)
		}
		if len(f.audit.entries) != 1 {
			t.Fatalf("仍应写审计: %+v", f.audit.entries)
		}
	})
	t.Run("审计写入失败", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.tasks[5] = repo.Task{ID: 5, AIID: 1, Status: domain.TaskStatusPending}
		f.audit.err = errBoom
		if _, err := f.svc.CancelAITask(context.Background(), 100, 5); !errors.Is(err, errBoom) {
			t.Fatalf("应透传审计错误: %v", err)
		}
	})
}

// ===== 调用记录读取 =====

func TestAdminCallLogReads(t *testing.T) {
	f := newFlowFixture()
	f.callLogs.listResult = []repo.CallLog{{ID: 2}, {ID: 1}}
	f.callLogs.listMore = true
	views, hasMore, err := f.svc.ListCallLogs(context.Background(), repo.CallLogFilter{AIID: 3, Limit: 2})
	if err != nil {
		t.Fatalf("读取调用记录不应失败: %v", err)
	}
	if len(views) != 2 || !hasMore || views[0].ID != 2 {
		t.Fatalf("调用记录列表错误: %+v hasMore=%v", views, hasMore)
	}
	if f.callLogs.listFilters[0].AIID != 3 || f.callLogs.listFilters[0].Limit != 2 {
		t.Fatalf("过滤条件未透传: %+v", f.callLogs.listFilters[0])
	}

	f.callLogs.listErr = errBoom
	if _, _, err := f.svc.ListCallLogs(context.Background(), repo.CallLogFilter{}); !errors.Is(err, errBoom) {
		t.Fatalf("应透传列表错误: %v", err)
	}

	f.callLogs.getResult = repo.CallLog{ID: 7, Model: "test-model"}
	entry, err := f.svc.GetCallLog(context.Background(), 7)
	if err != nil || entry.ID != 7 || entry.Model != "test-model" {
		t.Fatalf("调用记录详情错误: %+v err=%v", entry, err)
	}
	f.callLogs.getErr = errBoom
	if _, err := f.svc.GetCallLog(context.Background(), 7); !errors.Is(err, errBoom) {
		t.Fatalf("应透传详情错误: %v", err)
	}
}

func TestScheduledAtFromRaw(t *testing.T) {
	now := testClock().Now()
	future := now.Add(time.Hour).Format(time.RFC3339)
	at, err := scheduledAtFromRaw("", now)
	if at != nil || err != nil {
		t.Fatalf("空串应返回 (nil,nil)，实际 (%v,%v)", at, err)
	}
	if _, err := scheduledAtFromRaw("not-a-time", now); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法格式应返回 Validation: %v", err)
	}
	if _, err := scheduledAtFromRaw(now.Add(-time.Hour).Format(time.RFC3339), now); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("早于当前应返回 Validation: %v", err)
	}
	parsed, err := scheduledAtFromRaw(future, now)
	if err != nil || parsed == nil || !parsed.Equal(now.Add(time.Hour)) {
		t.Fatalf("合法时间解析错误: %v %v", parsed, err)
	}
}

// ===== read.go 只读摘要 =====

func TestRecentTaskBriefs(t *testing.T) {
	f := newFlowFixture()
	if briefs, err := f.svc.RecentTaskBriefs(context.Background(), 0, 10); briefs != nil || err != nil {
		t.Fatalf("非法 AI 应返回空: %+v %v", briefs, err)
	}

	f = newFlowFixture()
	f.tasks.listResult = []repo.Task{{
		ID: 7, TriggerType: domain.TriggerManual, ActionType: domain.ActionOriginalPost,
		Status: domain.TaskStatusSucceeded, CreatedAt: f.clk.Now(),
	}}
	briefs, err := f.svc.RecentTaskBriefs(context.Background(), 1, 0)
	if err != nil {
		t.Fatalf("任务摘要不应失败: %v", err)
	}
	if len(briefs) != 1 || briefs[0].ID != 7 || briefs[0].Status != domain.TaskStatusSucceeded {
		t.Fatalf("任务摘要映射错误: %+v", briefs)
	}
	if f.tasks.listFilters[0].Limit != 20 {
		t.Fatalf("非法 limit 应回退 20，实际 %+v", f.tasks.listFilters[0])
	}
	if _, err := f.svc.RecentTaskBriefs(context.Background(), 1, 500); err != nil {
		t.Fatalf("超限 limit 不应失败: %v", err)
	}
	if f.tasks.listFilters[1].Limit != 20 {
		t.Fatalf("超限 limit 应回退 20，实际 %+v", f.tasks.listFilters[1])
	}
	if _, err := f.svc.RecentTaskBriefs(context.Background(), 1, 5); err != nil {
		t.Fatalf("合法 limit 不应失败: %v", err)
	}
	if f.tasks.listFilters[2].Limit != 5 {
		t.Fatalf("limit 未透传: %+v", f.tasks.listFilters[2])
	}

	f.tasks.listErr = errBoom
	if _, err := f.svc.RecentTaskBriefs(context.Background(), 1, 5); !errors.Is(err, errBoom) {
		t.Fatalf("应透传列表错误: %v", err)
	}
}

func TestTodayActionCounts(t *testing.T) {
	f := newFlowFixture()
	f.counters.totals = map[int64]int{1: 3, 2: 5}
	counts, err := f.svc.TodayActionCounts(context.Background(), []int64{1, 0, 2}, f.clk.Now())
	if err != nil {
		t.Fatalf("统计不应失败: %v", err)
	}
	if counts[1] != 3 || counts[2] != 5 {
		t.Fatalf("统计结果错误: %+v", counts)
	}
	if _, exists := counts[0]; exists {
		t.Fatalf("非法 AI ID 应跳过: %+v", counts)
	}

	f.counters.totalErr = errBoom
	if _, err := f.svc.TodayActionCounts(context.Background(), []int64{1}, f.clk.Now()); !errors.Is(err, errBoom) {
		t.Fatalf("应透传计数错误: %v", err)
	}

	f = newFlowFixture()
	empty, err := f.svc.TodayActionCounts(context.Background(), nil, f.clk.Now())
	if err != nil || len(empty) != 0 {
		t.Fatalf("空列表应返回空 map: %+v %v", empty, err)
	}
}

// ===== 记忆来源复核 =====

func TestRecheckSourceMemoriesLimitAndErrors(t *testing.T) {
	t.Run("扫描上限兜底与巡检批量", func(t *testing.T) {
		f := newFlowFixture()
		f.content.posts[200] = publicPost(200, 5)
		f.ai.activeSourceRefs = []contracts.MemorySourceRef{{SourceType: domain.RefPost, SourceID: 200}}
		if err := f.svc.recheckSourceMemoriesLimit(context.Background(), 1, 0); err != nil {
			t.Fatalf("复核不应失败: %v", err)
		}
		if len(f.ai.activeRefsLimits) != 1 || f.ai.activeRefsLimits[0] != memoryRefBatch {
			t.Fatalf("非正上限应回退默认批次: %+v", f.ai.activeRefsLimits)
		}
		if err := f.svc.recheckSourceMemoriesLimit(context.Background(), 1, memoryRefSchedulerBatch); err != nil {
			t.Fatalf("复核不应失败: %v", err)
		}
		if f.ai.activeRefsLimits[1] != memoryRefSchedulerBatch {
			t.Fatalf("巡检批次未透传: %+v", f.ai.activeRefsLimits)
		}
		if len(f.ai.invalidated) != 0 {
			t.Fatalf("来源仍公开不应失效: %+v", f.ai.invalidated)
		}
	})
	t.Run("读取来源失败", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.activeRefsErr = errBoom
		if err := f.svc.recheckSourceMemories(context.Background(), 1); !errors.Is(err, errBoom) {
			t.Fatalf("应透传读取错误: %v", err)
		}
	})
	t.Run("内容读取基础设施错误", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.activeSourceRefs = []contracts.MemorySourceRef{{SourceType: domain.RefPost, SourceID: 100}}
		f.content.visibleErr = errBoom
		if err := f.svc.recheckSourceMemories(context.Background(), 1); !errors.Is(err, errBoom) {
			t.Fatalf("应透传内容读取错误: %v", err)
		}
	})
	t.Run("评论读取基础设施错误", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.activeSourceRefs = []contracts.MemorySourceRef{{SourceType: domain.RefComment, SourceID: 500}}
		f.social.commentGetErr = errBoom
		if err := f.svc.recheckSourceMemories(context.Background(), 1); !errors.Is(err, errBoom) {
			t.Fatalf("应透传评论读取错误: %v", err)
		}
	})
	t.Run("已删除评论来源失效", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.activeSourceRefs = []contracts.MemorySourceRef{{SourceType: domain.RefComment, SourceID: 500}}
		f.social.comments[500] = model.Comment{ID: 500, PostID: 200, Status: model.CommentStatusDeleted}
		if err := f.svc.recheckSourceMemories(context.Background(), 1); err != nil {
			t.Fatalf("复核不应失败: %v", err)
		}
		if len(f.ai.invalidated) != 1 || f.ai.invalidated[0].SourceID != 500 {
			t.Fatalf("已删除评论应失效: %+v", f.ai.invalidated)
		}
	})
	t.Run("评论所在帖不可见来源失效", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.activeSourceRefs = []contracts.MemorySourceRef{{SourceType: domain.RefComment, SourceID: 500}}
		f.social.comments[500] = model.Comment{ID: 500, PostID: 999, Status: model.CommentStatusPublished}
		if err := f.svc.recheckSourceMemories(context.Background(), 1); err != nil {
			t.Fatalf("复核不应失败: %v", err)
		}
		if len(f.ai.invalidated) != 1 {
			t.Fatalf("评论所在帖不可见应失效: %+v", f.ai.invalidated)
		}
	})
	t.Run("未知来源类型跳过", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.activeSourceRefs = []contracts.MemorySourceRef{{SourceType: "unknown", SourceID: 1}}
		if err := f.svc.recheckSourceMemories(context.Background(), 1); err != nil {
			t.Fatalf("复核不应失败: %v", err)
		}
		if len(f.ai.invalidated) != 0 {
			t.Fatalf("未知来源类型不应失效: %+v", f.ai.invalidated)
		}
	})
	t.Run("失效写入失败", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.activeSourceRefs = []contracts.MemorySourceRef{{SourceType: domain.RefPost, SourceID: 100}}
		f.ai.invalidateErr = errBoom
		if err := f.svc.recheckSourceMemories(context.Background(), 1); !errors.Is(err, errBoom) {
			t.Fatalf("应透传失效错误: %v", err)
		}
	})
	t.Run("批量失效写入失败", func(t *testing.T) {
		f := newFlowFixture()
		f.ai.activeSourceRefs = []contracts.MemorySourceRef{{SourceType: domain.RefPost, SourceID: 100}}
		f.ai.invalidateBySourcesErr = errBoom
		if err := f.svc.recheckSourceMemories(context.Background(), 1); !errors.Is(err, errBoom) {
			t.Fatalf("应透传批量失效错误: %v", err)
		}
	})
}

// ===== 目标授权分支 =====

func newTargetFixture() *flowFixture {
	f := newFlowFixture()
	f.content.posts = map[int64]model.Post{
		100: publicPost(100, 7),
		101: {ID: 101, AuthorID: 7, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
		103: publicPost(103, 9),
	}
	f.users.users = map[int64]model.User{
		7: activeHuman(7),
		8: {ID: 8, Username: "ai_other", Status: model.StatusActive, IsAI: true},
		9: {ID: 9, Username: "suspended", Status: model.StatusSuspended},
	}
	f.social.comments = map[int64]model.Comment{
		500: {ID: 500, PostID: 100, AuthorID: 7, Status: model.CommentStatusPublished},
		501: {ID: 501, PostID: 100, AuthorID: 7, Status: model.CommentStatusDeleted},
		502: {ID: 502, PostID: 101, AuthorID: 7, Status: model.CommentStatusPublished},
		503: {ID: 503, PostID: 999, AuthorID: 7, Status: model.CommentStatusPublished},
	}
	f.social.depths = map[int64]int{500: 1, 501: 1, 502: 1, 503: 1}
	f.social.likedComments = map[int64]bool{}
	f.social.likedPosts = map[int64]bool{}
	f.social.following = map[int64]bool{}
	return f
}

func TestValidateTargetBranches(t *testing.T) {
	baseRT := contracts.Runtime{
		AIID:     1,
		Behavior: contracts.BehaviorSettings{MaxReplyDepth: 2},
	}
	cases := []struct {
		name     string
		decision domain.Decision
		behavior contracts.BehaviorSettings
		wantSkip string
		wantErr  bool
	}{
		{
			name:     "原创帖无需目标",
			decision: domain.Decision{Action: domain.ActionOriginalPost},
		},
		{
			name:     "关注目标 ID 无效",
			decision: domain.Decision{Action: domain.ActionFollow},
			wantSkip: "关注目标无效",
		},
		{
			name:     "不能关注自己",
			decision: domain.Decision{Action: domain.ActionFollow, TargetID: strPtr("1")},
			wantSkip: "不能关注自己",
		},
		{
			name:     "关注目标作者不存在",
			decision: domain.Decision{Action: domain.ActionFollow, TargetID: strPtr("999")},
			wantSkip: "目标作者不存在",
		},
		{
			name:     "关注停用账号",
			decision: domain.Decision{Action: domain.ActionFollow, TargetID: strPtr("9")},
			wantSkip: "目标作者账号已停用",
		},
		{
			name:     "AI 间互动未开启",
			decision: domain.Decision{Action: domain.ActionFollow, TargetID: strPtr("8")},
			wantSkip: "AI 间互动未开启",
		},
		{
			name:     "关注用户成功",
			decision: domain.Decision{Action: domain.ActionFollow, TargetID: strPtr("7")},
		},
		{
			name:     "取关要求当前关注关系",
			decision: domain.Decision{Action: domain.ActionUnfollow, TargetID: strPtr("7")},
			wantSkip: "该用户不在当前关注列表中",
		},
		{
			name:     "转发目标 ID 无效",
			decision: domain.Decision{Action: domain.ActionRepost},
			wantSkip: "目标 ID 无效",
		},
		{
			name:     "转发不可见帖子",
			decision: domain.Decision{Action: domain.ActionRepost, TargetID: strPtr("999")},
			wantSkip: "目标帖子不可见",
		},
		{
			name:     "只能转发公开内容",
			decision: domain.Decision{Action: domain.ActionRepost, TargetID: strPtr("101")},
			wantSkip: "只能转发公开内容",
		},
		{
			name:     "转发停用作者内容",
			decision: domain.Decision{Action: domain.ActionRepost, TargetID: strPtr("103")},
			wantSkip: "目标作者账号已停用",
		},
		{
			name:     "转发成功",
			decision: domain.Decision{Action: domain.ActionRepost, TargetID: strPtr("100")},
		},
		{
			name:     "评论目标 ID 无效",
			decision: domain.Decision{Action: domain.ActionComment},
			wantSkip: "目标 ID 无效",
		},
		{
			name:     "评论不可见帖子",
			decision: domain.Decision{Action: domain.ActionComment, TargetID: strPtr("999")},
			wantSkip: "目标帖子不可见",
		},
		{
			name:     "评论停用作者内容",
			decision: domain.Decision{Action: domain.ActionComment, TargetID: strPtr("103")},
			wantSkip: "目标作者账号已停用",
		},
		{
			name:     "回复目标 ID 无效",
			decision: domain.Decision{Action: domain.ActionReply},
			wantSkip: "目标 ID 无效",
		},
		{
			name:     "回复评论不存在",
			decision: domain.Decision{Action: domain.ActionReply, TargetID: strPtr("999")},
			wantErr:  true,
		},
		{
			name:     "回复已删除评论",
			decision: domain.Decision{Action: domain.ActionReply, TargetID: strPtr("501")},
			wantSkip: "评论已删除",
		},
		{
			name:     "回复评论所在内容不可见",
			decision: domain.Decision{Action: domain.ActionReply, TargetID: strPtr("503")},
			wantSkip: "评论所在内容不可见",
		},
		{
			name:     "回复深度超限",
			decision: domain.Decision{Action: domain.ActionReply, TargetID: strPtr("500")},
			behavior: contracts.BehaviorSettings{MaxReplyDepth: 1},
			wantSkip: "回复深度超限",
		},
		{
			name:     "回复成功",
			decision: domain.Decision{Action: domain.ActionReply, TargetID: strPtr("500")},
		},
		{
			name:     "引用目标 ID 无效",
			decision: domain.Decision{Action: domain.ActionQuote},
			wantSkip: "目标 ID 无效",
		},
		{
			name:     "只能引用公开内容",
			decision: domain.Decision{Action: domain.ActionQuote, TargetID: strPtr("101")},
			wantSkip: "只能引用公开内容",
		},
		{
			name:     "引用成功",
			decision: domain.Decision{Action: domain.ActionQuote, TargetID: strPtr("100")},
		},
		{
			name:     "点赞目标 ID 无效",
			decision: domain.Decision{Action: domain.ActionLike},
			wantSkip: "目标 ID 无效",
		},
		{
			name:     "点赞已删除评论",
			decision: domain.Decision{Action: domain.ActionLike, TargetType: domain.RefComment, TargetID: strPtr("501")},
			wantSkip: "评论已删除",
		},
		{
			name:     "点赞评论所在内容不可见",
			decision: domain.Decision{Action: domain.ActionLike, TargetType: domain.RefComment, TargetID: strPtr("503")},
			wantSkip: "评论所在内容不可见",
		},
		{
			name:     "点赞评论成功",
			decision: domain.Decision{Action: domain.ActionLike, TargetType: domain.RefComment, TargetID: strPtr("500")},
		},
		{
			name:     "取消点赞评论要求当前点赞",
			decision: domain.Decision{Action: domain.ActionUnlike, TargetType: domain.RefComment, TargetID: strPtr("500")},
			wantSkip: "该评论不在当前点赞列表中",
		},
		{
			name:     "只能互动公开内容",
			decision: domain.Decision{Action: domain.ActionLike, TargetID: strPtr("101")},
			wantSkip: "只能互动公开内容",
		},
		{
			name:     "取消点赞帖子要求当前点赞",
			decision: domain.Decision{Action: domain.ActionUnlike, TargetID: strPtr("100")},
			wantSkip: "该帖子不在当前点赞列表中",
		},
		{
			name:     "取消点赞帖子成功",
			decision: domain.Decision{Action: domain.ActionUnlike, TargetID: strPtr("100")},
		},
		{
			name:     "不支持的动作",
			decision: domain.Decision{Action: "magic"},
			wantSkip: "不支持的动作",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := newTargetFixture()
			if testCase.name == "取消点赞帖子成功" {
				f.social.likedPosts[100] = true
			}
			rt := baseRT
			if testCase.name == "AI 间互动未开启" {
				rt.Behavior.AllowAIToAI = false
			}
			if testCase.behavior.MaxReplyDepth != 0 {
				rt.Behavior = testCase.behavior
			}
			_, skip, err := f.svc.validateTarget(context.Background(), rt, repo.Task{ID: 1, AIID: 1}, testCase.decision, nil)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("期望基础设施错误")
				}
				return
			}
			if err != nil {
				t.Fatalf("validateTarget 不应返回基础设施错误: %v", err)
			}
			if skip != testCase.wantSkip {
				t.Fatalf("期望 skip=%q，实际 %q", testCase.wantSkip, skip)
			}
		})
	}
}

func TestValidateTargetRepostTargetFields(t *testing.T) {
	f := newTargetFixture()
	rt := contracts.Runtime{AIID: 1, Behavior: contracts.BehaviorSettings{MaxReplyDepth: 2}}
	target, skip, err := f.svc.validateTarget(context.Background(), rt, repo.Task{ID: 1, AIID: 1},
		domain.Decision{Action: domain.ActionRepost, TargetID: strPtr("100")}, nil)
	if err != nil || skip != "" {
		t.Fatalf("转发应通过: skip=%q err=%v", skip, err)
	}
	if target.Type != domain.RefPost || target.ID != 100 || target.PostID != 100 || target.AuthorID != 7 {
		t.Fatalf("转发目标字段错误: %+v", target)
	}
}
