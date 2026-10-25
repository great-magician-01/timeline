package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/group/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/chat"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/richtext"
)

// SendMessage 在群内发送一条纯文本消息（与私信同一文本管线）。
// 事务内重新校验成员资格与群状态（防解散/被移出的窗口期）；
// 提交后向全部有效成员推送 group 信号，并按 @提及 触发成员 AI 回复任务。
func (s *Service) SendMessage(
	ctx context.Context, viewer model.Viewer, groupID int64, format, source string,
) (Message, error) {
	if err := s.requireWritable(viewer); err != nil {
		return Message{}, err
	}
	if format != richtext.FormatRichText {
		return Message{}, apperr.Validation("群消息只支持纯文本格式")
	}
	if err := s.allowAction(viewer); err != nil {
		return Message{}, err
	}
	now := s.now()
	messageID, err := s.newID()
	if err != nil {
		return Message{}, err
	}
	var row model.GroupMessage
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		group, getErr := s.deps.Groups.GetForUpdate(ctx, groupID)
		if getErr != nil {
			return getErr
		}
		member, getErr := s.deps.Members.GetForUpdate(ctx, groupID, viewer.ID)
		if getErr != nil {
			return getErr
		}
		if getErr := domain.CanSendMessage(group, member, viewer.ID); getErr != nil {
			return getErr
		}
		if getErr := chat.ValidateSource(source); getErr != nil {
			return getErr
		}
		safeHTML, plain := richtext.Render(format, source)
		if getErr := chat.ValidateBody(safeHTML, plain); getErr != nil {
			return getErr
		}
		inserted, insertErr := s.deps.Messages.Insert(ctx, model.GroupMessage{
			ID:            messageID,
			GroupID:       group.ID,
			SenderID:      viewer.ID,
			ContentFormat: format,
			ContentSource: source,
			ContentHTML:   safeHTML,
			PlainText:     plain,
			Status:        model.GroupMessageStatusPublished,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
		if insertErr != nil {
			return insertErr
		}
		row = inserted
		if touchErr := s.deps.Groups.TouchOnSend(ctx, group.ID, inserted.ID, now); touchErr != nil {
			return touchErr
		}
		// 提交后向全部有效成员推送信号（含发送者自己的其他标签页）。
		if s.deps.Realtime != nil {
			memberIDs, getErr := s.activeMemberIDs(ctx, group.ID)
			if getErr != nil {
				return getErr
			}
			s.publishAfterCommit(ctx, memberIDs, group.ID)
		}
		// 提交后解析 @提及：命中成员 AI 时交 agent 投递回复任务（实现方幂等）。
		if s.deps.AITrigger != nil {
			if getErr := s.triggerAIMentions(ctx, group.ID, inserted, plain); getErr != nil {
				return getErr
			}
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	sender, err := s.deps.Users.GetUserBrief(ctx, viewer.ID)
	if err != nil {
		return Message{}, err
	}
	return Message{Row: row, Sender: sender, CanRecall: true}, nil
}

// MessageList 返回群内消息：
//   - afterID > 0 时增量返回其后的新消息（id ASC），供对话页轮询；
//   - 否则按 id DESC 翻旧消息，cursor=0 为最新一页。
//
// 非成员一律按不存在处理；解散群对成员保留只读访问。
func (s *Service) MessageList(
	ctx context.Context, viewer model.Viewer, groupID, cursor, afterID int64, limit int,
) (MessagePage, error) {
	member, err := s.deps.Members.GetByGroupAndUser(ctx, groupID, viewer.ID)
	if err != nil {
		return MessagePage{}, err
	}
	if err := domain.RequireMember(member, viewer.ID); err != nil {
		return MessagePage{}, err
	}
	now := s.now()
	page := MessagePage{}
	var rows []model.GroupMessage
	if afterID > 0 {
		rows, err = s.deps.Messages.ListAfter(ctx, groupID, afterID, limit)
		if err != nil {
			return MessagePage{}, err
		}
		page.HasMore = len(rows) == limit
	} else {
		rows, err = s.deps.Messages.List(ctx, groupID, cursor, limit+1)
		if err != nil {
			return MessagePage{}, err
		}
		rows, page.HasMore = trimPage(rows, limit)
	}
	if len(rows) > 0 {
		page.NextCursor = rows[len(rows)-1].ID
		page.LastID = rows[len(rows)-1].ID
	}
	items, err := s.assembleMessages(ctx, viewer, rows, now)
	if err != nil {
		return MessagePage{}, err
	}
	page.Items = items
	return page, nil
}

// MarkRead 把访问者的已读游标推进到群当前最后消息（幂等，只向前推进）。
func (s *Service) MarkRead(ctx context.Context, viewer model.Viewer, groupID int64) (bool, error) {
	member, err := s.deps.Members.GetByGroupAndUser(ctx, groupID, viewer.ID)
	if err != nil {
		return false, err
	}
	if err := domain.RequireMember(member, viewer.ID); err != nil {
		return false, err
	}
	group, err := s.deps.Groups.GetByID(ctx, groupID)
	if err != nil {
		return false, err
	}
	if group.LastMessageID == nil {
		return false, nil
	}
	return s.deps.Members.MarkRead(ctx, groupID, viewer.ID, *group.LastMessageID)
}

// RecallMessage 撤回访问者本人在时间窗内发送的群消息；重复撤回幂等。
func (s *Service) RecallMessage(ctx context.Context, viewer model.Viewer, messageID int64) (Message, error) {
	if err := s.requireWritable(viewer); err != nil {
		return Message{}, err
	}
	now := s.now()
	var row model.GroupMessage
	err := s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		current, getErr := s.deps.Messages.GetForUpdate(ctx, messageID)
		if getErr != nil {
			return getErr
		}
		if getErr := domain.CanRecall(current, viewer.ID, now); getErr != nil {
			return getErr
		}
		if !current.Recalled() {
			if _, recallErr := s.deps.Messages.Recall(ctx, messageID, viewer.ID, now); recallErr != nil {
				return recallErr
			}
			current.Status = model.GroupMessageStatusRecalled
			current.UpdatedAt = now
			// 实际发生撤回才推送信号（幂等重放不发）；全部成员重新拉取。
			if s.deps.Realtime != nil {
				memberIDs, getErr := s.activeMemberIDs(ctx, current.GroupID)
				if getErr != nil {
					return getErr
				}
				s.publishAfterCommit(ctx, memberIDs, current.GroupID)
			}
		}
		row = current
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	sender, err := s.deps.Users.GetUserBrief(ctx, row.SenderID)
	if err != nil {
		return Message{}, err
	}
	return Message{Row: row, Sender: sender, CanRecall: false}, nil
}

// UnreadCount 返回访问者的群聊未读总数（跨全部群，徽章轮询）。
func (s *Service) UnreadCount(ctx context.Context, viewer model.Viewer) (int, error) {
	unread, err := s.deps.Messages.UnreadByGroups(ctx, viewer.ID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, count := range unread {
		total += count
	}
	return total, nil
}

// triggerAIMentions 解析消息中的 @提及，对命中且为本群成员的 AI 发出回复触发信号。
// 仅生成信号，任务投递的幂等与资格判定（AI 开关/被动回复/群成员）由 agent 实现。
func (s *Service) triggerAIMentions(ctx context.Context, groupID int64, msg model.GroupMessage, plain string) error {
	mentions := richtext.ExtractMentions(plain)
	if len(mentions) == 0 {
		return nil
	}
	users, err := s.deps.Users.FindActiveUsersByUsernames(ctx, mentions)
	if err != nil {
		return err
	}
	for _, user := range users {
		if !user.IsAI {
			continue
		}
		member, err := s.deps.Members.GetByGroupAndUser(ctx, groupID, user.ID)
		if err != nil || !member.Active() {
			continue
		}
		signal := contracts.GroupMentionSignal{
			AIID:        user.ID,
			GroupID:     groupID,
			MessageID:   msg.ID,
			MentionedBy: msg.SenderID,
		}
		db.AfterCommit(ctx, func(context.Context) {
			// 触发是尽力而为：失败只告警不重试，日志用于排查「AI 没有接话」类问题。
			if err := s.deps.AITrigger.OnGroupMention(context.Background(), signal); err != nil {
				s.deps.Log.Warn("群消息 @提及 触发 AI 回复任务失败", "err", err,
					"group_id", groupID, "ai_id", user.ID, "message_id", msg.ID)
			}
		})
	}
	return nil
}

// canRecall 判断消息当前对访问者是否可撤回（已撤回不再展示撤回动作）。
func (s *Service) canRecall(msg model.GroupMessage, viewerID int64, now time.Time) bool {
	if msg.Recalled() {
		return false
	}
	return domain.CanRecall(msg, viewerID, now) == nil
}

// assembleMessages 批量补齐发送者摘要并计算 can_recall，保持输入顺序。
func (s *Service) assembleMessages(ctx context.Context, viewer model.Viewer, rows []model.GroupMessage, now time.Time) ([]Message, error) {
	senderIDs := make([]int64, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.SenderID]; ok {
			continue
		}
		seen[row.SenderID] = struct{}{}
		senderIDs = append(senderIDs, row.SenderID)
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, senderIDs)
	if err != nil {
		return nil, err
	}
	items := make([]Message, 0, len(rows))
	for _, row := range rows {
		items = append(items, Message{
			Row:       row,
			Sender:    briefs[row.SenderID],
			CanRecall: s.canRecall(row, viewer.ID, now),
		})
	}
	return items, nil
}
