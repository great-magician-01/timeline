package httpx

import (
	"net/http"
	"testing"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestViewerContextRoundTrip(t *testing.T) {
	c, _ := newTestContext()
	if _, ok := Viewer(c); ok {
		t.Fatal("未写入时 Viewer 应返回 false")
	}

	viewer := model.Viewer{ID: 42, Authenticated: true, IsAdmin: true, IsSuperAdmin: true}
	SetViewer(c, viewer)
	got, ok := Viewer(c)
	if !ok || got != viewer {
		t.Fatalf("Viewer=%+v ok=%v", got, ok)
	}

	if required, ok := RequireViewer(c); !ok || required.ID != 42 {
		t.Fatalf("RequireViewer=%+v ok=%v", required, ok)
	}
}

func TestRequireViewerRejectsGuest(t *testing.T) {
	c, recorder := newTestContext()
	SetViewer(c, model.GuestViewer())
	if _, ok := RequireViewer(c); ok {
		t.Fatal("游客不应通过 RequireViewer")
	}
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
	body := errorBody(t, recorder)
	if body["code"] != "unauthenticated" {
		t.Fatalf("code=%v", body["code"])
	}
}

func TestSessionContextRoundTrip(t *testing.T) {
	c, _ := newTestContext()
	if _, ok := Session(c); ok {
		t.Fatal("未写入时 Session 应返回 false")
	}

	session := model.Session{ID: 7, UserID: 42, CSRFToken: "token"}
	SetSession(c, session)
	got, ok := Session(c)
	if !ok || got.CSRFToken != "token" {
		t.Fatalf("Session=%+v ok=%v", got, ok)
	}
	if required, ok := RequireSession(c); !ok || required.ID != 7 {
		t.Fatalf("RequireSession=%+v ok=%v", required, ok)
	}
}

func TestRequireSessionMissing(t *testing.T) {
	c, recorder := newTestContext()
	if _, ok := RequireSession(c); ok {
		t.Fatal("缺失会话不应通过 RequireSession")
	}
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
	if !c.IsAborted() {
		t.Fatal("RequireSession 必须中止后续 handler")
	}
}

func TestSetViewerOverridesPreviousValue(t *testing.T) {
	c, _ := newTestContext()
	SetViewer(c, model.Viewer{ID: 1, Authenticated: true})
	SetViewer(c, model.Viewer{ID: 2, Authenticated: true})
	got, ok := Viewer(c)
	if !ok || got.ID != 2 {
		t.Fatalf("后写入的 Viewer 应覆盖前值: %+v", got)
	}
}
