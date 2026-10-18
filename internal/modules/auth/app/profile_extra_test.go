package app

// 本文补充 auth/app 资料用例：UpdateProfile 的 trim/长度/头像限制分支、
// 关注审批设置读写、个人主页资料与统计组装、用户搜索的游标分页截断。
// 已有 app_test.go 已覆盖 SearchUsers 关键词长度与基本搜索，此处不重复。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func strPtr(value string) *string { return &value }

func TestUpdateProfileMatrix(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, clk := newTestService(t, true)
	user := mustCreateUser(t, users, model.User{ID: 1, Username: "alice", Nickname: "alice", Bio: ""})

	t.Run("未提供任何字段返回 Invalid", func(t *testing.T) {
		_, err := svc.UpdateProfile(ctx, UpdateProfileInput{UserID: user.ID})
		if !apperr.IsKind(err, apperr.KindInvalid) {
			t.Fatalf("未提供字段应返回 Invalid，实际: %v", err)
		}
	})

	t.Run("昵称为空", func(t *testing.T) {
		_, err := svc.UpdateProfile(ctx, UpdateProfileInput{UserID: user.ID, Nickname: strPtr("   ")})
		appErr, ok := apperr.AsKind(err)
		if !ok || appErr.Kind != apperr.KindValidation {
			t.Fatalf("空昵称应返回 Validation，实际: %v", err)
		}
		if details, ok := appErr.Details.(map[string]string); !ok || details["nickname"] == "" {
			t.Fatalf("应返回 nickname 字段详情，实际: %+v", appErr.Details)
		}
	})

	t.Run("昵称超长", func(t *testing.T) {
		_, err := svc.UpdateProfile(ctx, UpdateProfileInput{UserID: user.ID, Nickname: strPtr(strings.Repeat("名", 51))})
		if !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("超长昵称应返回 Validation，实际: %v", err)
		}
	})

	t.Run("简介超长", func(t *testing.T) {
		_, err := svc.UpdateProfile(ctx, UpdateProfileInput{UserID: user.ID, Bio: strPtr(strings.Repeat("简", 501))})
		if !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("超长简介应返回 Validation，实际: %v", err)
		}
	})

	t.Run("资料更新会 trim 并写入应用时钟", func(t *testing.T) {
		updated, err := svc.UpdateProfile(ctx, UpdateProfileInput{
			UserID:   user.ID,
			Nickname: strPtr("  爱丽丝  "),
			Bio:      strPtr("  简介内容  "),
		})
		if err != nil {
			t.Fatalf("更新资料失败: %v", err)
		}
		if updated.Nickname != "爱丽丝" || updated.Bio != "简介内容" {
			t.Fatalf("资料应 trim 后保存: nickname=%q bio=%q", updated.Nickname, updated.Bio)
		}
		if !updated.UpdatedAt.Equal(clk.Now()) {
			t.Fatalf("UpdatedAt = %v，期望应用时钟 %v", updated.UpdatedAt, clk.Now())
		}
		if stored := users.users[user.ID]; stored.Nickname != "爱丽丝" || stored.Bio != "简介内容" {
			t.Fatalf("仓储未保存 trim 后的资料: %+v", stored)
		}
	})

	t.Run("头像与当前值不同时拒绝", func(t *testing.T) {
		_, err := svc.UpdateProfile(ctx, UpdateProfileInput{UserID: user.ID, AvatarURL: strPtr("/api/v1/attachments/1/content")})
		appErr, ok := apperr.AsKind(err)
		if !ok || appErr.Kind != apperr.KindValidation {
			t.Fatalf("直接修改头像应返回 Validation，实际: %v", err)
		}
		if details, ok := appErr.Details.(map[string]string); !ok || details["avatar_url"] == "" {
			t.Fatalf("应返回 avatar_url 字段详情: %+v", appErr.Details)
		}
	})

	t.Run("头像与当前值相同允许", func(t *testing.T) {
		current := users.users[user.ID]
		if _, err := svc.UpdateProfile(ctx, UpdateProfileInput{
			UserID: user.ID, AvatarURL: strPtr(current.AvatarURL),
		}); err != nil {
			t.Fatalf("头像未变化时应允许更新: %v", err)
		}
	})

	t.Run("目标账号不存在", func(t *testing.T) {
		_, err := svc.UpdateProfile(ctx, UpdateProfileInput{UserID: 999, Nickname: strPtr("somebody")})
		if !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("账号不存在应返回 NotFound，实际: %v", err)
		}
	})

	t.Run("仓储未装配", func(t *testing.T) {
		if _, err := (&Service{}).UpdateProfile(ctx, UpdateProfileInput{Nickname: strPtr("x")}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
	})

	t.Run("仓储更新失败返回 Internal", func(t *testing.T) {
		svc.users = &stubUserRepo{fakeUserRepo: users, updateProfileErr: errInjected}
		t.Cleanup(func() { svc.users = users })
		if _, err := svc.UpdateProfile(ctx, UpdateProfileInput{UserID: user.ID, Nickname: strPtr("失败")}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("仓储失败应返回 Internal，实际: %v", err)
		}
	})
}

