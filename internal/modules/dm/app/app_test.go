package app_test

import (
	"context"
	"testing"
	"time"

	dmapp "github.com/great-magician-01/timeline/internal/modules/dm/app"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

var testStart = time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)

type harness struct {
	svc           *dmapp.Service
	users         *fakeUsers
	relations     *fakeRelations
	conversations *fakeConversationRepo
	messages      *fakeMessageRepo
	clk           *clock.Fake
	ids           *fakeIDs
	tx            *fakeTx
}

func newHarness(t *testing.T, options ...func(*dmapp.Deps)) *harness {
	t.Helper()
	users := newFakeUsers(
		model.User{ID: 1, Username: "alice", Nickname: "Alice", Status: model.StatusActive, DMPolicy: model.DMPolicyAll},
		model.User{ID: 2, Username: "bob", Nickname: "Bob", Status: model.StatusActive, DMPolicy: model.DMPolicyAll},
		model.User{ID: 3, Username: "carol", Nickname: "Carol", Status: model.StatusActive, DMPolicy: model.DMPolicyAll},
		model.User{ID: 4, Username: "ai-bot", Nickname: "AI", IsAI: true, Status: model.StatusActive, DMPolicy: model.DMPolicyAll},
	)
	clk := clock.NewFake(testStart)
	ids := &fakeIDs{}
	tx := &fakeTx{}
	deps := dmapp.Deps{
		Clock:         clk,
		IDs:           ids,
		Tx:            tx,
		Users:         users,
		Relations:     newFakeRelations(),
		Conversations: newFakeConversationRepo(),
		Messages:      newFakeMessageRepo(),
	}
	for _, option := range options {
		option(&deps)
	}
	h := &harness{
		svc:           dmapp.New(deps),
		users:         users,
		relations:     deps.Relations.(*fakeRelations),
		conversations: deps.Conversations.(*fakeConversationRepo),
		messages:      deps.Messages.(*fakeMessageRepo),
		clk:           clk,
		ids:           ids,
		tx:            tx,
	}
	return h
}

func viewer(id int64) model.Viewer {
	return model.Viewer{ID: id, Authenticated: true}
}

func (h *harness) setPolicy(userID int64, policy string) {
	u := h.users.byID[userID]
	u.DMPolicy = policy
	h.users.byID[userID] = u
	h.users.byName[u.Username] = u
}

func (h *harness) setUser(u model.User) {
	h.users.byID[u.ID] = u
	h.users.byName[u.Username] = u
}

func (h *harness) createConversation(t *testing.T, from int64, username string) dmapp.Conversation {
	t.Helper()
	conv, err := h.svc.CreateOrGetConversation(context.Background(), viewer(from), username)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	return conv
}

func (h *harness) send(t *testing.T, from, conversationID int64, text string) dmapp.Message {
	t.Helper()
	msg, err := h.svc.SendMessage(context.Background(), viewer(from), conversationID, "richtext", "<p>"+text+"</p>")
	if err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}
	return msg
}

func TestCreateOrGetConversationIdempotentAndNormalized(t *testing.T) {
	h := newHarness(t)
	first := h.createConversation(t, 1, "bob")
	if first.Row.UserAID != 1 || first.Row.UserBID != 2 {
		t.Fatalf("会话双方应规范化为 (1, 2)，实际 (%d, %d)", first.Row.UserAID, first.Row.UserBID)
	}
	if first.Peer.ID != 2 || first.Peer.Nickname != "Bob" {
		t.Fatalf("对方摘要不正确: %+v", first.Peer)
	}
	second := h.createConversation(t, 1, "bob")
	if second.Row.ID != first.Row.ID {
		t.Fatalf("重复创建应返回同一会话，实际 %d != %d", second.Row.ID, first.Row.ID)
	}
	// 反向发起同样命中同一会话。
	reversed := h.createConversation(t, 2, "alice")
	if reversed.Row.ID != first.Row.ID {
		t.Fatalf("反向发起应返回同一会话，实际 %d != %d", reversed.Row.ID, first.Row.ID)
	}
	if first.Row.UserAHiddenAt != nil || first.Row.UserBHiddenAt != nil {
		t.Fatal("新会话不应带隐藏标记")
	}
}

func TestCreateConversationRejectsInvalidTargets(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.CreateOrGetConversation(ctx, viewer(1), "alice"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("对自己发起应为 Validation，实际 %v", err)
	}
	if _, err := h.svc.CreateOrGetConversation(ctx, viewer(1), "ai-bot"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("对 AI 发起应为 Forbidden，实际 %v", err)
	}
	if _, err := h.svc.CreateOrGetConversation(ctx, viewer(1), "nobody"); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("不存在的用户应为 NotFound，实际 %v", err)
	}
	h.setPolicy(2, model.DMPolicyNone)
	if _, err := h.svc.CreateOrGetConversation(ctx, viewer(1), "bob"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("对方关闭私信应为 Forbidden，实际 %v", err)
	}
}

