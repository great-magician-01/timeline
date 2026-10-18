package app

// 逻辑删除与头像用例：uploader 归属、历史版本引用规则、衍生文件取消、审计同事务
// 以及头像附件校验与 auth 端口回写参数。物理文件永不删除。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestDeleteValidation 覆盖删除前的存在性、可用性与归属校验（均不进入事务）。
func TestDeleteValidation(t *testing.T) {
	cases := []struct {
		name    string
		userID  int64
		want    apperr.Kind
		results []pgResult
	}{
		{name: "非法 ID", userID: 7, want: apperr.KindNotFound},
		{name: "记录不存在", userID: 7, want: apperr.KindNotFound, results: []pgResult{noAttachmentResult()}},
		{
			name:   "附件已失效",
			userID: 7, want: apperr.KindNotFound,
			results: []pgResult{attachmentResult(func() repo.AttachmentRow {
				row := testAttachmentRow(42, 7)
				row.Status = model.AttachmentDeleted
				return row
			}())},
		},
		{
			name:   "非上传者不可删",
			userID: 8, want: apperr.KindNotFound,
			results: []pgResult{attachmentResult(testAttachmentRow(42, 7))},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newMediaTestEnv(t)
			id := int64(42)
			if len(tc.results) == 0 {
				id = 0
			}
			env.expect(tc.results...)
			_, err := env.svc.Delete(context.Background(), tc.userID, id)
			requireKind(t, err, tc.want)
			if got := env.stub.pending(); got != 0 {
				t.Fatalf("校验失败后不应继续消费查询结果，剩余 %d", got)
			}
		})
	}
}

// TestDeleteReferencedByVersionKeepsAvailable 仍被历史版本引用时只解除草稿关联，不置失效。
func TestDeleteReferencedByVersionKeepsAvailable(t *testing.T) {
	env := newMediaTestEnv(t)
	env.content.detached = true
	env.content.referenced = true
	env.expect(attachmentResult(testAttachmentRow(42, 7)))

	result, err := env.svc.Delete(context.Background(), 7, 42)
	if err != nil {
		t.Fatalf("Delete 返回错误: %v", err)
	}
	if !result.Detached || result.Deleted {
		t.Fatalf("结果 = %+v，期望 detached=true、deleted=false", result)
	}
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("被版本引用时不应再发 UPDATE，剩余 %d 条预设结果", got)
	}
	if len(env.content.detachedIDs) != 1 || env.content.detachedIDs[0] != 42 {
		t.Fatalf("解除草稿关联的附件 = %v，期望 [42]", env.content.detachedIDs)
	}
	if env.content.detachedAt != testNow {
		t.Fatalf("解除关联时间 = %v，期望应用时钟 %v", env.content.detachedAt, testNow)
	}
	if len(env.content.versionChecks) != 1 || env.content.versionChecks[0] != 42 {
		t.Fatalf("版本引用检查 = %v，期望 [42]", env.content.versionChecks)
	}
}

// TestDeleteMarksDeletedAndCancelsDerivatives 无历史版本引用时逻辑失效并取消未完成衍生文件。
func TestDeleteMarksDeletedAndCancelsDerivatives(t *testing.T) {
	env := newMediaTestEnv(t)
	env.content.detached = true
	env.content.referenced = false
	env.expect(
		attachmentResult(testAttachmentRow(42, 7)),
		commandResult("UPDATE 1"),
		commandResult("UPDATE 0"),
	)

	result, err := env.svc.Delete(context.Background(), 7, 42)
	if err != nil {
		t.Fatalf("Delete 返回错误: %v", err)
	}
	if !result.Detached || !result.Deleted {
		t.Fatalf("结果 = %+v，期望均成功", result)
	}
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("Delete 应消费全部预设结果，剩余 %d", got)
	}
	if !strings.Contains(strings.Join(env.stub.recorded(), "|"), "UPDATE") {
		t.Fatalf("Delete 未发出 UPDATE，实际查询：%v", env.stub.recorded())
	}
}

