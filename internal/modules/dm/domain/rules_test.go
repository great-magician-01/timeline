package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/dm/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestNormalizePair(t *testing.T) {
	tests := []struct {
		name         string
		a, b         int64
		wantA, wantB int64
		wantKind     apperr.Kind
	}{
		{name: "已排序", a: 1, b: 2, wantA: 1, wantB: 2},
		{name: "倒序规范化", a: 9, b: 3, wantA: 3, wantB: 9},
		{name: "相同 ID 拒绝", a: 5, b: 5, wantKind: apperr.KindValidation},
		{name: "零值拒绝", a: 0, b: 5, wantKind: apperr.KindValidation},
		{name: "负数拒绝", a: -1, b: 5, wantKind: apperr.KindValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b, err := domain.NormalizePair(tt.a, tt.b)
			if tt.wantKind != "" {
				if !apperr.IsKind(err, tt.wantKind) {
					t.Fatalf("期望错误 kind=%s，实际 %v", tt.wantKind, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
			if a != tt.wantA || b != tt.wantB {
				t.Fatalf("规范化结果 = (%d, %d)，期望 (%d, %d)", a, b, tt.wantA, tt.wantB)
			}
		})
	}
}

func TestPeerID(t *testing.T) {
	conv := model.DMConversation{ID: 1, UserAID: 10, UserBID: 20}
	if peer, err := domain.PeerID(conv, 10); err != nil || peer != 20 {
		t.Fatalf("A 侧对方应为 20，实际 %d, err=%v", peer, err)
	}
	if peer, err := domain.PeerID(conv, 20); err != nil || peer != 10 {
		t.Fatalf("B 侧对方应为 10，实际 %d, err=%v", peer, err)
	}
	if _, err := domain.PeerID(conv, 30); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("非成员应按不存在处理，实际 %v", err)
	}
}

func TestCanSendToPolicyMatrix(t *testing.T) {
	recipient := model.User{ID: 2, Status: model.StatusActive}
	senderID := int64(1)
	tests := []struct {
		name             string
		policy           string
		senderFollows    bool
		recipientFollows bool
		wantKind         apperr.Kind
	}{
		{name: "默认 all 放行", policy: "", senderFollows: false, recipientFollows: false},
		{name: "all 放行", policy: model.DMPolicyAll},
		{name: "following 接收者关注发送者放行", policy: model.DMPolicyFollowing, recipientFollows: true},
		{name: "following 接收者未关注发送者拒绝", policy: model.DMPolicyFollowing, senderFollows: true, wantKind: apperr.KindForbidden},
		{name: "following 仅发送者关注仍拒绝", policy: model.DMPolicyFollowing, senderFollows: true, recipientFollows: false, wantKind: apperr.KindForbidden},
		{name: "mutual 双向放行", policy: model.DMPolicyMutual, senderFollows: true, recipientFollows: true},
		{name: "mutual 单向拒绝", policy: model.DMPolicyMutual, senderFollows: true, recipientFollows: false, wantKind: apperr.KindForbidden},
		{name: "mutual 反向单向拒绝", policy: model.DMPolicyMutual, senderFollows: false, recipientFollows: true, wantKind: apperr.KindForbidden},
		{name: "none 拒绝", policy: model.DMPolicyNone, senderFollows: true, recipientFollows: true, wantKind: apperr.KindForbidden},
		{name: "未知取值失败关闭", policy: "everyone", senderFollows: true, recipientFollows: true, wantKind: apperr.KindForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := recipient
			target.DMPolicy = tt.policy
			err := domain.CanSendTo(target, senderID, tt.senderFollows, tt.recipientFollows)
			if tt.wantKind != "" {
				if !apperr.IsKind(err, tt.wantKind) {
					t.Fatalf("期望 kind=%s，实际 %v", tt.wantKind, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
		})
	}
}

func TestCanSendToRejectsSelfAIAndSuspended(t *testing.T) {
	if err := domain.CanSendTo(model.User{ID: 1, Status: model.StatusActive}, 1, false, false); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("给自己发送应为 Validation，实际 %v", err)
	}
	if err := domain.CanSendTo(model.User{ID: 2, IsAI: true, Status: model.StatusActive}, 1, false, false); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("AI 收件人应为 Forbidden，实际 %v", err)
	}
	if err := domain.CanSendTo(model.User{ID: 2, Status: model.StatusSuspended}, 1, false, false); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("停用收件人应为 Forbidden，实际 %v", err)
	}
}

