package access

import (
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

var (
	guest     = model.GuestViewer()
	author    = model.Viewer{ID: 1, Authenticated: true}
	other     = model.Viewer{ID: 2, Authenticated: true}
	aiViewer  = model.Viewer{ID: 3, Authenticated: true, IsAI: true}
	admin     = model.Viewer{ID: 4, Authenticated: true, IsAdmin: true}
	superUser = model.Viewer{ID: 5, Authenticated: true, IsAdmin: true, IsSuperAdmin: true}
	suspended = model.Viewer{ID: 6, Authenticated: true, IsSuspended: true}
)

func post(status, visibility string) model.Post {
	return model.Post{ID: 10, AuthorID: 1, Status: status, Visibility: visibility}
}

func TestCanViewPostWithFollowing(t *testing.T) {
	following := map[int64]bool{1: true}
	cases := []struct {
		name  string
		view  model.Viewer
		p     model.Post
		want  bool
		extra map[int64]bool
	}{
		{"游客看公开", guest, post(model.PostStatusPublished, model.VisibilityPublic), true, nil},
		{"游客看仅关注者", guest, post(model.PostStatusPublished, model.VisibilityFollowers), false, nil},
		{"游客看仅自己", guest, post(model.PostStatusPublished, model.VisibilityPrivate), false, nil},
		{"关注者看仅关注者", other, post(model.PostStatusPublished, model.VisibilityFollowers), true, following},
		{"未关注者看仅关注者", other, post(model.PostStatusPublished, model.VisibilityFollowers), false, nil},
		{"作者看仅自己", author, post(model.PostStatusPublished, model.VisibilityPrivate), true, nil},
		{"非作者看仅自己", other, post(model.PostStatusPublished, model.VisibilityPrivate), false, nil},
		{"AI 看公开", aiViewer, post(model.PostStatusPublished, model.VisibilityPublic), true, nil},
		{"AI 看仅关注者", model.Viewer{ID: 7, Authenticated: true, IsAI: true}, post(model.PostStatusPublished, model.VisibilityFollowers), false, following},
		{"AI 看自己的草稿", model.Viewer{ID: 1, Authenticated: true, IsAI: true}, post(model.PostStatusDraft, model.VisibilityPublic), true, nil},
		{"作者看草稿", author, post(model.PostStatusDraft, model.VisibilityPrivate), true, nil},
		{"他人看草稿", other, post(model.PostStatusDraft, model.VisibilityPrivate), false, nil},
		{"管理员看草稿", admin, post(model.PostStatusDraft, model.VisibilityPrivate), true, nil},
		{"游客看草稿", guest, post(model.PostStatusDraft, model.VisibilityPublic), false, nil},
		{"作者看定时", author, post(model.PostStatusScheduled, model.VisibilityPublic), true, nil},
		{"他人看定时", other, post(model.PostStatusScheduled, model.VisibilityPublic), false, nil},
		{"管理员看已删除", admin, post(model.PostStatusDeleted, model.VisibilityPublic), true, nil},
		{"作者看已删除", author, post(model.PostStatusDeleted, model.VisibilityPublic), false, nil},
		{"游客看已删除", guest, post(model.PostStatusDeleted, model.VisibilityPublic), false, nil},
		{"未知状态", admin, post("unknown", model.VisibilityPublic), false, nil},
		{"未知可见性", other, post(model.PostStatusPublished, "unknown"), false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			followingSet := tc.extra
			if got := CanViewPostWithFollowing(tc.view, tc.p, followingSet); got != tc.want {
				t.Fatalf("CanViewPostWithFollowing = %v，期望 %v", got, tc.want)
			}
			// CanViewPost 是同一策略的标准入口，必须与之一致。
			if got := CanViewPost(tc.view, tc.p, followingSet); got != tc.want {
				t.Fatalf("CanViewPost = %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestRequireVisiblePost(t *testing.T) {
	visible := post(model.PostStatusPublished, model.VisibilityPublic)
	if err := RequireVisiblePost(guest, visible, nil); err != nil {
		t.Fatalf("公开帖子游客应可见: %v", err)
	}
	err := RequireVisiblePost(guest, post(model.PostStatusPublished, model.VisibilityPrivate), nil)
	if !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("不可见内容应返回 not_found，实际 %v", err)
	}
	var target *apperr.Error
	if e, ok := apperr.AsKind(err); ok {
		target = e
	} else {
		t.Fatalf("错误必须是 *apperr.Error")
	}
	if target.Message != "内容不存在或无权访问" {
		t.Fatalf("错误文案不兼容: %q", target.Message)
	}
}

func TestCanWriteAndRequireWritable(t *testing.T) {
	if !CanWrite(author) {
		t.Fatalf("普通登录用户应可写")
	}
	for name, view := range map[string]model.Viewer{"游客": guest, "AI": aiViewer, "停用": suspended} {
		if CanWrite(view) {
			t.Errorf("%s 不应可写", name)
		}
	}
	cases := []struct {
		name string
		view model.Viewer
		kind apperr.Kind
	}{
		{"游客", guest, apperr.KindUnauthenticated},
		{"停用", suspended, apperr.KindSuspended},
		{"AI", aiViewer, apperr.KindForbidden},
	}
	for _, tc := range cases {
		err := RequireWritable(tc.view)
		if !apperr.IsKind(err, tc.kind) {
			t.Errorf("%s：期望 %v，实际 %v", tc.name, tc.kind, err)
		}
	}
	if err := RequireWritable(author); err != nil {
		t.Fatalf("正常用户应通过: %v", err)
	}
}

func TestIsHumanActiveAndActorWritable(t *testing.T) {
	if !IsHumanActive(model.User{Status: model.StatusActive}) {
		t.Fatalf("启用的人类账号应判定为 human active")
	}
	if IsHumanActive(model.User{Status: model.StatusSuspended}) {
		t.Fatalf("停用账号不是 human active")
	}
	if IsHumanActive(model.User{Status: model.StatusActive, IsAI: true}) {
		t.Fatalf("AI 账号不是 human active")
	}
	if err := RequireActorWritable(model.User{Status: model.StatusActive, IsAI: true}); err != nil {
		t.Fatalf("AI 主体执行后台动作应允许: %v", err)
	}
	if err := RequireActorWritable(model.User{Status: model.StatusSuspended}); !apperr.IsKind(err, apperr.KindSuspended) {
		t.Fatalf("停用主体应返回 suspended，实际 %v", err)
	}
}

func TestCanInteract(t *testing.T) {
	published := post(model.PostStatusPublished, model.VisibilityPublic)
	if err := CanInteract(author, published, true); err != nil {
		t.Fatalf("作者本人可在自己被停用时互动: %v", err)
	}
	if err := CanInteract(other, published, false); err != nil {
		t.Fatalf("正常互动应允许: %v", err)
	}
	if err := CanInteract(other, published, true); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("对方停用应禁止互动，实际 %v", err)
	}
	if err := CanInteract(other, post(model.PostStatusDraft, model.VisibilityPublic), false); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("未发布内容应禁止互动，实际 %v", err)
	}
}

func TestRequireInteractivePost(t *testing.T) {
	publicPost := post(model.PostStatusPublished, model.VisibilityPublic)
	if err := RequireInteractivePost(other, publicPost, nil, false); err != nil {
		t.Fatalf("正常互动应允许: %v", err)
	}
	if err := RequireInteractivePost(guest, publicPost, nil, false); !apperr.IsKind(err, apperr.KindUnauthenticated) {
		t.Fatalf("游客互动应要求登录，实际 %v", err)
	}
	if err := RequireInteractivePost(aiViewer, publicPost, nil, false); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("AI 会话不得互动，实际 %v", err)
	}
	// 私密帖对非作者返回 not_found，而不是 forbidden（不暴露存在性）。
	privatePost := post(model.PostStatusPublished, model.VisibilityPrivate)
	if err := RequireInteractivePost(other, privatePost, nil, false); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("不可见帖子应返回 not_found，实际 %v", err)
	}
	// 作者对已删除内容：可见性判断优先返回 not_found（旧实现顺序一致）。
	deleted := post(model.PostStatusDeleted, model.VisibilityPublic)
	if err := RequireInteractivePost(other, deleted, nil, false); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("已删除内容应返回 not_found，实际 %v", err)
	}
	if err := RequireInteractivePost(other, publicPost, nil, true); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("作者停用应禁止互动，实际 %v", err)
	}
}

