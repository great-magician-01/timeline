package app

import (
	"context"
	"strconv"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestBookmarkPostSuccess 验证收藏帖子成功路径：默认夹与指定夹两种归属，
// 且不产生通知、不更新帖子计数（收藏完全私有）。
func TestBookmarkPostSuccess(t *testing.T) {
	t.Run("默认收藏（folder_id 为空）", func(t *testing.T) {
		stubs := newStubSet()
		got, err := stubs.service().BookmarkPost(context.Background(), stubViewer(), stubPostID, nil)
		requireOK(t, err)
		if !got.Bookmarked || got.FolderID != nil {
			t.Fatalf("收藏结果 = (%v, %v)，期望 (true, nil)", got.Bookmarked, got.FolderID)
		}
		if len(stubs.bookmarks.upserts) != 1 {
			t.Fatalf("收藏写入调用 = %d，期望 1", len(stubs.bookmarks.upserts))
		}
		call := stubs.bookmarks.upserts[0]
		if call.userID != stubViewerID || call.postID != stubPostID || call.folderID != nil {
			t.Fatalf("收藏写入参数 = %+v，期望 (%d, %d, nil)", call, stubViewerID, stubPostID)
		}
		if call.id != 101 || !call.at.Equal(stubNow) {
			t.Fatalf("收藏行 ID/时间 = (%d, %v)，期望 (101, %v)", call.id, call.at, stubNow)
		}
		if len(stubs.notices.notifications) != 0 || len(stubs.posts.counters) != 0 {
			t.Fatal("收藏不应写通知或调整帖子计数")
		}
		if len(stubs.folders.getCalls) != 0 {
			t.Fatal("默认收藏不应校验收藏夹")
		}
	})

	t.Run("收藏到指定收藏夹", func(t *testing.T) {
		stubs := newStubSet()
		folderID := int64(800)
		got, err := stubs.service().BookmarkPost(context.Background(), stubViewer(), stubPostID, &folderID)
		requireOK(t, err)
		if got.FolderID == nil || *got.FolderID != folderID {
			t.Fatalf("收藏归属 = %v，期望 %d", got.FolderID, folderID)
		}
		if len(stubs.folders.getCalls) != 1 || stubs.folders.getCalls[0] != folderID {
			t.Fatalf("收藏夹校验调用 = %v，期望 [%d]", stubs.folders.getCalls, folderID)
		}
		call := stubs.bookmarks.upserts[0]
		if call.folderID == nil || *call.folderID != folderID {
			t.Fatalf("收藏写入归属 = %v，期望 %d", call.folderID, folderID)
		}
	})
}

// TestBookmarkPostIdempotentMove 验证重复收藏即"移动"语义：更新归属参数、
// 不产生计数/通知等副作用。
func TestBookmarkPostIdempotentMove(t *testing.T) {
	stubs := newStubSet()
	folderID := int64(801)
	svc := stubs.service()
	if _, err := svc.BookmarkPost(context.Background(), stubViewer(), stubPostID, nil); err != nil {
		t.Fatalf("首次收藏失败: %v", err)
	}
	got, err := svc.BookmarkPost(context.Background(), stubViewer(), stubPostID, &folderID)
	requireOK(t, err)
	if got.FolderID == nil || *got.FolderID != folderID {
		t.Fatalf("再次收藏归属 = %v，期望 %d", got.FolderID, folderID)
	}
	if len(stubs.bookmarks.upserts) != 2 {
		t.Fatalf("收藏写入调用 = %d，期望 2", len(stubs.bookmarks.upserts))
	}
	if len(stubs.notices.notifications) != 0 || len(stubs.posts.counters) != 0 {
		t.Fatal("重复收藏不应产生通知或计数副作用")
	}
}

// TestBookmarkPostGuards 覆盖收藏帖子的鉴权、可见性与互动限制。
func TestBookmarkPostGuards(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		viewer   model.Viewer
		setup    func(*stubSet)
		wantKind apperr.Kind
	}{
		{"游客", stubGuestViewer(), func(*stubSet) {}, apperr.KindUnauthenticated},
		{"停用账号", stubSuspendedViewer(), func(*stubSet) {}, apperr.KindSuspended},
		{
			name:     "AI 身份",
			viewer:   model.ViewerFrom(model.User{ID: stubAIID, IsAI: true, Status: model.StatusActive}),
			setup:    func(*stubSet) {},
			wantKind: apperr.KindForbidden,
		},
		{
			name:     "帖子不可见",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.posts.visibleErr = apperr.NotFound("内容不存在或无权访问") },
			wantKind: apperr.KindNotFound,
		},
		{
			name:     "帖子未发布",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.posts.visiblePost.Status = model.PostStatusDraft },
			wantKind: apperr.KindForbidden,
		},
		{
			name:     "帖子作者停用",
			viewer:   stubViewer(),
			setup:    func(s *stubSet) { s.users.suspended[stubAuthorID] = true },
			wantKind: apperr.KindForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			_, err := stubs.service().BookmarkPost(ctx, tc.viewer, stubPostID, nil)
			requireKind(t, err, tc.wantKind)
		})
	}

	t.Run("限频", func(t *testing.T) {
		stubs := newStubSet()
		stubs.bookmarkLimiter = newFixedLimiter(1)
		svc := stubs.service()
		svc.deps.BookmarkLimiter.Consume("user:" + strconv.FormatInt(stubViewerID, 10))
		_, err := svc.BookmarkPost(ctx, stubViewer(), stubPostID, nil)
		requireKind(t, err, apperr.KindRateLimited)
	})
}

