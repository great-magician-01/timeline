package http

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/app"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// pendingRequest 构造一条待处理的关注申请行。
func pendingRequest() app.RequestRow {
	return app.RequestRow{
		ID:          700,
		RequesterID: uidViewer,
		TargetID:    uidAuthor,
		Status:      model.FollowRequestPending,
		CreatedAt:   testNow.Add(-time.Hour),
	}
}

// TestListFollowRequestsHandlerParams 验证申请列表的鉴权、参数与错误映射。
func TestListFollowRequestsHandlerParams(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		withViewer bool
		wantStatus int
		wantCode   string
		setup      func(*fixture)
	}{
		{"未登录", "/api/v1/follow-requests", false, http.StatusUnauthorized, "unauthenticated", nil},
		{"limit 非法", "/api/v1/follow-requests?limit=0", true, http.StatusBadRequest, "invalid_request", nil},
		{"游标非法", "/api/v1/follow-requests?cursor=bad", true, http.StatusBadRequest, "invalid_request", nil},
		{"direction 非法", "/api/v1/follow-requests?direction=sideways", true, http.StatusBadRequest, "invalid_request", nil},
		{
			name:       "服务错误映射 500",
			target:     "/api/v1/follow-requests",
			withViewer: true,
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_error",
			setup:      func(f *fixture) { f.requests.incomingErr = errors.New("数据库连接中断") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			if tc.setup != nil {
				tc.setup(f)
			}
			c, recorder := newTestContext(http.MethodGet, tc.target)
			if tc.withViewer {
				withViewer(c, viewerFor(uidViewer))
			}
			f.handler().ListFollowRequests(c)
			requireStatus(t, recorder, tc.wantStatus)
			requireErrorCode(t, recorder, tc.wantCode)
		})
	}
}

// TestListFollowRequestsHandlerSuccess 验证申请列表 JSON 字段与方向。
func TestListFollowRequestsHandlerSuccess(t *testing.T) {
	t.Run("incoming", func(t *testing.T) {
		f := newFixture()
		row := pendingRequest()
		f.requests.incoming = []app.RequestRow{row}
		c, recorder := newTestContext(http.MethodGet, "/api/v1/follow-requests")
		withViewer(c, viewerFor(uidAuthor))
		f.handler().ListFollowRequests(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[listBody[followRequestResponse]](t, recorder)
		if len(body.Items) != 1 || body.HasMore || body.NextCursor != nil {
			t.Fatalf("列表结构 = %+v", body)
		}
		item := body.Items[0]
		if item.ID != "700" || item.Status != model.FollowRequestPending {
			t.Fatalf("申请项 = %+v", item)
		}
		if item.Requester.Username != "alice" || item.Target.Username != "bob" {
			t.Fatalf("申请双方 = (%q, %q)", item.Requester.Username, item.Target.Username)
		}
		if item.CreatedAt != format.FormatTime(row.CreatedAt) {
			t.Fatalf("创建时间 = %q，期望 %q", item.CreatedAt, format.FormatTime(row.CreatedAt))
		}
		if item.ResolvedAt != nil {
			t.Fatalf("待处理申请不应有处理时间: %v", item.ResolvedAt)
		}
	})

	t.Run("outgoing 带处理时间", func(t *testing.T) {
		f := newFixture()
		resolvedAt := testNow.Add(-time.Minute)
		row := pendingRequest()
		row.ResolvedAt = &resolvedAt
		f.requests.outgoing = []app.RequestRow{row}
		c, recorder := newTestContext(http.MethodGet, "/api/v1/follow-requests?direction=outgoing")
		withViewer(c, viewerFor(uidViewer))
		f.handler().ListFollowRequests(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[listBody[followRequestResponse]](t, recorder)
		if len(body.Items) != 1 || body.Items[0].ResolvedAt == nil {
			t.Fatalf("outgoing 列表 = %+v，期望携带处理时间", body.Items)
		}
		if *body.Items[0].ResolvedAt != format.FormatTime(resolvedAt) {
			t.Fatalf("处理时间 = %q，期望 %q", *body.Items[0].ResolvedAt, format.FormatTime(resolvedAt))
		}
	})
}

// TestApproveFollowRequestHandler 验证批准申请的成功、冲突与找不到。
func TestApproveFollowRequestHandler(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		f := newFixture()
		f.requests.row = pendingRequest()
		f.requests.hasRow = true
		c, recorder := newTestContext(http.MethodPost, "/api/v1/follow-requests/700/approve")
		withParam(c, "id", "700")
		withViewer(c, viewerFor(uidAuthor))
		f.handler().ApproveFollowRequest(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]string](t, recorder)
		if body["status"] != model.FollowRequestApproved || body["follow_state"] != model.FollowStateFollowing {
			t.Fatalf("批准响应 = %+v", body)
		}
		if f.requests.updatedState != model.FollowRequestApproved {
			t.Fatalf("状态迁移 = %q，期望 approved", f.requests.updatedState)
		}
	})

	t.Run("状态冲突", func(t *testing.T) {
		f := newFixture()
		row := pendingRequest()
		row.Status = model.FollowRequestRejected
		f.requests.row = row
		f.requests.hasRow = true
		c, recorder := newTestContext(http.MethodPost, "/api/v1/follow-requests/700/approve")
		withParam(c, "id", "700")
		withViewer(c, viewerFor(uidAuthor))
		f.handler().ApproveFollowRequest(c)
		requireStatus(t, recorder, http.StatusConflict)
		requireErrorCode(t, recorder, "request_resolved")
	})

	t.Run("申请不存在", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/follow-requests/700/approve")
		withParam(c, "id", "700")
		withViewer(c, viewerFor(uidAuthor))
		f.handler().ApproveFollowRequest(c)
		requireStatus(t, recorder, http.StatusNotFound)
		requireErrorCode(t, recorder, "not_found")
	})

	t.Run("未登录", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/follow-requests/700/approve")
		withParam(c, "id", "700")
		f.handler().ApproveFollowRequest(c)
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireErrorCode(t, recorder, "unauthenticated")
	})

	t.Run("ID 非法", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/follow-requests/abc/approve")
		withParam(c, "id", "abc")
		withViewer(c, viewerFor(uidAuthor))
		f.handler().ApproveFollowRequest(c)
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, "invalid_request")
	})
}

