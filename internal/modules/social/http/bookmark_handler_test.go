package http

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/app"
	"github.com/great-magician-01/timeline/internal/shared/format"
)

// TestBookmarkPostHandler 验证收藏接口的响应形状与 folder_id 解析。
func TestBookmarkPostHandler(t *testing.T) {
	t.Run("无请求体收藏到默认夹", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/bookmark")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().BookmarkPost(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["bookmarked"] != true {
			t.Fatalf("响应 = %+v，期望 bookmarked=true", body)
		}
		if _, ok := body["folder_id"]; !ok {
			t.Fatalf("响应应包含 folder_id 字段: %+v", body)
		}
	})

	t.Run("显式 null 归属默认夹", func(t *testing.T) {
		f := newFixture()
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/posts/20/bookmark", `{"folder_id":null}`)
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().BookmarkPost(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["folder_id"] != nil {
			t.Fatalf("folder_id = %v，期望 null", body["folder_id"])
		}
	})

	t.Run("指定收藏夹回显字符串 ID", func(t *testing.T) {
		f := newFixture()
		f.folders.folderID = 800
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/posts/20/bookmark", `{"folder_id":"800"}`)
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().BookmarkPost(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["folder_id"] != "800" {
			t.Fatalf("folder_id = %v，期望 \"800\"（雪花 ID 字符串）", body["folder_id"])
		}
	})

	t.Run("非法 folder_id 返回 400", func(t *testing.T) {
		f := newFixture()
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/posts/20/bookmark", `{"folder_id":"abc"}`)
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().BookmarkPost(c)
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, "invalid_request")
	})

	t.Run("非法 JSON 体返回 400", func(t *testing.T) {
		f := newFixture()
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/posts/20/bookmark", `{oops}`)
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().BookmarkPost(c)
		requireStatus(t, recorder, http.StatusBadRequest)
		requireErrorCode(t, recorder, "invalid_request")
	})

	t.Run("未登录返回 401", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/bookmark")
		withParam(c, "id", "20")
		f.handler().BookmarkPost(c)
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireErrorCode(t, recorder, "unauthenticated")
	})

	t.Run("领域错误映射", func(t *testing.T) {
		f := newFixture()
		f.bookmarks.upsertErr = errors.New("db down")
		c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/bookmark")
		withParam(c, "id", "20")
		withViewer(c, viewerFor(uidViewer))
		f.handler().BookmarkPost(c)
		requireStatus(t, recorder, http.StatusInternalServerError)
		requireErrorCode(t, recorder, "internal_error")
	})
}

// TestUnbookmarkPostHandler 验证取消收藏响应。
func TestUnbookmarkPostHandler(t *testing.T) {
	f := newFixture()
	c, recorder := newTestContext(http.MethodPost, "/api/v1/posts/20/unbookmark")
	withParam(c, "id", "20")
	withViewer(c, viewerFor(uidViewer))
	f.handler().UnbookmarkPost(c)
	requireStatus(t, recorder, http.StatusOK)
	body := decodeBody[map[string]any](t, recorder)
	if body["bookmarked"] != false {
		t.Fatalf("响应 = %+v，期望 bookmarked=false", body)
	}
}

// TestBookmarkFolderHandlers 验证收藏夹 CRUD 接口。
func TestBookmarkFolderHandlers(t *testing.T) {
	t.Run("列表", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodGet, "/api/v1/me/bookmark-folders")
		withViewer(c, viewerFor(uidViewer))
		f.handler().ListBookmarkFolders(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		items := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("收藏夹数量 = %d，期望 1", len(items))
		}
		first := items[0].(map[string]any)
		if first["id"] != "800" || first["name"] != "默认夹" {
			t.Fatalf("首项 = %+v，期望 800/默认夹", first)
		}
		if _, ok := first["bookmark_count"]; !ok {
			t.Fatalf("收藏夹应包含 bookmark_count 字段: %+v", first)
		}
		if _, err := time.Parse(time.RFC3339, first["created_at"].(string)); err != nil {
			t.Fatalf("created_at 应为 RFC3339: %v", first["created_at"])
		}
	})

	t.Run("创建", func(t *testing.T) {
		f := newFixture()
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/me/bookmark-folders", `{"name":"技术文章"}`)
		withViewer(c, viewerFor(uidViewer))
		f.handler().CreateBookmarkFolder(c)
		requireStatus(t, recorder, http.StatusCreated)
		body := decodeBody[map[string]any](t, recorder)
		if body["name"] != "技术文章" {
			t.Fatalf("响应 = %+v，期望 技术文章", body)
		}
	})

	t.Run("创建缺名称返回 400", func(t *testing.T) {
		f := newFixture()
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/me/bookmark-folders", `{"name":"  "}`)
		withViewer(c, viewerFor(uidViewer))
		f.handler().CreateBookmarkFolder(c)
		requireStatus(t, recorder, http.StatusBadRequest)
	})

	t.Run("重命名", func(t *testing.T) {
		f := newFixture()
		c, recorder := newJSONContext(http.MethodPost, "/api/v1/me/bookmark-folders/800", `{"name":"新名字"}`)
		withParam(c, "id", "800")
		withViewer(c, viewerFor(uidViewer))
		f.handler().RenameBookmarkFolder(c)
		requireStatus(t, recorder, http.StatusOK)
		body := decodeBody[map[string]any](t, recorder)
		if body["name"] != "新名字" {
			t.Fatalf("响应 = %+v，期望 新名字", body)
		}
	})

	t.Run("删除返回 204", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodPost, "/api/v1/me/bookmark-folders/800/delete")
		withParam(c, "id", "800")
		withViewer(c, viewerFor(uidViewer))
		f.handler().DeleteBookmarkFolder(c)
		// gin 的 c.Status 延迟写头，单元测试断言 gin Writer 的状态码。
		if c.Writer.Status() != http.StatusNoContent {
			t.Fatalf("状态码 = %d，期望 204", c.Writer.Status())
		}
		if recorder.Body.Len() != 0 {
			t.Fatalf("204 响应不应有 body: %s", recorder.Body.String())
		}
	})

	t.Run("未登录返回 401", func(t *testing.T) {
		f := newFixture()
		c, recorder := newTestContext(http.MethodGet, "/api/v1/me/bookmark-folders")
		f.handler().ListBookmarkFolders(c)
		requireStatus(t, recorder, http.StatusUnauthorized)
	})
}

// TestBookmarkFolderTimeFormat 校验收藏夹时间输出使用北京时间 RFC3339。
func TestBookmarkFolderTimeFormat(t *testing.T) {
	resp := bookmarkFolderResponseFor(app.BookmarkFolder{
		ID:            800,
		Name:          "夹",
		BookmarkCount: 1,
		CreatedAt:     testNow,
		UpdatedAt:     testNow,
	})
	if resp.CreatedAt != format.FormatTime(testNow) {
		t.Fatalf("created_at = %q，期望 %q", resp.CreatedAt, format.FormatTime(testNow))
	}
}
