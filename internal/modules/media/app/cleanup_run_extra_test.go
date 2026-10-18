package app

// 临时附件清理任务：保留期截止、头像引用排除、草稿引用二次过滤与逻辑失效；
// 关注点是不误删（只更新状态）和端口调用参数。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// TestRunCleanupSkipsWhenAvatarLookupFails 读取头像引用失败时跳过本次清理，不触库。
func TestRunCleanupSkipsWhenAvatarLookupFails(t *testing.T) {
	env := newMediaTestEnv(t)
	env.auth.avatarIDsErr = errors.New("auth 不可用")
	env.svc.RunCleanup(context.Background())
	if queries := len(env.stub.recorded()); queries != 0 {
		t.Fatalf("头像引用读取失败后不应触库，实际查询 %d 次", queries)
	}
}

// TestRunCleanupSkipsWhenCandidateQueryFails 候选查询失败时跳过本次清理。
func TestRunCleanupSkipsWhenCandidateQueryFails(t *testing.T) {
	env := newMediaTestEnv(t)
	env.auth.avatarIDs = []int64{99}
	env.expect(dbErrorResult("查询候选失败"))
	env.svc.RunCleanup(context.Background())
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("候选查询失败后不应继续，剩余 %d", got)
	}
	if len(env.content.draftQueries) != 0 {
		t.Fatalf("候选为空时不应查询草稿引用，实际 %v", env.content.draftQueries)
	}
}

// TestRunCleanupFullFlow 覆盖完整清理：剔除草稿引用后批量失效并取消衍生文件。
func TestRunCleanupFullFlow(t *testing.T) {
	env := newMediaTestEnv(t)
	env.settings.values.TempAttachmentRetention = 48 * time.Hour
	env.auth.avatarIDs = []int64{99}
	env.content.activeDraft = []int64{2, 4}
	env.expect(
		idsResult(1, 2, 3, 4),
		commandResult("UPDATE 2"),
		commandResult("UPDATE 1"),
	)

	env.svc.RunCleanup(context.Background())

	if got := env.stub.pending(); got != 0 {
		t.Fatalf("清理应消费全部结果，剩余 %d", got)
	}
	if len(env.content.draftQueries) != 1 {
		t.Fatalf("草稿引用应查询一次，实际 %v", env.content.draftQueries)
	}
	draftQuery := env.content.draftQueries[0]
	if len(draftQuery) != 4 || draftQuery[0] != 1 || draftQuery[3] != 4 {
		t.Fatalf("草稿引用查询候选 = %v，期望 [1 2 3 4]", draftQuery)
	}
}

// TestRunCleanupNoCandidates 没有过期候选时仍执行“取消已失效附件衍生文件”的兜底步骤。
func TestRunCleanupNoCandidates(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(idsResult(), commandResult("UPDATE 0"))
	env.svc.RunCleanup(context.Background())
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("空候选仍应完成衍生文件取消步骤，剩余 %d", got)
	}
	if len(env.content.draftQueries) != 0 {
		t.Fatalf("空候选不应查询草稿引用，实际 %v", env.content.draftQueries)
	}
}

// TestRunCleanupFiltersDraftReferences 草稿引用信息由 content 端口提供，不直接查 content 表。
func TestRunCleanupFiltersDraftReferences(t *testing.T) {
	env := newMediaTestEnv(t)
	env.content.activeDraft = []int64{1}
	env.expect(idsResult(1, 2), commandResult("UPDATE 1"), commandResult("UPDATE 1"))
	env.svc.RunCleanup(context.Background())
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("清理应消费全部结果，剩余 %d", got)
	}
}

// TestRunCleanupSkipsOnDraftFilterError 草稿引用查询失败时跳过本次清理（不误标失效）。
func TestRunCleanupSkipsOnDraftFilterError(t *testing.T) {
	env := newMediaTestEnv(t)
	env.content.activeDraftErr = errors.New("content 不可用")
	env.expect(idsResult(1))
	env.svc.RunCleanup(context.Background())
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("草稿引用失败后不应写库，剩余 %d", got)
	}
}

// TestRunCleanupSkipsOnMarkDeletedError 批量失效失败时跳过后续步骤。
func TestRunCleanupSkipsOnMarkDeletedError(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(idsResult(1), dbErrorResult("mark 失败"))
	env.svc.RunCleanup(context.Background())
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("批量失效失败后不应继续，剩余 %d", got)
	}
}

// TestRunCleanupSkipsOnCancelError 取消衍生文件失败时直接返回，不 panic。
func TestRunCleanupSkipsOnCancelError(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(idsResult(1), commandResult("UPDATE 1"), dbErrorResult("cancel 失败"))
	env.svc.RunCleanup(context.Background())
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("取消失败后不应继续，剩余 %d", got)
	}
}

// TestFilterDraftReferencedContentNotConfigured 未装配 content 端口时清理第二步返回明确错误。
func TestFilterDraftReferencedContentNotConfigured(t *testing.T) {
	env := newMediaTestEnv(t)
	env.svc.content = nil
	_, err := env.svc.filterDraftReferenced(context.Background(), []int64{1})
	requireKind(t, err, apperr.KindInternal)
}

// TestCleanupRetentionApplied 校验清理使用系统设置中的保留期（非法值回退默认值）。
func TestCleanupRetentionApplied(t *testing.T) {
	env := newMediaTestEnv(t)
	env.settings.values.TempAttachmentRetention = 0 // 非法值应回退 domain.DefaultTempRetention
	if got := domain.CleanupRetention(env.settings.values.TempAttachmentRetention); got != domain.DefaultTempRetention {
		t.Fatalf("保留期回退 = %v，期望 %v", got, domain.DefaultTempRetention)
	}
	env.expect(idsResult(), commandResult("UPDATE 0"))
	env.svc.RunCleanup(context.Background())
}
