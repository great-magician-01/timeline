package http

import (
	"github.com/great-magician-01/timeline/internal/modules/group/app"
	"github.com/great-magician-01/timeline/internal/shared/chat"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/view"
)

// groupResponse 是群会话 DTO（docs/API.md 群聊章）。
type groupResponse struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	OwnerID     string               `json:"owner_id"`
	Status      string               `json:"status"`
	Role        string               `json:"role"`
	MemberCount int                  `json:"member_count"`
	LastMessage *lastMessageResponse `json:"last_message"`
	UnreadCount int                  `json:"unread_count"`
	CreatedAt   string               `json:"created_at"`
	UpdatedAt   string               `json:"updated_at"`
}

// lastMessageResponse 是群列表的最后消息预览；空群为 null。
type lastMessageResponse struct {
	ID        string `json:"id"`
	Preview   string `json:"preview"`
	CreatedAt string `json:"created_at"`
}

// memberResponse 是群成员 DTO（成员列表与拉人返回共用）。
type memberResponse struct {
	ID       string                 `json:"id"`
	User     view.UserBriefResponse `json:"user"`
	Role     string                 `json:"role"`
	JoinedAt string                 `json:"joined_at"`
}

// messageResponse 是群消息 DTO；撤回消息的正文为空字符串。
type messageResponse struct {
	ID          string                 `json:"id"`
	GroupID     string                 `json:"group_id"`
	Sender      view.UserBriefResponse `json:"sender"`
	ContentHTML string                 `json:"content_html"`
	Status      string                 `json:"status"`
	CanRecall   bool                   `json:"can_recall"`
	CreatedAt   string                 `json:"created_at"`
}

// groupResponseFor 组装单个群 DTO。
func groupResponseFor(item app.Group) groupResponse {
	resp := groupResponse{
		ID:          format.FormatID(item.Row.ID),
		Name:        item.Row.Name,
		OwnerID:     format.FormatID(item.Row.OwnerID),
		Status:      item.Row.Status,
		Role:        item.Role,
		MemberCount: item.MemberCount,
		UnreadCount: item.UnreadCount,
		CreatedAt:   format.FormatTime(item.Row.CreatedAt),
		UpdatedAt:   format.FormatTime(item.Row.UpdatedAt),
	}
	if item.LastMessage != nil {
		resp.LastMessage = &lastMessageResponse{
			ID:        format.FormatID(item.LastMessage.ID),
			Preview:   chat.Preview(item.LastMessage.Recalled(), item.LastMessage.PlainText),
			CreatedAt: format.FormatTime(item.LastMessage.CreatedAt),
		}
	}
	return resp
}

// groupResponsesFor 批量组装群 DTO。
func groupResponsesFor(items []app.Group) []groupResponse {
	responses := make([]groupResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, groupResponseFor(item))
	}
	return responses
}

// memberResponseFor 组装单个成员 DTO（ID 可为空串：拉人返回不含成员行时）。
func memberResponseFor(item app.MemberItem) memberResponse {
	resp := memberResponse{
		User:     view.UserBriefResponseFor(item.Brief),
		Role:     item.Row.Role,
		JoinedAt: format.FormatTime(item.Row.JoinedAt),
	}
	if item.Row.ID != 0 {
		resp.ID = format.FormatID(item.Row.ID)
	}
	return resp
}

// memberResponsesFor 批量组装成员 DTO。
func memberResponsesFor(items []app.MemberItem) []memberResponse {
	responses := make([]memberResponse, 0, len(items))
	for _, item := range items {
		responses = append(responses, memberResponseFor(item))
	}
	return responses
}

// messageResponseFor 组装单条消息 DTO；已撤回消息不返回正文。
func messageResponseFor(item app.Message) messageResponse {
	msg := item.Row
	resp := messageResponse{
		ID:          format.FormatID(msg.ID),
		GroupID:     format.FormatID(msg.GroupID),
		Sender:      view.UserBriefResponseFor(item.Sender),
		ContentHTML: msg.ContentHTML,
		Status:      msg.Status,
		CanRecall:   item.CanRecall,
		CreatedAt:   format.FormatTime(msg.CreatedAt),
	}
	if msg.Recalled() {
		resp.ContentHTML = ""
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
