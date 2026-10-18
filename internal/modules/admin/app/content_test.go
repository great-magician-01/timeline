package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// 内容处置审计由 content/social/media 三个端口在同一事务内写入，admin 不重复写审计；
// 这里只断言 admin 传给端口的入参与错误透传。

func TestDeletePostUsesClockAndAdminID(t *testing.T) {
	env := newAdminTestEnv(t)
	content := &fakeContent{}
	env.service.content = content
	env.clock.Advance(3 * time.Minute)
	now := env.clock.Now()

	if err := env.service.DeletePost(context.Background(), adminActor(), 88); err != nil {
		t.Fatalf("删除帖子失败: %v", err)
	}
	if len(content.deleted) != 1 {
		t.Fatalf("content 端口调用次数 = %d，期望 1", len(content.deleted))
	}
	input := content.deleted[0]
	if input.AdminID != adminActor().ID || input.PostID != 88 || !input.At.Equal(now) {
		t.Fatalf("DeletePostAsAdmin 入参不符: %+v", input)
	}
	if len(env.audit.entries) != 0 {
		t.Fatalf("帖子删除审计由 content 端口写入，admin 不应重复写: %v", env.audit.actions())
	}
}

func TestDeletePostPropagatesError(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("删除帖子失败")
	env.service.content = &fakeContent{deleteErr: sentinel}

	if err := env.service.DeletePost(context.Background(), adminActor(), 88); !errors.Is(err, sentinel) {
		t.Fatalf("期望透传端口错误，实际: %v", err)
	}
	if len(env.audit.entries) != 0 {
		t.Fatalf("端口失败时不应写审计")
	}
}

func TestDeleteCommentUsesAdminFlag(t *testing.T) {
	env := newAdminTestEnv(t)
	social := &fakeSocial{}
	env.service.social = social
	env.clock.Advance(time.Minute)
	now := env.clock.Now()

	if err := env.service.DeleteComment(context.Background(), adminActor(), 66); err != nil {
		t.Fatalf("删除评论失败: %v", err)
	}
	if len(social.deleted) != 1 {
		t.Fatalf("social 端口调用次数 = %d，期望 1", len(social.deleted))
	}
	input := social.deleted[0]
	if input.ActorID != adminActor().ID || input.CommentID != 66 || !input.ByAdmin || !input.At.Equal(now) {
		t.Fatalf("DeleteCommentAsAdmin 入参不符: %+v", input)
	}
	if len(env.audit.entries) != 0 {
		t.Fatalf("评论删除审计由 social 端口写入，admin 不应重复写: %v", env.audit.actions())
	}
}

func TestDeleteCommentPropagatesError(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("删除评论失败")
	env.service.social = &fakeSocial{deleteErr: sentinel}

	if err := env.service.DeleteComment(context.Background(), adminActor(), 66); !errors.Is(err, sentinel) {
		t.Fatalf("期望透传端口错误，实际: %v", err)
	}
}

func TestDeleteAttachmentUsesClockAndAdminID(t *testing.T) {
	env := newAdminTestEnv(t)
	media := &fakeMedia{}
	env.service.media = media
	env.clock.Advance(30 * time.Second)
	now := env.clock.Now()

	if err := env.service.DeleteAttachment(context.Background(), adminActor(), 55); err != nil {
		t.Fatalf("删除附件失败: %v", err)
	}
	if len(media.deleted) != 1 {
		t.Fatalf("media 端口调用次数 = %d，期望 1", len(media.deleted))
	}
	input := media.deleted[0]
	if input.AdminID != adminActor().ID || input.AttachmentID != 55 || !input.At.Equal(now) {
		t.Fatalf("DeleteAttachmentAsAdmin 入参不符: %+v", input)
	}
	if len(env.audit.entries) != 0 {
		t.Fatalf("附件删除审计由 media 端口写入，admin 不应重复写: %v", env.audit.actions())
	}
}

func TestDeleteAttachmentPropagatesError(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("删除附件失败")
	env.service.media = &fakeMedia{deleteErr: sentinel}

	if err := env.service.DeleteAttachment(context.Background(), adminActor(), 55); !errors.Is(err, sentinel) {
		t.Fatalf("期望透传端口错误，实际: %v", err)
	}
	// 删除失败时审计由 media 在同一事务内回滚，admin 不写审计。
	if len(env.audit.entries) != 0 {
		t.Fatalf("端口失败时 admin 不应写审计")
	}
}

// 编译期断言：fake 端口确实满足 contracts 接口（避免接口漂移后测试静默失效）。
var (
	_ contracts.ContentService = (*fakeContent)(nil)
	_ contracts.SocialService  = (*fakeSocial)(nil)
	_ contracts.MediaService   = (*fakeMedia)(nil)
)
