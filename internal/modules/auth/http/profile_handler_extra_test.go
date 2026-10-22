package http

// 本文补充 auth/http 资料接口用例：资料更新与服务端校验、关注审批设置读写、
// 用户搜索游标分页参数与响应形状、个人主页统计/关注状态/停用标记与错误映射。
// 已有 auth_handler_test.go 覆盖关注审批字段缺失与零装配服务的 500 分支，此处不重复。

import (
	"net/http"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/auth/app"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestUpdateProfileHandler(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()
	user := h.seedUser(t, model.User{ID: 1, Username: "alice", Nickname: "爱丽丝", Bio: "旧简介"})
	h.seedSession(user, "profile-token", "profile-csrf")

	t.Run("缺少 CSRF 返回 403", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me", `{"nickname":"新昵称"}`, "profile-token", "")
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("无效 JSON 返回 400", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me", `{`, "profile-token", "profile-csrf")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("未提供任何字段返回 400", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me", `{}`, "profile-token", "profile-csrf")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("昵称超长返回 400 与字段详情", func(t *testing.T) {
		body := `{"nickname":"` + strings.Repeat("名", 51) + `"}`
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me", body, "profile-token", "profile-csrf")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		if errorDetails(t, recorder)["nickname"] == nil {
			t.Fatal("应返回 nickname 字段详情")
		}
	})

	t.Run("直接修改头像返回 400", func(t *testing.T) {
		body := `{"avatar_url":"/api/v1/attachments/9/content"}`
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me", body, "profile-token", "profile-csrf")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		if errorDetails(t, recorder)["avatar_url"] == nil {
			t.Fatal("应返回 avatar_url 字段详情")
		}
	})

	t.Run("更新成功返回 trim 后的私有资料", func(t *testing.T) {
		body := `{"nickname":"  新昵称  ","bio":"  新简介  "}`
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me", body, "profile-token", "profile-csrf")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		responseUser, ok := bodyMap(t, recorder)["user"].(map[string]any)
		if !ok {
			t.Fatalf("响应缺少 user: %s", recorder.Body.String())
		}
		if responseUser["nickname"] != "新昵称" || responseUser["bio"] != "新简介" {
			t.Fatalf("资料未 trim: %v", responseUser)
		}
		if email, present := responseUser["email"]; present && email != "" {
			t.Fatalf("空邮箱不应输出非空 email: %v", responseUser)
		}
	})

	t.Run("缺少 Viewer 的直接调用返回 401", func(t *testing.T) {
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/users/me", `{"nickname":"新昵称"}`)
		h.handler.UpdateProfile(c)
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestDeleteBannerHandler(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()
	user := h.seedUser(t, model.User{
		ID: 1, Username: "alice", Nickname: "爱丽丝", BannerURL: "/api/v1/attachments/9/content",
	})
	h.seedSession(user, "banner-token", "banner-csrf")

	t.Run("缺少 CSRF 返回 403", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me/banner/delete", "", "banner-token", "")
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("移除成功返回清空横幅的私有资料", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me/banner/delete", "", "banner-token", "banner-csrf")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		responseUser, ok := bodyMap(t, recorder)["user"].(map[string]any)
		if !ok {
			t.Fatalf("响应缺少 user: %s", recorder.Body.String())
		}
		if responseUser["banner_url"] != "" {
			t.Fatalf("banner_url 未清空: %v", responseUser["banner_url"])
		}
	})

	t.Run("缺少 Viewer 的直接调用返回 401", func(t *testing.T) {
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/users/me/banner/delete", "")
		h.handler.DeleteBanner(c)
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestFollowSettingsHandlers(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()
	user := h.seedUser(t, model.User{ID: 1, Username: "alice"})
	h.seedSession(user, "follow-token", "follow-csrf")

	t.Run("默认关闭", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodGet, "/api/v1/users/me/follow-settings", "", "follow-token", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
		if bodyMap(t, recorder)["require_follow_approval"] != false {
			t.Fatalf("默认值不符: %s", recorder.Body.String())
		}
	})

	t.Run("缺少字段返回 400 与详情", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me/follow-settings", `{}`, "follow-token", "follow-csrf")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		if errorDetails(t, recorder)["require_follow_approval"] == nil {
			t.Fatal("应返回 require_follow_approval 字段详情")
		}
	})

	t.Run("开启后读回为 true", func(t *testing.T) {
		body := `{"require_follow_approval":true}`
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me/follow-settings", body, "follow-token", "follow-csrf")
		if recorder.Code != http.StatusOK || bodyMap(t, recorder)["require_follow_approval"] != true {
			t.Fatalf("更新失败: 状态码 = %d body=%s", recorder.Code, recorder.Body.String())
		}
		recorder = h.performAs(t, engine, http.MethodGet, "/api/v1/users/me/follow-settings", "", "follow-token", "")
		if recorder.Code != http.StatusOK || bodyMap(t, recorder)["require_follow_approval"] != true {
			t.Fatalf("读回失败: %s", recorder.Body.String())
		}
		if !h.users.users[user.ID].RequireFollowApproval {
			t.Fatal("设置未写入仓储")
		}
	})

	t.Run("缺少 Viewer 的直接调用返回 401", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/users/me/follow-settings")
		h.handler.FollowSettings(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("FollowSettings 状态码 = %d", recorder.Code)
		}
		c, recorder = newJSONContext(http.MethodPost, "/api/v1/users/me/follow-settings", `{"require_follow_approval":true}`)
		h.handler.UpdateFollowSettings(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("UpdateFollowSettings 状态码 = %d", recorder.Code)
		}
	})

	t.Run("无效 JSON 返回 400", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me/follow-settings", `{`, "follow-token", "follow-csrf")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("读设置失败返回 500", func(t *testing.T) {
		h.users.byIDErr = fakePortErr
		t.Cleanup(func() { h.users.byIDErr = nil })
		recorder := h.performAs(t, engine, http.MethodGet, "/api/v1/users/me/follow-settings", "", "follow-token", "")
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("写设置失败返回 500", func(t *testing.T) {
		h.users.updateFollowErr = fakePortErr
		t.Cleanup(func() { h.users.updateFollowErr = nil })
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/users/me/follow-settings",
			`{"require_follow_approval":true}`, "follow-token", "follow-csrf")
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
	})
}

func TestSearchUsersHandler(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()
	h.seedUser(t, model.User{ID: 1, Username: "alice", Nickname: "爱丽丝"})
	h.seedUser(t, model.User{ID: 2, Username: "alicia", Nickname: "阿莉西亚"})
	h.seedUser(t, model.User{ID: 3, Username: "bob", Nickname: "鲍勃"})

	t.Run("返回公开资料列表与游标", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/users/search?q=ali", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		body := bodyMap(t, recorder)
		items, ok := body["items"].([]any)
		if !ok || len(items) != 2 {
			t.Fatalf("items 不符: %v", body["items"])
		}
		if body["has_more"] != false {
			t.Fatalf("has_more = %v，期望 false", body["has_more"])
		}
		first, _ := items[0].(map[string]any)
		if id, _ := first["id"].(string); id == "" {
			t.Fatalf("用户 ID 必须序列化为字符串: %v", first["id"])
		}
		for _, private := range []string{"email", "role", "status", "is_super_admin", "password_hash"} {
			if _, leaked := first[private]; leaked {
				t.Fatalf("公开搜索结果不得包含私有字段 %q: %v", private, first)
			}
		}
	})

	t.Run("limit 截断返回 has_more 与 next_cursor", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/users/search?q=ali&limit=1", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
		body := bodyMap(t, recorder)
		if body["has_more"] != true {
			t.Fatalf("has_more = %v，期望 true", body["has_more"])
		}
		if cursor, _ := body["next_cursor"].(string); cursor == "" {
			t.Fatalf("next_cursor 不能为空: %v", body["next_cursor"])
		}
	})

	t.Run("非法参数返回 400", func(t *testing.T) {
		cases := []string{
			"/api/v1/users/search?q=",
			"/api/v1/users/search?q=ali&limit=abc",
			"/api/v1/users/search?q=ali&limit=0",
			"/api/v1/users/search?q=ali&limit=51",
			"/api/v1/users/search?q=ali&cursor=bad",
		}
		for _, path := range cases {
			recorder := h.perform(t, engine, http.MethodGet, path, "")
			if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
				t.Fatalf("%s 状态码 = %d，body=%s", path, recorder.Code, recorder.Body.String())
			}
		}
	})

	t.Run("搜索未授权错误映射", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodGet, "/api/v1/users/search?q=ali")
		// 未装配仓储的零服务返回 500：验证公开接口的内部错误映射。
		New(zeroService()).SearchUsers(c)
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestGetProfileHandler(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()
	h.seedUser(t, model.User{ID: 1, Username: "alice", Nickname: "爱丽丝", Bio: "简介"})
	h.stats.value = app.ProfileStats{Posts: 3, Followers: 5, Following: 6}
	h.relations.state = model.FollowStateFollowing

	t.Run("游客视角返回统计与 none 关注态", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/users/alice", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		body := bodyMap(t, recorder)
		if body["post_count"] != float64(3) || body["following_count"] != float64(6) || body["follower_count"] != float64(5) {
			t.Fatalf("统计数据不符: %v", body)
		}
		if body["follow_state"] != model.FollowStateNone {
			t.Fatalf("游客 follow_state = %v，期望 none", body["follow_state"])
		}
		if body["bio"] != "简介" {
			t.Fatalf("bio = %v", body["bio"])
		}
		profileUser, _ := body["user"].(map[string]any)
		if profileUser["username"] != "alice" {
			t.Fatalf("user 字段不符: %v", profileUser)
		}
	})

	t.Run("登录访客返回关注状态且公开资料不含私有字段", func(t *testing.T) {
		viewer := h.seedUser(t, model.User{ID: 2, Username: "bob"})
		h.seedSession(viewer, "viewer-token", "viewer-csrf")
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/users/alice", "",
			withCookie(SessionCookieName, "viewer-token"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
		body := bodyMap(t, recorder)
		if body["follow_state"] != model.FollowStateFollowing {
			t.Fatalf("follow_state = %v，期望 following", body["follow_state"])
		}
		profileUser, _ := body["user"].(map[string]any)
		for _, private := range []string{"email", "role", "status", "is_super_admin"} {
			if _, leaked := profileUser[private]; leaked {
				t.Fatalf("公开主页不得包含私有字段 %q: %v", private, profileUser)
			}
		}
	})

	t.Run("停用账号公开资料标记 is_suspended", func(t *testing.T) {
		h.seedUser(t, model.User{ID: 3, Username: "suspended_user", Status: model.StatusSuspended})
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/users/suspended_user", "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
		profileUser, _ := bodyMap(t, recorder)["user"].(map[string]any)
		if profileUser["is_suspended"] != true {
			t.Fatalf("停用账号应标记 is_suspended: %v", profileUser)
		}
	})

	t.Run("账号不存在返回 404", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/users/nobody", "")
		if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != "not_found" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("统计读取失败返回 500", func(t *testing.T) {
		h.stats.err = fakePortErr
		t.Cleanup(func() { h.stats.err = nil })
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/users/alice", "")
		if recorder.Code != http.StatusInternalServerError || errorCode(t, recorder) != "internal_error" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("关注状态读取失败返回 500", func(t *testing.T) {
		viewer := h.seedUser(t, model.User{ID: 20, Username: "viewer2"})
		h.seedSession(viewer, "viewer2-token", "viewer2-csrf")
		h.relations.err = fakePortErr
		t.Cleanup(func() { h.relations.err = nil })
		recorder := h.perform(t, engine, http.MethodGet, "/api/v1/users/alice", "",
			withCookie(SessionCookieName, "viewer2-token"))
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("状态码 = %d", recorder.Code)
		}
	})
}