func TestCreateConversationFollowPolicies(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.setPolicy(2, model.DMPolicyFollowing)
	if _, err := h.svc.CreateOrGetConversation(ctx, viewer(1), "bob"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("接收者未关注发送者应被拒绝，实际 %v", err)
	}
	h.relations.setFollowing(2, 1, true) // Bob（接收者）关注 Alice（发送者）。
	if _, err := h.svc.CreateOrGetConversation(ctx, viewer(1), "bob"); err != nil {
		t.Fatalf("接收者已关注发送者应放行: %v", err)
	}

	h.setPolicy(3, model.DMPolicyMutual)
	if _, err := h.svc.CreateOrGetConversation(ctx, viewer(1), "carol"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("无关注关系不应满足 mutual，实际 %v", err)
	}
	h.relations.setFollowing(3, 1, true)
	if _, err := h.svc.CreateOrGetConversation(ctx, viewer(1), "carol"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("仅接收者关注不应满足 mutual，实际 %v", err)
	}
	h.relations.setFollowing(1, 3, true)
	if _, err := h.svc.CreateOrGetConversation(ctx, viewer(1), "carol"); err != nil {
		t.Fatalf("双向关注应放行: %v", err)
	}
}

func TestSendMessagePersistsAndTouchesConversation(t *testing.T) {
	h := newHarness(t)
	conv := h.createConversation(t, 1, "bob")
	msg := h.send(t, 1, conv.Row.ID, "你好")
	if msg.Row.SenderID != 1 || msg.Row.RecipientID != 2 || msg.Row.ConversationID != conv.Row.ID {
		t.Fatalf("消息参与方不正确: %+v", msg.Row)
	}
	if msg.Row.Status != model.DMMessageStatusPublished || msg.Row.PlainText != "你好" {
		t.Fatalf("消息状态或纯文本不正确: %+v", msg.Row)
	}
	if !msg.CanRecall {
		t.Fatal("刚发送的消息应可撤回")
	}
	stored := h.conversations.byID[conv.Row.ID]
	if stored.LastMessageID == nil || *stored.LastMessageID != msg.Row.ID {
		t.Fatalf("会话最后消息指针未推进: %+v", stored.LastMessageID)
	}
	if h.tx.calls == 0 {
		t.Fatal("发送必须经过事务")
	}
	unread, err := h.svc.UnreadCount(context.Background(), viewer(2))
	if err != nil || unread != 1 {
		t.Fatalf("B 未读数应为 1，实际 %d, err=%v", unread, err)
	}
}

func TestSendMessageRechecksPolicyInsideTransaction(t *testing.T) {
	h := newHarness(t)
	conv := h.createConversation(t, 1, "bob")
	h.setPolicy(2, model.DMPolicyNone)
	if _, err := h.svc.SendMessage(context.Background(), viewer(1), conv.Row.ID, "richtext", "<p>你好</p>"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("策略收紧后发送应被拒绝，实际 %v", err)
	}
	if len(h.messages.byID) != 0 {
		t.Fatal("被拒绝的发送不得写入消息")
	}
}

func TestSendMessageValidation(t *testing.T) {
	h := newHarness(t)
	conv := h.createConversation(t, 1, "bob")
	ctx := context.Background()
	if _, err := h.svc.SendMessage(ctx, viewer(1), conv.Row.ID, "markdown", "**hi**"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非纯文本格式应为 Validation，实际 %v", err)
	}
	if _, err := h.svc.SendMessage(ctx, viewer(1), conv.Row.ID, "richtext", "<p>  </p>"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("空白内容应为 Validation，实际 %v", err)
	}
	if _, err := h.svc.SendMessage(ctx, viewer(1), conv.Row.ID, "richtext", `<p><img src="/x"></p>`); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("图片内容应为 Validation，实际 %v", err)
	}
	if _, err := h.svc.SendMessage(ctx, viewer(3), conv.Row.ID, "richtext", "<p>hi</p>"); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非成员发送应按不存在处理，实际 %v", err)
	}
}

