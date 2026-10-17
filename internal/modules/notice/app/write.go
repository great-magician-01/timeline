package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// CreateNotification 写入一条通知（contracts.NoticeService）。
// 以下情况静默跳过：接收者就是动作发起者、接收者不存在/停用/AI、幂等键重复。
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
	return s.deps.Notices.Insert(ctx, model.Notification{
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
}

// RebuildMentions 按当前正文重建提及记录（contracts.NoticeService）。
func (s *Service) RebuildMentions(ctx context.Context, in contracts.RebuildMentionsInput) error {
	at := s.eventTime(in.At)
	userIDs, err := s.resolveMentionIDs(ctx, in.MentionNames)
	if err != nil {
		return err
	}
	if err := s.deps.Mentions.DeactivateExcept(ctx, in.SourceType, in.SourceID, userIDs); err != nil {
		return err
	}
	for _, userID := range userIDs {
		id, err := s.newID()
		if err != nil {
			return err
		}
		if err := s.deps.Mentions.Upsert(ctx, id, in.SourceType, in.SourceID, userID, at); err != nil {
			return err
		}
	}
	return nil
}

// MentionNotifications 为已发布的公开内容生成提及通知（contracts.NoticeService）。
func (s *Service) MentionNotifications(ctx context.Context, in contracts.MentionNotificationsInput) error {
	at := s.eventTime(in.At)
	userIDs, err := s.resolveMentionIDs(ctx, in.MentionNames)
	if err != nil {
		return err
	}
	for _, userID := range userIDs {
		var commentID *int64
		if in.SourceType == model.NoticeSourceComment {
			id := in.SourceID
			commentID = &id
		}
		postID := in.PostID
		if err := s.CreateNotification(ctx, contracts.NotificationInput{
			IdempotencyKey: mentionKey(in.SourceType, in.SourceID, userID),
			RecipientID:    userID,
			ActorID:        in.AuthorID,
			Type:           model.NoticeTypeMention,
			TargetType:     targetTypeForSource(in.SourceType),
			TargetID:       in.PostID,
			PostID:         &postID,
			CommentID:      commentID,
			At:             at,
		}); err != nil {
			return err
		}
	}
	return nil
}

// PublishPostNotifications 在帖子发布时重建提及并写提及通知（contracts.NoticeService）。
func (s *Service) PublishPostNotifications(ctx context.Context, in contracts.PostNotificationsInput) error {
	at := s.eventTime(in.At)
	if err := s.RebuildMentions(ctx, contracts.RebuildMentionsInput{
		SourceType:   model.NoticeSourcePost,
		SourceID:     in.PostID,
		PostID:       in.PostID,
		MentionNames: in.MentionNames,
		At:           at,
	}); err != nil {
		return err
	}
	if len(in.MentionNames) == 0 {
		return nil
	}
	return s.MentionNotifications(ctx, contracts.MentionNotificationsInput{
		SourceType:   model.NoticeSourcePost,
		SourceID:     in.PostID,
		PostID:       in.PostID,
		AuthorID:     in.AuthorID,
		MentionNames: in.MentionNames,
		At:           at,
	})
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
