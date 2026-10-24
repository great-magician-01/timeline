package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"正常", " 项目交流群 ", "项目交流群", false},
		{"空白拒绝", "   ", "", true},
		{"空拒绝", "", "", true},
		{"正好上限", strings.Repeat("群", MaxNameRunes), strings.Repeat("群", MaxNameRunes), false},
		{"超长拒绝", strings.Repeat("群", MaxNameRunes+1), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateName(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateName(%q) err=%v，期望出错=%v", tc.input, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("ValidateName(%q) = %q，期望 %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCanBeInvited(t *testing.T) {
	now := time.Now()
	active := model.User{ID: 1, Status: model.StatusActive, DMPolicy: model.DMPolicyAll}
	closed := model.User{ID: 2, Status: model.StatusActive, DMPolicy: model.DMPolicyNone}
	suspended := model.User{ID: 3, Status: model.StatusSuspended, SuspendedAt: &now, DMPolicy: model.DMPolicyAll}
	aiClosed := model.User{ID: 4, IsAI: true, Status: model.StatusActive, DMPolicy: model.DMPolicyNone}

	if err := CanBeInvited(active); err != nil {
		t.Fatalf("普通用户应可被拉入：%v", err)
	}
	if !apperr.IsKind(CanBeInvited(closed), apperr.KindForbidden) {
		t.Fatalf("关闭私信用户应拒绝拉入：%v", CanBeInvited(closed))
	}
	if !apperr.IsKind(CanBeInvited(suspended), apperr.KindForbidden) {
		t.Fatalf("停用账号应拒绝拉入：%v", CanBeInvited(suspended))
	}
	if err := CanBeInvited(aiClosed); err != nil {
		t.Fatalf("AI 账号不受 dm_policy 约束，应可被拉入：%v", err)
	}
}

func TestRequireOwnerAndMember(t *testing.T) {
	group := model.GroupConversation{ID: 10, OwnerID: 1, Status: model.GroupStatusActive}
	owner := model.GroupMember{GroupID: 10, UserID: 1, Role: model.GroupRoleOwner}
	member := model.GroupMember{GroupID: 10, UserID: 2, Role: model.GroupRoleMember}
	left := model.GroupMember{GroupID: 10, UserID: 3, LeftAt: &time.Time{}}

	if err := RequireOwner(group, owner, 1); err != nil {
		t.Fatalf("群主校验应通过：%v", err)
	}
	if err := RequireOwner(group, member, 2); err == nil {
		t.Fatal("普通成员不应通过群主校验")
	}
	if err := RequireMember(member, 2); err != nil {
		t.Fatalf("成员校验应通过：%v", err)
	}
	if !apperr.IsKind(RequireMember(left, 3), apperr.KindNotFound) {
		t.Fatalf("已退出成员应按不存在处理：%v", RequireMember(left, 3))
	}
	if !apperr.IsKind(RequireMember(member, 99), apperr.KindNotFound) {
		t.Fatalf("非成员应按不存在处理：%v", RequireMember(member, 99))
	}
}

func TestCanSendMessage(t *testing.T) {
	active := model.GroupConversation{ID: 10, OwnerID: 1, Status: model.GroupStatusActive}
	dissolved := model.GroupConversation{ID: 10, OwnerID: 1, Status: model.GroupStatusDissolved}
	member := model.GroupMember{GroupID: 10, UserID: 2, Role: model.GroupRoleMember}

	if err := CanSendMessage(active, member, 2); err != nil {
		t.Fatalf("有效成员应可发言：%v", err)
	}
	if !apperr.IsKind(CanSendMessage(dissolved, member, 2), apperr.KindForbidden) {
		t.Fatalf("解散群应拒绝发言：%v", CanSendMessage(dissolved, member, 2))
	}
}

func TestCanRecall(t *testing.T) {
	now := time.Now()
	msg := model.GroupMessage{ID: 1, GroupID: 10, SenderID: 2, Status: model.GroupMessageStatusPublished, CreatedAt: now.Add(-time.Minute)}
	expired := model.GroupMessage{ID: 2, GroupID: 10, SenderID: 2, Status: model.GroupMessageStatusPublished, CreatedAt: now.Add(-3 * time.Minute)}
	recalled := model.GroupMessage{ID: 3, GroupID: 10, SenderID: 2, Status: model.GroupMessageStatusRecalled, CreatedAt: now.Add(-3 * time.Minute)}

	if err := CanRecall(msg, 2, now); err != nil {
		t.Fatalf("窗口内应可撤回：%v", err)
	}
	if !apperr.IsKind(CanRecall(expired, 2, now), apperr.KindForbidden) {
		t.Fatalf("超窗应拒绝撤回：%v", CanRecall(expired, 2, now))
	}
	if err := CanRecall(recalled, 2, now); err != nil {
		t.Fatalf("重复撤回应幂等成功：%v", err)
	}
	if !apperr.IsKind(CanRecall(msg, 3, now), apperr.KindNotFound) {
		t.Fatalf("非发送者应按不存在处理：%v", CanRecall(msg, 3, now))
	}
}

func TestCanRemoveAndLeave(t *testing.T) {
	owner := model.GroupMember{GroupID: 10, UserID: 1, Role: model.GroupRoleOwner}
	member := model.GroupMember{GroupID: 10, UserID: 2, Role: model.GroupRoleMember}
	left := model.GroupMember{GroupID: 10, UserID: 3, LeftAt: &time.Time{}}

	if err := CanRemove(member); err != nil {
		t.Fatalf("普通成员应可被移除：%v", err)
	}
	if CanRemove(owner) == nil {
		t.Fatal("不能移除群主")
	}
	if !apperr.IsKind(CanRemove(left), apperr.KindNotFound) {
		t.Fatalf("已退出成员应按不存在处理：%v", CanRemove(left))
	}
	if RequireTransferBeforeLeave(member) != nil {
		t.Fatal("普通成员可直接退出")
	}
	if RequireTransferBeforeLeave(owner) == nil {
		t.Fatal("群主退出前必须转让或解散")
	}
}
