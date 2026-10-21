package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// BookmarkPost 收藏帖子（私有阅读清单：不产生通知、不更新帖子计数）。
// folderID 为 nil 表示收藏到默认收藏（未分组）；指定夹时校验属主（非本人/已删除按 404）。
// 幂等：重复收藏只更新归属与时间，不产生重复记录。
func (s *Service) BookmarkPost(ctx context.Context, viewer model.Viewer, postID int64, folderID *int64) (BookmarkResult, error) {
	post, err := s.requireInteractivePost(ctx, viewer, postID)
	if err != nil {
		return BookmarkResult{}, err
	}
	if err := s.allowAction(s.deps.BookmarkLimiter, viewer); err != nil {
		return BookmarkResult{}, err
	}
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		now := s.now()
		if folderID != nil {
			// 先锁夹行再校验属主：与删除收藏夹并发时由行锁串行化，避免把收藏挂进正在删除的夹。
			folder, err := s.deps.Folders.GetActiveFolderForUpdate(ctx, *folderID)
			if err != nil {
				return err
			}
			if folder.UserID != viewer.ID {
				return apperr.NotFound("收藏夹不存在")
			}
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		_, err = s.deps.Bookmarks.UpsertActive(ctx, id, viewer.ID, post.ID, folderID, now)
		return err
	})
	if err != nil {
		return BookmarkResult{}, err
	}
	return BookmarkResult{Bookmarked: true, FolderID: folderID}, nil
}

// UnbookmarkPost 取消收藏（幂等）：生效记录逻辑失效，folder_id 保留；
// 已删除/不可见的帖子同样可取消收藏记录（守卫只校验访问者身份）。
func (s *Service) UnbookmarkPost(ctx context.Context, viewer model.Viewer, postID int64) (BookmarkResult, error) {
	if err := s.requireWritable(viewer); err != nil {
		return BookmarkResult{}, err
	}
	if err := s.allowAction(s.deps.BookmarkLimiter, viewer); err != nil {
		return BookmarkResult{}, err
	}
	err := s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		_, err := s.deps.Bookmarks.Deactivate(ctx, viewer.ID, postID, s.now())
		return err
	})
	if err != nil {
		return BookmarkResult{}, err
	}
	return BookmarkResult{Bookmarked: false}, nil
}

// bookmarkFolderFromRow 把 repo 行转换为读模型。
func bookmarkFolderFromRow(row domain.BookmarkFolderRow) BookmarkFolder {
	return BookmarkFolder{
		ID:            row.ID,
		Name:          row.Name,
		BookmarkCount: row.BookmarkCount,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}
