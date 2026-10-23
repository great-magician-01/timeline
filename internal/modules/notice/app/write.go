package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// CreateNotification 写入一条通知（contracts.NoticeService）。
// 以下情况静默跳过：接收者就是动作发起者、接收者不存在/停用/AI、幂等键重复。
// 实际写入成功后经 db.AfterCommit 推送实时提示信号：调用方（social/content/agent）
// 通常在自身事务内调用本方法，AfterCommit 保证信号发出时数据必然已提交；
// 事务外调用时信号立即发出。幂等跳过不发信号，避免客户端幽灵红点。
func (s *Service) CreateNotification(ctx context.Context, in contracts.NotificationInput) error {
	if in.RecipientID <= 0 || in.ActorID <= 0 || in.RecipientID == in.ActorID || in.IdempotencyKey == "" {
		return nil
	}
	recipient, err := s.deps.Users.GetUser(ctx, in.RecipientID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return nil
		}
		return err
	}
	if !recipient.IsActive() || recipient.IsAI {
		return nil
	}
	id, err := s.newID()
	if err != nil {
		return err
	}
	postID := in.PostID
	commentID := in.CommentID
	inserted, err := s.deps.Notices.Insert(ctx, model.Notification{
		ID:             id,
		RecipientID:    in.RecipientID,
		ActorID:        in.ActorID,
		Type:           in.Type,
		TargetType:     in.TargetType,
		TargetID:       in.TargetID,
		PostID:         postID,
		CommentID:      commentID,
		IsRead:         false,
		IdempotencyKey: in.IdempotencyKey,
		CreatedAt:      s.eventTime(in.At),
	})
	if err != nil || !inserted {
		return err
	}
	if s.deps.Realtime != nil {
		db.AfterCommit(ctx, func(context.Context) {
			s.deps.Realtime.PublishNotification(in.RecipientID)
		})
	}
	return nil
}

// RebuildMentions 按当前正文重建提及记录（contracts.NoticeService）。
func (s *Service) RebuildMentions(ctx context.Context, in contracts.RebuildMentionsInput) error {
	at := s.eventTime(in.At)
	userIDs, err := s.resolveMentionIDs(ctx, in.MentionNames)
	if err != nil {
		return err
	}
	return s.rebuildMentions(ctx, in.SourceType, in.SourceID, userIDs, at)
}

// PublishMentions 解析提及用户名一次后，重建提及记录并写入提及通知（contracts.NoticeService）。
//
// 此前调用方需连续调用 RebuildMentions + MentionNotifications，两次都会重新解析用户名；
// 这里只解析一次并把解析结果复用给重建与通知两条路径，减少一次账号查询与重复往返。
func (s *Service) PublishMentions(ctx context.Context, in contracts.PublishMentionsInput) error {
	at := s.eventTime(in.At)
	userIDs, err := s.resolveMentionIDs(ctx, in.MentionNames)
	if err != nil {
		return err
	}
	if err := s.rebuildMentions(ctx, in.SourceType, in.SourceID, userIDs, at); err != nil {
		return err
	}
	return s.notifyMentions(ctx, in.SourceType, in.SourceID, in.PostID, in.AuthorID, userIDs, at)
}

// MentionNotifications 为已发布的公开内容生成提及通知（contracts.NoticeService）。
func (s *Service) MentionNotifications(ctx context.Context, in contracts.MentionNotificationsInput) error {
	at := s.eventTime(in.At)
	userIDs, err := s.resolveMentionIDs(ctx, in.MentionNames)
	if err != nil {
		return err
	}
	return s.notifyMentions(ctx, in.SourceType, in.SourceID, in.PostID, in.AuthorID, userIDs, at)
}

// PublishPostNotifications 在帖子发布时重建提及并写提及通知（contracts.NoticeService）。
// 内部只解析一次提及用户名，重建与通知复用同一份解析结果。
func (s *Service) PublishPostNotifications(ctx context.Context, in contracts.PostNotificationsInput) error {
	at := s.eventTime(in.At)
	userIDs, err := s.resolveMentionIDs(ctx, in.MentionNames)
	if err != nil {
		return err
	}
	if err := s.rebuildMentions(ctx, model.NoticeSourcePost, in.PostID, userIDs, at); err != nil {
		return err
	}
	if len(userIDs) == 0 {
		return nil
	}
	return s.notifyMentions(ctx, model.NoticeSourcePost, in.PostID, in.PostID, in.AuthorID, userIDs, at)
}

