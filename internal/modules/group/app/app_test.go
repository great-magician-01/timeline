package app

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/group/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/chat"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// errFakeNotFound 是 fake 仓储的统一不存在错误（apperr not_found）。
var errFakeNotFound = apperr.NotFound("不存在")

// harness 组装被测服务与全部内存桩。
type harness struct {
	svc      *Service
	users    *fakeUsers
	groups   *fakeGroups
	members  *fakeMembers
	messages *fakeMessages
	realtime *fakeRealtime
	ai       *fakeAITrigger
	clock    *fakeClock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	users := &fakeUsers{byID: map[int64]model.User{}, byName: map[string]model.User{}}
	members := &fakeMembers{rows: map[int64]map[int64]model.GroupMember{}}
	groups := &fakeGroups{rows: map[int64]model.GroupConversation{}, members: members}
	messages := &fakeMessages{rows: map[int64]model.GroupMessage{}, members: members}
	realtime := &fakeRealtime{}
	ai := &fakeAITrigger{}
	clock := &fakeClock{current: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	svc := New(Deps{
		Clock:     clock,
		IDs:       &fakeIDs{next: 1000},
		Tx:        &fakeTx{},
		Users:     users,
		Groups:    groups,
		Members:   members,
		Messages:  messages,
		Realtime:  realtime,
		AITrigger: ai,
	})
	return &harness{svc: svc, users: users, groups: groups, members: members, messages: messages, realtime: realtime, ai: ai, clock: clock}
}

func (h *harness) addUser(id int64, username string, isAI bool, policy string) model.User {
	u := model.User{ID: id, Username: username, Status: model.StatusActive, DMPolicy: policy, IsAI: isAI}
	h.users.byID[id] = u
	h.users.byName[username] = u
	return u
}

func viewer(id int64, _ string) model.Viewer {
	return model.Viewer{ID: id, Authenticated: true}
}

func TestCreateGroup(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	h.addUser(3, "carol", false, model.DMPolicyNone) // 关闭私信
	h.addUser(4, "ai-bot", true, model.DMPolicyNone) // AI 不受 dm_policy 约束

	group, err := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), " 项目群 ", []string{"bob", "ai-bot", "bob"})
	if err != nil {
		t.Fatalf("建群失败：%v", err)
	}
	if group.Row.Name != "项目群" || group.Role != model.GroupRoleOwner {
		t.Fatalf("群视图 = %+v", group)
	}
	if group.MemberCount != 3 {
		t.Fatalf("成员数 = %d，期望 3（群主+bob+AI）", group.MemberCount)
	}

	// 拉入 dm_policy=none 的用户被拒绝，整个建群失败。
	if _, err := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "第二群", []string{"carol"}); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("关闭私信用户应拒绝建群：%v", err)
	}
	// 群主名额累计。
	for i := 0; i < domain.MaxOwnedGroups-1; i++ {
		if _, err := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "填充群", nil); err != nil {
			t.Fatalf("第 %d 次建群失败：%v", i+2, err)
		}
	}
	if _, err := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "超限群", nil); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("超出群主名额应 409：%v", err)
	}
}

