package access

import (
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// adminViewer 构造管理员访问者。
func adminViewer(id int64) model.Viewer {
	return model.ViewerFrom(model.User{ID: id, Role: model.RoleAdmin, Status: model.StatusActive})
}

// humanViewer 构造普通登录访问者。
func humanViewer(id int64) model.Viewer {
	return model.ViewerFrom(model.User{ID: id, Role: model.RoleUser, Status: model.StatusActive})
}

// TestCanViewPublishedAsAdmin 校验管理员可查看任意已发布内容（含仅关注者/仅自己可见），
// 不需要关注集合。
func TestCanViewPublishedAsAdmin(t *testing.T) {
	admin := adminViewer(1)
	posts := []model.Post{
		{ID: 10, AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
		{ID: 11, AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityPrivate},
		{ID: 12, AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic},
	}
	for _, post := range posts {
		if !CanViewPostWithFollowing(admin, post, nil) {
			t.Errorf("管理员应能查看已发布帖子 %d（visibility=%s）", post.ID, post.Visibility)
		}
	}
}

// TestCanViewFollowersSelfPost 校验仅关注者可见内容：作者本人无需关注关系即可查看，
// 其他登录人类用户需要关注作者，游客与 AI 会话不能借关注关系查看。
func TestCanViewFollowersSelfPost(t *testing.T) {
	post := model.Post{
		ID: 20, AuthorID: 7,
		Status:     model.PostStatusPublished,
		Visibility: model.VisibilityFollowers,
	}
	if !CanViewPostWithFollowing(humanViewer(7), post, nil) {
		t.Fatal("作者本人应能查看自己仅关注者可见的内容")
	}
	follower := humanViewer(8)
	if !CanViewPostWithFollowing(follower, post, map[int64]bool{7: true}) {
		t.Fatal("已关注作者的人类用户应能查看")
	}
	if CanViewPostWithFollowing(follower, post, map[int64]bool{7: false}) {
		t.Fatal("未关注作者（集合值为 false）不应能查看")
	}
	aiViewer := model.ViewerFrom(model.User{ID: 9, IsAI: true, Status: model.StatusActive})
	if CanViewPostWithFollowing(aiViewer, post, map[int64]bool{7: true}) {
		t.Fatal("AI 会话不能借关注关系查看仅关注者可见内容")
	}
	if CanViewPostWithFollowing(model.GuestViewer(), post, map[int64]bool{7: true}) {
		t.Fatal("游客不能查看仅关注者可见内容")
	}
}

// TestCanGrantAdminRoleSuspendedTarget 校验超级管理员授权方向：已停用账号不能授权为管理员，
// 撤销方向（CanChangeAdminRole）不受停用状态限制。
func TestCanGrantAdminRoleSuspendedTarget(t *testing.T) {
	actor := model.User{ID: 1, Role: model.RoleAdmin, IsSuperAdmin: true, Status: model.StatusActive}
	suspended := model.User{ID: 2, Role: model.RoleUser, Status: model.StatusSuspended}
	active := model.User{ID: 3, Role: model.RoleUser, Status: model.StatusActive}

	if err := CanChangeAdminRole(actor, suspended); err != nil {
		t.Fatalf("撤销方向不要求目标启用: %v", err)
	}
	if err := CanGrantAdminRole(actor, suspended); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("停用账号授权应为 validation_failed，实际 %v", err)
	}
	if err := CanGrantAdminRole(actor, active); err != nil {
		t.Fatalf("启用账号授权应允许: %v", err)
	}
}