func TestFollowSettingsAndUpdate(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, clk := newTestService(t, true)
	user := mustCreateUser(t, users, model.User{ID: 1, Username: "alice"})

	t.Run("默认关闭", func(t *testing.T) {
		enabled, err := svc.FollowSettings(ctx, user.ID)
		if err != nil || enabled {
			t.Fatalf("默认应关闭关注审批: enabled=%v err=%v", enabled, err)
		}
	})

	t.Run("更新为开启并写入应用时钟", func(t *testing.T) {
		enabled, err := svc.UpdateFollowSettings(ctx, user.ID, true)
		if err != nil || !enabled {
			t.Fatalf("更新关注审批失败: enabled=%v err=%v", enabled, err)
		}
		stored := users.users[user.ID]
		if !stored.RequireFollowApproval || !stored.UpdatedAt.Equal(clk.Now()) {
			t.Fatalf("仓储未按应用时钟保存设置: %+v", stored)
		}
		enabled, err = svc.FollowSettings(ctx, user.ID)
		if err != nil || !enabled {
			t.Fatalf("读取关注审批失败: enabled=%v err=%v", enabled, err)
		}
	})

	t.Run("关闭关注审批", func(t *testing.T) {
		enabled, err := svc.UpdateFollowSettings(ctx, user.ID, false)
		if err != nil || enabled {
			t.Fatalf("关闭关注审批失败: enabled=%v err=%v", enabled, err)
		}
	})

	t.Run("账号不存在", func(t *testing.T) {
		if _, err := svc.FollowSettings(ctx, 999); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("读取不存在账号应返回 NotFound，实际: %v", err)
		}
		if _, err := svc.UpdateFollowSettings(ctx, 999, true); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("更新不存在账号应返回 NotFound，实际: %v", err)
		}
	})

	t.Run("仓储未装配", func(t *testing.T) {
		if _, err := (&Service{}).FollowSettings(ctx, 1); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
		if _, err := (&Service{}).UpdateFollowSettings(ctx, 1, true); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
	})

	t.Run("仓储失败返回 Internal", func(t *testing.T) {
		svc.users = &stubUserRepo{fakeUserRepo: users, updateFollowErr: errInjected}
		t.Cleanup(func() { svc.users = users })
		if _, err := svc.UpdateFollowSettings(ctx, user.ID, true); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("仓储失败应返回 Internal，实际: %v", err)
		}
	})
}