func TestInvitePolicyMatrix(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	h.addUser(3, "carol", false, model.DMPolicyNone)
	h.addUser(4, "ai-bot", true, model.DMPolicyNone)
	now := time.Now()
	suspended := model.User{ID: 5, Username: "suspended", Status: model.StatusSuspended, SuspendedAt: &now, DMPolicy: model.DMPolicyAll}
	h.users.byID[5] = suspended
	h.users.byName["suspended"] = suspended

	group, err := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "策略群", nil)
	if err != nil {
		t.Fatalf("建群失败：%v", err)
	}
	gid := group.Row.ID

	items, err := h.svc.Invite(context.Background(), viewer(1, "alice"), gid, []string{"bob", "ai-bot"})
	if err != nil {
		t.Fatalf("拉人失败：%v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应新增 2 名成员，得到 %d", len(items))
	}
	// 返回项必须是完整成员行（id/role/joined_at），而不是只有摘要。
	for _, item := range items {
		if item.Row.ID == 0 || item.Row.UserID == 0 || item.Row.Role != model.GroupRoleMember || item.Row.JoinedAt.IsZero() {
			t.Fatalf("拉人返回应含完整成员行：%+v", item.Row)
		}
	}
	// 重复拉同一人幂等（不重复返回）。
	items, err = h.svc.Invite(context.Background(), viewer(1, "alice"), gid, []string{"bob"})
	if err != nil || len(items) != 0 {
		t.Fatalf("重复拉人应幂等空返回：items=%d err=%v", len(items), err)
	}
	if _, err := h.svc.Invite(context.Background(), viewer(1, "alice"), gid, []string{"carol"}); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("关闭私信用户应拒绝拉入：%v", err)
	}
	if _, err := h.svc.Invite(context.Background(), viewer(1, "alice"), gid, []string{"suspended"}); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("停用账号应拒绝拉入：%v", err)
	}
	if _, err := h.svc.Invite(context.Background(), viewer(1, "alice"), gid, []string{"ghost"}); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("不存在用户应 404：%v", err)
	}
	// 非群主拉人被拒（权限不足）。
	if _, err := h.svc.Invite(context.Background(), viewer(2, "bob"), gid, []string{"carol"}); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("非群主拉人应被拒绝：%v", err)
	}
}

func TestSendMessageAndUnread(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	h.addUser(3, "carol", false, model.DMPolicyAll)
	group, err := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "消息群", []string{"bob", "carol"})
	if err != nil {
		t.Fatalf("建群失败：%v", err)
	}
	gid := group.Row.ID

	msg, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>大家好</p>")
	if err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if msg.Row.PlainText != "大家好" || !msg.CanRecall {
		t.Fatalf("消息视图 = %+v", msg)
	}

	// 未读：bob/carol 各 1，alice 自己 0。
	bob, err := h.svc.GroupDetail(context.Background(), viewer(2, "bob"), gid)
	if err != nil {
		t.Fatalf("bob 读群详情失败：%v", err)
	}
	if bob.UnreadCount != 1 {
		t.Fatalf("bob 未读 = %d，期望 1", bob.UnreadCount)
	}
	total, err := h.svc.UnreadCount(context.Background(), viewer(2, "bob"))
	if err != nil || total != 1 {
		t.Fatalf("bob 未读总数 = %d, %v", total, err)
	}

	// SSE 信号：全部成员各收到一次。
	if len(h.realtime.groupUsers) != 3 {
		t.Fatalf("信号用户 = %v，期望 3 人", h.realtime.groupUsers)
	}
	for _, gid := range h.realtime.groupIDs {
		if gid != gid {
			t.Fatal("信号群 ID 不一致")
		}
	}

	// 已读：bob 推进游标后未读清零。
	if changed, err := h.svc.MarkRead(context.Background(), viewer(2, "bob"), gid); err != nil || !changed {
		t.Fatalf("已读失败：changed=%v err=%v", changed, err)
	}
	if changed, _ := h.svc.MarkRead(context.Background(), viewer(2, "bob"), gid); changed {
		t.Fatal("重复已读应幂等 false")
	}
	bob, _ = h.svc.GroupDetail(context.Background(), viewer(2, "bob"), gid)
	if bob.UnreadCount != 0 {
		t.Fatalf("已读后未读 = %d，期望 0", bob.UnreadCount)
	}

	// 非成员发送被拒（按不存在处理）。
	h.addUser(9, "mallory", false, model.DMPolicyAll)
	if _, err := h.svc.SendMessage(context.Background(), viewer(9, "mallory"), gid, "richtext", "<p>渗透</p>"); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非成员发送应 404：%v", err)
	}
}