func TestClassifyFollowState(t *testing.T) {
	cases := []struct {
		following, pending, incoming bool
		want                         string
	}{
		{true, false, false, model.FollowStateFollowing},
		{true, true, true, model.FollowStateFollowing},
		{false, true, false, model.FollowStatePending},
		{false, true, true, model.FollowStatePending},
		{false, false, true, model.FollowStateIncomingPending},
		{false, false, false, model.FollowStateNone},
	}
	for _, tc := range cases {
		if got := ClassifyFollowState(tc.following, tc.pending, tc.incoming); got != tc.want {
			t.Errorf("ClassifyFollowState(%v,%v,%v) = %q，期望 %q",
				tc.following, tc.pending, tc.incoming, got, tc.want)
		}
	}
}

func TestCanManageAdmins(t *testing.T) {
	if !CanManageAdmins(superUser) {
		t.Fatalf("超级管理员应可管理管理员")
	}
	for name, view := range map[string]model.Viewer{
		"游客":        guest,
		"普通用户":      author,
		"普通管理员":     admin,
		"AI 伪超管":    model.Viewer{ID: 8, Authenticated: true, IsAdmin: true, IsSuperAdmin: true, IsAI: true},
		"超管标记但非管理员": model.Viewer{ID: 9, Authenticated: true, IsSuperAdmin: true},
	} {
		if CanManageAdmins(view) {
			t.Errorf("%s 不应可管理管理员", name)
		}
	}
}