// TestDeleteConcurrentAlreadyDeleted 并发删除导致 MarkDeleted 未变更时仍按已失效处理。
func TestDeleteConcurrentAlreadyDeleted(t *testing.T) {
	env := newMediaTestEnv(t)
	env.content.detached = true
	env.expect(attachmentResult(testAttachmentRow(42, 7)), commandResult("UPDATE 0"))

	result, err := env.svc.Delete(context.Background(), 7, 42)
	if err != nil {
		t.Fatalf("Delete 返回错误: %v", err)
	}
	if !result.Detached || !result.Deleted {
		t.Fatalf("结果 = %+v，期望均成功", result)
	}
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("已失效时不应再取消失效附件衍生文件，剩余 %d", got)
	}
}

// TestDeleteErrors 覆盖删除流程中的数据库与跨模块端口错误映射。
func TestDeleteErrors(t *testing.T) {
	t.Run("解除草稿关联失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.content.detachErr = errors.New("content 不可用")
		env.expect(attachmentResult(testAttachmentRow(42, 7)))
		_, err := env.svc.Delete(context.Background(), 7, 42)
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("检查历史版本引用失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.content.referencedErr = errors.New("content 不可用")
		env.expect(attachmentResult(testAttachmentRow(42, 7)))
		_, err := env.svc.Delete(context.Background(), 7, 42)
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("失效附件失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), dbErrorResult("update 失败"))
		_, err := env.svc.Delete(context.Background(), 7, 42)
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("取消衍生文件失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), commandResult("UPDATE 1"), dbErrorResult("cancel 失败"))
		_, err := env.svc.Delete(context.Background(), 7, 42)
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("事务入口未初始化", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.svc.tx = nil
		env.expect(attachmentResult(testAttachmentRow(42, 7)))
		_, err := env.svc.Delete(context.Background(), 7, 42)
		if err == nil || !strings.Contains(err.Error(), "事务管理器未初始化") {
			t.Fatalf("期望事务管理器未初始化错误，实际 %v", err)
		}
	})
}

// TestDeleteAttachmentAsAdmin 覆盖管理员处置：跳过归属校验、同一事务写审计。
func TestDeleteAttachmentAsAdmin(t *testing.T) {
	env := newMediaTestEnv(t)
	env.content.detached = true
	env.expect(
		attachmentResult(testAttachmentRow(42, 7)),
		commandResult("UPDATE 1"),
		commandResult("UPDATE 0"),
	)
	if err := env.svc.DeleteAttachmentAsAdmin(context.Background(), contracts.DeleteAttachmentInput{
		AdminID:      1,
		AttachmentID: 42,
		At:           testNow,
	}); err != nil {
		t.Fatalf("DeleteAttachmentAsAdmin 返回错误: %v", err)
	}
	if len(env.audit.entries) != 1 {
		t.Fatalf("审计条目 = %d，期望 1", len(env.audit.entries))
	}
	entry := env.audit.entries[0]
	if entry.AdminID != 1 || entry.Action != contracts.AuditActionAttachmentDelete {
		t.Fatalf("审计条目 = %+v", entry)
	}
	if entry.TargetType != "attachment" || entry.TargetID == nil || *entry.TargetID != 42 {
		t.Fatalf("审计目标 = %q/%v", entry.TargetType, entry.TargetID)
	}
	if entry.At != testNow {
		t.Fatalf("审计时间 = %v，期望应用时钟 %v", entry.At, testNow)
	}
	detail, ok := entry.Detail.(map[string]any)
	if !ok || detail["detached"] != true || detail["deleted"] != true {
		t.Fatalf("审计详情 = %#v", entry.Detail)
	}
}

// TestDeleteAttachmentAsAdminErrors 覆盖管理员处置的存在性、可用性与审计失败分支。
func TestDeleteAttachmentAsAdminErrors(t *testing.T) {
	t.Run("附件不存在", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(noAttachmentResult())
		err := env.svc.DeleteAttachmentAsAdmin(context.Background(), contracts.DeleteAttachmentInput{AdminID: 1, AttachmentID: 42, At: testNow})
		requireKind(t, err, apperr.KindNotFound)
		if len(env.audit.entries) != 0 {
			t.Fatal("附件不存在时不应写审计")
		}
	})
	t.Run("附件已失效", func(t *testing.T) {
		env := newMediaTestEnv(t)
		row := testAttachmentRow(42, 7)
		row.Status = model.AttachmentDeleted
		env.expect(attachmentResult(row))
		err := env.svc.DeleteAttachmentAsAdmin(context.Background(), contracts.DeleteAttachmentInput{AdminID: 1, AttachmentID: 42, At: testNow})
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("审计写入失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.audit.err = errors.New("audit 不可用")
		env.expect(attachmentResult(testAttachmentRow(42, 7)))
		err := env.svc.DeleteAttachmentAsAdmin(context.Background(), contracts.DeleteAttachmentInput{AdminID: 1, AttachmentID: 42, At: testNow})
		requireKind(t, err, apperr.KindInternal)
	})
}

// TestSetAvatarValidation 覆盖头像设置的附件归属、可用性与用途校验。
func TestSetAvatarValidation(t *testing.T) {
	t.Run("非法 ID", func(t *testing.T) {
		env := newMediaTestEnv(t)
		requireKind(t, env.svc.SetAvatar(context.Background(), 7, 0), apperr.KindNotFound)
	})
	t.Run("附件不存在", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(noAttachmentResult())
		requireKind(t, env.svc.SetAvatar(context.Background(), 7, 42), apperr.KindNotFound)
	})
	t.Run("附件已失效", func(t *testing.T) {
		env := newMediaTestEnv(t)
		row := testAttachmentRow(42, 7)
		row.Purpose = domain.PurposeAvatar
		row.Status = model.AttachmentDeleted
		env.expect(attachmentResult(row))
		requireKind(t, env.svc.SetAvatar(context.Background(), 7, 42), apperr.KindNotFound)
	})
	t.Run("非本人上传", func(t *testing.T) {
		env := newMediaTestEnv(t)
		row := testAttachmentRow(42, 8)
		row.Purpose = domain.PurposeAvatar
		env.expect(attachmentResult(row))
		requireKind(t, env.svc.SetAvatar(context.Background(), 7, 42), apperr.KindForbidden)
	})
	t.Run("非头像用途", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)))
		requireKind(t, env.svc.SetAvatar(context.Background(), 7, 42), apperr.KindValidation)
	})
}

