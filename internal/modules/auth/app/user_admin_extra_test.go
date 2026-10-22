package app

// 本文补充 auth/app 跨模块 contracts.UserReader/UserWriter 实现用例：
// 批量读取、摘要、@提及解析、AI 标记过滤、AI 账号创建、资料字段更新与头像引用。
// 这些方法是 social/content/media/admin 等模块的只读/写入端口，必须有直接单测。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func int64Set(values []int64) map[int64]bool {
	set := make(map[int64]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func TestUserReaderContracts(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, _ := newTestService(t, true)
	mustCreateUser(t, users, model.User{ID: 1, Username: "alice", Nickname: "爱丽丝", Email: "a@example.com"})
	mustCreateUser(t, users, model.User{ID: 2, Username: "bob", Role: model.RoleAdmin})
	mustCreateUser(t, users, model.User{ID: 3, Username: "ai_bot", IsAI: true})
	mustCreateUser(t, users, model.User{ID: 4, Username: "dave", Status: model.StatusSuspended})

	t.Run("GetUser", func(t *testing.T) {
		user, err := svc.GetUser(ctx, 1)
		if err != nil || user.Username != "alice" {
			t.Fatalf("GetUser: %+v err=%v", user, err)
		}
		if _, err := svc.GetUser(ctx, 999); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("不存在的账号应返回 NotFound，实际: %v", err)
		}
	})

	t.Run("GetUserByUsername", func(t *testing.T) {
		user, err := svc.GetUserByUsername(ctx, "ALICE")
		if err != nil || user.ID != 1 {
			t.Fatalf("GetUserByUsername: %+v err=%v", user, err)
		}
		if _, err := svc.GetUserByUsername(ctx, "nobody"); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("不存在的用户名应返回 NotFound，实际: %v", err)
		}
	})

	t.Run("GetUsers 批量且缺失 ID 不在结果中", func(t *testing.T) {
		batch, err := svc.GetUsers(ctx, []int64{1, 3, 999})
		if err != nil {
			t.Fatalf("GetUsers: %v", err)
		}
		if len(batch) != 2 || batch[1].Username != "alice" || !batch[3].IsAI {
			t.Fatalf("批量读取结果不符: %+v", batch)
		}
	})

	t.Run("GetUserBrief 与 GetUserBriefs/BriefsByIDs", func(t *testing.T) {
		brief, err := svc.GetUserBrief(ctx, 2)
		if err != nil || brief.ID != 2 || brief.Username != "bob" {
			t.Fatalf("GetUserBrief: %+v err=%v", brief, err)
		}
		if _, err := svc.GetUserBrief(ctx, 999); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("不存在的账号摘要应返回 NotFound，实际: %v", err)
		}
		briefs, err := svc.GetUserBriefs(ctx, []int64{1, 2})
		if err != nil || len(briefs) != 2 {
			t.Fatalf("GetUserBriefs: %+v err=%v", briefs, err)
		}
		viaAlias, err := svc.BriefsByIDs(ctx, []int64{1, 2})
		if err != nil || len(viaAlias) != 2 || viaAlias[2].Nickname != "bob" {
			t.Fatalf("BriefsByIDs: %+v err=%v", viaAlias, err)
		}
	})

	t.Run("FindActiveUsersByUsernames 忽略停用账号", func(t *testing.T) {
		briefs, err := svc.FindActiveUsersByUsernames(ctx, []string{"alice", "dave", "unknown"})
		if err != nil {
			t.Fatalf("FindActiveUsersByUsernames: %v", err)
		}
		if len(briefs) != 1 || briefs[0].Username != "alice" {
			t.Fatalf("@提及解析应只返回生效账号: %+v", briefs)
		}
	})

	t.Run("IsSuspended", func(t *testing.T) {
		if suspended, err := svc.IsSuspended(ctx, 4); err != nil || !suspended {
			t.Fatalf("停用账号应返回 true: suspended=%v err=%v", suspended, err)
		}
		if suspended, err := svc.IsSuspended(ctx, 1); err != nil || suspended {
			t.Fatalf("正常账号应返回 false: suspended=%v err=%v", suspended, err)
		}
		if _, err := svc.IsSuspended(ctx, 999); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("不存在的账号应返回 NotFound，实际: %v", err)
		}
	})

	t.Run("ListUserIDsByAIFlag", func(t *testing.T) {
		aiIDs, err := svc.ListUserIDsByAIFlag(ctx, true)
		if err != nil || !int64Set(aiIDs)[3] || len(aiIDs) != 1 {
			t.Fatalf("AI 账号 ID 列表不符: %v err=%v", aiIDs, err)
		}
		humanIDs, err := svc.ListUserIDsByAIFlag(ctx, false)
		if err != nil {
			t.Fatalf("人类账号 ID 列表失败: %v", err)
		}
		set := int64Set(humanIDs)
		if len(humanIDs) != 3 || !set[1] || !set[2] || !set[4] {
			t.Fatalf("人类账号 ID 列表不符: %v", humanIDs)
		}
	})

	t.Run("ListAvatarAttachmentIDs", func(t *testing.T) {
		ids, err := svc.ListAvatarAttachmentIDs(ctx)
		if err != nil || ids != nil {
			t.Fatalf("头像附件引用列表不符: ids=%v err=%v", ids, err)
		}
	})

	t.Run("仓储未装配", func(t *testing.T) {
		empty := &Service{}
		checks := []error{
			func() error { _, err := empty.GetUser(ctx, 1); return err }(),
			func() error { _, err := empty.GetUserByUsername(ctx, "alice"); return err }(),
			func() error { _, err := empty.GetUsers(ctx, []int64{1}); return err }(),
			func() error { _, err := empty.GetUserBriefs(ctx, []int64{1}); return err }(),
			func() error { _, err := empty.FindActiveUsersByUsernames(ctx, []string{"alice"}); return err }(),
			func() error { _, err := empty.ListUserIDsByAIFlag(ctx, false); return err }(),
			func() error { _, err := empty.ListAvatarAttachmentIDs(ctx); return err }(),
		}
		for i, err := range checks {
			if !apperr.IsKind(err, apperr.KindInternal) {
				t.Fatalf("第 %d 个缺少仓储分支应返回 Internal，实际: %v", i, err)
			}
		}
	})

	t.Run("仓储故障分支透传或映射 Internal", func(t *testing.T) {
		svc.users = &stubUserRepo{
			fakeUserRepo:     users,
			byIDErr:          errInjected,
			byUsernameErr:    errInjected,
			getUsersErr:      errInjected,
			getUserBriefsErr: errInjected,
			findActiveErr:    errInjected,
			listUserIDsErr:   errInjected,
			listAvatarIDsErr: errInjected,
		}
		t.Cleanup(func() { svc.users = users })

		// GetUser/GetUserByUsername/IsSuspended 原样透传仓储错误（生产仓储只会返回
		// 领域错误，故不额外包装）；批量/派生读取统一包装为 Internal。
		rawChecks := []error{
			func() error { _, err := svc.GetUser(ctx, 1); return err }(),
			func() error { _, err := svc.GetUserByUsername(ctx, "alice"); return err }(),
			func() error { _, err := svc.IsSuspended(ctx, 1); return err }(),
		}
		for i, err := range rawChecks {
			if !errors.Is(err, errInjected) {
				t.Fatalf("第 %d 个读取分支应透传仓储错误，实际: %v", i, err)
			}
		}
		internalChecks := []error{
			func() error { _, err := svc.GetUsers(ctx, []int64{1}); return err }(),
			func() error { _, err := svc.GetUserBrief(ctx, 1); return err }(),
			func() error { _, err := svc.FindActiveUsersByUsernames(ctx, []string{"alice"}); return err }(),
			func() error { _, err := svc.ListUserIDsByAIFlag(ctx, false); return err }(),
			func() error { _, err := svc.ListAvatarAttachmentIDs(ctx); return err }(),
		}
		for i, err := range internalChecks {
			if !apperr.IsKind(err, apperr.KindInternal) {
				t.Fatalf("第 %d 个仓储故障分支应返回 Internal，实际: %v", i, err)
			}
		}
	})
}

