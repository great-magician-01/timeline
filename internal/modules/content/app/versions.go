package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ListVersions 返回历史版本列表；历史版本继承当前帖子的访问权限，不单独授权。
func (s *Service) ListVersions(
	ctx context.Context,
	viewer model.Viewer,
	postID int64,
	cursorID int64,
	limit int,
) (VersionPage, error) {
	post, err := s.repo.GetPost(ctx, postID)
	if err != nil {
		return VersionPage{}, publicContentNotFound(err)
	}
	if _, err := s.requireVisible(ctx, viewer, post); err != nil {
		return VersionPage{}, err
	}
	rows, hasMore, err := s.repo.ListVersions(ctx, postID, cursorID, limit)
	if err != nil {
		return VersionPage{}, s.wrap(err, "读取历史版本列表失败")
	}
	items := make([]VersionItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, VersionItem{
			ID:                    row.ID,
			VersionNumber:         row.VersionNumber,
			Visibility:            row.Visibility,
			CreatedAt:             row.CreatedAt,
			RestoredFromVersionID: row.RestoredFromVersionID,
			Summary:               domain.Summary(row.PlainText),
		})
	}
	var nextID int64
	if len(items) > 0 {
		nextID = items[len(items)-1].ID
	}
	return VersionPage{Items: items, NextID: nextID, HasMore: hasMore}, nil
}

// GetVersionDetail 返回指定历史版本的完整快照。
func (s *Service) GetVersionDetail(
	ctx context.Context,
	viewer model.Viewer,
	postID, versionID int64,
) (VersionDetail, error) {
	post, err := s.repo.GetPost(ctx, postID)
	if err != nil {
		return VersionDetail{}, publicContentNotFound(err)
	}
	if _, err := s.requireVisible(ctx, viewer, post); err != nil {
		return VersionDetail{}, err
	}
	version, err := s.repo.GetVersion(ctx, postID, versionID)
	if err != nil {
		return VersionDetail{}, err
	}
	attachments, err := s.versionAttachmentViews(ctx, version.ID, false)
	if err != nil {
		return VersionDetail{}, err
	}
	return VersionDetail{
		ID:                    version.ID,
		VersionNumber:         version.VersionNumber,
		ContentFormat:         version.ContentFormat,
		ContentSource:         version.ContentSource,
		ContentHTML:           version.ContentHTML,
		Visibility:            version.Visibility,
		CreatedAt:             version.CreatedAt,
		CreatedBy:             version.CreatedBy,
		RestoredFromVersionID: version.RestoredFromVersionID,
		Attachments:           attachments,
	}, nil
}

// RestoreVersion 以历史快照生成一个新的当前版本，不覆盖也不删除既有历史。
func (s *Service) RestoreVersion(
	ctx context.Context,
	viewer model.Viewer,
	postID, versionID int64,
) (PostDetail, error) {
	if err := s.requireWritableViewer(viewer); err != nil {
		return PostDetail{}, err
	}
	if err := s.consumePostQuota(ctx, viewer.ID); err != nil {
		return PostDetail{}, err
	}
	var following map[int64]bool
	var updated model.Post
	err := s.withTx(ctx, func(txCtx context.Context) error {
		post, err := s.repo.GetPostForUpdate(txCtx, postID)
		if err != nil {
			return err
		}
		if domain.IsDeleted(post.Status) {
			return apperr.NotFound("内容不存在或无权访问")
		}
		following, err = s.requireAuthor(txCtx, viewer, post)
		if err != nil {
			return err
		}
		if post.Status != model.PostStatusPublished {
			return apperr.Forbidden("只有已发布的内容可以恢复历史版本")
		}
		version, err := s.repo.GetVersion(txCtx, postID, versionID)
		if err != nil {
			return err
		}
		author, err := s.loadUser(txCtx, post.AuthorID, "作者账号不存在")
		if err != nil {
			return err
		}
		if err := domain.CheckVisibilityPolicy(author.IsAI, version.Visibility); err != nil {
			return err
		}
		now := s.Now()
		maxVersion, err := s.repo.MaxVersionNumber(txCtx, postID)
		if err != nil {
			return s.wrap(err, "读取当前版本号失败")
		}
		views, err := s.versionAttachmentViews(txCtx, version.ID, true)
		if err != nil {
			return err
		}
		attachmentIDs := make([]int64, 0, len(views))
		for _, view := range views {
			attachmentIDs = append(attachmentIDs, view.Attachment.ID)
		}
		sourceVersionID := version.ID
		newVersionID, err := s.nextID()
		if err != nil {
			return err
		}
		if err := s.repo.InsertVersion(txCtx, model.PostVersion{
			ID:                    newVersionID,
			PostID:                postID,
			VersionNumber:         domain.NextVersionNumber(maxVersion),
			ContentFormat:         version.ContentFormat,
			ContentSource:         version.ContentSource,
			ContentHTML:           version.ContentHTML,
			PlainText:             version.PlainText,
			Visibility:            version.Visibility,
			RestoredFromVersionID: &sourceVersionID,
			CreatedBy:             post.AuthorID,
			CreatedAt:             now,
		}); err != nil {
			return s.wrap(err, "写入恢复版本失败")
		}
		if err := s.repo.InsertVersionAttachments(txCtx, newVersionID, attachmentIDs); err != nil {
			return s.wrap(err, "写入恢复版本附件失败")
		}
		if err := s.linkAttachments(txCtx, postID, attachmentIDs, now); err != nil {
			return err
		}
		if err := s.repo.SetCurrentVersionAndVisibility(
			txCtx, postID, newVersionID, version.Visibility, now); err != nil {
			return s.wrap(err, "更新当前版本失败")
		}
		updated, err = s.repo.GetPost(txCtx, postID)
		if err != nil {
			return err
		}
		return s.rebuildDerived(txCtx, updated, version.PlainText, now)
	})
	if err != nil {
		return PostDetail{}, err
	}
	// 详情组装在事务提交后执行，不占 posts 行锁。
	return s.loadDetail(ctx, viewer, following, updated)
}

// versionAttachmentViews 读取版本快照引用附件的展示视图。
// onlyAvailable 为真时只保留仍然有效的附件（恢复版本时使用）。
func (s *Service) versionAttachmentViews(
	ctx context.Context,
	versionID int64,
	onlyAvailable bool,
) ([]AttachmentView, error) {
	byVersion, err := s.repo.VersionAttachmentIDsMany(ctx, []int64{versionID})
	if err != nil {
		return nil, s.wrap(err, "读取历史版本附件失败")
	}
	ids := byVersion[versionID]
	rows, derivatives, err := s.loadAttachments(ctx, ids)
	if err != nil {
		return nil, err
	}
	views := make([]AttachmentView, 0, len(ids))
	for _, id := range ids {
		attachment, ok := rows[id]
		if !ok {
			continue
		}
		if onlyAvailable && !attachment.Available() {
			continue
		}
		views = append(views, AttachmentView{Attachment: attachment, Derivatives: derivatives[id]})
	}
	return views, nil
}
