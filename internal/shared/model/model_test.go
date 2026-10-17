package model

import (
	"testing"
	"time"
)

func TestConstantsMatchDatabaseContract(t *testing.T) {
	cases := map[string]string{
		"RoleUser":               RoleUser,
		"RoleAdmin":              RoleAdmin,
		"StatusActive":           StatusActive,
		"StatusSuspended":        StatusSuspended,
		"PostStatusDraft":        PostStatusDraft,
		"PostStatusScheduled":    PostStatusScheduled,
		"PostStatusPublished":    PostStatusPublished,
		"PostStatusDeleted":      PostStatusDeleted,
		"VisibilityPublic":       VisibilityPublic,
		"VisibilityFollowers":    VisibilityFollowers,
		"VisibilityPrivate":      VisibilityPrivate,
		"CommentStatusPublished": CommentStatusPublished,
		"CommentStatusDeleted":   CommentStatusDeleted,
		"AttachmentAvailable":    AttachmentAvailable,
		"AttachmentDeleted":      AttachmentDeleted,
		"DerivativeThumbnail":    DerivativeThumbnail,
		"DerivativeVideoCover":   DerivativeVideoCover,
		"DerivativeReady":        DerivativeReady,
		"FollowRequestPending":   FollowRequestPending,
		"FollowRequestApproved":  FollowRequestApproved,
		"FollowRequestRejected":  FollowRequestRejected,
		"FollowRequestCanceled":  FollowRequestCanceled,
		"FollowStateNone":        FollowStateNone,
		"FollowStateFollowing":   FollowStateFollowing,
		"FollowStatePending":     FollowStatePending,
		"FollowStateIncoming":    FollowStateIncomingPending,
		"NoticeTypePostLike":     NoticeTypePostLike,
		"NoticeTypeCommentLike":  NoticeTypeCommentLike,
		"NoticeTypeComment":      NoticeTypeComment,
		"NoticeTypeReply":        NoticeTypeReply,
		"NoticeTypeRepost":       NoticeTypeRepost,
		"NoticeTypeQuote":        NoticeTypeQuote,
		"NoticeTypeFollow":       NoticeTypeFollow,
		"NoticeTypeFollowReq":    NoticeTypeFollowRequest,
		"NoticeTypeFollowAppr":   NoticeTypeFollowApprove,
		"NoticeTypeMention":      NoticeTypeMention,
		"NoticeSourcePost":       NoticeSourcePost,
		"NoticeSourceComment":    NoticeSourceComment,
		"NoticeTargetPost":       NoticeTargetPost,
		"NoticeTargetComment":    NoticeTargetComment,
		"NoticeTargetUser":       NoticeTargetUser,
	}
	for name, got := range cases {
		if got == "" {
			t.Errorf("常量 %s 不能为空", name)
		}
	}
	// 关键枚举值必须与旧实现/数据库 CHECK 约束一致。
	if RoleUser != "user" || RoleAdmin != "admin" {
		t.Fatalf("角色常量不兼容: %q/%q", RoleUser, RoleAdmin)
	}
	if FollowStateIncomingPending != "incoming_pending" {
		t.Fatalf("关注状态常量不兼容: %q", FollowStateIncomingPending)
	}
	if NoticeTypeFollowApprove != "follow_approved" {
		t.Fatalf("通知类型常量不兼容: %q", NoticeTypeFollowApprove)
	}
	if DerivativeVideoCover != "video_cover" {
		t.Fatalf("衍生文件类型常量不兼容: %q", DerivativeVideoCover)
	}
}

func TestUserPredicates(t *testing.T) {
	active := User{Status: StatusActive}
	if active.IsSuspended() || !active.IsActive() {
		t.Fatalf("正常账号判定错误")
	}
	suspended := User{Status: StatusSuspended}
	if !suspended.IsSuspended() || suspended.IsActive() {
		t.Fatalf("停用账号判定错误")
	}
	if !(&User{Role: RoleAdmin}).IsAdmin() {
		t.Fatalf("管理员角色判定错误")
	}
	if (&User{Role: RoleUser}).IsAdmin() {
		t.Fatalf("普通用户不应判定为管理员")
	}
	if (&User{IsAI: true}).IsHuman() {
		t.Fatalf("AI 账号不是人类身份")
	}
}

