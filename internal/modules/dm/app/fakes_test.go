package app_test

import (
	"context"
	"sort"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

type fakeIDs struct{ next int64 }

func (f *fakeIDs) Next() (int64, error) {
	f.next++
	return f.next, nil
}

type fakeTx struct{ calls int }

func (f *fakeTx) WithTx(ctx context.Context, fn func(context.Context) error) error {
	f.calls++
	return fn(ctx)
}

type fakeUsers struct {
	byID     map[int64]model.User
	byName   map[string]model.User
	briefs   map[int64]model.UserBrief
	userErr  error
	briefErr error
}

func newFakeUsers(users ...model.User) *fakeUsers {
	f := &fakeUsers{byID: map[int64]model.User{}, byName: map[string]model.User{}, briefs: map[int64]model.UserBrief{}}
	for _, u := range users {
		f.byID[u.ID] = u
		f.byName[u.Username] = u
		f.briefs[u.ID] = model.UserBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname, IsAI: u.IsAI}
	}
	return f
}

func (f *fakeUsers) GetUser(_ context.Context, userID int64) (model.User, error) {
	if f.userErr != nil {
		return model.User{}, f.userErr
	}
	u, ok := f.byID[userID]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return u, nil
}

func (f *fakeUsers) GetUserByUsername(_ context.Context, username string) (model.User, error) {
	u, ok := f.byName[username]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return u, nil
}

func (f *fakeUsers) GetUsers(_ context.Context, userIDs []int64) (map[int64]model.User, error) {
	result := make(map[int64]model.User, len(userIDs))
	for _, id := range userIDs {
		if u, ok := f.byID[id]; ok {
			result[id] = u
		}
	}
	return result, nil
}

func (f *fakeUsers) GetUserBrief(_ context.Context, userID int64) (model.UserBrief, error) {
	if f.briefErr != nil {
		return model.UserBrief{}, f.briefErr
	}
	b, ok := f.briefs[userID]
	if !ok {
		return model.UserBrief{}, apperr.NotFound("用户不存在")
	}
	return b, nil
}

func (f *fakeUsers) GetUserBriefs(_ context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	result := make(map[int64]model.UserBrief, len(userIDs))
	for _, id := range userIDs {
		if b, ok := f.briefs[id]; ok {
			result[id] = b
		}
	}
	return result, nil
}

func (f *fakeUsers) BriefsByIDs(ctx context.Context, ids []int64) (map[int64]model.UserBrief, error) {
	return f.GetUserBriefs(ctx, ids)
}

func (f *fakeUsers) FindActiveUsersByUsernames(_ context.Context, usernames []string) ([]model.UserBrief, error) {
	result := make([]model.UserBrief, 0, len(usernames))
	for _, name := range usernames {
		if u, ok := f.byName[name]; ok && u.IsActive() {
			result = append(result, f.briefs[u.ID])
		}
	}
	return result, nil
}

func (f *fakeUsers) IsSuspended(_ context.Context, userID int64) (bool, error) {
	u, ok := f.byID[userID]
	if !ok {
		return false, apperr.NotFound("用户不存在")
	}
	return u.IsSuspended(), nil
}

