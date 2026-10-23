package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/dm/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/richtext"
)

// SendMessage 在会话内发送一条纯文本私信。
// 事务内重新加载会话与接收者，按最新接收策略全量复检（防守策略收紧的窗口期）。
func (s *Service) SendMessage(
	ctx context.Context, viewer model.Viewer, conversationID int64, format, source string,
) (Message, error) {
	if err := s.requireWritable(viewer); err != nil {
		return Message{}, err
	}
	if format != richtext.FormatRichText {
		return Message{}, apperr.Validation("私信只支持纯文本格式")
	}
	if err := s.allowAction(viewer); err != nil {
		return Message{}, err
	}
	now := s.now()
	messageID, err := s.newID()
	if err != nil {
		return Message{}, err
	}
	var row model.DMMessage
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		conv, getErr := s.deps.Conversations.GetForUpdate(ctx, conversationID)
		if getErr != nil {
			return getErr
		}
		peerID, getErr := domain.PeerID(conv, viewer.ID)
		if getErr != nil {
			return getErr
		}
		recipient, getErr := s.deps.Users.GetUser(ctx, peerID)
		if getErr != nil {
			return getErr
		}
		if getErr := s.ensureCanSend(ctx, viewer.ID, recipient); getErr != nil {
			return getErr
		}
		if getErr := domain.ValidateMessageSource(source); getErr != nil {
			return getErr
		}
		safeHTML, plain := richtext.Render(format, source)
		if getErr := domain.ValidateMessageBody(safeHTML, plain); getErr != nil {
			return getErr
		}
		inserted, insertErr := s.deps.Messages.Insert(ctx, model.DMMessage{
			ID:             messageID,
			ConversationID: conv.ID,
			SenderID:       viewer.ID,
			RecipientID:    recipient.ID,
			ContentFormat:  format,
			ContentSource:  source,
			ContentHTML:    safeHTML,
			PlainText:      plain,
			Status:         model.DMMessageStatusPublished,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		if insertErr != nil {
			return insertErr
		}
		row = inserted
		if touchErr := s.deps.Conversations.TouchOnSend(ctx, conv.ID, inserted.ID, now); touchErr != nil {
			return touchErr
		}
		// 提交后向双方推送实时提示信号：接收者即时收信，发送者自己的其他
		// 标签页同步。AfterCommit 保证信号发出时消息必然已提交。
		if s.deps.Realtime != nil {
			peerID := recipient.ID
			convID := conv.ID
			senderID := viewer.ID
			db.AfterCommit(ctx, func(context.Context) {
				s.deps.Realtime.PublishDM(senderID, convID)
				s.deps.Realtime.PublishDM(peerID, convID)
			})
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

// MessageList 返回会话内消息：
//   - afterID > 0 时增量返回其后的新消息（id ASC），供对话页轮询；
//   - 否则按 id DESC 翻旧消息，cursor=0 为最新一页。
//
// 非会话成员一律按不存在处理。
func (s *Service) MessageList(
	ctx context.Context, viewer model.Viewer, conversationID, cursor, afterID int64, limit int,
) (MessagePage, error) {
	conv, err := s.deps.Conversations.GetByID(ctx, conversationID)
	if err != nil {
		return MessagePage{}, err
	}
	if _, err := domain.PeerID(conv, viewer.ID); err != nil {
		return MessagePage{}, err
	}
	now := s.now()
	page := MessagePage{}
	var rows []model.DMMessage
	if afterID > 0 {
		rows, err = s.deps.Messages.ListAfter(ctx, conversationID, afterID, limit)
		if err != nil {
			return MessagePage{}, err
		}
		page.HasMore = len(rows) == limit
	} else {
		rows, err = s.deps.Messages.List(ctx, conversationID, cursor, limit+1)
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

// RecallMessage 撤回访问者本人在时间窗内发送的消息；重复撤回幂等。
func (s *Service) RecallMessage(ctx context.Context, viewer model.Viewer, messageID int64) (Message, error) {
	if err := s.requireWritable(viewer); err != nil {
		return Message{}, err
	}
	now := s.now()
	var row model.DMMessage
	err := s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		current, getErr := s.deps.Messages.GetForUpdate(ctx, messageID)
		if getErr != nil {
			return getErr
		}
		if getErr := domain.CanRecall(current, viewer, now); getErr != nil {
			return getErr
		}
		if !current.Recalled() {
			if _, recallErr := s.deps.Messages.Recall(ctx, messageID, viewer.ID, now); recallErr != nil {
				return recallErr
			}
			current.Status = model.DMMessageStatusRecalled
			current.UpdatedAt = now
			// 实际发生撤回才推送信号（幂等重放不发）；双方都应重新拉取会话。
			if s.deps.Realtime != nil {
				convID := current.ConversationID
				peerID := current.SenderID
				if viewer.ID == peerID {
					peerID = current.RecipientID
				}
				senderID := viewer.ID
				db.AfterCommit(ctx, func(context.Context) {
					s.deps.Realtime.PublishDM(senderID, convID)
					s.deps.Realtime.PublishDM(peerID, convID)
				})
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
