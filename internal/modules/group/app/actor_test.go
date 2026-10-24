package app

import (
	"context"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/richtext"
)

// TestWriteFromAI 覆盖 AI 代发群消息：格式归一（表只接受 richtext）、
// 成员/停用/解散校验、落库与群指针推进。
func TestWriteFromAI(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "ai_bot", true, model.DMPolicyNone)
	group, err := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "代发群", []string{"ai_bot"})
	if err != nil {
		t.Fatalf("建群失败：%v", err)
	}
	gid := group.Row.ID

	t.Run("markdown 输入按 richtext 落库并推进群指针", func(t *testing.T) {
		messageID, err := h.svc.WriteFromAI(context.Background(), contracts.GroupMessageInput{
			GroupID:       gid,
			SenderID:      2,
			ContentFormat: richtext.FormatMarkdown,
			ContentSource: "**大家好**，我是 AI",
		})
		if err != nil {
			t.Fatalf("代发失败：%v", err)
		}
		msg := h.messages.rows[messageID]
		if msg.ContentFormat != richtext.FormatRichText {
			t.Fatalf("落库格式 = %q，期望 richtext（表 CHECK 只接受 richtext）", msg.ContentFormat)
		}
		if msg.SenderID != 2 || msg.Status != model.GroupMessageStatusPublished {
			t.Fatalf("消息行错误：%+v", msg)
		}
		if len(msg.ContentHTML) == 0 || len(msg.PlainText) == 0 {
			t.Fatalf("渲染产物不应为空：%+v", msg)
		}
		got := h.groups.rows[gid]
		if got.LastMessageID == nil || *got.LastMessageID != messageID {
			t.Fatalf("群最后消息指针未推进：%+v", got)
		}
	})

	t.Run("非成员代发按不存在处理", func(t *testing.T) {
		h.addUser(9, "outsider", false, model.DMPolicyAll)
		_, err := h.svc.WriteFromAI(context.Background(), contracts.GroupMessageInput{
			GroupID: gid, SenderID: 9, ContentFormat: richtext.FormatRichText, ContentSource: "<p>hi</p>",
		})
		if !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("非成员应 404（与人类路径一致，不暴露存在性）：%v", err)
		}
	})

	t.Run("停用账号代发拒绝", func(t *testing.T) {
		suspended := model.User{ID: 2, Username: "ai_bot", Status: model.StatusSuspended, IsAI: true}
		h.users.byID[2] = suspended
		defer func() { h.addUser(2, "ai_bot", true, model.DMPolicyNone) }()
		_, err := h.svc.WriteFromAI(context.Background(), contracts.GroupMessageInput{
			GroupID: gid, SenderID: 2, ContentFormat: richtext.FormatRichText, ContentSource: "<p>hi</p>",
		})
		if !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("停用账号应 403：%v", err)
		}
	})

	t.Run("解散群代发拒绝", func(t *testing.T) {
		dissolved := h.groups.rows[gid]
		dissolved.Status = model.GroupStatusDissolved
		h.groups.rows[gid] = dissolved
		defer func() {
			restored := h.groups.rows[gid]
			restored.Status = model.GroupStatusActive
			h.groups.rows[gid] = restored
		}()
		_, err := h.svc.WriteFromAI(context.Background(), contracts.GroupMessageInput{
			GroupID: gid, SenderID: 2, ContentFormat: richtext.FormatRichText, ContentSource: "<p>hi</p>",
		})
		if !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("解散群应 403：%v", err)
		}
	})
}

// TestGroupReaderPorts 覆盖 group app 实现的 GroupReader 三方法。
func TestGroupReaderPorts(t *testing.T) {
	h := newHarness(t)
	h.addUser(1, "alice", false, model.DMPolicyAll)
	h.addUser(2, "bob", false, model.DMPolicyAll)
	group, _ := h.svc.CreateGroup(context.Background(), viewer(1, "alice"), "读取群", []string{"bob"})
	gid := group.Row.ID
	if _, err := h.svc.SendMessage(context.Background(), viewer(1, "alice"), gid, "richtext", "<p>第一条</p>"); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if _, err := h.svc.SendMessage(context.Background(), viewer(2, "bob"), gid, "richtext", "<p>第二条</p>"); err != nil {
		t.Fatalf("发送失败：%v", err)
	}

	// IsMemberActive：成员 true、非成员 false、解散后 false。
	if ok, _ := h.svc.IsMemberActive(context.Background(), gid, 2); !ok {
		t.Fatal("有效成员应返回 true")
	}
	if ok, _ := h.svc.IsMemberActive(context.Background(), gid, 99); ok {
		t.Fatal("非成员应返回 false")
	}

	// Snapshot：群名 + 升序最近消息。
	snapshot, err := h.svc.Snapshot(context.Background(), gid, 0, 10)
	if err != nil {
		t.Fatalf("Snapshot 失败：%v", err)
	}
	if snapshot.Name != "读取群" || !snapshot.Active || len(snapshot.RecentMessages) != 2 {
		t.Fatalf("快照错误：%+v", snapshot)
	}
	if snapshot.RecentMessages[0].PlainText != "第一条" || snapshot.RecentMessages[1].PlainText != "第二条" {
		t.Fatalf("消息应升序：%+v", snapshot.RecentMessages)
	}

	// ListActiveGroupIDs：包含所在群；解散后剔除。
	ids, err := h.svc.ListActiveGroupIDs(context.Background(), 2)
	if err != nil || len(ids) != 1 || ids[0] != gid {
		t.Fatalf("ListActiveGroupIDs = %v, %v", ids, err)
	}
	dissolved := h.groups.rows[gid]
	dissolved.Status = model.GroupStatusDissolved
	h.groups.rows[gid] = dissolved
	if ids, _ = h.svc.ListActiveGroupIDs(context.Background(), 2); len(ids) != 0 {
		t.Fatalf("解散群应剔除：%v", ids)
	}
	if ok, _ := h.svc.IsMemberActive(context.Background(), gid, 2); ok {
		t.Fatal("解散后 IsMemberActive 应为 false")
	}
}