func TestRecallWindow(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "撤回群", []string{"bob"})
	gid := group.Row.ID

	msg, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>发错人</p>")
	if err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	// bob 不能撤回别人的消息。
	if _, err := h.svc.RecallMessage(context.Background(), viewer(2, "bob"), msg.Row.ID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非发送者撤回应 404：%v", err)
	}
	// 窗口内可撤回。
	recalled, err := h.svc.RecallMessage(context.Background(), viewer(1, "alice"), msg.Row.ID)
	if err != nil || !recalled.Row.Recalled() {
		t.Fatalf("撤回失败：%v", err)
	}
	// 重复撤回幂等。
	if _, err := h.svc.RecallMessage(context.Background(), viewer(1, "alice"), msg.Row.ID); err != nil {
		t.Fatalf("重复撤回应幂等：%v", err)
	}
	// 超窗拒绝。
	msg2, _ := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>再试</p>")
	h.clock.advance(chat.RecallWindow + time.Second)
	if _, err := h.svc.RecallMessage(context.Background(), viewer(1, "alice"), msg2.Row.ID); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("超窗撤回应 403：%v", err)
	}
}

func TestDissolvedGroupReadOnly(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "解散群", []string{"bob"})
	gid := group.Row.ID
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>最后发言</p>"); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	if err := h.svc.Dissolve(context.Background(), viewer(1, "alice"), gid); err != nil {
		t.Fatalf("解散失败：%v", err)
	}
	if err := h.svc.Dissolve(context.Background(), viewer(1, "alice"), gid); err != nil {
		t.Fatalf("重复解散应幂等：%v", err)
	}
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>还想说</p>"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("解散后发送应 403：%v", err)
	}
	if _, err := h.svc.Invite(context.Background(), viewer(1, "alice"), gid, []string{"bob"}); err == nil {
		t.Fatal("解散后拉人应被拒绝")
	}
	// 解散后成员保留只读访问。
	if _, err := h.svc.MessageList(context.Background(), viewer(2, "bob"), gid, 0, 0, 20); err != nil {
		t.Fatalf("解散后成员应可读历史：%v", err)
	}

	// 解散群可隐藏：列表消失、未读清零且不会重新出现（不会再有新消息）。
	if unread, _ := h.svc.UnreadCount(context.Background(), viewer(2, "bob")); unread != 1 {
		t.Fatalf("隐藏前解散群未读应为 1，实际 %d", unread)
	}
	if err := h.svc.HideGroup(context.Background(), viewer(2, "bob"), gid); err != nil {
		t.Fatalf("解散群应可隐藏：%v", err)
	}
	page, err := h.svc.GroupList(context.Background(), viewer(2, "bob"), 0, 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("隐藏后解散群应从列表消失：items=%d err=%v", len(page.Items), err)
	}
	if unread, _ := h.svc.UnreadCount(context.Background(), viewer(2, "bob")); unread != 0 {
		t.Fatalf("隐藏后解散群未读应清零，实际 %d", unread)
	}
	// 隐藏后详情仍可只读访问（直接链接进入）。
	if _, err := h.svc.GroupDetail(context.Background(), viewer(2, "bob"), gid); err != nil {
		t.Fatalf("隐藏后解散群详情应仍可读：%v", err)
	}
}

func TestTransferAndLeave(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "转让群", []string{"bob"})
	gid := group.Row.ID

	// 群主不能直接退出。
	if err := h.svc.Leave(context.Background(), viewer(1, "alice"), gid); err == nil {
		t.Fatal("群主退出应被拒绝")
	}
	// bob 退出后可被重新拉回。
	if err := h.svc.Leave(context.Background(), viewer(2, "bob"), gid); err != nil {
		t.Fatalf("成员退出失败：%v", err)
	}
	if _, err := h.svc.Invite(context.Background(), viewer(1, "alice"), gid, []string{"bob"}); err != nil {
		t.Fatalf("重新拉回应成功：%v", err)
	}
	// 转让：新群主必须是在群成员；原群主降级。
	if err := h.svc.TransferOwner(context.Background(), viewer(1, "alice"), gid, 2); err != nil {
		t.Fatalf("转让失败：%v", err)
	}
	detail, err := h.svc.GroupDetail(context.Background(), viewer(1, "alice"), gid)
	if err != nil {
		t.Fatalf("读详情失败：%v", err)
	}
	if detail.Role != model.GroupRoleMember || detail.Row.OwnerID != 2 {
		t.Fatalf("转让后视图 role=%s owner=%d", detail.Role, detail.Row.OwnerID)
	}
	// 原群主现在是普通成员，可退出。
	if err := h.svc.Leave(context.Background(), viewer(1, "alice"), gid); err != nil {
		t.Fatalf("原群主退出失败：%v", err)
	}
	// 新群主（bob）现在可以改名。
	if _, err := h.svc.Rename(context.Background(), viewer(2, "bob"), gid, "新名字群"); err != nil {
		t.Fatalf("新群主改名失败：%v", err)
	}
}