func TestCreateAIUserContracts(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, clk := newTestService(t, true)
	now := clk.Now()

	t.Run("创建 AI 账号默认昵称并写入应用时钟", func(t *testing.T) {
		created, err := svc.CreateAIUser(ctx, contracts.CreateAIUserInput{Username: "bot_one"})
		if err != nil {
			t.Fatalf("创建 AI 账号失败: %v", err)
		}
		if created.Username != "bot_one" || created.Nickname != "bot_one" {
			t.Fatalf("用户名字段不符: %+v", created)
		}
		if !created.IsAI || created.Role != model.RoleUser || created.Status != model.StatusActive {
			t.Fatalf("AI 账号默认字段不符: %+v", created)
		}
		if created.TokenVersion != 1 || !created.CreatedAt.Equal(now) || !created.UpdatedAt.Equal(now) {
			t.Fatalf("AI 账号时间/token_version 不符: %+v", created)
		}
		if stored := users.users[created.ID]; stored.Username != "bot_one" || !stored.IsAI {
			t.Fatalf("仓储未保存 AI 账号: %+v", stored)
		}
	})

	t.Run("显式昵称/简介 trim 且尊重传入时间", func(t *testing.T) {
		at := now.Add(2 * time.Hour)
		created, err := svc.CreateAIUser(ctx, contracts.CreateAIUserInput{
			Username: "  bot_two  ", Nickname: "  二号  ", Bio: "  简介  ", At: at,
		})
		if err != nil {
			t.Fatalf("创建 AI 账号失败: %v", err)
		}
		if created.Username != "bot_two" || created.Nickname != "二号" || created.Bio != "简介" {
			t.Fatalf("字段未 trim: %+v", created)
		}
		if !created.CreatedAt.Equal(at) || !created.UpdatedAt.Equal(at) {
			t.Fatalf("应使用传入时间: %+v", created)
		}
	})

	t.Run("用户名非法", func(t *testing.T) {
		_, err := svc.CreateAIUser(ctx, contracts.CreateAIUserInput{Username: "1-bad"})
		appErr, ok := apperr.AsKind(err)
		if !ok || appErr.Kind != apperr.KindValidation {
			t.Fatalf("非法用户名应返回 Validation，实际: %v", err)
		}
		if details, ok := appErr.Details.(map[string]string); !ok || details["username"] == "" {
			t.Fatalf("应返回 username 字段详情: %+v", appErr.Details)
		}
	})

	t.Run("昵称超长", func(t *testing.T) {
		longNickname := strings.Repeat("名", 51)
		if _, err := svc.CreateAIUser(ctx, contracts.CreateAIUserInput{Username: "bot_three", Nickname: longNickname}); !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("超长昵称应返回 Validation，实际: %v", err)
		}
	})

	t.Run("用户名重复返回 Conflict", func(t *testing.T) {
		if _, err := svc.CreateAIUser(ctx, contracts.CreateAIUserInput{Username: "bot_one"}); !apperr.IsKind(err, apperr.KindConflict) {
			t.Fatalf("重复用户名应返回 Conflict，实际: %v", err)
		}
	})

	t.Run("ID 生成失败", func(t *testing.T) {
		originalIDs := svc.ids
		svc.ids = failingIDs{}
		t.Cleanup(func() { svc.ids = originalIDs })
		if _, err := svc.CreateAIUser(ctx, contracts.CreateAIUserInput{Username: "bot_four"}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("ID 生成失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("依赖未装配", func(t *testing.T) {
		if _, err := (&Service{}).CreateAIUser(ctx, contracts.CreateAIUserInput{Username: "bot_five"}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少依赖应返回 Internal，实际: %v", err)
		}
	})
}

func TestUpdateUserProfileAndAvatarContracts(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _, clk := newTestService(t, true)
	user := mustCreateUser(t, users, model.User{ID: 1, Username: "alice", Nickname: "爱丽丝", Bio: "旧简介"})
	now := clk.Now()

	t.Run("未提供字段返回 Invalid", func(t *testing.T) {
		if err := svc.UpdateUserProfile(ctx, contracts.UpdateUserProfileInput{UserID: user.ID}); !apperr.IsKind(err, apperr.KindInvalid) {
			t.Fatalf("未提供字段应返回 Invalid，实际: %v", err)
		}
	})

	t.Run("用户名非法", func(t *testing.T) {
		bad := "no"
		err := svc.UpdateUserProfile(ctx, contracts.UpdateUserProfileInput{UserID: user.ID, Username: &bad})
		appErr, ok := apperr.AsKind(err)
		if !ok || appErr.Kind != apperr.KindValidation {
			t.Fatalf("非法用户名应返回 Validation，实际: %v", err)
		}
		if details, ok := appErr.Details.(map[string]string); !ok || details["username"] == "" {
			t.Fatalf("应返回 username 字段详情: %+v", appErr.Details)
		}
	})

	t.Run("字段 trim 且未传时间使用应用时钟", func(t *testing.T) {
		username := "  alice_new  "
		nickname := "  新昵称  "
		bio := "  新简介  "
		if err := svc.UpdateUserProfile(ctx, contracts.UpdateUserProfileInput{
			UserID: user.ID, Username: &username, Nickname: &nickname, Bio: &bio,
		}); err != nil {
			t.Fatalf("更新资料失败: %v", err)
		}
		stored := users.users[user.ID]
		if stored.Username != "alice_new" || stored.Nickname != "新昵称" || stored.Bio != "新简介" {
			t.Fatalf("字段未 trim: %+v", stored)
		}
		if !stored.UpdatedAt.Equal(now) {
			t.Fatalf("应使用应用时钟 now=%v，实际: %v", now, stored.UpdatedAt)
		}
	})

	t.Run("显式时间优先", func(t *testing.T) {
		at := now.Add(time.Hour)
		nickname := "定时昵称"
		if err := svc.UpdateUserProfile(ctx, contracts.UpdateUserProfileInput{UserID: user.ID, Nickname: &nickname, At: at}); err != nil {
			t.Fatalf("更新资料失败: %v", err)
		}
		if stored := users.users[user.ID]; !stored.UpdatedAt.Equal(at) {
			t.Fatalf("应使用显式时间 %v，实际: %v", at, stored.UpdatedAt)
		}
	})

	t.Run("目标账号不存在", func(t *testing.T) {
		nickname := "任意"
		if err := svc.UpdateUserProfile(ctx, contracts.UpdateUserProfileInput{UserID: 999, Nickname: &nickname}); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("账号不存在应返回 NotFound，实际: %v", err)
		}
	})

	t.Run("仓储未装配与仓储失败", func(t *testing.T) {
		nickname := "任意"
		if err := (&Service{}).UpdateUserProfile(ctx, contracts.UpdateUserProfileInput{UserID: 1, Nickname: &nickname}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
		svc.users = &stubUserRepo{fakeUserRepo: users, updateProfileFieldsErr: errInjected}
		t.Cleanup(func() { svc.users = users })
		if err := svc.UpdateUserProfile(ctx, contracts.UpdateUserProfileInput{UserID: user.ID, Nickname: &nickname}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("仓储失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("SetAvatar 写入头像引用", func(t *testing.T) {
		svc.users = users
		at := now.Add(3 * time.Hour)
		if err := svc.SetAvatar(ctx, contracts.AvatarUpdateInput{
			UserID: user.ID, AttachmentID: 42, AvatarURL: "/api/v1/attachments/42/content", At: at,
		}); err != nil {
			t.Fatalf("SetAvatar: %v", err)
		}
		stored := users.users[user.ID]
		if stored.AvatarURL != "/api/v1/attachments/42/content" || !stored.UpdatedAt.Equal(at) {
			t.Fatalf("头像引用未保存: %+v", stored)
		}
	})

	t.Run("SetAvatar 依赖与错误分支", func(t *testing.T) {
		if err := (&Service{}).SetAvatar(ctx, contracts.AvatarUpdateInput{UserID: 1}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
		if err := svc.SetAvatar(ctx, contracts.AvatarUpdateInput{UserID: 999}); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("账号不存在应返回 NotFound，实际: %v", err)
		}
		svc.users = &stubUserRepo{fakeUserRepo: users, updateAvatarErr: errors.New("头像写入失败")}
		t.Cleanup(func() { svc.users = users })
		if err := svc.SetAvatar(ctx, contracts.AvatarUpdateInput{UserID: user.ID}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("仓储失败应返回 Internal，实际: %v", err)
		}
	})

	t.Run("SetBanner 写入横幅引用", func(t *testing.T) {
		svc.users = users
		at := now.Add(4 * time.Hour)
		attachmentID := int64(43)
		if err := svc.SetBanner(ctx, contracts.BannerUpdateInput{
			UserID: user.ID, AttachmentID: &attachmentID, BannerURL: "/api/v1/attachments/43/content", At: at,
		}); err != nil {
			t.Fatalf("SetBanner: %v", err)
		}
		stored := users.users[user.ID]
		if stored.BannerURL != "/api/v1/attachments/43/content" || !stored.UpdatedAt.Equal(at) {
			t.Fatalf("横幅引用未保存: %+v", stored)
		}
	})

	t.Run("SetBanner 依赖与错误分支", func(t *testing.T) {
		if err := (&Service{}).SetBanner(ctx, contracts.BannerUpdateInput{UserID: 1}); !apperr.IsKind(err, apperr.KindInternal) {
			t.Fatalf("缺少仓储应返回 Internal，实际: %v", err)
		}
		if err := svc.SetBanner(ctx, contracts.BannerUpdateInput{UserID: 999}); !apperr.IsKind(err, apperr.KindNotFound) {
			t.Fatalf("账号不存在应返回 NotFound，实际: %v", err)
		}
	})
}
