package social

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/ratelimit"
)

func newTestContext(target string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	return c
}

func TestTrimPage(t *testing.T) {
	cases := []struct {
		name     string
		count    int
		limit    int
		wantLen  int
		wantMore bool
	}{
		{name: "多查一条说明还有下一页", count: 3, limit: 2, wantLen: 2, wantMore: true},
		{name: "恰好一页没有下一页", count: 3, limit: 3, wantLen: 3, wantMore: false},
		{name: "不足一页", count: 3, limit: 5, wantLen: 3, wantMore: false},
		{name: "空结果", count: 0, limit: 5, wantLen: 0, wantMore: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]int64, tc.count)
			for i := range items {
				items[i] = int64(i + 1)
			}
			got, hasMore := trimPage(items, tc.limit)
			if len(got) != tc.wantLen || hasMore != tc.wantMore {
				t.Fatalf("trimPage(len=%d, limit=%d) = (%d, %v)，期望 (%d, %v)",
					tc.count, tc.limit, len(got), hasMore, tc.wantLen, tc.wantMore)
			}
			if hasMore && len(got) > 0 && got[len(got)-1] != int64(tc.limit) {
				t.Fatalf("截断后最后一条 = %d，期望 %d", got[len(got)-1], tc.limit)
			}
		})
	}
}

func TestListParamsValidation(t *testing.T) {
	cases := []struct {
		name       string
		target     string
		wantLimit  int
		wantCursor int64
		wantErr    bool
	}{
		{name: "默认 limit 与空游标", target: "/x", wantLimit: 20, wantCursor: 0},
		{name: "指定 limit", target: "/x?limit=7", wantLimit: 7, wantCursor: 0},
		{name: "limit 超上限", target: "/x?limit=51", wantErr: true},
		{name: "limit 为零", target: "/x?limit=0", wantErr: true},
		{name: "limit 非数字", target: "/x?limit=abc", wantErr: true},
		{name: "有效游标", target: "/x?cursor=" + api.EncodeCursor(987654321), wantLimit: 20, wantCursor: 987654321},
		{name: "无效游标", target: "/x?cursor=not-a-cursor", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, cursor, err := listParams(newTestContext(tc.target))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，得到 limit=%d cursor=%d", limit, cursor)
				}
				return
			}
			if err != nil {
				t.Fatalf("未期望报错：%v", err)
			}
			if limit != tc.wantLimit || cursor != tc.wantCursor {
				t.Fatalf("得到 (limit=%d, cursor=%d)，期望 (%d, %d)",
					limit, cursor, tc.wantLimit, tc.wantCursor)
			}
		})
	}
}

func TestOptionalIDFromBody(t *testing.T) {
	blank := ""
	spaces := "   "
	valid := "1234567890123456789"
	zero := "0"
	negative := "-12"
	invalid := "abc"

	if id, err := optionalIDFromBody(nil); id != nil || err != nil {
		t.Fatalf("nil 应视为未提供，得到 (%v, %v)", id, err)
	}
	if id, err := optionalIDFromBody(&blank); id != nil || err != nil {
		t.Fatalf("空字符串应视为未提供，得到 (%v, %v)", id, err)
	}
	if id, err := optionalIDFromBody(&spaces); id != nil || err != nil {
		t.Fatalf("空白字符串应视为未提供，得到 (%v, %v)", id, err)
	}
	id, err := optionalIDFromBody(&valid)
	if err != nil || id == nil || *id != 1234567890123456789 {
		t.Fatalf("合法 ID 解析失败：(%v, %v)", id, err)
	}
	for name, raw := range map[string]*string{"零": &zero, "负数": &negative, "非数字": &invalid} {
		if _, err := optionalIDFromBody(raw); err == nil {
			t.Fatalf("%s 应解析失败", name)
		}
	}
}

func TestThreadPosition(t *testing.T) {
	parent, root := threadPosition(42, nil)
	if parent == nil || root == nil || *parent != 42 || *root != 42 {
		t.Fatalf("顶层评论回复的 root 应等于父评论 ID，得到 parent=%v root=%v", parent, root)
	}
	parentRoot := int64(7)
	parent, root = threadPosition(42, &parentRoot)
	if parent == nil || root == nil || *parent != 42 || *root != 7 {
		t.Fatalf("楼中楼回复应继承父评论的 root，得到 parent=%v root=%v", parent, root)
	}
}