func TestCanRecall(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	viewer := model.Viewer{ID: 7, Authenticated: true}
	base := model.DMMessage{ID: 1, SenderID: 7, Status: model.DMMessageStatusPublished, CreatedAt: now.Add(-time.Minute)}

	tests := []struct {
		name     string
		msg      model.DMMessage
		viewer   model.Viewer
		wantKind apperr.Kind
	}{
		{name: "窗口内本人撤回", msg: base},
		{name: "窗口边界允许", msg: withCreatedAt(base, now.Add(-domain.RecallWindow))},
		{name: "超窗拒绝", msg: withCreatedAt(base, now.Add(-domain.RecallWindow-time.Second)), wantKind: apperr.KindForbidden},
		{name: "非本人按不存在", msg: base, viewer: model.Viewer{ID: 8, Authenticated: true}, wantKind: apperr.KindNotFound},
		{name: "已撤回幂等", msg: withStatus(base, model.DMMessageStatusRecalled)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := viewer
			if tt.viewer.ID != 0 {
				v = tt.viewer
			}
			err := domain.CanRecall(tt.msg, v, now)
			if tt.wantKind != "" {
				if !apperr.IsKind(err, tt.wantKind) {
					t.Fatalf("期望 kind=%s，实际 %v", tt.wantKind, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
		})
	}
}

func withCreatedAt(msg model.DMMessage, at time.Time) model.DMMessage {
	msg.CreatedAt = at
	return msg
}

func withStatus(msg model.DMMessage, status string) model.DMMessage {
	msg.Status = status
	return msg
}

func TestValidateMessageBody(t *testing.T) {
	if err := domain.ValidateMessageBody("<p>你好</p>", "你好"); err != nil {
		t.Fatalf("正常正文不应报错: %v", err)
	}
	if err := domain.ValidateMessageBody("<p>  </p>", "  "); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("空白正文应为 Validation，实际 %v", err)
	}
	if err := domain.ValidateMessageBody(`<p><img src="/api/v1/attachments/1/content"></p>`, "图片"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("含图片应为 Validation，实际 %v", err)
	}

	atLimit := strings.Repeat("字", domain.MaxMessageRunes)
	if err := domain.ValidateMessageBody("<p>"+atLimit+"</p>", atLimit); err != nil {
		t.Fatalf("达到长度上限的正文不应报错: %v", err)
	}
	if err := domain.ValidateMessageBody("<p>x</p>", atLimit+"！"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("超长正文应为 Validation，实际 %v", err)
	}
}

func TestValidateMessageSource(t *testing.T) {
	if err := domain.ValidateMessageSource(strings.Repeat("字", 100)); err != nil {
		t.Fatalf("正常体积不应报错: %v", err)
	}
	oversize := strings.Repeat("a", domain.MaxMessageSourceBytes+1)
	if err := domain.ValidateMessageSource(oversize); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("超过体积上限的来源应为 Validation，实际 %v", err)
	}
}

func TestPreview(t *testing.T) {
	msg := model.DMMessage{Status: model.DMMessageStatusPublished, PlainText: "  你好\n世界  "}
	if got := domain.Preview(msg); got != "你好\n世界" {
		t.Fatalf("预览应去除首尾空白，实际 %q", got)
	}
	runes := make([]rune, domain.PreviewRunes+10)
	for i := range runes {
		runes[i] = '字'
	}
	msg.PlainText = string(runes)
	got := domain.Preview(msg)
	if want := string(runes[:domain.PreviewRunes]) + "…"; got != want {
		t.Fatalf("截断结果不符：got %q", got)
	}
	recalled := model.DMMessage{Status: model.DMMessageStatusRecalled, PlainText: "原文"}
	if got := domain.Preview(recalled); got != domain.RecalledPreview {
		t.Fatalf("撤回预览应为占位文案，实际 %q", got)
	}
}
