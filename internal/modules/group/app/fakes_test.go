package app

import (
	"context"
	"sort"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// fakeIDs 每次递增的 ID 生成器。
type fakeIDs struct{ next int64 }

func (f *fakeIDs) Next() (int64, error) {
	f.next++
	return f.next, nil
}

// fakeTx 直接执行回调（内存仓储天然一致）。
type fakeTx struct{ calls int }

func (f *fakeTx) WithTx(ctx context.Context, fn func(context.Context) error) error {
	f.calls++
	return fn(ctx)
}

// fakeClock 固定/步进时钟。
type fakeClock struct{ current time.Time }

func (f *fakeClock) Now() time.Time          { return f.current }
func (f *fakeClock) advance(d time.Duration) { f.current = f.current.Add(d) }

// fakeRealtime 记录实时提示信号的发布（contracts.RealtimePublisher）。
// fakeTx 无真实事务，db.AfterCommit 的回调立即执行，因此信号在操作内同步到达。
type fakeRealtime struct {
	groupUsers []int64
	groupIDs   []int64
}

func (f *fakeRealtime) PublishNotification(recipientID int64) {}

func (f *fakeRealtime) PublishDM(userID, conversationID int64) {}

func (f *fakeRealtime) PublishGroup(userID, groupID int64) {
	f.groupUsers = append(f.groupUsers, userID)
	f.groupIDs = append(f.groupIDs, groupID)
}

// fakeAITrigger 记录群 @提及 触发信号（contracts.GroupAITrigger）。
type fakeAITrigger struct {
	signals []contracts.GroupMentionSignal
}

func (f *fakeAITrigger) OnGroupMention(ctx context.Context, s contracts.GroupMentionSignal) error {
	f.signals = append(f.signals, s)
	return nil
}

// fakeUsers 内存版 UserReader（按需实现用例用到的方法）。
type fakeUsers struct {
	byID   map[int64]model.User
	byName map[string]model.User
}

func (f *fakeUsers) GetUser(ctx context.Context, userID int64) (model.User, error) {
	u, ok := f.byID[userID]
	if !ok {
		return model.User{}, errFakeNotFound
	}
	return u, nil
}

func (f *fakeUsers) GetUserByUsername(ctx context.Context, username string) (model.User, error) {
	u, ok := f.byName[username]
	if !ok {
		return model.User{}, errFakeNotFound
	}
	return u, nil
}

func (f *fakeUsers) GetUsers(ctx context.Context, userIDs []int64) (map[int64]model.User, error) {
	out := make(map[int64]model.User, len(userIDs))
	for _, id := range userIDs {
		if u, ok := f.byID[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

func (f *fakeUsers) GetUserBrief(ctx context.Context, userID int64) (model.UserBrief, error) {
	u, ok := f.byID[userID]
	if !ok {
		return model.UserBrief{}, errFakeNotFound
	}
	return model.UserBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname, IsAI: u.IsAI}, nil
}

func (f *fakeUsers) GetUserBriefs(ctx context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	out := make(map[int64]model.UserBrief, len(userIDs))
	for _, id := range userIDs {
		if u, ok := f.byID[id]; ok {
			out[id] = model.UserBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname, IsAI: u.IsAI}
		}
	}
	return out, nil
}

func (f *fakeUsers) BriefsByIDs(ctx context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	return f.GetUserBriefs(ctx, userIDs)
}

func (f *fakeUsers) FindActiveUsersByUsernames(ctx context.Context, usernames []string) ([]model.UserBrief, error) {
	out := make([]model.UserBrief, 0, len(usernames))
	for _, name := range usernames {
		if u, ok := f.byName[name]; ok && !u.IsSuspended() {
			out = append(out, model.UserBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname, IsAI: u.IsAI})
		}
	}
	return out, nil
}

func (f *fakeUsers) IsSuspended(ctx context.Context, userID int64) (bool, error) {
	u, ok := f.byID[userID]
	if !ok {
		return false, errFakeNotFound
	}
	return u.IsSuspended(), nil
}

func (f *fakeUsers) ListUserIDsByAIFlag(ctx context.Context, isAI bool) ([]int64, error) {
	return nil, nil
}

func (f *fakeUsers) ListAvatarAttachmentIDs(ctx context.Context) ([]int64, error) {
	return nil, nil
}

// fakeGroups 内存版 ConversationRepo；members 提供可见性判断（列表谓词依赖成员行）。
type fakeGroups struct {
	rows    map[int64]model.GroupConversation
	members *fakeMembers
}

func (f *fakeGroups) Insert(ctx context.Context, group model.GroupConversation) (model.GroupConversation, error) {
	f.rows[group.ID] = group
	return group, nil
}

func (f *fakeGroups) GetByID(ctx context.Context, id int64) (model.GroupConversation, error) {
	group, ok := f.rows[id]
	if !ok {
		return model.GroupConversation{}, errFakeNotFound
	}
	return group, nil
}

func (f *fakeGroups) GetForUpdate(ctx context.Context, id int64) (model.GroupConversation, error) {
	return f.GetByID(ctx, id)
}

func (f *fakeGroups) ListByUser(ctx context.Context, userID, cursor int64, limit int) ([]model.GroupConversation, error) {
	out := make([]model.GroupConversation, 0)
	for _, g := range f.rows {
		member, ok := f.members.rows[g.ID][userID]
		if !ok || !member.Active() {
			continue
		}
		if g.LastMessageID == nil {
			continue
		}
		// 隐藏谓词：hidden_message_id 非空且群最后消息未越过它 → 不可见。
		if member.HiddenMessageID != nil && g.LastMessageID != nil && *g.LastMessageID <= *member.HiddenMessageID {
			continue
		}
		if cursor > 0 && *g.LastMessageID >= cursor {
			continue
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return *out[i].LastMessageID > *out[j].LastMessageID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeGroups) TouchOnSend(ctx context.Context, id, messageID int64, at time.Time) error {
	group, ok := f.rows[id]
	if !ok {
		return errFakeNotFound
	}
	group.LastMessageID = &messageID
	group.UpdatedAt = at
	f.rows[id] = group
	return nil
}

func (f *fakeGroups) Rename(ctx context.Context, id int64, name string, at time.Time) error {
	group, ok := f.rows[id]
	if !ok {
		return errFakeNotFound
	}
	group.Name = name
	group.UpdatedAt = at
	f.rows[id] = group
	return nil
}

func (f *fakeGroups) Transfer(ctx context.Context, id, newOwnerID, currentOwnerID int64, at time.Time) (bool, error) {
	group, ok := f.rows[id]
	if !ok || group.OwnerID != currentOwnerID || group.Dissolved() {
		return false, nil
	}
	group.OwnerID = newOwnerID
	group.UpdatedAt = at
	f.rows[id] = group
	return true, nil
}

func (f *fakeGroups) Dissolve(ctx context.Context, id int64, at time.Time) (bool, error) {
	group, ok := f.rows[id]
	if !ok || group.Dissolved() {
		return false, nil
	}
	group.Status = model.GroupStatusDissolved
	group.UpdatedAt = at
	f.rows[id] = group
	return true, nil
}

// fakeMembers 内存版 MemberRepo（含 fakeGroups.ListByUser 需要的成员行）。
type fakeMembers struct {
	rows map[int64]map[int64]model.GroupMember // groupID → userID → row
}

func (f *fakeMembers) Insert(ctx context.Context, member model.GroupMember) (model.GroupMember, bool, error) {
	if f.rows[member.GroupID] == nil {
		f.rows[member.GroupID] = make(map[int64]model.GroupMember)
	}
	if _, exists := f.rows[member.GroupID][member.UserID]; exists {
		return model.GroupMember{}, false, nil
	}
	f.rows[member.GroupID][member.UserID] = member
	return member, true, nil
}

func (f *fakeMembers) Rejoin(ctx context.Context, groupID, userID int64, role string, at time.Time) (model.GroupMember, bool, error) {
	member, ok := f.rows[groupID][userID]
	if !ok {
		return model.GroupMember{}, false, nil
	}
	member.Role = role
	member.LeftAt = nil
	member.LastReadMessageID = nil
	member.HiddenMessageID = nil
	member.JoinedAt = at
	f.rows[groupID][userID] = member
	return member, true, nil
}

func (f *fakeMembers) GetByGroupAndUser(ctx context.Context, groupID, userID int64) (model.GroupMember, error) {
	member, ok := f.rows[groupID][userID]
	if !ok {
		return model.GroupMember{}, errFakeNotFound
	}
	return member, nil
}

func (f *fakeMembers) GetForUpdate(ctx context.Context, groupID, userID int64) (model.GroupMember, error) {
	return f.GetByGroupAndUser(ctx, groupID, userID)
}

func (f *fakeMembers) ListByGroup(ctx context.Context, groupID int64, cursorJoinedAt time.Time, cursorID int64, limit int) ([]model.GroupMember, error) {
	out := make([]model.GroupMember, 0)
	for _, m := range f.rows[groupID] {
		if !m.Active() {
			continue
		}
		if cursorID > 0 && (m.JoinedAt.Before(cursorJoinedAt) || (m.JoinedAt.Equal(cursorJoinedAt) && m.ID <= cursorID)) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].JoinedAt.Equal(out[j].JoinedAt) {
			return out[i].JoinedAt.Before(out[j].JoinedAt)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeMembers) MembershipsByUser(ctx context.Context, userID int64) ([]model.GroupMember, error) {
	out := make([]model.GroupMember, 0)
	for _, members := range f.rows {
		if m, ok := members[userID]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeMembers) CountActive(ctx context.Context, groupID int64) (int, error) {
	count := 0
	for _, m := range f.rows[groupID] {
		if m.Active() {
			count++
		}
	}
	return count, nil
}

func (f *fakeMembers) GroupIDsByUser(ctx context.Context, userID int64) ([]int64, error) {
	out := make([]int64, 0)
	for groupID, members := range f.rows {
		if m, ok := members[userID]; ok && m.Active() {
			out = append(out, groupID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (f *fakeMembers) OwnedGroupCount(ctx context.Context, userID int64) (int, error) {
	// 由测试直接注入（ fakeGroups 与角色行分离时使用）；这里按 owner 角色行统计。
	count := 0
	for _, members := range f.rows {
		if m, ok := members[userID]; ok && m.Active() && m.IsOwner() {
			count++
		}
	}
	return count, nil
}

func (f *fakeMembers) Leave(ctx context.Context, groupID, userID int64, at time.Time) (bool, error) {
	member, ok := f.rows[groupID][userID]
	if !ok || !member.Active() {
		return false, nil
	}
	member.LeftAt = &at
	f.rows[groupID][userID] = member
	return true, nil
}

func (f *fakeMembers) SetRole(ctx context.Context, groupID, userID int64, role string) error {
	member, ok := f.rows[groupID][userID]
	if !ok {
		return errFakeNotFound
	}
	member.Role = role
	f.rows[groupID][userID] = member
	return nil
}

func (f *fakeMembers) MarkRead(ctx context.Context, groupID, userID, messageID int64) (bool, error) {
	member, ok := f.rows[groupID][userID]
	if !ok || !member.Active() {
		return false, nil
	}
	if member.LastReadMessageID != nil && *member.LastReadMessageID >= messageID {
		return false, nil
	}
	member.LastReadMessageID = &messageID
	f.rows[groupID][userID] = member
	return true, nil
}

func (f *fakeMembers) Hide(ctx context.Context, groupID, userID, messageID int64) error {
	member, ok := f.rows[groupID][userID]
	if !ok {
		return errFakeNotFound
	}
	member.HiddenMessageID = &messageID
	f.rows[groupID][userID] = member
	return nil
}

// fakeMessages 内存版 MessageRepo；members 提供成员游标（未读统计依赖）。
type fakeMessages struct {
	rows    map[int64]model.GroupMessage // id → row
	members *fakeMembers
}

func (f *fakeMessages) Insert(ctx context.Context, msg model.GroupMessage) (model.GroupMessage, error) {
	f.rows[msg.ID] = msg
	return msg, nil
}

func (f *fakeMessages) GetForUpdate(ctx context.Context, id int64) (model.GroupMessage, error) {
	msg, ok := f.rows[id]
	if !ok {
		return model.GroupMessage{}, errFakeNotFound
	}
	return msg, nil
}

func (f *fakeMessages) ByIDs(ctx context.Context, ids []int64) (map[int64]model.GroupMessage, error) {
	out := make(map[int64]model.GroupMessage, len(ids))
	for _, id := range ids {
		if msg, ok := f.rows[id]; ok {
			out[id] = msg
		}
	}
	return out, nil
}

func (f *fakeMessages) List(ctx context.Context, groupID, cursor int64, limit int) ([]model.GroupMessage, error) {
	out := make([]model.GroupMessage, 0)
	for _, m := range f.rows {
		if m.GroupID != groupID {
			continue
		}
		if cursor > 0 && m.ID >= cursor {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeMessages) ListAfter(ctx context.Context, groupID, afterID int64, limit int) ([]model.GroupMessage, error) {
	out := make([]model.GroupMessage, 0)
	for _, m := range f.rows {
		if m.GroupID != groupID || m.ID <= afterID {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeMessages) ListBefore(ctx context.Context, groupID, beforeID int64, limit int) ([]model.GroupMessage, error) {
	out := make([]model.GroupMessage, 0)
	for _, m := range f.rows {
		if m.GroupID != groupID || (beforeID > 0 && m.ID >= beforeID) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeMessages) Recall(ctx context.Context, id, senderID int64, at time.Time) (bool, error) {
	msg, ok := f.rows[id]
	if !ok || msg.SenderID != senderID || msg.Recalled() {
		return false, nil
	}
	msg.Status = model.GroupMessageStatusRecalled
	msg.UpdatedAt = at
	f.rows[id] = msg
	return true, nil
}

func (f *fakeMessages) UnreadCount(ctx context.Context, groupID, userID int64, lastReadMessageID *int64) (int, error) {
	cursor := int64(0)
	if lastReadMessageID != nil {
		cursor = *lastReadMessageID
	}
	count := 0
	for _, m := range f.rows {
		if m.GroupID == groupID && m.SenderID != userID && !m.Recalled() && m.ID > cursor {
			count++
		}
	}
	return count, nil
}

func (f *fakeMessages) UnreadByGroups(ctx context.Context, userID int64) (map[int64]int, error) {
	out := make(map[int64]int)
	for groupID, members := range f.members.rows {
		member, ok := members[userID]
		if !ok || !member.Active() {
			continue
		}
		cursor := int64(0)
		if member.LastReadMessageID != nil {
			cursor = *member.LastReadMessageID
		}
		count := 0
		for _, m := range f.rows {
			if m.GroupID == groupID && m.SenderID != userID && !m.Recalled() && m.ID > cursor {
				count++
			}
		}
		if count > 0 {
			out[groupID] = count
		}
	}
	return out, nil
}