func TestViewerFrom(t *testing.T) {
	admin := ViewerFrom(User{ID: 7, Role: RoleAdmin, Status: StatusActive})
	if !admin.Authenticated || !admin.IsAdmin || admin.IsAI || admin.ID != 7 {
		t.Fatalf("管理员 Viewer 映射错误: %+v", admin)
	}
	super := ViewerFrom(User{ID: 8, Role: RoleAdmin, IsSuperAdmin: true, Status: StatusActive})
	if !super.IsSuperAdmin {
		t.Fatalf("超管标记丢失: %+v", super)
	}
	aiAdmin := ViewerFrom(User{ID: 9, Role: RoleAdmin, IsSuperAdmin: true, IsAI: true, Status: StatusActive})
	if aiAdmin.IsAdmin || aiAdmin.IsSuperAdmin {
		t.Fatalf("AI 账号不得获得管理员/超管权限: %+v", aiAdmin)
	}
	blocked := ViewerFrom(User{ID: 10, Role: RoleUser, Status: StatusSuspended})
	if !blocked.IsSuspended || blocked.IsAdmin {
		t.Fatalf("停用账号 Viewer 映射错误: %+v", blocked)
	}
	guest := GuestViewer()
	if guest.Authenticated || !guest.IsGuest() || guest.IsAdmin {
		t.Fatalf("游客 Viewer 映射错误: %+v", guest)
	}
}

func TestUserBriefFromUser(t *testing.T) {
	brief := UserBriefFromUser(User{
		ID: 3, Username: "alice", Nickname: "爱丽丝", AvatarURL: "/a.png",
		IsAI: true, Status: StatusSuspended,
	})
	if brief.ID != 3 || brief.Username != "alice" || brief.Nickname != "爱丽丝" ||
		brief.AvatarURL != "/a.png" || !brief.IsAI || !brief.IsSuspended {
		t.Fatalf("作者摘要组装错误: %+v", brief)
	}
}

func TestPostSelfPosted(t *testing.T) {
	post := Post{ID: 1, AuthorID: 5}
	if !post.SelfPosted(Viewer{ID: 5, Authenticated: true}) {
		t.Fatalf("作者本人应判定为自发帖")
	}
	if post.SelfPosted(Viewer{ID: 6, Authenticated: true}) {
		t.Fatalf("非作者不满足 SelfPosted")
	}
	if post.SelfPosted(GuestViewer()) {
		t.Fatalf("游客不满足 SelfPosted")
	}
	if post.SelfPosted(Viewer{ID: 5}) {
		t.Fatalf("未认证身份不满足 SelfPosted")
	}
}

func TestCommentDeletedAndAttachmentAvailable(t *testing.T) {
	if !(&Comment{Status: CommentStatusDeleted}).Deleted() {
		t.Fatalf("已删除评论判定错误")
	}
	if (&Comment{Status: CommentStatusPublished}).Deleted() {
		t.Fatalf("已发布评论不应判定为删除")
	}
	if !(&Attachment{Status: AttachmentAvailable}).Available() {
		t.Fatalf("有效附件判定错误")
	}
	if (&Attachment{Status: AttachmentDeleted}).Available() {
		t.Fatalf("已失效附件不应判定为可用")
	}
}

func TestSessionRevoked(t *testing.T) {
	if (&Session{}).Revoked() {
		t.Fatalf("未吊销会话判定错误")
	}
	now := time.Now()
	if !(&Session{RevokedAt: &now}).Revoked() {
		t.Fatalf("已吊销会话判定错误")
	}
}