func TestRemoveMember(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "踢人群", []string{"bob"})
	gid := group.Row.ID

	// 不能踢自己（群主）。
	if err := h.svc.RemoveMember(context.Background(), viewer(1, "alice"), gid, 1); err == nil {
		t.Fatal("移除群主应被拒绝")
	}
	if err := h.svc.RemoveMember(context.Background(), viewer(1, "alice"), gid, 2); err != nil {
		t.Fatalf("踢人失败：%v", err)
	}
	// bob 被踢后读不到群。
	if _, err := h.svc.GroupDetail(context.Background(), viewer(2, "bob"), gid); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("被踢后应 404：%v", err)
	}
	// 非群主不能踢人。
	if err := h.svc.RemoveMember(context.Background(), viewer(2, "bob"), gid, 1); err == nil {
		t.Fatal("非群主踢人应被拒绝")
	}
}

func TestHideAndReappear(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "隐藏群", []string{"bob"})
	gid := group.Row.ID
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>第一条</p>"); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	if err := h.svc.HideGroup(context.Background(), viewer(2, "bob"), gid); err != nil {
		t.Fatalf("隐藏失败：%v", err)
	}
	page, err := h.svc.GroupList(context.Background(), viewer(2, "bob"), 0, 20)
	if err != nil {
		t.Fatalf("读列表失败：%v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("隐藏后列表应为空，得到 %d", len(page.Items))
	}
	// 新消息到达后自动重新出现且未读。
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>第二条</p>"); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	page, err = h.svc.GroupList(context.Background(), viewer(2, "bob"), 0, 20)
	if err != nil {
		t.Fatalf("读列表失败：%v", err)
	}
	if len(page.Items) != 1 || page.Items[0].UnreadCount != 1 {
		t.Fatalf("新消息后应重新出现，items=%d", len(page.Items))
	}
}

func TestMentionTriggersAI(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	h.addUser(3, "ai_bot", true, model.DMPolicyNone) // AI 成员
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "AI群", []string{"ai_bot"})
	gid := group.Row.ID

	// @ AI 成员：触发一次信号。
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>@ai_bot 你怎么看</p>"); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if len(h.ai.signals) != 1 {
		t.Fatalf("AI 触发信号 = %d，期望 1", len(h.ai.signals))
	}
	sig := h.ai.signals[0]
	if sig.AIID != 3 || sig.GroupID != gid || sig.MentionedBy != 1 {
		t.Fatalf("信号内容 = %+v", sig)
	}

	// @ 非成员（未提及任何 AI）：不触发。
	h.addUser(4, "outsider_ai", true, model.DMPolicyNone)
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>@outsider_ai 在吗</p>"); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if len(h.ai.signals) != 1 {
		t.Fatalf("非成员 AI 不应触发，信号 = %d", len(h.ai.signals))
	}

	// 无 @：不触发。
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>普通消息</p>"); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if len(h.ai.signals) != 1 {
		t.Fatalf("普通消息不应触发，信号 = %d", len(h.ai.signals))
	}
}

func TestMessageBodyLimits(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "校验群", nil)
	gid := group.Row.ID

	// 图片拒绝。
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", `<p><img src="x"></p>`); err == nil {
		t.Fatal("图片应拒绝")
	}
	// 空白拒绝。
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>  </p>"); err == nil {
		t.Fatal("空白应拒绝")
	}
	// 超长拒绝。
	source := "<p>"
	for i := 0; i < 2001; i++ {
		source += "字"
	}
	source += "</p>"
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", source); err == nil {
		t.Fatal("超长应拒绝")
	}
	// 非 richtext 格式拒绝。
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "markdown", "# 标题"); err == nil {
		t.Fatal("非 richtext 应拒绝")
	}
}

