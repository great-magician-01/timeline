package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// cleanupSettings 是固定返回指定系统设置的假 SettingsReader。
type cleanupSettings struct{ values contracts.SystemSettings }

func (f cleanupSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	return f.values, nil
}

func (f cleanupSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	return contracts.WebSearchSettings{}, nil
}

// fakeContentPort 记录清理流程传入的候选集合并返回预设的草稿引用附件。
// 内嵌 ContentPort 只为满足接口，未使用的方法不会在该测试路径被调用。
type fakeContentPort struct {
	ContentPort
	referenced []int64
	err        error
	calls      int
	got        []int64
}

func (f *fakeContentPort) ActiveDraftAttachmentIDs(_ context.Context, attachmentIDs []int64) ([]int64, error) {
	f.calls++
	f.got = append([]int64{}, attachmentIDs...)
	return f.referenced, f.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestFilterDraftReferenced 覆盖清理第二步：候选集剔除仍被生效草稿关联的附件，保持原顺序。
func TestFilterDraftReferenced(t *testing.T) {
	port := &fakeContentPort{referenced: []int64{2, 4}}
	svc := &Service{content: port, log: discardLogger()}

	got, err := svc.filterDraftReferenced(context.Background(), []int64{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatalf("filterDraftReferenced 返回错误: %v", err)
	}
	want := []int64{1, 3, 5}
	if len(got) != len(want) {
		t.Fatalf("过滤结果 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("过滤结果 = %v，期望 %v", got, want)
		}
	}
	if port.calls != 1 {
		t.Fatalf("content 端口调用次数 = %d，期望 1", port.calls)
	}
	if len(port.got) != 5 {
		t.Fatalf("content 端口收到候选 = %v，期望全部 5 个候选", port.got)
	}
}

// TestFilterDraftReferencedEmptyCandidates 空候选不应调用跨模块端口。
func TestFilterDraftReferencedEmptyCandidates(t *testing.T) {
	port := &fakeContentPort{}
	svc := &Service{content: port, log: discardLogger()}

	got, err := svc.filterDraftReferenced(context.Background(), nil)
	if err != nil || got != nil {
		t.Fatalf("空候选应直接返回 nil，得到 %v/%v", got, err)
	}
	if port.calls != 0 {
		t.Fatalf("空候选不应调用 content 端口，实际调用 %d 次", port.calls)
	}
}

// TestFilterDraftReferencedContentError 端口失败时清理流程应中断（不误删附件）。
func TestFilterDraftReferencedContentError(t *testing.T) {
	port := &fakeContentPort{err: errors.New("content 不可用")}
	svc := &Service{content: port, log: discardLogger()}

	if _, err := svc.filterDraftReferenced(context.Background(), []int64{1}); err == nil {
		t.Fatal("content 端口失败时不应返回成功")
	}
}

// TestPrecheckUploadRateLimited 验证预检在读取请求体前返回限频错误，且不触发数据库查询
// （Service 仅装配设置与限频器，Pools/Tx 为空；若代码走到 SQL 会 panic）。
func TestPrecheckUploadRateLimited(t *testing.T) {
	limiter := ratelimit.New(1, time.Hour)
	svc := &Service{
		settings: cleanupSettings{values: contracts.SystemSettings{
			UploadRateLimit:  1,
			UploadRateWindow: time.Hour,
		}},
		uploadLimiter: limiter,
		log:           discardLogger(),
	}
	// 先消耗唯一一次额度，使预检必然超限。
	if allowed, _ := limiter.ConsumeLimit("user:1", 1, time.Hour); !allowed {
		t.Fatal("首次消耗额度应成功")
	}

	_, err := svc.PrecheckUpload(context.Background(), model.Viewer{ID: 1, Authenticated: true})
	if !apperr.IsKind(err, apperr.KindRateLimited) {
		t.Fatalf("预检应返回 rate_limited，实际 %v", err)
	}
}

// TestPrecheckUploadRejectsNonWritable 停用/未登录账号在预检阶段即被拒绝。
func TestPrecheckUploadRejectsNonWritable(t *testing.T) {
	svc := &Service{settings: cleanupSettings{}, log: discardLogger()}
	if _, err := svc.PrecheckUpload(context.Background(), model.Viewer{ID: 1, Authenticated: true, IsSuspended: true}); !apperr.IsKind(err, apperr.KindSuspended) {
		t.Fatalf("停用账号应返回 account_suspended，实际 %v", err)
	}
}
