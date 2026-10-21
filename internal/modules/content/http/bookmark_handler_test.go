package http

import (
	"net/http"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/content/app"
)

// bookmarkScopeParamOf 按 target 构造请求后解析收藏夹过滤参数。
func bookmarkScopeParamOf(t *testing.T, target string) (app.BookmarkScope, bool) {
	t.Helper()
	c, _ := newContentContext(t, http.MethodGet, target, "")
	return bookmarkScopeParam(c)
}

// TestListBookmarksRequiresLogin 校验收藏列表要求登录。
func TestListBookmarksRequiresLogin(t *testing.T) {
	handler := newContentHandler()
	c, recorder := newContentContext(t, http.MethodGet, "/api/v1/me/bookmarks", "")
	handler.ListBookmarks(c)
	requireContentStatus(t, recorder, http.StatusUnauthorized)
}

// TestListBookmarksScopeParam 校验收藏夹过滤参数的三态解析。
func TestListBookmarksScopeParam(t *testing.T) {
	cases := []struct {
		name      string
		target    string
		wantScope app.BookmarkScope
	}{
		{"缺省返回全部", "/api/v1/me/bookmarks", app.BookmarkScope{All: true}},
		{"default 只看未分组", "/api/v1/me/bookmarks?folder_id=default", app.BookmarkScope{DefaultOnly: true}},
		{"夹 ID 精确过滤", "/api/v1/me/bookmarks?folder_id=800", app.BookmarkScope{FolderID: 800}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, ok := bookmarkScopeParamOf(t, tc.target)
			if !ok || scope != tc.wantScope {
				t.Fatalf("解析结果 = (%+v, %v)，期望 %+v", scope, ok, tc.wantScope)
			}
		})
	}
}

// TestListBookmarksInvalidScope 校验非法 folder_id 返回 400。
func TestListBookmarksInvalidScope(t *testing.T) {
	handler := newContentHandler()
	c, recorder := newContentContext(t, http.MethodGet, "/api/v1/me/bookmarks?folder_id=abc", "")
	setContentViewer(c, 1)
	handler.ListBookmarks(c)
	requireContentStatus(t, recorder, http.StatusBadRequest)
	body := decodeContentError(t, recorder)
	if body.Code != "invalid_request" {
		t.Fatalf("错误码 = %q，期望 invalid_request", body.Code)
	}
}

// TestListBookmarksPortMissing 校验端口缺失时返回 500（参数全部合法后才会触达）。
func TestListBookmarksPortMissing(t *testing.T) {
	handler := newContentHandler()
	c, recorder := newContentContext(t, http.MethodGet, "/api/v1/me/bookmarks", "")
	setContentViewer(c, 1)
	handler.ListBookmarks(c)
	requireContentStatus(t, recorder, http.StatusInternalServerError)
}