func TestGetProfileMatrix(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, _ := newTestService(t, true)
	author := mustCreateUser(t, users, model.User{ID: 1, Username: "alice", Nickname: "爱丽丝"})

	t.Run("账号不存在", func(t *testing.T) {
		_, err := svc.GetProfile(ctx, model.GuestViewer(), "nobody")
		if !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("账号不存在应返回 NotFound，实际: %v", err)
		}
	})

	t.Run("未注入统计端口时返回零值", func(t *testing.T) {
		result, err := svc.GetProfile(ctx, model.GuestViewer(), "alice")
		if err != nil {
			t.Fatalf("GetProfile: %v", err)
		}
		if result.Stats != (ProfileStats{}) || result.FollowState != model.FollowStateNone {
			t.Fatalf("游客/无统计时结果不符: %+v", result)
		}
	})

	t.Run("返回统计与关注状态", func(t *testing.T) {
		svc.stats = fakeStats{value: ProfileStats{Posts: 3, Comments: 4, Followers: 5, Following: 6, LikesGiven: 7, LikesReceived: 8, Reposts: 9}}
		svc.relations = fakeRelations{state: model.FollowStateFollowing}
		t.Cleanup(func() {
			svc.stats = nil
			svc.relations = fakeRelations{}
		})
		viewer := model.Viewer{ID: 2, Authenticated: true}
		result, err := svc.GetProfile(ctx, viewer, "alice")
		if err != nil {
			t.Fatalf("GetProfile: %v", err)
		}
		if result.User.ID != author.ID || result.FollowState != model.FollowStateFollowing {
			t.Fatalf("资料/关注状态不符: %+v", result)
		}
		if result.Stats.Posts != 3 || result.Stats.Followers != 5 || result.Stats.Reposts != 9 {
			t.Fatalf("统计不符: %+v", result.Stats)
		}
	})

	t.Run("本人不查询关注状态", func(t *testing.T) {
		svc.relations = failingRelations{err: errInjected}
		t.Cleanup(func() { svc.relations = fakeRelations{} })
		viewer := model.Viewer{ID: author.ID, Authenticated: true}
		result, err := svc.GetProfile(ctx, viewer, "alice")
		if err != nil {
			t.Fatalf("本人查看自己不应读取关注关系: %v", err)
		}
		if result.FollowState != model.FollowStateNone {
			t.Fatalf("本人查看自己关注状态应为 none，实际: %q", result.FollowState)
		}
	})

	t.Run("AI 访问者不查询关注状态", func(t *testing.T) {
		svc.relations = failingRelations{err: errInjected}
		t.Cleanup(func() { svc.relations = fakeRelations{} })
		viewer := model.Viewer{ID: 3, Authenticated: true, IsAI: true}
		if _, err := svc.GetProfile(ctx, viewer, "alice"); err != nil {
			t.Fatalf("AI 访问者不应读取关注关系: %v", err)
		}
	})

	t.Run("关注状态读取失败返回 Internal", func(t *testing.T) {
		svc.relations = failingRelations{err: errInjected}
		t.Cleanup(func() { svc.relations = fakeRelations{} })
		if _, err := svc.GetProfile(ctx, model.Viewer{ID: 9, Authenticated: true}, "alice"); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("关注状态读取失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("统计读取失败返回 Internal", func(t *testing.T) {
		svc.stats = fakeStats{err: errInjected}
		t.Cleanup(func() { svc.stats = nil })
		if _, err := svc.GetProfile(ctx, model.GuestViewer(), "alice"); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("统计读取失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("仓储未装配", func(t *testing.T) {
		if _, err := (&Service{}).GetProfile(ctx, model.GuestViewer(), "alice"); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
	})
}

func TestSearchUsersPagination(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, _ := newTestService(t, true)
	for i, username := range []string{"alice", "alicia", "alina"} {
		mustCreateUser(t, users, model.User{ID: int64(i + 1), Username: username, Nickname: "昵称" + username, CreatedAt: time.Now()})
	}

	t.Run("关键词大小写与空白归一化", func(t *testing.T) {
		result, err := svc.SearchUsers(ctx, "  ALI  ", 0, 20)
		if err != nil {
			t.Fatalf("搜索失败: %v", err)
		}
		if len(result.Users) != 3 {
			t.Fatalf("应匹配 3 个账号，实际 %d", len(result.Users))
		}
	})

	t.Run("超过 limit 时截断并返回游标", func(t *testing.T) {
		result, err := svc.SearchUsers(ctx, "ali", 0, 2)
		if err != nil {
			t.Fatalf("搜索失败: %v", err)
		}
		if len(result.Users) != 2 || !result.HasMore || result.LastID != result.Users[1].ID {
			t.Fatalf("分页结果不符: users=%d hasMore=%v lastID=%d", len(result.Users), result.HasMore, result.LastID)
		}
	})

	t.Run("空关键词返回 Invalid", func(t *testing.T) {
		if _, err := svc.SearchUsers(ctx, "   ", 0, 20); !apperr.IsKind(err, apperr.KindInvalid) {
			t.Fatalf("空关键词应返回 Invalid，实际: %v", err)
		}
	})

	t.Run("仓储未装配", func(t *testing.T) {
		if _, err := (&Service{}).SearchUsers(ctx, "alice", 0, 20); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
	})

	t.Run("仓储失败返回 Internal", func(t *testing.T) {
		svc.users = &stubUserRepo{fakeUserRepo: users, searchErr: errInjected}
		t.Cleanup(func() { svc.users = users })
		if _, err := svc.SearchUsers(ctx, "alice", 0, 20); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("仓储失败应返回 Internal，实际: %v", err)
		}
	})
}