func (f *fakeUsers) ListUserIDsByAIFlag(_ context.Context, isAI bool) ([]int64, error) {
	ids := make([]int64, 0)
	for id, u := range f.byID {
		if u.IsAI == isAI {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (f *fakeUsers) ListAvatarAttachmentIDs(context.Context) ([]int64, error) { return nil, nil }

type fakeRelations struct {
	// following 集合：key 为 [followerID, followeeID]。
	following map[[2]int64]bool
	err       error
}

func newFakeRelations() *fakeRelations { return &fakeRelations{following: map[[2]int64]bool{}} }

func (f *fakeRelations) setFollowing(followerID, followeeID int64, active bool) {
	f.following[[2]int64{followerID, followeeID}] = active
}

func (f *fakeRelations) IsFollowing(_ context.Context, followerID, followeeID int64) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.following[[2]int64{followerID, followeeID}], nil
}

func (f *fakeRelations) FollowingSet(_ context.Context, followerID int64) (map[int64]bool, error) {
	result := map[int64]bool{}
	for key, active := range f.following {
		if active && key[0] == followerID {
			result[key[1]] = true
		}
	}
	return result, nil
}

func (f *fakeRelations) FollowState(context.Context, int64, int64) (string, error) {
	return model.FollowStateNone, nil
}

type fakeConversationRepo struct {
	byID  map[int64]model.DMConversation
	pairs map[[2]int64]int64
}

func newFakeConversationRepo() *fakeConversationRepo {
	return &fakeConversationRepo{byID: map[int64]model.DMConversation{}, pairs: map[[2]int64]int64{}}
}

func pairKey(a, b int64) [2]int64 { return [2]int64{a, b} }

func (f *fakeConversationRepo) Upsert(_ context.Context, conv model.DMConversation) (model.DMConversation, error) {
	if existingID, ok := f.pairs[pairKey(conv.UserAID, conv.UserBID)]; ok {
		return f.byID[existingID], nil
	}
	conv.LastMessageID = nil
	f.byID[conv.ID] = conv
	f.pairs[pairKey(conv.UserAID, conv.UserBID)] = conv.ID
	return conv, nil
}

func (f *fakeConversationRepo) GetByID(_ context.Context, id int64) (model.DMConversation, error) {
	conv, ok := f.byID[id]
	if !ok {
		return model.DMConversation{}, apperr.NotFound("会话不存在")
	}
	return conv, nil
}

func (f *fakeConversationRepo) GetForUpdate(ctx context.Context, id int64) (model.DMConversation, error) {
	return f.GetByID(ctx, id)
}

func (f *fakeConversationRepo) ListByUser(_ context.Context, userID, cursor int64, limit int) ([]model.DMConversation, error) {
	result := make([]model.DMConversation, 0)
	for _, conv := range f.byID {
		if conv.LastMessageID == nil || conv.HiddenFor(userID) {
			continue
		}
		if conv.UserAID != userID && conv.UserBID != userID {
			continue
		}
		if cursor > 0 && *conv.LastMessageID >= cursor {
			continue
		}
		result = append(result, conv)
	}
	sort.Slice(result, func(i, j int) bool { return *result[i].LastMessageID > *result[j].LastMessageID })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (f *fakeConversationRepo) TouchOnSend(_ context.Context, id, messageID int64, at time.Time) error {
	conv, ok := f.byID[id]
	if !ok {
		return apperr.NotFound("会话不存在")
	}
	conv.LastMessageID = &messageID
	conv.UserAHiddenAt = nil
	conv.UserBHiddenAt = nil
	conv.UpdatedAt = at
	f.byID[id] = conv
	return nil
}

func (f *fakeConversationRepo) HideFor(_ context.Context, id, userID int64, at time.Time) (bool, error) {
	conv, ok := f.byID[id]
	if !ok || (conv.UserAID != userID && conv.UserBID != userID) {
		return false, nil
	}
	if conv.UserAID == userID {
		conv.UserAHiddenAt = &at
	} else {
		conv.UserBHiddenAt = &at
	}
	conv.UpdatedAt = at
	f.byID[id] = conv
	return true, nil
}

type fakeMessageRepo struct {
	byID map[int64]model.DMMessage
}

func newFakeMessageRepo() *fakeMessageRepo {
	return &fakeMessageRepo{byID: map[int64]model.DMMessage{}}
}

func (f *fakeMessageRepo) Insert(_ context.Context, msg model.DMMessage) (model.DMMessage, error) {
	f.byID[msg.ID] = msg
	return msg, nil
}

func (f *fakeMessageRepo) GetForUpdate(_ context.Context, id int64) (model.DMMessage, error) {
	msg, ok := f.byID[id]
	if !ok {
		return model.DMMessage{}, apperr.NotFound("消息不存在")
	}
	return msg, nil
}

func (f *fakeMessageRepo) ByIDs(_ context.Context, ids []int64) (map[int64]model.DMMessage, error) {
	result := make(map[int64]model.DMMessage, len(ids))
	for _, id := range ids {
		if msg, ok := f.byID[id]; ok {
			result[id] = msg
		}
	}
	return result, nil
}

func (f *fakeMessageRepo) List(_ context.Context, conversationID, cursor int64, limit int) ([]model.DMMessage, error) {
	messages := f.byConversation(conversationID)
	result := make([]model.DMMessage, 0, len(messages))
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if cursor > 0 && msg.ID >= cursor {
			continue
		}
		result = append(result, msg)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (f *fakeMessageRepo) ListAfter(_ context.Context, conversationID, afterID int64, limit int) ([]model.DMMessage, error) {
	messages := f.byConversation(conversationID)
	result := make([]model.DMMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.ID <= afterID {
			continue
		}
		result = append(result, msg)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (f *fakeMessageRepo) MarkRead(_ context.Context, conversationID, recipientID int64, at time.Time) (int64, error) {
	var updated int64
	for id, msg := range f.byID {
		if msg.ConversationID != conversationID || msg.RecipientID != recipientID ||
			msg.ReadAt != nil || msg.Recalled() {
			continue
		}
		readAt := at
		msg.ReadAt = &readAt
		msg.UpdatedAt = at
		f.byID[id] = msg
		updated++
	}
	return updated, nil
}

func (f *fakeMessageRepo) Recall(_ context.Context, id, senderID int64, at time.Time) (bool, error) {
	msg, ok := f.byID[id]
	if !ok || msg.SenderID != senderID || msg.Recalled() {
		return false, nil
	}
	msg.Status = model.DMMessageStatusRecalled
	msg.UpdatedAt = at
	f.byID[id] = msg
	return true, nil
}

func (f *fakeMessageRepo) UnreadCount(_ context.Context, recipientID int64) (int, error) {
	count := 0
	for _, msg := range f.byID {
		if msg.RecipientID == recipientID && msg.ReadAt == nil && !msg.Recalled() {
			count++
		}
	}
	return count, nil
}

func (f *fakeMessageRepo) UnreadByConversations(_ context.Context, recipientID int64, conversationIDs []int64) (map[int64]int, error) {
	include := make(map[int64]bool, len(conversationIDs))
	for _, id := range conversationIDs {
		include[id] = true
	}
	result := map[int64]int{}
	for _, msg := range f.byID {
		if !include[msg.ConversationID] || msg.RecipientID != recipientID || msg.ReadAt != nil || msg.Recalled() {
			continue
		}
		result[msg.ConversationID]++
	}
	return result, nil
}

func (f *fakeMessageRepo) byConversation(conversationID int64) []model.DMMessage {
	result := make([]model.DMMessage, 0)
	for _, msg := range f.byID {
		if msg.ConversationID == conversationID {
			result = append(result, msg)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