func TestNotifyKeys(t *testing.T) {
	if got := notifyKey("post_like", 100); got != "post_like:100" {
		t.Fatalf("notifyKey = %q", got)
	}
	if notifyKey("post_like", 100) == notifyKey("comment_like", 100) {
		t.Fatal("不同事件类型的幂等键必须不同")
	}
	if notifyKey("reply", 1) == notifyKey("reply", 2) {
		t.Fatal("不同目标的幂等键必须不同")
	}

	first := time.Unix(1700000000, 0)
	key := followNotifyKey(55, first)
	if key != "follow:55:1700000000" {
		t.Fatalf("followNotifyKey = %q", key)
	}
	if followNotifyKey(55, first) != key {
		t.Fatal("同一事件的幂等键必须稳定")
	}
	if followNotifyKey(55, first.Add(time.Second)) == key {
		t.Fatal("重新关注（新的生效时间）必须使用新的幂等键")
	}
}

func TestRequestTransition(t *testing.T) {
	cases := []struct {
		name        string
		current     string
		action      string
		wantNext    string
		wantChanged bool
		wantErr     bool
	}{
		{name: "待处理批准", current: model.FollowRequestPending, action: actionApprove, wantNext: model.FollowRequestApproved, wantChanged: true},
		{name: "重复批准幂等", current: model.FollowRequestApproved, action: actionApprove, wantNext: model.FollowRequestApproved},
		{name: "待处理拒绝", current: model.FollowRequestPending, action: actionReject, wantNext: model.FollowRequestRejected, wantChanged: true},
		{name: "重复拒绝幂等", current: model.FollowRequestRejected, action: actionReject, wantNext: model.FollowRequestRejected},
		{name: "待处理取消", current: model.FollowRequestPending, action: actionCancel, wantNext: model.FollowRequestCanceled, wantChanged: true},
		{name: "重复取消幂等", current: model.FollowRequestCanceled, action: actionCancel, wantNext: model.FollowRequestCanceled},
		{name: "已拒绝不能再批准", current: model.FollowRequestRejected, action: actionApprove, wantErr: true},
		{name: "已批准不能再拒绝", current: model.FollowRequestApproved, action: actionReject, wantErr: true},
		{name: "已批准不能再取消", current: model.FollowRequestApproved, action: actionCancel, wantErr: true},
		{name: "未知动作", current: model.FollowRequestPending, action: "explode", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, changed, err := requestTransition(tc.current, tc.action)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，得到 (%q, %v)", next, changed)
				}
				return
			}
			if err != nil {
				t.Fatalf("未期望报错：%v", err)
			}
			if next != tc.wantNext || changed != tc.wantChanged {
				t.Fatalf("得到 (%q, %v)，期望 (%q, %v)", next, changed, tc.wantNext, tc.wantChanged)
			}
		})
	}
}

func TestCountDelta(t *testing.T) {
	cases := []struct {
		was, now bool
		want     int
	}{
		{was: false, now: true, want: 1},
		{was: true, now: false, want: -1},
		{was: true, now: true, want: 0},
		{was: false, now: false, want: 0},
	}
	for _, tc := range cases {
		if got := countDelta(tc.was, tc.now); got != tc.want {
			t.Fatalf("countDelta(%v, %v) = %d，期望 %d", tc.was, tc.now, got, tc.want)
		}
	}
}

func TestValidateCommentBody(t *testing.T) {
	cases := []struct {
		name     string
		safeHTML string
		plain    string
		wantErr  bool
	}{
		{name: "空正文", safeHTML: "", plain: "", wantErr: true},
		{name: "只有空白", safeHTML: "   ", plain: "\n  ", wantErr: true},
		{name: "空标签", safeHTML: "<p></p>", plain: "", wantErr: true},
		{name: "正常文本", safeHTML: "<p>你好</p>", plain: "你好"},
		{name: "仅图片", safeHTML: `<p><img src="/api/v1/attachments/1/content" alt=""></p>`, plain: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCommentBody(tc.safeHTML, tc.plain)
			if tc.wantErr && err == nil {
				t.Fatal("期望 validation_failed")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("未期望报错：%v", err)
			}
		})
	}
}

