package notice

import "testing"

// TestNormalizeNoticeType 验证通知列表 type 过滤只接受迁移与常量中的合法类型。
func TestNormalizeNoticeType(t *testing.T) {
	for _, noticeType := range notificationTypes {
		got, err := normalizeNoticeType(" " + noticeType + " ")
		if err != nil || got != noticeType {
			t.Fatalf("合法类型 %q 被拒绝: %q %v", noticeType, got, err)
		}
	}
	if got, err := normalizeNoticeType(""); err != nil || got != "" {
		t.Fatalf("空类型应表示不过滤: %q %v", got, err)
	}
	for _, invalid := range []string{"all", "post", "unknown", "POST_LIKE", "like"} {
		if _, err := normalizeNoticeType(invalid); err == nil {
			t.Fatalf("非法类型 %q 应被拒绝", invalid)
		}
	}
}

// TestNotificationTypesMatchConstants 防止新增通知类型时遗漏过滤白名单。
func TestNotificationTypesMatchConstants(t *testing.T) {
	want := []string{
		TypePostLike, TypeCommentLike, TypeComment, TypeReply, TypeRepost,
		TypeQuote, TypeFollow, TypeFollowRequest, TypeFollowApprove, TypeMention,
	}
	if len(notificationTypes) != len(want) {
		t.Fatalf("合法类型数量 = %d，期望 %d", len(notificationTypes), len(want))
	}
	for i := range want {
		if notificationTypes[i] != want[i] {
			t.Fatalf("第 %d 个合法类型 = %q，期望 %q", i, notificationTypes[i], want[i])
		}
	}
}
