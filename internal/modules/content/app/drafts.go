package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/content/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ListMyDrafts 返回当前用户的草稿列表。
func (s *Service) ListMyDrafts(
	ctx context.Context,
	viewer model.Viewer,
	cursorID int64,
	limit int,
) (DraftPage, error) {
	return s.listMyPosts(ctx, viewer, model.PostStatusDraft, cursorID, limit)
}

// ListMyScheduled 返回当前用户的定时发布列表。
func (s *Service) ListMyScheduled(
	ctx context.Context,
	viewer model.Viewer,
	cursorID int64,
	limit int,
) (DraftPage, error) {
	return s.listMyPosts(ctx, viewer, model.PostStatusScheduled, cursorID, limit)
}

func (s *Service) listMyPosts(
	ctx context.Context,
	viewer model.Viewer,
	status string,
	cursorID int64,
	limit int,
) (DraftPage, error) {
	if !viewer.Authenticated {
		return DraftPage{}, apperr.Unauthenticated("请先登录")
	}
	if viewer.IsAI {
		return DraftPage{}, apperr.Forbidden("AI 账号不能通过此接口操作")
	}
	rows, hasMore, err := s.repo.ListMyPosts(ctx, viewer.ID, status, cursorID, limit)
	if err != nil {
		return DraftPage{}, s.wrap(err, "读取草稿列表失败")
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Post.ID)
	}
	jobs, err := s.publishJobsByRef(ctx, ids)
	if err != nil {
		return DraftPage{}, err
	}
	items := make([]DraftItem, 0, len(rows))
	for _, row := range rows {
		job := jobs[row.Post.ID]
		displayStatus, publishFailed, publishError := domain.PublishStateFor(
			row.Post.Status, job.Status, job.LastError)
		item := DraftItem{
			ID:              row.Post.ID,
			ContentHTML:     row.ContentHTML,
			PlainText:       domain.Summary(row.PlainText),
			Visibility:      row.Post.Visibility,
			Revision:        row.Revision,
			Status:          displayStatus,
			UpdatedAt:       row.Post.UpdatedAt,
			AttachmentCount: row.AttachmentCount,
		}
		// 只有定时发布列表返回失败/暂停信息，草稿列表字段保持原样。
		if status == model.PostStatusScheduled {
			failed := publishFailed
			message := publishError
			item.PublishFailed = &failed
			item.PublishError = &message
		}
		if row.Post.ScheduledAt != nil {
			scheduled := *row.Post.ScheduledAt
			item.ScheduledAt = &scheduled
		}
		items = append(items, item)
	}
	var nextID int64
	if len(items) > 0 {
		nextID = items[len(items)-1].ID
	}
	return DraftPage{Items: items, NextID: nextID, HasMore: hasMore}, nil
}

// publishJobsByRef 通过 jobs 端口补充每个帖子的最新定时发布作业快照。
// background_jobs 归 platform/jobs，content 只读该端口，不直接查表。
// ListByRef 只支持全局 LIMIT，故按小批 ref 查询；若某批结果达到上限（可能被截断），
// 再对批内未命中的帖子逐帖补查，避免历史作业多的帖子挤占其它帖的最新作业。
func (s *Service) publishJobsByRef(
	ctx context.Context,
	postIDs []int64,
) (map[int64]contracts.JobSnapshot, error) {
	result := map[int64]contracts.JobSnapshot{}
	if len(postIDs) == 0 || s.deps.Jobs == nil {
		return result, nil
	}
	const (
		chunkSize     = 4
		perQueryLimit = 200 // platform/jobs.ListByRef 允许的上限
	)
	for start := 0; start < len(postIDs); start += chunkSize {
		end := start + chunkSize
		if end > len(postIDs) {
			end = len(postIDs)
		}
		chunk := postIDs[start:end]
		snapshots, err := s.deps.Jobs.ListByRef(ctx, contracts.JobRefQuery{
			RefType: domain.RefTypePost,
			RefIDs:  chunk,
			Types:   []string{contracts.JobTypePostPublish},
			Limit:   perQueryLimit,
		})
		if err != nil {
			return nil, s.wrap(err, "读取定时发布作业失败")
		}
		collectLatestPublishJobs(snapshots, result)
		if len(snapshots) < perQueryLimit {
			continue
		}
		// 结果达到上限时可能被截断，对批内仍未命中的帖子逐帖补查最新作业。
		for _, postID := range chunk {
			if _, ok := result[postID]; ok {
				continue
			}
			fallback, err := s.deps.Jobs.ListByRef(ctx, contracts.JobRefQuery{
				RefType: domain.RefTypePost,
				RefIDs:  []int64{postID},
				Types:   []string{contracts.JobTypePostPublish},
				Limit:   perQueryLimit,
			})
			if err != nil {
				return nil, s.wrap(err, "读取定时发布作业失败")
			}
			collectLatestPublishJobs(fallback, result)
		}
	}
	return result, nil
}

// collectLatestPublishJobs 把快照中每个引用对象的最新有效状态写入结果。
// 只保留 pending/running/failed/paused：已成功或已取消的旧作业不展示状态。
func collectLatestPublishJobs(
	snapshots []contracts.JobSnapshot,
	result map[int64]contracts.JobSnapshot,
) {
	for _, snapshot := range snapshots {
		if snapshot.RefID == nil {
			continue
		}
		switch snapshot.Status {
		case contracts.JobStatusPending, contracts.JobStatusRunning,
			contracts.JobStatusFailed, contracts.JobStatusPaused:
		default:
			continue
		}
		refID := *snapshot.RefID
		if existing, ok := result[refID]; !ok || snapshot.ID > existing.ID {
			result[refID] = snapshot
		}
	}
}
