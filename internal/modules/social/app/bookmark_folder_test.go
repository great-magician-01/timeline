package app

import (
	"context"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// TestListBookmarkFolders 验证收藏夹列表的行映射与守卫。
func TestListBookmarkFolders(t *testing.T) {
	stubs := newStubSet()
	stubs.folders.folders = []domain.BookmarkFolderRow{
		{ID: 802, UserID: stubViewerID, Name: "技术文章", BookmarkCount: 3, CreatedAt: stubNow, UpdatedAt: stubNow},
		{ID: 801, UserID: stubViewerID, Name: "读书笔记", BookmarkCount: 0, CreatedAt: stubNow, UpdatedAt: stubNow},
	}

	got, err := stubs.service().ListBookmarkFolders(context.Background(), stubViewer())
	requireOK(t, err)
	if len(got) != 2 {
		t.Fatalf("收藏夹数量 = %d，期望 2", len(got))
	}
	if got[0].ID != 802 || got[0].Name != "技术文章" || got[0].BookmarkCount != 3 {
		t.Fatalf("第一项 = %+v，期望夹 802/技术文章/3", got[0])
	}
	if got[1].BookmarkCount != 0 {
		t.Fatalf("第二项收藏数 = %d，期望 0", got[1].BookmarkCount)
	}
}

// TestListBookmarkFoldersGuard 校验游客不可读取收藏夹列表。
func TestListBookmarkFoldersGuard(t *testing.T) {
	stubs := newStubSet()
	_, err := stubs.service().ListBookmarkFolders(context.Background(), stubGuestViewer())
	requireKind(t, err, apperr.KindUnauthenticated)
}

// TestCreateBookmarkFolder 覆盖创建收藏夹的成功、校验与上限路径。
func TestCreateBookmarkFolder(t *testing.T) {
	t.Run("成功（名称去首尾空白）", func(t *testing.T) {
		stubs := newStubSet()
		got, err := stubs.service().CreateBookmarkFolder(context.Background(), stubViewer(), "  技术文章  ")
		requireOK(t, err)
		if got.Name != "技术文章" || got.ID == 0 {
			t.Fatalf("创建结果 = (%d, %q)，期望新 ID/技术文章", got.ID, got.Name)
		}
		if len(stubs.folders.creates) != 1 {
			t.Fatalf("创建调用 = %d，期望 1", len(stubs.folders.creates))
		}
		if stubs.folders.creates[0].userID != stubViewerID || stubs.folders.creates[0].id == 0 {
			t.Fatalf("创建参数 = %+v，期望访问者与新 ID", stubs.folders.creates[0])
		}
	})

	t.Run("名称校验失败", func(t *testing.T) {
		stubs := newStubSet()
		_, err := stubs.service().CreateBookmarkFolder(context.Background(), stubViewer(), "   ")
		requireKind(t, err, apperr.KindValidation)
		if len(stubs.folders.creates) != 0 || stubs.folders.countCalls != 0 {
			t.Fatal("名称校验失败不应进入事务")
		}
	})

	t.Run("达到上限返回 folder_limit 冲突", func(t *testing.T) {
		stubs := newStubSet()
		stubs.folders.countActive = domain.MaxBookmarkFoldersPerUser
		_, err := stubs.service().CreateBookmarkFolder(context.Background(), stubViewer(), "新夹")
		e := requireKind(t, err, apperr.KindConflict)
		if e.Code != "folder_limit" {
			t.Fatalf("冲突码 = %q，期望 folder_limit", e.Code)
		}
		if len(stubs.folders.creates) != 0 {
			t.Fatal("达上限时不应创建收藏夹")
		}
	})

	t.Run("计数失败上抛", func(t *testing.T) {
		stubs := newStubSet()
		stubs.folders.countErr = apperr.Internal("内部错误")
		if _, err := stubs.service().CreateBookmarkFolder(context.Background(), stubViewer(), "新夹"); err == nil {
			t.Fatal("期望返回错误")
		}
	})
}

// TestRenameBookmarkFolderOwnerOnly 覆盖重命名的属主与存在性校验。
func TestRenameBookmarkFolderOwnerOnly(t *testing.T) {
	folderID := int64(800)

	t.Run("本人重命名成功", func(t *testing.T) {
		stubs := newStubSet()
		got, err := stubs.service().RenameBookmarkFolder(context.Background(), stubViewer(), folderID, "新名字")
		requireOK(t, err)
		if got.Name != "新名字" {
			t.Fatalf("重命名结果 = %q，期望 新名字", got.Name)
		}
		if len(stubs.folders.renames) != 1 || stubs.folders.renames[0].folderID != folderID {
			t.Fatalf("重命名调用 = %+v，期望夹 %d", stubs.folders.renames, folderID)
		}
	})

	t.Run("他人收藏夹按 404", func(t *testing.T) {
		stubs := newStubSet()
		stubs.folders.activeFolder.UserID = stubOtherID
		_, err := stubs.service().RenameBookmarkFolder(context.Background(), stubViewer(), folderID, "新名字")
		requireKind(t, err, apperr.KindNotFound)
		if len(stubs.folders.renames) != 0 {
			t.Fatal("属主校验失败时不应执行重命名")
		}
	})

	t.Run("收藏夹不存在按 404", func(t *testing.T) {
		stubs := newStubSet()
		stubs.folders.getErr = apperr.NotFound("收藏夹不存在")
		_, err := stubs.service().RenameBookmarkFolder(context.Background(), stubViewer(), folderID, "新名字")
		requireKind(t, err, apperr.KindNotFound)
	})

	t.Run("名称校验失败", func(t *testing.T) {
		stubs := newStubSet()
		_, err := stubs.service().RenameBookmarkFolder(
			context.Background(), stubViewer(), folderID, strings.Repeat("长", domain.MaxBookmarkFolderNameRunes+1))
		requireKind(t, err, apperr.KindValidation)
	})
}

// TestDeleteBookmarkFolder 覆盖删除收藏夹与"生效收藏归还默认"的同事务配对。
func TestDeleteBookmarkFolder(t *testing.T) {
	folderID := int64(800)

	t.Run("删除成功并归还默认", func(t *testing.T) {
		stubs := newStubSet()
		err := stubs.service().DeleteBookmarkFolder(context.Background(), stubViewer(), folderID)
		requireOK(t, err)
		if len(stubs.folders.deletes) != 1 || stubs.folders.deletes[0] != folderID {
			t.Fatalf("删除调用 = %v，期望 [%d]", stubs.folders.deletes, folderID)
		}
		if len(stubs.folders.reassigns) != 1 {
			t.Fatalf("归还默认调用 = %d，期望 1", len(stubs.folders.reassigns))
		}
		reassign := stubs.folders.reassigns[0]
		if reassign.userID != stubViewerID || reassign.folderID != folderID {
			t.Fatalf("归还参数 = %+v，期望 (%d, %d)", reassign, stubViewerID, folderID)
		}
	})

	t.Run("他人收藏夹按 404 且不删除", func(t *testing.T) {
		stubs := newStubSet()
		stubs.folders.activeFolder.UserID = stubOtherID
		err := stubs.service().DeleteBookmarkFolder(context.Background(), stubViewer(), folderID)
		requireKind(t, err, apperr.KindNotFound)
		if len(stubs.folders.deletes) != 0 || len(stubs.folders.reassigns) != 0 {
			t.Fatal("属主校验失败时不应删除或归还")
		}
	})

	t.Run("写入失败不归还", func(t *testing.T) {
		stubs := newStubSet()
		stubs.folders.deleteErr = apperr.Internal("内部错误")
		if err := stubs.service().DeleteBookmarkFolder(context.Background(), stubViewer(), folderID); err == nil {
			t.Fatal("期望返回错误")
		}
		if len(stubs.folders.reassigns) != 0 {
			t.Fatal("删除失败时不应归还收藏")
		}
	})
}