// rebuildMentions 用已解析的用户 ID 重建来源对象上的提及：先停用不在保留集合中的旧提及，
// 再逐个建链/恢复。调用方负责在重建与通知之间复用同一份 userIDs，避免重复解析用户名。
func (s *Service) rebuildMentions(
	ctx context.Context, sourceType string, sourceID int64, userIDs []int64, at time.Time,
) error {
	if err := s.deps.Mentions.DeactivateExcept(ctx, sourceType, sourceID, userIDs); err != nil {
		return err
	}
	for _, userID := range userIDs {
		id, err := s.newID()
		if err != nil {
			return err
		}
		if err := s.deps.Mentions.Upsert(ctx, id, sourceType, sourceID, userID, at); err != nil {
			return err
		}
	}
	return nil
}

// notifyMentions 用已解析的用户 ID 写提及通知（接收者校验与幂等键由 CreateNotification 负责）。
func (s *Service) notifyMentions(
	ctx context.Context, sourceType string, sourceID, postID, authorID int64, userIDs []int64, at time.Time,
) error {
	for _, userID := range userIDs {
		var commentID *int64
		if sourceType == model.NoticeSourceComment {
			id := sourceID
			commentID = &id
		}
		post := postID
		if err := s.CreateNotification(ctx, contracts.NotificationInput{
			IdempotencyKey: mentionKey(sourceType, sourceID, userID),
			RecipientID:    userID,
			ActorID:        authorID,
			Type:           model.NoticeTypeMention,
			TargetType:     targetTypeForSource(sourceType),
			TargetID:       postID,
			PostID:         &post,
			CommentID:      commentID,
			At:             at,
		}); err != nil {
			return err
		}
	}
	return nil
}

// DeactivateMentions 在内容重新变为非公开或删除时停用提及（contracts.NoticeService）。
func (s *Service) DeactivateMentions(ctx context.Context, sourceType string, sourceID int64, at time.Time) error {
	_ = at // mentions 表无 updated_at，停用不写时间
	return s.deps.Mentions.DeactivateAll(ctx, sourceType, sourceID)
}

// ListActiveMentions 读取某用户指定时间后的生效提及（contracts.NoticeService；供 agent 调度）。
func (s *Service) ListActiveMentions(ctx context.Context, query contracts.MentionQuery) ([]contracts.MentionRef, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.deps.Mentions.ListActive(ctx, query.MentionedUserID, query.Since, limit)
	if err != nil {
		return nil, err
	}
	refs := make([]contracts.MentionRef, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, contracts.MentionRef{
			ID:              row.ID,
			MentionedUserID: row.MentionedUserID,
			SourceType:      row.SourceType,
			SourceID:        row.SourceID,
			CreatedAt:       row.CreatedAt,
		})
	}
	return refs, nil
}

// GetMention 读取单条生效提及（contracts.NoticeService；供 agent 触发任务）。
func (s *Service) GetMention(ctx context.Context, mentionID int64) (contracts.MentionRef, error) {
	row, err := s.deps.Mentions.GetActive(ctx, mentionID)
	if err != nil {
		return contracts.MentionRef{}, err
	}
	return contracts.MentionRef{
		ID:              row.ID,
		MentionedUserID: row.MentionedUserID,
		SourceType:      row.SourceType,
		SourceID:        row.SourceID,
		CreatedAt:       row.CreatedAt,
	}, nil
}

// resolveMentionIDs 把 @用户名 解析为生效账号 ID（忽略不存在/停用账号）。
func (s *Service) resolveMentionIDs(ctx context.Context, names []string) ([]int64, error) {
	if len(names) == 0 {
		return nil, nil
	}
	briefs, err := s.deps.Users.FindActiveUsersByUsernames(ctx, names)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(briefs))
	for _, brief := range briefs {
		ids = append(ids, brief.ID)
	}
	return ids, nil
}

// targetTypeForSource 把提及来源类型映射为通知目标类型。
func targetTypeForSource(sourceType string) string {
	if sourceType == model.NoticeSourceComment {
		return model.NoticeTargetComment
	}
	return model.NoticeTargetPost
}
