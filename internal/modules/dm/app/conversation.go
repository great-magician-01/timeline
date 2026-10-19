package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/dm/domain"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// CreateOrGetConversation 创建或返回与 username 的既有会话（幂等）。
// 创建入口先做接收策略前置校验以即时反馈；发送消息时仍会全量复检。
func (s *Service) CreateOrGetConversation(ctx context.Context, viewer model.Viewer, username string) (Conversation, error) {
	if err := s.requireWritable(viewer); err != nil {
		return Conversation{}, err
	}
	recipient, err := s.deps.Users.GetUserByUsername(ctx, username)
	if err != nil {
		return Conversation{}, err
	}
	if err := s.ensureCanSend(ctx, viewer.ID, recipient); err != nil {
		return Conversation{}, err
	}
	if err := s.allowAction(viewer); err != nil {
		return Conversation{}, err
	}
	userAID, userBID, err := domain.NormalizePair(viewer.ID, recipient.ID)
	if err != nil {
		return Conversation{}, err
	}
	id, err := s.newID()
	if err != nil {
		return Conversation{}, err
	}
	now := s.now()
	var conv model.DMConversation
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		var upsertErr error
		conv, upsertErr = s.deps.Conversations.Upsert(ctx, model.DMConversation{
			ID:        id,
			UserAID:   userAID,
			UserBID:   userBID,
			CreatedAt: now,
			UpdatedAt: now,
		})
		return upsertErr
	})
	if err != nil {
		return Conversation{}, err
	}
	return s.conversationView(ctx, viewer.ID, conv)
}

// ConversationList 返回访问者未隐藏且有消息的会话，按最后消息倒序分页。
func (s *Service) ConversationList(ctx context.Context, viewer model.Viewer, cursor int64, limit int) (ConversationPage, error) {
	rows, err := s.deps.Conversations.ListByUser(ctx, viewer.ID, cursor, limit+1)
	if err != nil {
		return ConversationPage{}, err
	}
	rows, hasMore := trimPage(rows, limit)

	lastMessageIDs := make([]int64, 0, len(rows))
	conversationIDs := make([]int64, 0, len(rows))
	peerIDs := make([]int64, 0, len(rows))
	peers := make(map[int64]int64, len(rows)) // conversationID → peerID
	for _, row := range rows {
		if row.LastMessageID != nil {
			lastMessageIDs = append(lastMessageIDs, *row.LastMessageID)
		}
		conversationIDs = append(conversationIDs, row.ID)
		if peerID, ok := row.PeerIDFor(viewer.ID); ok {
			peerIDs = append(peerIDs, peerID)
			peers[row.ID] = peerID
		}
	}
	lastMessages, err := s.deps.Messages.ByIDs(ctx, lastMessageIDs)
	if err != nil {
		return ConversationPage{}, err
	}
	unread, err := s.deps.Messages.UnreadByConversations(ctx, viewer.ID, conversationIDs)
	if err != nil {
		return ConversationPage{}, err
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, peerIDs)
	if err != nil {
		return ConversationPage{}, err
	}

	page := ConversationPage{Items: make([]Conversation, 0, len(rows)), HasMore: hasMore}
	for _, row := range rows {
		item := Conversation{
			Row:         row,
			Peer:        briefs[peers[row.ID]],
			UnreadCount: unread[row.ID],
		}
		if row.LastMessageID != nil {
			if msg, ok := lastMessages[*row.LastMessageID]; ok {
				item.LastMessage = &msg
			}
			page.NextCursor = *row.LastMessageID
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

// ConversationDetail 返回单个会话视图；非成员按不存在处理。
func (s *Service) ConversationDetail(ctx context.Context, viewer model.Viewer, conversationID int64) (Conversation, error) {
	conv, err := s.deps.Conversations.GetByID(ctx, conversationID)
	if err != nil {
		return Conversation{}, err
	}
	if _, err := domain.PeerID(conv, viewer.ID); err != nil {
		return Conversation{}, err
	}
	return s.conversationView(ctx, viewer.ID, conv)
}

// MarkRead 把会话内访问者接收方向的未读消息标记已读，返回更新条数（幂等）。
func (s *Service) MarkRead(ctx context.Context, viewer model.Viewer, conversationID int64) (int64, error) {
	conv, err := s.deps.Conversations.GetByID(ctx, conversationID)
	if err != nil {
		return 0, err
	}
	if _, err := domain.PeerID(conv, viewer.ID); err != nil {
		return 0, err
	}
	return s.deps.Messages.MarkRead(ctx, conversationID, viewer.ID, s.now())
}

// HideConversation 删除访问者的会话视图（仅自己一侧隐藏，对方无感知），幂等。
// 同一事务内把该会话自己的未读消息标记已读：删除后会话不再出现在列表，
// 未读徽章不能留下无法清除的残留。
func (s *Service) HideConversation(ctx context.Context, viewer model.Viewer, conversationID int64) error {
	if err := s.requireWritable(viewer); err != nil {
		return err
	}
	now := s.now()
	return s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		conv, err := s.deps.Conversations.GetForUpdate(ctx, conversationID)
		if err != nil {
			return err
		}
		if _, err := domain.PeerID(conv, viewer.ID); err != nil {
			return err
		}
		if _, err := s.deps.Messages.MarkRead(ctx, conversationID, viewer.ID, now); err != nil {
			return err
		}
		_, err = s.deps.Conversations.HideFor(ctx, conversationID, viewer.ID, now)
		return err
	})
}

// UnreadCount 返回访问者的私信未读总数（徽章轮询）。
func (s *Service) UnreadCount(ctx context.Context, viewer model.Viewer) (int, error) {
	return s.deps.Messages.UnreadCount(ctx, viewer.ID)
}
