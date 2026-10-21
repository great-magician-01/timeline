package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ListBookmarkFolders 返回当前用户的收藏夹列表（含各自的生效收藏数，新建在前）。
func (s *Service) ListBookmarkFolders(ctx context.Context, viewer model.Viewer) ([]BookmarkFolder, error) {
	if err := s.requireWritable(viewer); err != nil {
		return nil, err
	}
	rows, err := s.deps.Folders.ListFoldersWithCounts(ctx, viewer.ID)
	if err != nil {
		return nil, err
	}
	items := make([]BookmarkFolder, 0, len(rows))
	for _, row := range rows {
		items = append(items, bookmarkFolderFromRow(row))
	}
	return items, nil
}

// CreateBookmarkFolder 创建收藏夹；名称校验失败 400，达到上限返回 409 folder_limit。
func (s *Service) CreateBookmarkFolder(ctx context.Context, viewer model.Viewer, name string) (BookmarkFolder, error) {
	if err := s.requireWritable(viewer); err != nil {
		return BookmarkFolder{}, err
	}
	normalizedName, err := domain.NormalizeBookmarkFolderName(name)
	if err != nil {
		return BookmarkFolder{}, err
	}
	var row domain.BookmarkFolderRow
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		count, err := s.deps.Folders.CountActiveFolders(ctx, viewer.ID)
		if err != nil {
			return err
		}
		if count >= domain.MaxBookmarkFoldersPerUser {
			return apperr.Conflict("folder_limit", "收藏夹数量已达上限，请先删除部分收藏夹")
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		row, err = s.deps.Folders.CreateFolder(ctx, id, viewer.ID, normalizedName, s.now())
		return err
	})
	if err != nil {
		return BookmarkFolder{}, err
	}
	return bookmarkFolderFromRow(row), nil
}

// RenameBookmarkFolder 重命名收藏夹；非本人/不存在的收藏夹一律按 404（不暴露存在性）。
func (s *Service) RenameBookmarkFolder(ctx context.Context, viewer model.Viewer, folderID int64, name string) (BookmarkFolder, error) {
	if err := s.requireWritable(viewer); err != nil {
		return BookmarkFolder{}, err
	}
	normalizedName, err := domain.NormalizeBookmarkFolderName(name)
	if err != nil {
		return BookmarkFolder{}, err
	}
	var row domain.BookmarkFolderRow
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		folder, err := s.deps.Folders.GetActiveFolderForUpdate(ctx, folderID)
		if err != nil {
			return err
		}
		if folder.UserID != viewer.ID {
			return apperr.NotFound("收藏夹不存在")
		}
		row, err = s.deps.Folders.RenameFolder(ctx, folderID, normalizedName, s.now())
		return err
	})
	if err != nil {
		return BookmarkFolder{}, err
	}
	return bookmarkFolderFromRow(row), nil
}

// DeleteBookmarkFolder 逻辑删除收藏夹，并在同一事务内把夹下的生效收藏归还默认收藏
// （folder_id 置 NULL）；收藏记录本身不受影响。非本人/不存在的收藏夹按 404。
func (s *Service) DeleteBookmarkFolder(ctx context.Context, viewer model.Viewer, folderID int64) error {
	if err := s.requireWritable(viewer); err != nil {
		return err
	}
	return s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		folder, err := s.deps.Folders.GetActiveFolderForUpdate(ctx, folderID)
		if err != nil {
			return err
		}
		if folder.UserID != viewer.ID {
			return apperr.NotFound("收藏夹不存在")
		}
		changed, err := s.deps.Folders.DeleteFolder(ctx, folderID, viewer.ID, s.now())
		if err != nil || !changed {
			return err
		}
		return s.deps.Folders.ReassignToDefault(ctx, viewer.ID, folderID, s.now())
	})
}
