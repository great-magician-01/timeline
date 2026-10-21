package app

import (
	"context"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// fakeBookmarks 只覆写收藏 refs 读取与夹属主校验；未覆写的方法不触发。
type fakeBookmarks struct {
	contracts.BookmarkReader

	items      []contracts.BookmarkItem
	listErr    error
	hasFolder  bool
	hasErr     error
	lastCall   contracts.BookmarkListQuery
	hasCalls   [][2]int64
	folderErrs bool
}

func (f *fakeBookmarks) ListBookmarks(_ context.Context, query contracts.BookmarkListQuery) ([]contracts.BookmarkItem, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.lastCall = query
	return f.items, nil
}

func (f *fakeBookmarks) HasBookmarkFolder(_ context.Context, userID, folderID int64) (bool, error) {
	f.hasCalls = append(f.hasCalls, [2]int64{userID, folderID})
	if f.hasErr != nil {
		return false, f.hasErr
	}
	return f.hasFolder, nil
}

// TestListBookmarksNilPort 防回归：收藏端口缺失必须报内部错误而不是空列表，
// 避免装配遗漏把「我的收藏」静默变成空页。
func TestListBookmarksNilPort(t *testing.T) {
	svc := newTestService(t, Deps{Log: discardLogger()})
	_, err := svc.ListBookmarks(context.Background(), humanViewer(1), BookmarkScope{All: true}, 0, 20)
	e := requireKind(t, err, apperr.KindInternal)
	if e.Message != "收藏服务未就绪" {
		t.Fatalf("错误文案 = %q，期望 收藏服务未就绪", e.Message)
	}
}

// TestListBookmarksFollowingError 校验关注集合读取失败向上传递。
func TestListBookmarksFollowingError(t *testing.T) {
	boom := apperr.Internal("读取关注集合失败")
	svc := newTestService(t, Deps{
		Log:       discardLogger(),
		Social:    fakeSocial{followErr: boom},
		Bookmarks: &fakeBookmarks{},
	})
	_, err := svc.ListBookmarks(context.Background(), humanViewer(1), BookmarkScope{All: true}, 0, 20)
	requireKind(t, err, apperr.KindInternal)
}

// TestListBookmarksRefsError 校验收藏 refs 读取失败向上传递（不触库即可复现）。
func TestListBookmarksRefsError(t *testing.T) {
	boom := apperr.Internal("读取收藏失败")
	svc := newTestService(t, Deps{
		Log:       discardLogger(),
		Social:    fakeSocial{following: map[int64]bool{}},
		Bookmarks: &fakeBookmarks{listErr: boom},
	})
	_, err := svc.ListBookmarks(context.Background(), humanViewer(1), BookmarkScope{DefaultOnly: true}, 0, 20)
	requireKind(t, err, apperr.KindInternal)
}

// TestListBookmarksScopePassthrough 校验夹过滤三态原样透传给 social 端口。
func TestListBookmarksScopePassthrough(t *testing.T) {
	// refs 返回空列表时 fetch 不再触库（GetPosts 空参直接返回），
	// 可在不连库的前提下断言端口入参。
	stub := &fakeBookmarks{items: nil, hasFolder: true}
	svc := newTestService(t, Deps{
		Log:       discardLogger(),
		Social:    fakeSocial{following: map[int64]bool{}},
		Bookmarks: stub,
	})
	page, err := svc.ListBookmarks(context.Background(), humanViewer(7), BookmarkScope{FolderID: 800}, 901, 5)
	if err != nil {
		t.Fatalf("ListBookmarks: %v", err)
	}
	if len(page.Items) != 0 || page.HasMore {
		t.Fatalf("空 refs 应返回空页: %+v", page)
	}
	if len(stub.hasCalls) != 1 || stub.hasCalls[0] != [2]int64{7, 800} {
		t.Fatalf("夹属主校验调用 = %v，期望 [(7, 800)]", stub.hasCalls)
	}
	if stub.lastCall.UserID != 7 || stub.lastCall.All || stub.lastCall.DefaultOnly || stub.lastCall.FolderID != 800 {
		t.Fatalf("三态透传 = %+v，期望 FolderID=800", stub.lastCall)
	}
	if stub.lastCall.AfterID != 901 || stub.lastCall.Limit != 6 {
		t.Fatalf("游标/批次 = (%d, %d)，期望 (901, 6)（limit+1 多取一条）", stub.lastCall.AfterID, stub.lastCall.Limit)
	}
}

// TestListBookmarksFolderOwnership 校验指定夹过滤时的属主校验：
// 非本人/不存在的夹按 404，且不触发 refs 读取。
func TestListBookmarksFolderOwnership(t *testing.T) {
	stub := &fakeBookmarks{hasFolder: false}
	svc := newTestService(t, Deps{
		Log:       discardLogger(),
		Social:    fakeSocial{following: map[int64]bool{}},
		Bookmarks: stub,
	})
	_, err := svc.ListBookmarks(context.Background(), humanViewer(7), BookmarkScope{FolderID: 800}, 0, 20)
	requireKind(t, err, apperr.KindNotFound)
	if len(stub.hasCalls) != 1 {
		t.Fatalf("夹校验调用 = %d，期望 1", len(stub.hasCalls))
	}
	if stub.lastCall.Limit != 0 {
		t.Fatal("夹校验失败不应读取收藏 refs")
	}
}
