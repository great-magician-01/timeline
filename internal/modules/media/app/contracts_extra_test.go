package app

// contracts.MediaService 的跨模块读写方法：批量读取、可用性校验与帖子关联。

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestAttachmentsByIDs 覆盖空集合（不触库）、批量读取与数据库错误。
func TestAttachmentsByIDs(t *testing.T) {
	t.Run("空集合不触库", func(t *testing.T) {
		env := newMediaTestEnv(t)
		got, err := env.svc.AttachmentsByIDs(context.Background(), nil)
		if err != nil {
			t.Fatalf("空集合返回错误: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("空集合结果 = %v，期望空 map", got)
		}
		if queries := len(env.stub.recorded()); queries != 0 {
			t.Fatalf("空集合不应触库，实际查询 %d 次", queries)
		}
	})
	t.Run("批量读取返回附件行", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentRowsResult(testAttachmentRow(1, 7), testAttachmentRow(2, 8)))
		got, err := env.svc.AttachmentsByIDs(context.Background(), []int64{1, 2})
		if err != nil {
			t.Fatalf("批量读取返回错误: %v", err)
		}
		if len(got) != 2 || got[1].UploaderID != 7 || got[2].UploaderID != 8 {
			t.Fatalf("批量读取结果 = %+v", got)
		}
	})
	t.Run("数据库错误", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("list 失败"))
		_, err := env.svc.AttachmentsByIDs(context.Background(), []int64{1})
		requireKind(t, err, apperr.KindInternal)
	})
}

// TestDerivativeStatuses 覆盖空集合、状态映射与数据库错误。
func TestDerivativeStatuses(t *testing.T) {
	t.Run("空集合不触库", func(t *testing.T) {
		env := newMediaTestEnv(t)
		got, err := env.svc.DerivativeStatuses(context.Background(), nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("空集合结果 = %v/%v，期望空 map 与 nil 错误", got, err)
		}
		if queries := len(env.stub.recorded()); queries != 0 {
			t.Fatalf("空集合不应触库，实际查询 %d 次", queries)
		}
	})
	t.Run("批量读取状态", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(derivativeStatusesResult(map[int64]map[string]string{
			1: {model.DerivativeThumbnail: model.DerivativeReady},
			2: {model.DerivativeVideoCover: model.DerivativePending},
		}))
		got, err := env.svc.DerivativeStatuses(context.Background(), []int64{1, 2})
		if err != nil {
			t.Fatalf("批量读取状态返回错误: %v", err)
		}
		if got[1][model.DerivativeThumbnail] != model.DerivativeReady ||
			got[2][model.DerivativeVideoCover] != model.DerivativePending {
			t.Fatalf("状态映射 = %+v", got)
		}
	})
	t.Run("数据库错误", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("status 失败"))
		_, err := env.svc.DerivativeStatuses(context.Background(), []int64{1})
		requireKind(t, err, apperr.KindInternal)
	})
}

// TestCheckAttachmentsUsable 覆盖附件归属/可用性/用途校验与默认用途。
func TestCheckAttachmentsUsable(t *testing.T) {
	usable := testAttachmentRow(42, 7)
	deleted := testAttachmentRow(43, 7)
	deleted.Status = model.AttachmentDeleted
	avatar := testAttachmentRow(44, 7)
	avatar.Purpose = domain.PurposeAvatar
	foreign := testAttachmentRow(45, 8)

	cases := []struct {
		name    string
		input   contracts.AttachmentCheckInput
		results []pgResult
		wantErr bool
	}{
		{name: "空集合直接通过", input: contracts.AttachmentCheckInput{UploaderID: 7}},
		{
			name:    "全部可用",
			input:   contracts.AttachmentCheckInput{UploaderID: 7, AttachmentIDs: []int64{42}},
			results: []pgResult{attachmentRowsResult(usable)},
		},
		{
			name:    "默认用途回退 post",
			input:   contracts.AttachmentCheckInput{UploaderID: 7, AttachmentIDs: []int64{42}, Purpose: ""},
			results: []pgResult{attachmentRowsResult(usable)},
		},
		{
			name:    "附件不存在",
			input:   contracts.AttachmentCheckInput{UploaderID: 7, AttachmentIDs: []int64{99}},
			results: []pgResult{attachmentRowsResult()},
			wantErr: true,
		},
		{
			name:    "附件不属于该账号",
			input:   contracts.AttachmentCheckInput{UploaderID: 7, AttachmentIDs: []int64{45}},
			results: []pgResult{attachmentRowsResult(foreign)},
			wantErr: true,
		},
		{
			name:    "附件已失效",
			input:   contracts.AttachmentCheckInput{UploaderID: 7, AttachmentIDs: []int64{43}},
			results: []pgResult{attachmentRowsResult(deleted)},
			wantErr: true,
		},
		{
			name:    "用途不匹配",
			input:   contracts.AttachmentCheckInput{UploaderID: 7, AttachmentIDs: []int64{44}},
			results: []pgResult{attachmentRowsResult(avatar)},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newMediaTestEnv(t)
			env.expect(tc.results...)
			err := env.svc.CheckAttachmentsUsable(context.Background(), tc.input)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("期望校验通过，实际 %v", err)
				}
				return
			}
			requireKind(t, err, apperr.KindValidation)
			domainErr, ok := apperr.AsKind(err)
			details, isMap := domainErr.Details.(map[string]any)
			if !ok || !isMap || details["attachment_ids"] == nil {
				t.Fatalf("校验错误应携带字段详情，实际 %v", err)
			}
		})
	}

	t.Run("数据库错误", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("list 失败"))
		err := env.svc.CheckAttachmentsUsable(context.Background(), contracts.AttachmentCheckInput{
			UploaderID: 7, AttachmentIDs: []int64{42},
		})
		requireKind(t, err, apperr.KindInternal)
	})
}

// TestLinkAttachmentsToPost 覆盖空集合、成功（事务内 UPDATE）与失败映射。
func TestLinkAttachmentsToPost(t *testing.T) {
	t.Run("空集合不触库", func(t *testing.T) {
		env := newMediaTestEnv(t)
		if err := env.svc.LinkAttachmentsToPost(context.Background(), 5, nil, testNow); err != nil {
			t.Fatalf("空集合返回错误: %v", err)
		}
		if queries := len(env.stub.recorded()); queries != 0 {
			t.Fatalf("空集合不应触库，实际查询 %d 次", queries)
		}
	})
	t.Run("成功绑定", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(commandResult("UPDATE 2"))
		at := testNow.Add(time.Minute)
		if err := env.svc.LinkAttachmentsToPost(context.Background(), 5, []int64{1, 2}, at); err != nil {
			t.Fatalf("绑定附件返回错误: %v", err)
		}
		if got := env.stub.pending(); got != 0 {
			t.Fatalf("绑定应消费 UPDATE 结果，剩余 %d", got)
		}
		queries := env.stub.recorded()
		if len(queries) != 3 {
			t.Fatalf("应执行 begin/update/commit，实际查询: %v", queries)
		}
	})
	t.Run("数据库错误", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("update 失败"))
		err := env.svc.LinkAttachmentsToPost(context.Background(), 5, []int64{1}, testNow)
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("事务入口未初始化", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.svc.tx = nil
		err := env.svc.LinkAttachmentsToPost(context.Background(), 5, []int64{1}, testNow)
		requireKind(t, err, apperr.KindInternal)
	})
}
