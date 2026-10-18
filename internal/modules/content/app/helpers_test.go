package app

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 说明：content/app 的 Service 内部直连 *repo.Repo，无法 fake 数据库；因此本包新增测试
// 只覆盖纯函数、参数/领域校验、错误映射、端口 fake 可验证的分支，以及「校验通过后进入
// 事务前」的路径（用 nil TxManager 让事务入口直接返回，不触库）。既有的 app_test.go 已覆盖
// 发帖限频、定时作业入队/取消等用例，这里不重复声明 fake 类型，也不重复同名测试。

// contentTestNow 是 content/app 测试统一使用的应用时间。
var contentTestNow = time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)

// fakeUsers 只覆写测试需要的账号读端口方法；未覆写的方法会因嵌入 nil 接口而 panic。
type fakeUsers struct {
	contracts.UserReader
	user       model.User
	getErr     error
	byUsername model.User
	byNameErr  error
	suspended  bool
	suspendErr error
}

func (f fakeUsers) GetUser(context.Context, int64) (model.User, error) {
	return f.user, f.getErr
}

func (f fakeUsers) GetUserByUsername(context.Context, string) (model.User, error) {
	return f.byUsername, f.byNameErr
}

func (f fakeUsers) IsSuspended(context.Context, int64) (bool, error) {
	return f.suspended, f.suspendErr
}

// fakeSocial 只覆写关注集合读取。
type fakeSocial struct {
	contracts.SocialService
	following map[int64]bool
	followErr error
	reposts   []contracts.RepostEvent
	repostErr error
	state     string
	states    contracts.PostViewerStates
	statesErr error
}

func (f fakeSocial) FollowingSet(context.Context, int64) (map[int64]bool, error) {
	return f.following, f.followErr
}

func (f fakeSocial) FollowState(context.Context, int64, int64) (string, error) {
	return f.state, nil
}

func (f fakeSocial) ViewerPostStates(context.Context, int64, []int64) (contracts.PostViewerStates, error) {
	return f.states, f.statesErr
}

func (f fakeSocial) ListTimelineReposts(context.Context, contracts.RepostTimelineQuery) ([]contracts.RepostEvent, error) {
	return f.reposts, f.repostErr
}

// fakeMedia 记录附件校验/关联调用，可按需返回错误。
type fakeMedia struct {
	contracts.MediaService
	checkErr error
	linkErr  error
	checked  []contracts.AttachmentCheckInput
	linked   []int64
	linkedAt time.Time
}

func (f *fakeMedia) CheckAttachmentsUsable(_ context.Context, in contracts.AttachmentCheckInput) error {
	f.checked = append(f.checked, in)
	return f.checkErr
}

func (f *fakeMedia) LinkAttachmentsToPost(
	_ context.Context,
	postID int64,
	attachmentIDs []int64,
	at time.Time,
) error {
	f.linked = append(f.linked, attachmentIDs...)
	f.linkedAt = at
	return f.linkErr
}

// failingIDs 是总是失败的 ID 生成器。
type failingIDs struct{ err error }

func (f failingIDs) Next() (int64, error) { return 0, f.err }

// fixedIDs 返回固定 ID 的生成器。
type fixedIDs struct{ id int64 }

func (f fixedIDs) Next() (int64, error) { return f.id, nil }

// guestViewer 返回游客。
func guestViewer() model.Viewer { return model.GuestViewer() }

// humanViewer 返回已登录的人类用户。
func humanViewer(id int64) model.Viewer {
	return model.Viewer{ID: id, Authenticated: true}
}

// adminViewer 返回管理员。
func adminViewer(id int64) model.Viewer {
	return model.Viewer{ID: id, IsAdmin: true, Authenticated: true}
}

// requireKind 断言错误是期望 Kind 的领域错误。
func requireKind(t *testing.T, err error, kind apperr.Kind) *apperr.Error {
	t.Helper()
	e, ok := apperr.AsKind(err)
	if !ok {
		t.Fatalf("期望领域错误 %s，实际: %v", kind, err)
	}
	if e.Kind != kind {
		t.Fatalf("期望 Kind=%s，实际 Kind=%s（%v）", kind, e.Kind, err)
	}
	return e
}

// requireTransactionMissing 断言错误来自「事务管理器未初始化」，证明已越过全部参数/领域校验。
func requireTransactionMissing(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "事务管理器未初始化") {
		t.Fatalf("应返回事务管理器未初始化错误，实际 %v", err)
	}
}

// discardLogger 返回丢弃输出的日志器，避免测试输出噪声。
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// newClockedService 构造带固定应用时钟（contentTestNow）的 Service，供时间相关用例使用。
func newClockedService(t *testing.T, deps Deps) *Service {
	t.Helper()
	if deps.Clock == nil {
		deps.Clock = clock.NewFake(contentTestNow)
	}
	return newTestService(t, deps)
}
