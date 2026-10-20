package app

import (
	"context"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestPinPostGuards 校验置顶的账号状态校验与事务入口（数据库路径由集成测试覆盖）。
func TestPinPostGuards(t *testing.T) {
	ctx := context.Background()
	_, err := newClockedService(t, Deps{}).PinPost(ctx, guestViewer(), 1)
	requireKind(t, err, apperr.KindUnauthenticated)

	suspended := model.Viewer{ID: 1, Authenticated: true, IsSuspended: true}
	_, err = newClockedService(t, Deps{}).PinPost(ctx, suspended, 1)
	requireKind(t, err, apperr.KindSuspended)

	ai := model.Viewer{ID: 1, Authenticated: true, IsAI: true}
	_, err = newClockedService(t, Deps{}).PinPost(ctx, ai, 1)
	requireKind(t, err, apperr.KindForbidden)

	// 登录人类用户进入事务入口。
	_, err = newClockedService(t, Deps{}).PinPost(ctx, humanViewer(1), 1)
	requireTransactionMissing(t, err)
}

// TestUnpinPostGuards 校验取消置顶的账号状态校验与事务入口。
func TestUnpinPostGuards(t *testing.T) {
	ctx := context.Background()
	_, err := newClockedService(t, Deps{}).UnpinPost(ctx, guestViewer(), 1)
	requireKind(t, err, apperr.KindUnauthenticated)

	suspended := model.Viewer{ID: 1, Authenticated: true, IsSuspended: true}
	_, err = newClockedService(t, Deps{}).UnpinPost(ctx, suspended, 1)
	requireKind(t, err, apperr.KindSuspended)

	ai := model.Viewer{ID: 1, Authenticated: true, IsAI: true}
	_, err = newClockedService(t, Deps{}).UnpinPost(ctx, ai, 1)
	requireKind(t, err, apperr.KindForbidden)

	_, err = newClockedService(t, Deps{}).UnpinPost(ctx, humanViewer(1), 1)
	requireTransactionMissing(t, err)
}

// TestReorderPinnedPostsGuards 校验置顶排序的账号校验、请求体校验与事务入口：
// 列表校验发生在触库之前，即使未装配事务也能区分校验失败与事务入口。
func TestReorderPinnedPostsGuards(t *testing.T) {
	ctx := context.Background()
	svc := newClockedService(t, Deps{})

	_, err := svc.ReorderPinnedPosts(ctx, guestViewer(), []int64{1})
	requireKind(t, err, apperr.KindUnauthenticated)

	suspended := model.Viewer{ID: 1, Authenticated: true, IsSuspended: true}
	_, err = svc.ReorderPinnedPosts(ctx, suspended, []int64{1})
	requireKind(t, err, apperr.KindSuspended)

	ai := model.Viewer{ID: 1, Authenticated: true, IsAI: true}
	_, err = svc.ReorderPinnedPosts(ctx, ai, []int64{1})
	requireKind(t, err, apperr.KindForbidden)

	// 空列表 / 超上限 / 重复 ID：请求体校验先于事务。
	_, err = svc.ReorderPinnedPosts(ctx, humanViewer(1), nil)
	requireKind(t, err, apperr.KindValidation)
	_, err = svc.ReorderPinnedPosts(ctx, humanViewer(1), []int64{1, 2, 3, 4})
	requireKind(t, err, apperr.KindValidation)
	_, err = svc.ReorderPinnedPosts(ctx, humanViewer(1), []int64{1, 1})
	requireKind(t, err, apperr.KindValidation)
	_, err = svc.ReorderPinnedPosts(ctx, humanViewer(1), []int64{0})
	requireKind(t, err, apperr.KindValidation)

	// 合法列表进入事务入口。
	_, err = svc.ReorderPinnedPosts(ctx, humanViewer(1), []int64{2, 1})
	requireTransactionMissing(t, err)
}