func TestCanChangeAdminRole(t *testing.T) {
	superAdmin := model.User{ID: 1, Role: model.RoleAdmin, IsSuperAdmin: true, Status: model.StatusActive}
	superAdminAI := superAdmin
	superAdminAI.IsAI = true
	normalAdmin := model.User{ID: 2, Role: model.RoleAdmin, Status: model.StatusActive}
	human := model.User{ID: 3, Role: model.RoleUser, Status: model.StatusActive}
	suspendedHuman := model.User{ID: 4, Role: model.RoleUser, Status: model.StatusSuspended}
	ai := model.User{ID: 5, Role: model.RoleUser, IsAI: true, Status: model.StatusActive}
	otherSuper := model.User{ID: 6, Role: model.RoleAdmin, IsSuperAdmin: true, Status: model.StatusActive}

	if err := CanChangeAdminRole(superAdmin, human); err != nil {
		t.Fatalf("超管授权普通账号应允许: %v", err)
	}
	if err := CanChangeAdminRole(superAdmin, suspendedHuman); err != nil {
		t.Fatalf("撤销方向不应受停用限制: %v", err)
	}
	cases := []struct {
		name   string
		actor  model.User
		target model.User
		kind   apperr.Kind
	}{
		{"普通管理员越权", normalAdmin, human, apperr.KindForbidden},
		{"AI 伪超管", superAdminAI, human, apperr.KindForbidden},
		{"目标为 AI", superAdmin, ai, apperr.KindValidation},
		{"操作自己", superAdmin, superAdmin, apperr.KindValidation},
		{"操作其他超管", superAdmin, otherSuper, apperr.KindValidation},
	}
	for _, tc := range cases {
		if err := CanChangeAdminRole(tc.actor, tc.target); !apperr.IsKind(err, tc.kind) {
			t.Errorf("%s：期望 %v，实际 %v", tc.name, tc.kind, err)
		}
	}
	if err := CanGrantAdminRole(superAdmin, suspendedHuman); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("授权方向应拒绝停用账号，实际 %v", err)
	}
	if err := CanGrantAdminRole(superAdmin, human); err != nil {
		t.Fatalf("授权正常账号应允许: %v", err)
	}
}

func TestCanModerateUser(t *testing.T) {
	superTarget := model.User{ID: 1, Role: model.RoleAdmin, IsSuperAdmin: true}
	normalTarget := model.User{ID: 2, Role: model.RoleUser}
	aiTarget := model.User{ID: 3, Role: model.RoleUser, IsAI: true}

	if err := CanModerateUser(admin, normalTarget); err != nil {
		t.Fatalf("普通管理员处置普通用户应允许: %v", err)
	}
	if err := CanModerateUser(admin, aiTarget); err != nil {
		t.Fatalf("普通管理员处置 AI 账号应允许: %v", err)
	}
	if err := CanModerateUser(superUser, superTarget); err != nil {
		t.Fatalf("超级管理员处置超管账号应允许: %v", err)
	}
	cases := []struct {
		name   string
		actor  model.Viewer
		target model.User
		kind   apperr.Kind
	}{
		{"普通管理员操作超管", admin, superTarget, apperr.KindForbidden},
		{"游客操作", guest, normalTarget, apperr.KindForbidden},
		{"AI 操作", aiViewer, normalTarget, apperr.KindForbidden},
	}
	for _, tc := range cases {
		if err := CanModerateUser(tc.actor, tc.target); !apperr.IsKind(err, tc.kind) {
			t.Errorf("%s：期望 %v，实际 %v", tc.name, tc.kind, err)
		}
	}
}