func TestMessageListPagingAndIncrement(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "分页群", nil)
	gid := group.Row.ID

	var lastID int64
	for i := 0; i < 5; i++ {
		msg, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>消息</p>")
		if err != nil {
			t.Fatalf("发送失败：%v", err)
		}
		lastID = msg.Row.ID
	}

	// 翻旧消息：最新 2 条。
	page, err := h.svc.MessageList(context.Background(), viewer(1, "alice"), gid, 0, 0, 2)
	if err != nil || len(page.Items) != 2 || !page.HasMore {
		t.Fatalf("首页 = %d 条 hasMore=%v err=%v", len(page.Items), page.HasMore, err)
	}
	if page.Items[0].Row.ID != lastID {
		t.Fatal("首页应从最新开始")
	}
	// 增量拉新：after 最后一页游标之后无新消息。
	after, err := h.svc.MessageList(context.Background(), viewer(1, "alice"), gid, 0, lastID, 20)
	if err != nil || len(after.Items) != 0 {
		t.Fatalf("增量应为空：%d %v", len(after.Items), err)
	}
}

func TestGroupListCursor(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	// 建 3 个群，每个群各发一条消息（产生 last_message_id 排序键）。
	var ids []int64
	for i := 0; i < 3; i++ {
		group, err := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "列表群", nil)
		if err != nil {
			t.Fatalf("建群失败：%v", err)
		}
		if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), group.Row.ID, "richtext", "<p>激活</p>"); err != nil {
			t.Fatalf("发送失败：%v", err)
		}
		ids = append(ids, group.Row.ID)
	}
	// 空群（无消息）不进列表。
	if _, err := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "空群", nil); err != nil {
		t.Fatalf("建空群失败：%v", err)
	}
	first, err := h.svc.GroupList(context.Background(), viewer(1, "alice"), 0, 2)
	if err != nil || len(first.Items) != 2 || !first.HasMore {
		t.Fatalf("首页 = %d hasMore=%v err=%v", len(first.Items), first.HasMore, err)
	}
	second, err := h.svc.GroupList(context.Background(), viewer(1, "alice"), first.NextCursor, 2)
	if err != nil || len(second.Items) != 1 || second.HasMore {
		t.Fatalf("次页 = %d hasMore=%v err=%v", len(second.Items), second.HasMore, err)
	}
	// 最后一个群是最新发送的（倒序），与首页不同。
	if second.Items[0].Row.ID == first.Items[0].Row.ID {
		t.Fatal("次页群不应与首页重复")
	}
}

func TestRealtimeSignalsOnMembershipChanges(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	h.addUser(3, "carol", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "信号群", []string{"bob"})
	gid := group.Row.ID

	h.realtime.groupUsers = nil
	// 踢人：被踢者与剩余成员都收信号。
	if err := h.svc.RemoveMember(context.Background(), viewer(1, "alice"), gid, 2); err != nil {
		t.Fatalf("踢人失败：%v", err)
	}
	if len(h.realtime.groupUsers) != 2 {
		t.Fatalf("踢人信号 = %v", h.realtime.groupUsers)
	}
	// 拉人：全员收信号。
	h.realtime.groupUsers = nil
	if _, err := h.svc.Invite(context.Background(), viewer(1, "alice"), gid, []string{"carol"}); err != nil {
		t.Fatalf("拉人失败：%v", err)
	}
	if len(h.realtime.groupUsers) != 2 {
		t.Fatalf("拉人信号 = %v", h.realtime.groupUsers)
	}
	// 解散：全员收信号。
	h.realtime.groupUsers = nil
	if err := h.svc.Dissolve(context.Background(), viewer(1, "alice"), gid); err != nil {
		t.Fatalf("解散失败：%v", err)
	}
	if len(h.realtime.groupUsers) != 2 {
		t.Fatalf("解散信号 = %v", h.realtime.groupUsers)
	}
}

func TestWriteLimit(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "限频群", nil)
	gid := group.Row.ID

	var limited bool
	for i := 0; i < 61; i++ {
		_, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>刷屏</p>")
		if apperr.IsKind(err, apperr.KindRateLimited) {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("第 61 次发送应触发限频")
	}
}