// TestRejectFollowRequestHandler 验证拒绝申请响应。
func TestRejectFollowRequestHandler(t *testing.T) {
	f := newFixture()
	f.requests.row = pendingRequest()
	f.requests.hasRow = true
	c, recorder := newTestContext(http.MethodPost, "/api/v1/follow-requests/700/reject")
	withParam(c, "id", "700")
	withViewer(c, viewerFor(uidAuthor))
	f.handler().RejectFollowRequest(c)
	requireStatus(t, recorder, http.StatusOK)
	body := decodeBody[map[string]string](t, recorder)
	if body["status"] != model.FollowRequestRejected || body["follow_state"] != model.FollowStateNone {
		t.Fatalf("拒绝响应 = %+v", body)
	}
	if f.requests.updatedState != model.FollowRequestRejected {
		t.Fatalf("状态迁移 = %q，期望 rejected", f.requests.updatedState)
	}
}

// TestCancelFollowRequestHandler 验证取消自己发出的申请。
func TestCancelFollowRequestHandler(t *testing.T) {
	f := newFixture()
	f.requests.row = pendingRequest()
	f.requests.hasRow = true
	c, recorder := newTestContext(http.MethodPost, "/api/v1/follow-requests/700/cancel")
	withParam(c, "id", "700")
	withViewer(c, viewerFor(uidViewer))
	f.handler().CancelFollowRequest(c)
	requireStatus(t, recorder, http.StatusOK)
	body := decodeBody[map[string]string](t, recorder)
	if body["status"] != model.FollowRequestCanceled || body["follow_state"] != model.FollowStateNone {
		t.Fatalf("取消响应 = %+v", body)
	}
	if f.requests.updatedState != model.FollowRequestCanceled {
		t.Fatalf("状态迁移 = %q，期望 canceled", f.requests.updatedState)
	}
}