// TestBookmarkPostFolderValidation 覆盖指定收藏夹的属主与存在性校验。
func TestBookmarkPostFolderValidation(t *testing.T) {
	ctx := context.Background()
	folderID := int64(800)

	t.Run("收藏夹不存在", func(t *testing.T) {
		stubs := newStubSet()
		stubs.folders.getErr = apperr.NotFound("收藏夹不存在")
		_, err := stubs.service().BookmarkPost(ctx, stubViewer(), stubPostID, &folderID)
		requireKind(t, err, apperr.KindNotFound)
		if len(stubs.bookmarks.upserts) != 0 {
			t.Fatal("夹校验失败时不应写入收藏")
		}
	})

	t.Run("他人收藏夹按 404（不暴露存在性）", func(t *testing.T) {
		stubs := newStubSet()
		stubs.folders.activeFolder.UserID = stubOtherID
		_, err := stubs.service().BookmarkPost(ctx, stubViewer(), stubPostID, &folderID)
		requireKind(t, err, apperr.KindNotFound)
	})
}

// TestBookmarkPostErrors 覆盖收藏写入链路错误。
func TestBookmarkPostErrors(t *testing.T) {
	boom := apperr.Internal("内部错误")
	cases := []struct {
		name  string
		ids   IDGenerator
		tx    TxRunner
		setup func(*stubSet)
	}{
		{"ID 生成失败", errIDs{err: boom}, nil, func(*stubSet) {}},
		{"事务失败", nil, errTx{err: boom}, func(*stubSet) {}},
		{"收藏写入失败", nil, nil, func(s *stubSet) { s.bookmarks.upsertErr = boom }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubs := newStubSet()
			tc.setup(stubs)
			svc := stubs.serviceWith(func(deps *Deps) {
				if tc.ids != nil {
					deps.IDs = tc.ids
				}
				if tc.tx != nil {
					deps.Tx = tc.tx
				}
			})
			if _, err := svc.BookmarkPost(context.Background(), stubViewer(), stubPostID, nil); err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

// TestUnbookmarkPost 覆盖取消收藏真正生效与幂等两种路径；
// 守卫只校验访问者身份，帖子删除/不可见不影响清理自己的收藏记录。
func TestUnbookmarkPost(t *testing.T) {
	t.Run("取消成功", func(t *testing.T) {
		stubs := newStubSet()
		got, err := stubs.service().UnbookmarkPost(context.Background(), stubViewer(), stubPostID)
		requireOK(t, err)
		if got.Bookmarked {
			t.Fatal("取消后 bookmarked 应为 false")
		}
		if len(stubs.bookmarks.deactivates) != 1 {
			t.Fatalf("取消收藏调用 = %d，期望 1", len(stubs.bookmarks.deactivates))
		}
		call := stubs.bookmarks.deactivates[0]
		if call.userID != stubViewerID || call.postID != stubPostID || !call.at.Equal(stubNow) {
			t.Fatalf("取消参数 = %+v，期望 (%d, %d, %v)", call, stubViewerID, stubPostID, stubNow)
		}
	})

	t.Run("重复取消幂等", func(t *testing.T) {
		stubs := newStubSet()
		stubs.bookmarks.deactivateOK = false
		got, err := stubs.service().UnbookmarkPost(context.Background(), stubViewer(), stubPostID)
		requireOK(t, err)
		if got.Bookmarked {
			t.Fatal("幂等取消后 bookmarked 应为 false")
		}
	})

	t.Run("守卫与限频", func(t *testing.T) {
		stubs := newStubSet()
		_, err := stubs.service().UnbookmarkPost(context.Background(), stubGuestViewer(), stubPostID)
		requireKind(t, err, apperr.KindUnauthenticated)

		stubs = newStubSet()
		stubs.bookmarkLimiter = newFixedLimiter(1)
		svc := stubs.service()
		svc.deps.BookmarkLimiter.Consume("user:" + strconv.FormatInt(stubViewerID, 10))
		_, err = svc.UnbookmarkPost(context.Background(), stubViewer(), stubPostID)
		requireKind(t, err, apperr.KindRateLimited)
	})
}

// TestViewerPostStatesBookmarkStates 校验 ViewerPostStates 批量返回收藏状态。
func TestViewerPostStatesBookmarkStates(t *testing.T) {
	stubs := newStubSet()
	stubs.likes.activeStatesMap = map[int64]bool{stubPostID: true}
	stubs.reposts.activeStatesMap = map[int64]bool{}
	stubs.bookmarks.activeStatesMap = map[int64]bool{stubPostID: true}

	got, err := stubs.service().ViewerPostStates(context.Background(), stubViewerID, []int64{stubPostID})
	requireOK(t, err)
	if !got.Liked[stubPostID] || got.Reposted[stubPostID] || !got.Bookmarked[stubPostID] {
		t.Fatalf("访问者状态 = (liked=%v, reposted=%v, bookmarked=%v)，期望 (true, false, true)",
			got.Liked[stubPostID], got.Reposted[stubPostID], got.Bookmarked[stubPostID])
	}
}

// TestServiceListBookmarksRefs 校验收藏 refs 读取的三态映射与空参数短路。
func TestServiceListBookmarksRefs(t *testing.T) {
	folderID := int64(800)
	stubs := newStubSet()
	stubs.bookmarks.rows = []domain.BookmarkRow{
		{ID: 902, UserID: stubViewerID, PostID: 22, FolderID: &folderID, CreatedAt: stubNow, UpdatedAt: stubNow},
		{ID: 901, UserID: stubViewerID, PostID: 21, FolderID: nil, CreatedAt: stubNow, UpdatedAt: stubNow},
	}

	items, err := stubs.service().ListBookmarks(context.Background(), contractsQueryFor(stubViewerID, &folderID))
	requireOK(t, err)
	if len(items) != 2 {
		t.Fatalf("收藏条数 = %d，期望 2", len(items))
	}
	if items[0].ID != 902 || items[0].PostID != 22 || items[0].FolderID != &folderID {
		t.Fatalf("第一条 = %+v，期望行 902/帖 22/夹 %d", items[0], folderID)
	}
	if items[1].FolderID != nil {
		t.Fatalf("第二条归属 = %v，期望 nil（默认收藏）", items[1].FolderID)
	}
	q := stubs.bookmarks.lastQuery
	if q.All || q.DefaultOnly || q.FolderID != folderID || q.UserID != stubViewerID {
		t.Fatalf("三态映射 = %+v，期望 FolderID=%d 精确过滤", q, folderID)
	}

	t.Run("空参数短路", func(t *testing.T) {
		stubs := newStubSet()
		got, err := stubs.service().ListBookmarks(context.Background(), contractsQueryFor(0, nil))
		requireOK(t, err)
		if len(got) != 0 {
			t.Fatalf("空参数应返回空列表，实际 %d 条", len(got))
		}
	})
}

func contractsQueryFor(userID int64, folderID *int64) contracts.BookmarkListQuery {
	query := contracts.BookmarkListQuery{UserID: userID, Limit: 20}
	if folderID != nil {
		query.FolderID = *folderID
	}
	return query
}