func TestCommentResponseFor(t *testing.T) {
	created := time.Date(2026, 10, 17, 20, 30, 0, 0, time.Local)
	parentID := int64(11)
	rootID := int64(10)
	comment := model.Comment{
		ID:              12,
		PostID:          100,
		AuthorID:        5,
		ParentCommentID: &parentID,
		RootCommentID:   &rootID,
		ContentHTML:     "<p>回复</p>",
		Status:          model.CommentStatusPublished,
		LikeCount:       3,
		ReplyCount:      1,
		CreatedAt:       created,
	}
	author := model.UserBrief{ID: 5, Username: "alice", Nickname: "Alice"}
	postAuthorID := int64(9)

	authorViewer := model.Viewer{ID: 5, Authenticated: true}
	resp := commentResponseFor(comment, postAuthorID, author, authorViewer, true)
	if resp.ID != "12" || resp.PostID != "100" || resp.Author.Username != "alice" {
		t.Fatalf("基本字段组装错误：%+v", resp)
	}
	if resp.ParentCommentID == nil || *resp.ParentCommentID != "11" {
		t.Fatalf("parent_comment_id 组装错误：%v", resp.ParentCommentID)
	}
	if resp.RootCommentID == nil || *resp.RootCommentID != "10" {
		t.Fatalf("root_comment_id 组装错误：%v", resp.RootCommentID)
	}
	if !resp.LikedByMe || !resp.CanDelete {
		t.Fatalf("评论作者应可删除且保留点赞态：%+v", resp)
	}
	if resp.CreatedAt != api.FormatTime(created) {
		t.Fatalf("created_at = %q，期望 %q", resp.CreatedAt, api.FormatTime(created))
	}
	if resp.ParentCommentID != nil && resp.ReplyPreview != nil {
		t.Fatal("单条组装不应填充 reply_preview")
	}

	postAuthorViewer := model.Viewer{ID: 9, Authenticated: true}
	if got := commentResponseFor(comment, postAuthorID, author, postAuthorViewer, false); !got.CanDelete {
		t.Fatal("帖子作者应可删除评论")
	}
	otherViewer := model.Viewer{ID: 77, Authenticated: true}
	if got := commentResponseFor(comment, postAuthorID, author, otherViewer, false); got.CanDelete {
		t.Fatal("普通用户不能删除他人评论")
	}
	adminViewer := model.Viewer{ID: 88, IsAdmin: true, Authenticated: true}
	if got := commentResponseFor(comment, postAuthorID, author, adminViewer, false); !got.CanDelete {
		t.Fatal("管理员应可删除评论")
	}
	if got := commentResponseFor(comment, postAuthorID, author, model.GuestViewer(), false); got.CanDelete {
		t.Fatal("游客不能删除评论")
	}

	deleted := comment
	deleted.Status = model.CommentStatusDeleted
	deletedResp := commentResponseFor(deleted, postAuthorID, author, authorViewer, true)
	if deletedResp.Status != model.CommentStatusDeleted {
		t.Fatalf("status = %q", deletedResp.Status)
	}
	if deletedResp.ContentHTML != "" {
		t.Fatalf("已删除评论正文必须为空，得到 %q", deletedResp.ContentHTML)
	}
	if deletedResp.LikedByMe {
		t.Fatal("已删除评论的 liked_by_me 必须为 false")
	}
	if deletedResp.CanDelete {
		t.Fatal("已删除评论不能再次删除")
	}
	if deletedResp.LikeCount != comment.LikeCount {
		t.Fatalf("已删除评论应保留点赞数，得到 %d", deletedResp.LikeCount)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	cases := []struct {
		name  string
		after time.Duration
		want  int
	}{
		{name: "整秒", after: 42 * time.Second, want: 42},
		{name: "小数秒向上取整", after: 1500 * time.Millisecond, want: 2},
		{name: "零值兜底为 1", after: 0, want: 1},
		{name: "负值兜底为 1", after: -time.Second, want: 1},
		{name: "亚秒向上取整", after: 10 * time.Millisecond, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAfterSeconds(tc.after); got != tc.want {
				t.Fatalf("retryAfterSeconds(%s) = %d，期望 %d", tc.after, got, tc.want)
			}
		})
	}
}

// TestAllowActionSetsRetryAfter 验证互动限频超限时返回 429 且带 Retry-After 头。
func TestAllowActionSetsRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &Service{}
	limiter := ratelimit.New(1, time.Minute)

	first, _ := gin.CreateTestContext(httptest.NewRecorder())
	first.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	if !service.allowAction(first, limiter, 42) {
		t.Fatal("首次操作应放行")
	}

	recorder := httptest.NewRecorder()
	second, _ := gin.CreateTestContext(recorder)
	second.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	if service.allowAction(second, limiter, 42) {
		t.Fatal("超过阈值应被限频")
	}
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("状态码 = %d，期望 429", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("429 响应必须带 Retry-After")
	}
}
