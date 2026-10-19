package http

import (
	"github.com/great-magician-01/timeline/internal/modules/dm/app"
	"github.com/great-magician-01/timeline/internal/modules/dm/domain"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/view"
)

// conversationResponse 是私信会话 DTO（docs/API.md 第 10 章）。
type conversationResponse struct {
	ID          string                 `json:"id"`
	Peer        view.UserBriefResponse `json:"peer"`
	LastMessage *lastMessageResponse   `json:"last_message"`
	UnreadCount int                    `json:"unread_count"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
}

// lastMessageResponse 是会话列表的最后消息预览；空会话为 null。
type lastMessageResponse struct {
	ID        string `json:"id"`
	Preview   string `json:"preview"`
	CreatedAt string `json:"created_at"`
}

// messageResponse 是私信消息 DTO；撤回消息的正文为空字符串。
type messageResponse struct {
	ID             string                 `json:"id"`
	ConversationID string                 `json:"conversation_id"`
	Sender         view.UserBriefResponse `json:"sender"`
	ContentHTML    string                 `json:"content_html"`
	Status         string                 `json:"status"`
	ReadAt         *string                `json:"read_at"`
	CanRecall      bool                   `json:"can_recall"`
	CreatedAt      string                 `json:"created_at"`
}

// conversationResponseFor 组装单个会话 DTO。
func conversationResponseFor(item app.Conversation) conversationResponse {
	resp := conversationResponse{
		ID:          format.FormatID(item.Row.ID),
		Peer:        view.UserBriefResponseFor(item.Peer),
		UnreadCount: item.UnreadCount,
		CreatedAt:   format.FormatTime(item.Row.CreatedAt),
		UpdatedAt:   format.FormatTime(item.Row.UpdatedAt),
	}
	if item.LastMessage != nil {
		resp.LastMessage = &lastMessageResponse{
			ID:        format.FormatID(item.LastMessage.ID),
			Preview:   domain.Preview(*item.LastMessage),
			CreatedAt: format.FormatTime(item.LastMessage.CreatedAt),
		}
	}
	return resp
}

// messageResponseFor 组装单条消息 DTO；已撤回消息不返回正文。
func messageResponseFor(item app.Message) messageResponse {
	msg := item.Row
	resp := messageResponse{
		ID:             format.FormatID(msg.ID),
		ConversationID: format.FormatID(msg.ConversationID),
		Sender:         view.UserBriefResponseFor(item.Sender),
		ContentHTML:    msg.ContentHTML,
		Status:         msg.Status,
		CanRecall:      item.CanRecall,
		CreatedAt:      format.FormatTime(msg.CreatedAt),
	}
	if msg.Recalled() {
		resp.ContentHTML = ""
	}
	if msg.ReadAt != nil {
		readAt := format.FormatTime(*msg.ReadAt)
		resp.ReadAt = &readAt
	}
	return resp
}

// messageResponsesFor 批量组装消息 DTO。
func messageResponsesFor(items []app.Message) []messageResponse {
	responses := make([]messageResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, messageResponseFor(item))
	}
	return responses
}
