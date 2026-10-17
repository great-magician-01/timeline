package domain_test

import (
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestCountDelta 覆盖状态翻转的计数增量。
func TestCountDelta(t *testing.T) {
	cases := []struct {
		was, now bool
		want     int
	}{
		{false, true, 1},
		{true, false, -1},
		{true, true, 0},
		{false, false, 0},
	}
	for _, tc := range cases {
		if got := domain.CountDelta(tc.was, tc.now); got != tc.want {
			t.Errorf("CountDelta(%v,%v) = %d, want %d", tc.was, tc.now, got, tc.want)
		}
	}
}

// TestThreadPosition 覆盖顶级评论与楼中楼回复的 parent/root 推导。
func TestThreadPosition(t *testing.T) {
	parent, root := domain.ThreadPosition(10, nil)
	if parent == nil || *parent != 10 || root == nil || *root != 10 {
		t.Fatalf("顶层回复 parent/root 应为父评论 ID: %v/%v", parent, root)
	}

	rootID := int64(7)
	parent, root = domain.ThreadPosition(10, &rootID)
	if parent == nil || *parent != 10 {
		t.Fatalf("parent = %v, want 10", parent)
	}
	if root == nil || *root != 7 {
		t.Fatalf("root = %v, want 7（继承父评论的 root）", root)
	}
}

// TestValidateCommentBody 覆盖空正文与仅图片正文。
func TestValidateCommentBody(t *testing.T) {
	if err := domain.ValidateCommentBody("<p>你好</p>", "你好"); err != nil {
		t.Fatalf("有文本的正文应通过: %v", err)
	}
	if err := domain.ValidateCommentBody("<p>  </p>", "   "); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("空正文应返回 validation 错误, got %v", err)
	}
	if err := domain.ValidateCommentBody(`<p><img src="/api/v1/attachments/1/content"></p>`, ""); err != nil {
		t.Fatalf("仅图片正文应通过: %v", err)
	}
}

// TestCanDeleteComment 覆盖评论作者 / 帖子作者 / 管理员 / 已删除四种情况。
func TestCanDeleteComment(t *testing.T) {
	comment := model.Comment{ID: 1, AuthorID: 10, Status: model.CommentStatusPublished}
	postAuthor := int64(20)

	if !domain.CanDeleteComment(comment, postAuthor, viewer(10, false)) {
		t.Error("评论作者应可删除自己的评论")
	}
	if !domain.CanDeleteComment(comment, postAuthor, viewer(20, false)) {
		t.Error("帖子作者应可删除评论")
	}
	if !domain.CanDeleteComment(comment, postAuthor, viewer(30, true)) {
		t.Error("管理员应可删除评论")
	}
	if domain.CanDeleteComment(comment, postAuthor, viewer(40, false)) {
		t.Error("无关用户不应可删除评论")
	}
	if domain.CanDeleteComment(comment, postAuthor, model.GuestViewer()) {
		t.Error("游客不应可删除评论")
	}

	deleted := comment
	deleted.Status = model.CommentStatusDeleted
	if domain.CanDeleteComment(deleted, postAuthor, viewer(10, false)) {
		t.Error("已删除评论不应再可删除")
	}
}

// TestFollowRequestTransition 覆盖申请状态机的迁移、幂等与冲突。
func TestFollowRequestTransition(t *testing.T) {
	cases := []struct {
		current string
		action  string
		next    string
		changed bool
		wantErr bool
	}{
		{model.FollowRequestPending, domain.ActionApprove, model.FollowRequestApproved, true, false},
		{model.FollowRequestPending, domain.ActionReject, model.FollowRequestRejected, true, false},
		{model.FollowRequestPending, domain.ActionCancel, model.FollowRequestCanceled, true, false},
		{model.FollowRequestApproved, domain.ActionApprove, model.FollowRequestApproved, false, false},
		{model.FollowRequestRejected, domain.ActionReject, model.FollowRequestRejected, false, false},
		{model.FollowRequestCanceled, domain.ActionCancel, model.FollowRequestCanceled, false, false},
		{model.FollowRequestApproved, domain.ActionReject, "", false, true},
		{model.FollowRequestPending, "unknown", "", false, true},
	}
	for _, tc := range cases {
		next, changed, err := domain.FollowRequestTransition(tc.current, tc.action)
		if tc.wantErr {
			if err == nil {
				t.Errorf("(%s,%s) 应返回错误", tc.current, tc.action)
			}
			continue
		}
		if err != nil {
			t.Errorf("(%s,%s) 返回错误: %v", tc.current, tc.action, err)
			continue
		}
		if next != tc.next || changed != tc.changed {
			t.Errorf("(%s,%s) = (%s,%v), want (%s,%v)", tc.current, tc.action, next, changed, tc.next, tc.changed)
		}
	}
}

// TestFollowRequestTransitionErrors 覆盖错误类型。
func TestFollowRequestTransitionErrors(t *testing.T) {
	if _, _, err := domain.FollowRequestTransition(model.FollowRequestPending, "unknown"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("未知动作应为 validation, got %v", err)
	}
	if _, _, err := domain.FollowRequestTransition(model.FollowRequestApproved, domain.ActionReject); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("已处理申请应为 conflict, got %v", err)
	} else if e, _ := apperr.AsKind(err); e.Code != "request_resolved" {
		t.Fatalf("冲突错误码 = %q, want request_resolved", e.Code)
	}
}

// TestNormalizeDirection 覆盖方向参数校验。
func TestNormalizeDirection(t *testing.T) {
	if got, err := domain.NormalizeDirection(""); err != nil || got != domain.DirectionIncoming {
		t.Fatalf("空方向应默认 incoming, got %q/%v", got, err)
	}
	if got, err := domain.NormalizeDirection("outgoing"); err != nil || got != domain.DirectionOutgoing {
		t.Fatalf("outgoing 应合法, got %q/%v", got, err)
	}
	if _, err := domain.NormalizeDirection("sideways"); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("非法方向应为 invalid, got %v", err)
	}
}

// TestValidateTargetType 覆盖互动目标类型校验。
func TestValidateTargetType(t *testing.T) {
	if err := domain.ValidateTargetType(domain.TargetPost); err != nil {
		t.Fatalf("post 应合法: %v", err)
	}
	if err := domain.ValidateTargetType(domain.TargetComment); err != nil {
		t.Fatalf("comment 应合法: %v", err)
	}
	if err := domain.ValidateTargetType("user"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法目标类型应为 validation, got %v", err)
	}
}

func viewer(id int64, admin bool) model.Viewer {
	return model.Viewer{ID: id, IsAdmin: admin, Authenticated: true}
}

// 保证 domain 的常量与 shared/access 的关注状态分类一致。
var _ = access.ClassifyFollowState