func TestSendMessageRejectsSuspendedAndAIRecipients(t *testing.T) {
	h := newHarness(t)
	conv := h.createConversation(t, 1, "bob")
	bob := h.users.byID[2]
	bob.Status = model.StatusSuspended
	h.setUser(bob)
	if _, err := h.svc.SendMessage(context.Background(), viewer(1), conv.Row.ID, "richtext", "<p>hi</p>"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("停用接收者应为 Forbidden，实际 %v", err)
	}

	// 直接构造与 AI 的会话行（正常入口不允许创建），发送仍必须拒绝。
	aiConv, err := h.conversations.Upsert(context.Background(), model.DMConversation{
		ID: 999, UserAID: 1, UserBID: 4, CreatedAt: testStart, UpdatedAt: testStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SendMessage(context.Background(), viewer(1), aiConv.ID, "richtext", "<p>hi</p>"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("AI 接收者应为 Forbidden，实际 %v", err)
	}
}

func TestSendMessageRateLimited(t *testing.T) {
	h := newHarness(t, func(deps *dmapp.Deps) {
		deps.MessageLimiter = ratelimit.New(1, time.Minute)
	})
	conv := h.createConversation(t, 1, "bob") // 创建会话消耗唯一一次配额。
	if _, err := h.svc.SendMessage(context.Background(), viewer(1), conv.Row.ID, "richtext", "<p>hi</p>"); !apperr.IsKind(err, apperr.KindRateLimited) {
		t.Fatalf("超出限频应为 RateLimited，实际 %v", err)
	}
}

func TestMessageListAndRecall(t *testing.T) {
	h := newHarness(t)
	conv := h.createConversation(t, 1, "bob")
	first := h.send(t, 1, conv.Row.ID, "第一条")
	second := h.send(t, 2, conv.Row.ID, "第二条")
	ctx := context.Background()

	page, err := h.svc.MessageList(ctx, viewer(1), conv.Row.ID, 0, 0, 20)
	if err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].Row.ID != second.Row.ID || page.Items[1].Row.ID != first.Row.ID {
		t.Fatalf("消息应按 id 倒序返回: %+v", page.Items)
	}
	if !page.Items[1].CanRecall || page.Items[0].CanRecall {
		t.Fatalf("can_recall 计算不正确: %+v", page.Items)
	}
	if _, err := h.svc.MessageList(ctx, viewer(3), conv.Row.ID, 0, 0, 20); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非成员读取应按不存在处理，实际 %v", err)
	}

	// 增量拉取：after_id 只返回其后的消息，升序。
	incremental, err := h.svc.MessageList(ctx, viewer(1), conv.Row.ID, 0, first.Row.ID, 20)
	if err != nil || len(incremental.Items) != 1 || incremental.Items[0].Row.SenderID != 2 {
		t.Fatalf("增量拉取不正确: %+v, err=%v", incremental.Items, err)
	}

	// 窗口内撤回成功；重复撤回幂等。
	recalled, err := h.svc.RecallMessage(ctx, viewer(1), first.Row.ID)
	if err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	if recalled.Row.Status != model.DMMessageStatusRecalled || recalled.CanRecall {
		t.Fatalf("撤回后状态不正确: %+v", recalled)
	}
	if _, err := h.svc.RecallMessage(ctx, viewer(1), first.Row.ID); err != nil {
		t.Fatalf("重复撤回应幂等: %v", err)
	}
	if _, err := h.svc.RecallMessage(ctx, viewer(2), first.Row.ID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非发送者撤回应按不存在处理，实际 %v", err)
	}

	// 超窗撤回被拒绝。
	h.clk.Advance(3 * time.Minute)
	late := h.send(t, 2, conv.Row.ID, "迟到消息")
	h.clk.Advance(3 * time.Minute)
	if _, err := h.svc.RecallMessage(ctx, viewer(2), late.Row.ID); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("超窗撤回应为 Forbidden，实际 %v", err)
	}
}

func TestMarkReadIdempotentAndUnreadCounts(t *testing.T) {
	h := newHarness(t)
	conv := h.createConversation(t, 1, "bob")
	h.send(t, 1, conv.Row.ID, "一")
	h.send(t, 1, conv.Row.ID, "二")
	ctx := context.Background()

	page, err := h.svc.ConversationList(ctx, viewer(2), 0, 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].UnreadCount != 2 {
		t.Fatalf("B 会话列表未读数应为 2: %+v, err=%v", page.Items, err)
	}
	updated, err := h.svc.MarkRead(ctx, viewer(2), conv.Row.ID)
	if err != nil || updated != 2 {
		t.Fatalf("首次已读应更新 2 条，实际 %d, err=%v", updated, err)
	}
	updated, err = h.svc.MarkRead(ctx, viewer(2), conv.Row.ID)
	if err != nil || updated != 0 {
		t.Fatalf("重复已读应更新 0 条，实际 %d, err=%v", updated, err)
	}
	unread, err := h.svc.UnreadCount(ctx, viewer(2))
	if err != nil || unread != 0 {
		t.Fatalf("已读后未读数应为 0，实际 %d, err=%v", unread, err)
	}
	if _, err := h.svc.MarkRead(ctx, viewer(3), conv.Row.ID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非成员已读应按不存在处理，实际 %v", err)
	}

	// 撤回消息不计入未读。
	msg := h.send(t, 1, conv.Row.ID, "三")
	if _, err := h.svc.RecallMessage(ctx, viewer(1), msg.Row.ID); err != nil {
		t.Fatal(err)
	}
	unread, err = h.svc.UnreadCount(ctx, viewer(2))
	if err != nil || unread != 0 {
		t.Fatalf("撤回消息不应计入未读，实际 %d, err=%v", unread, err)
	}
}

