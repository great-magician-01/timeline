package http

import (
	"net/http"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/social/app"
)

// TestListFollowersHandler 验证粉丝列表参数、DTO 与错误映射。
func TestListFollowersHandler(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		f := newFixture()
		f.follows.followerRows = []app.FollowRow{{RowID: 900, UserID: uidAuthor}}
		c, recorder := newTestContext(http.MethodGet, "/api/v1/users/bob/followers")
		withParam(c, "username", "bob")
		f.handler().ListFollowers(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[listBody[userBriefResponseAlias]](t, recorder)
		if len(body.Items) != 1 || body.Items[0].ID != "2" || body.Items[0].Username != "bob" {
			t.Fatalf("粉丝列表 = %+v", body.Items)
		}
		if body.HasMore || body.NextCursor != nil {
			t.Fatalf("单页列表不应有下一页: %+v", body)
		}
	})

	t.Run("分页游标", func(t *testing.T) {
		f := newFixture()
		f.follows.followerRows = []app.FollowRow{{RowID: 900, UserID: uidAuthor}, {RowID: 800, UserID: uidOther}}
		c, recorder := newTestContext(http.MethodGet, "/api/v1/users/bob/followers?limit=1")
		withParam(c, "username", "bob")
		f.handler().ListFollowers(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[listBody[userBriefResponseAlias]](t, recorder)
		if len(body.Items) != 1 || !body.HasMore || body.NextCursor == nil {
			t.Fatalf("分页字段 = %+v", body)
		}
	})

	t.Run("limit 非法", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodGet, "/api/v1/users/bob/followers?limit=100")
		withParam(c, "username", "bob")
		f.handler().ListFollowers(c)
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, "invalid_request")
	})

	t.Run("用户不存在", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodGet, "/api/v1/users/nobody/followers")
		withParam(c, "username", "nobody")
		f.handler().ListFollowers(c)
		requireStatus(t, recorder, http.StatusNotFound)
		requireErrorCode(t, recorder, "not_found")
	})
}

// TestListFollowingHandler 验证关注列表成功响应与参数校验。
func TestListFollowingHandler(t *testing.T) {
	f := newFixture()
	f.follows.followingRows = []app.FollowRow{{RowID: 700, UserID: uidOther}}
	c, recorder := newTestContext(http.MethodGet, "/api/v1/users/bob/following")
	withParam(c, "username", "bob")
	f.handler().ListFollowing(c)
	requireStatus(t, recorder, http.StatusOK)
	body := decodeBody[listBody[userBriefResponseAlias]](t, recorder)
	if len(body.Items) != 1 || body.Items[0].Username != "carol" {
		t.Fatalf("关注列表 = %+v", body.Items)
	}

	c, recorder = newTestContext(http.MethodGet, "/api/v1/users/bob/following?cursor=bad")
	withParam(c, "username", "bob")
	f.handler().ListFollowing(c)
	requireStatus(t, recorder, http.StatusBadRequest)
	requireErrorCode(t, recorder, "invalid_request")
}

// TestFollowHandler 验证关注接口的鉴权、状态响应与错误映射。
func TestFollowHandler(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/users/bob/follow")
		withParam(c, "username", "bob")
		withViewer(c, viewerFor(uidViewer))
		f.handler().Follow(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]string](t, recorder)
		if body["follow_state"] != "following" {
			t.Fatalf("关注状态 = %q，期望 following", body["follow_state"])
		}
	})

	t.Run("审批模式返回 pending", func(t *testing.T) {
		f := newFixture()
		carol := f.users.users[uidOther]
		carol.RequireFollowApproval = true
		f.users.users[uidOther] = carol
		f.users.byName["carol"] = carol
		c, recorder := newTestContext(http.MethodPost, "/api/v1/users/carol/follow")
		withParam(c, "username", "carol")
		withViewer(c, viewerFor(uidViewer))
		f.handler().Follow(c)
		requireStatus(t, recorder, http.StatusOK)
		if body := decodeBody[map[string]string](t, recorder); body["follow_state"] != "pending" {
			t.Fatalf("关注状态 = %q，期望 pending", body["follow_state"])
		}
	})

	t.Run("未登录", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/users/bob/follow")
		withParam(c, "username", "bob")
		f.handler().Follow(c)
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireErrorCode(t, recorder, "unauthenticated")
	})

	t.Run("关注自己", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/users/alice/follow")
		withParam(c, "username", "alice")
		withViewer(c, viewerFor(uidViewer))
		f.handler().Follow(c)
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, "validation_failed")
	})
}

// TestUnfollowHandler 验证取关接口。
func TestUnfollowHandler(t *testing.T) {
	f := newFixture()
	c, recorder := newTestContext(http.MethodPost, "/api/v1/users/bob/unfollow")
	withParam(c, "username", "bob")
	withViewer(c, viewerFor(uidViewer))
	f.handler().Unfollow(c)
	requireStatus(t, recorder, http.StatusOK)
	if body := decodeBody[map[string]string](t, recorder); body["follow_state"] != "none" {
		t.Fatalf("取关状态 = %q，期望 none", body["follow_state"])
	}

	c, recorder = newTestContext(http.MethodPost, "/api/v1/users/bob/unfollow")
	withParam(c, "username", "bob")
	f.handler().Unfollow(c)
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, "unauthenticated")
}

// TestRemoveFollowerHandler 验证移除粉丝接口。
func TestRemoveFollowerHandler(t *testing.T) {
	f := newFixture()
	c, recorder := newTestContext(http.MethodPost, "/api/v1/users/bob/remove-follower")
	withParam(c, "username", "bob")
	withViewer(c, viewerFor(uidViewer))
	f.handler().RemoveFollower(c)
	requireStatus(t, recorder, http.StatusOK)
	if body := decodeBody[map[string]string](t, recorder); body["follow_state"] != "none" {
		t.Fatalf("移除粉丝状态 = %q，期望 none", body["follow_state"])
	}

	c, recorder = newTestContext(http.MethodPost, "/api/v1/users/bob/remove-follower")
	withParam(c, "username", "bob")
	f.handler().RemoveFollower(c)
	requireStatus(t, recorder, http.StatusUnauthorized)
	requireErrorCode(t, recorder, "unauthenticated")
}
