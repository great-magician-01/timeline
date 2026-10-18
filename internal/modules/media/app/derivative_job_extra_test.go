package app

// 衍生文件作业用例：负载校验、附件/衍生行状态机幂等、认领失败、缩略图成功与失败、
// 视频封面降级（ffmpeg 不可用或抽取失败统一记录为 failed）。数据库分支由协议桩驱动。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/jobs"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// derivativeJob 组装一条衍生文件作业。
func derivativeJob(payload string) jobs.Job {
	return jobs.Job{ID: 900, Type: jobs.TypeMediaDerivative, Payload: []byte(payload)}
}

// thumbnailDerivative 返回一条指定状态的缩略图衍生记录。
func thumbnailDerivative(status, storageKey string) repo.Derivative {
	return repo.Derivative{
		ID: 5, AttachmentID: 42, DerivativeType: model.DerivativeThumbnail,
		Status: status, StorageKey: storageKey, CreatedAt: testNow, UpdatedAt: testNow,
	}
}

// TestHandleDerivativeJobInvalidPayload 负载解析失败或字段非法时返回错误且不触库。
func TestHandleDerivativeJobInvalidPayload(t *testing.T) {
	env := newMediaTestEnv(t)
	cases := []struct {
		name    string
		payload string
	}{
		{"非法 JSON", "{not-json"},
		{"空负载", ""},
		{"附件 ID 非法", `{"attachment_id":0,"type":"thumbnail"}`},
		{"衍生类型非法", `{"attachment_id":42,"type":"poster"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(tc.payload)); err == nil {
				t.Fatal("非法负载应返回错误")
			}
		})
	}
	if got := len(env.stub.recorded()); got != 0 {
		t.Fatalf("非法负载不应触库，实际查询 %d 次", got)
	}
}

// TestHandleDerivativeJobSkips 覆盖附件缺失/失效、衍生行缺失/已取消等直接跳过分支。
func TestHandleDerivativeJobSkips(t *testing.T) {
	payload := `{"attachment_id":42,"type":"thumbnail"}`

	t.Run("附件不存在", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(noAttachmentResult())
		if err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload)); err != nil {
			t.Fatalf("附件不存在应跳过而不是报错: %v", err)
		}
	})
	t.Run("附件已失效", func(t *testing.T) {
		env := newMediaTestEnv(t)
		row := testAttachmentRow(42, 7)
		row.Status = model.AttachmentDeleted
		env.expect(attachmentResult(row))
		if err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload)); err != nil {
			t.Fatalf("附件已失效应跳过: %v", err)
		}
		if got := env.stub.pending(); got != 0 {
			t.Fatalf("已失效附件不应再查询衍生行，剩余 %d", got)
		}
	})
	t.Run("附件读取失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("读取失败"))
		err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("衍生记录不存在", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), noDerivativeResult())
		if err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload)); err != nil {
			t.Fatalf("衍生记录不存在应跳过: %v", err)
		}
	})
	t.Run("衍生记录读取失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), dbErrorResult("衍生读取失败"))
		err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("衍生行已取消", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(
			attachmentResult(testAttachmentRow(42, 7)),
			derivativeResult(thumbnailDerivative(model.DerivativeCanceled, "")),
		)
		if err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload)); err != nil {
			t.Fatalf("已取消的衍生行应跳过: %v", err)
		}
		if got := env.stub.pending(); got != 0 {
			t.Fatalf("已取消的衍生行不应再认领，剩余 %d", got)
		}
	})
}

// TestHandleDerivativeJobIdempotent 覆盖“已 ready 且物理文件存在”与“ready 但文件缺失”两条路径。
func TestHandleDerivativeJobIdempotent(t *testing.T) {
	payload := `{"attachment_id":42,"type":"thumbnail"}`
	t.Run("ready 且文件存在直接返回", func(t *testing.T) {
		env := newMediaTestEnv(t)
		key := env.saveBytes(testJPEG(t, 4, 4))
		row := testAttachmentRow(42, 7)
		env.expect(attachmentResult(row), derivativeResult(thumbnailDerivative(model.DerivativeReady, key)))
		if err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload)); err != nil {
			t.Fatalf("幂等返回错误: %v", err)
		}
		if got := env.stub.pending(); got != 0 {
			t.Fatalf("幂等命中时不应认领，剩余 %d", got)
		}
	})
	t.Run("ready 但文件缺失等待人工处理", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(
			attachmentResult(testAttachmentRow(42, 7)),
			derivativeResult(thumbnailDerivative(model.DerivativeReady, testStorageKey(9))),
			commandResult("UPDATE 0"),
		)
		if err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload)); err != nil {
			t.Fatalf("认领失败应静默返回: %v", err)
		}
		if got := env.stub.pending(); got != 0 {
			t.Fatalf("认领后不应继续处理，剩余 %d", got)
		}
	})
}

// TestHandleDerivativeJobThumbnailSuccess 覆盖缩略图生成的完整状态机：
// 认领 processing → 解码/缩放/编码并另存 → MarkReady。
func TestHandleDerivativeJobThumbnailSuccess(t *testing.T) {
	env := newMediaTestEnv(t)
	payload := testPNG(t, 640, 320)
	row := testAttachmentRow(42, 7)
	row.StorageKey = env.saveBytes(payload)
	env.expect(
		attachmentResult(row),
		derivativeResult(thumbnailDerivative(model.DerivativePending, "")),
		commandResult("UPDATE 1"),
		commandResult("UPDATE 1"),
	)
	if err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(`{"attachment_id":42,"type":"thumbnail"}`)); err != nil {
		t.Fatalf("HandleDerivativeJob 返回错误: %v", err)
	}
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("成功路径应消费全部预设结果，剩余 %d", got)
	}
	queries := strings.Join(env.stub.recorded(), "|")
	if !strings.Contains(queries, "processing") || !strings.Contains(queries, "ready") {
		t.Fatalf("衍生文件作业应依次认领 processing 并置为 ready，实际查询: %s", queries)
	}
}

// TestHandleDerivativeJobThumbnailFailure 生成失败时写 failed 状态并返回错误交给作业层重试。
func TestHandleDerivativeJobThumbnailFailure(t *testing.T) {
	env := newMediaTestEnv(t)
	row := testAttachmentRow(42, 7)
	row.StorageKey = testStorageKey(9) // 物理文件不存在
	env.expect(
		attachmentResult(row),
		derivativeResult(thumbnailDerivative(model.DerivativePending, "")),
		commandResult("UPDATE 1"),
		commandResult("UPDATE 1"),
	)
	err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(`{"attachment_id":42,"type":"thumbnail"}`))
	if err == nil || !strings.Contains(err.Error(), "读取原图失败") {
		t.Fatalf("期望读取原图失败错误，实际 %v", err)
	}
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("失败后应写入 failed，剩余 %d", got)
	}
}

// TestHandleDerivativeJobDatabaseErrors 覆盖认领与 MarkReady 阶段数据库失败。
func TestHandleDerivativeJobDatabaseErrors(t *testing.T) {
	payload := `{"attachment_id":42,"type":"thumbnail"}`
	t.Run("认领失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(
			attachmentResult(testAttachmentRow(42, 7)),
			derivativeResult(thumbnailDerivative(model.DerivativePending, "")),
			dbErrorResult("claim 失败"),
		)
		err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload))
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("记录 ready 失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		source := testPNG(t, 64, 32)
		row := testAttachmentRow(42, 7)
		row.StorageKey = env.saveBytes(source)
		env.expect(
			attachmentResult(row),
			derivativeResult(thumbnailDerivative(model.DerivativePending, "")),
			commandResult("UPDATE 1"),
			dbErrorResult("mark ready 失败"),
		)
		err := env.svc.HandleDerivativeJob(context.Background(), derivativeJob(payload))
		requireKind(t, err, apperr.KindInternal)
	})
}

// TestGenerateVideoCoverDegrades 视频封面在 ffmpeg 不可用或抽取失败时统一降级为
// videoCoverFailedMessage（本机安装 ffmpeg 时走命令失败分支，未安装时走查找失败分支）。
func TestGenerateVideoCoverDegrades(t *testing.T) {
	env := newMediaTestEnv(t)

	// 非视频附件直接拒绝，不依赖 ffmpeg。
	if _, err := env.svc.generateVideoCover(context.Background(), repo.AttachmentRow{Attachment: model.Attachment{
		ID: 1, Kind: domain.KindImage, StorageKey: testStorageKey(1),
	}}); err == nil || !strings.Contains(err.Error(), "不支持抽取视频封面") {
		t.Fatalf("非视频附件应拒绝生成封面，实际 %v", err)
	}

	// 视频附件：物理文件缺失或内容非法时返回统一降级文案。
	row := repo.AttachmentRow{Attachment: model.Attachment{
		ID: 2, Kind: domain.KindVideo, StorageKey: testStorageKey(2),
	}}
	_, err := env.svc.generateVideoCover(context.Background(), row)
	if err == nil || !strings.Contains(err.Error(), videoCoverFailedMessage) {
		t.Fatalf("期望降级文案 %q，实际 %v", videoCoverFailedMessage, err)
	}
}

// TestMarkDerivativeFailedWritesTruncatedReason 失败原因截断后写入 last_error，写失败只记录日志。
func TestMarkDerivativeFailedWritesTruncatedReason(t *testing.T) {
	env := newMediaTestEnv(t)
	env.expect(commandResult("UPDATE 1"))
	long := strings.Repeat("错", domain.DerivativeErrorMaxRunes+50)
	env.svc.markDerivativeFailed(context.Background(), 42, model.DerivativeThumbnail, errors.New(long))
	if got := env.stub.pending(); got != 0 {
		t.Fatalf("MarkFailed 未被调用，剩余 %d", got)
	}
}