func TestHideAndRestoreConversation(t *testing.T) {
	h := newHarness(t)
	conv := h.createConversation(t, 1, "bob")
	h.send(t, 1, conv.Row.ID, "你好")
	ctx := context.Background()

	if err := h.svc.HideConversation(ctx, viewer(2), conv.Row.ID); err != nil {
		t.Fatalf("隐藏会话失败: %v", err)
	}
	if err := h.svc.HideConversation(ctx, viewer(2), conv.Row.ID); err != nil {
		t.Fatalf("重复隐藏应幂等: %v", err)
	}
	page, err := h.svc.ConversationList(ctx, viewer(2), 0, 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("隐藏后 B 列表应为空: %+v, err=%v", page.Items, err)
	}
	// 隐藏会话同时清空该会话未读，避免留下无法清除的徽章。
	if unread, _ := h.svc.UnreadCount(ctx, viewer(2)); unread != 0 {
		t.Fatalf("隐藏后未读数应清零，实际 %d", unread)
	}
	page, err = h.svc.ConversationList(ctx, viewer(1), 0, 20)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("A 列表不应受影响: %+v, err=%v", page.Items, err)
	}

	h.send(t, 1, conv.Row.ID, "新消息")
	page, err = h.svc.ConversationList(ctx, viewer(2), 0, 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].UnreadCount != 1 {
		t.Fatalf("新消息后会话应重新出现: %+v, err=%v", page.Items, err)
	}
	if err := h.svc.HideConversation(ctx, viewer(3), conv.Row.ID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非成员隐藏应按不存在处理，实际 %v", err)
	}
}

func TestConversationListPagination(t *testing.T) {
	h := newHarness(t)
	c1 := h.createConversation(t, 1, "bob")
	c2 := h.createConversation(t, 1, "carol")
	h.send(t, 1, c1.Row.ID, "给 B")
	h.send(t, 1, c2.Row.ID, "给 C")
	first, err := h.svc.ConversationList(context.Background(), viewer(1), 0, 1)
	if err != nil || len(first.Items) != 1 || !first.HasMore {
		t.Fatalf("第一页应返回 1 条且还有更多: %+v, err=%v", first, err)
	}
	if first.Items[0].Row.ID != c2.Row.ID {
		t.Fatalf("应按最后消息倒序，首条应为最新会话")
	}
	second, err := h.svc.ConversationList(context.Background(), viewer(1), first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].Row.ID != c1.Row.ID {
		t.Fatalf("第二页不正确: %+v, err=%v", second.Items, err)
	}
}

func TestUnreadCountExcludesHiddenAndRecalled(t *testing.T) {
	h := newHarness(t)
	conv := h.createConversation(t, 1, "bob")
	msg := h.send(t, 1, conv.Row.ID, "一")
	h.send(t, 1, conv.Row.ID, "二")
	ctx := context.Background()
	if unread, _ := h.svc.UnreadCount(ctx, viewer(2)); unread != 2 {
		t.Fatalf("未读数应为 2，实际 %d", unread)
	}
	if _, err := h.svc.RecallMessage(ctx, viewer(1), msg.Row.ID); err != nil {
		t.Fatal(err)
	}
	if unread, _ := h.svc.UnreadCount(ctx, viewer(2)); unread != 1 {
		t.Fatalf("撤回后未读数应为 1，实际 %d", unread)
	}
}

func TestConversationDetailMemberOnly(t *testing.T) {
	h := newHarness(t)
	conv := h.createConversation(t, 1, "bob")
	h.send(t, 1, conv.Row.ID, "你好")
	ctx := context.Background()
	detail, err := h.svc.ConversationDetail(ctx, viewer(2), conv.Row.ID)
	if err != nil || detail.Peer.ID != 1 || detail.LastMessage == nil {
		t.Fatalf("会话详情不正确: %+v, err=%v", detail, err)
	}
	if _, err := h.svc.ConversationDetail(ctx, viewer(3), conv.Row.ID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非成员详情应按不存在处理，实际 %v", err)
	}
}