// TestSetAvatarSuccessAndErrors 覆盖头像回写参数与 auth 端口错误映射（旧头像只切换引用）。
func TestSetAvatarSuccessAndErrors(t *testing.T) {
	t.Run("成功回写头像", func(t *testing.T) {
		env := newMediaTestEnv(t)
		row := testAttachmentRow(42, 7)
		row.Purpose = domain.PurposeAvatar
		env.expect(attachmentResult(row))
		if err := env.svc.SetAvatar(context.Background(), 7, 42); err != nil {
			t.Fatalf("SetAvatar 返回错误: %v", err)
		}
		if len(env.auth.avatarInputs) != 1 {
			t.Fatalf("头像回写次数 = %d，期望 1", len(env.auth.avatarInputs))
		}
		input := env.auth.avatarInputs[0]
		if input.UserID != 7 || input.AttachmentID != 42 {
			t.Fatalf("头像回写参数 = %+v", input)
		}
		if input.AvatarURL != format.AttachmentContentURL(42) {
			t.Fatalf("头像地址 = %q，期望 %q", input.AvatarURL, format.AttachmentContentURL(42))
		}
		if input.At != testNow {
			t.Fatalf("头像回写时间 = %v，期望应用时钟 %v", input.At, testNow)
		}
		if got := env.stub.pending(); got != 0 {
			t.Fatalf("头像回写不应有额外查询，剩余 %d", got)
		}
	})
	t.Run("auth 端口错误包装为内部错误", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.auth.setAvatarErr = errors.New("auth 不可用")
		row := testAttachmentRow(42, 7)
		row.Purpose = domain.PurposeAvatar
		env.expect(attachmentResult(row))
		requireKind(t, env.svc.SetAvatar(context.Background(), 7, 42), apperr.KindInternal)
	})
	t.Run("auth 端口领域错误原样返回", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.auth.setAvatarErr = apperr.Conflict("avatar_conflict", "头像更新冲突")
		row := testAttachmentRow(42, 7)
		row.Purpose = domain.PurposeAvatar
		env.expect(attachmentResult(row))
		requireKind(t, env.svc.SetAvatar(context.Background(), 7, 42), apperr.KindConflict)
	})
}
