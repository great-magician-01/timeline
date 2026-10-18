package app

// 鉴权读取用例：附件原文件与衍生文件的可见性继承、存在性与物理文件缺失处理。
// 依赖 db 的分支用内存协议桩驱动真实 repo 扫描路径；文件读写使用 t.TempDir 的真实存储。

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestGetAttachmentRejectsInvalidID 非法 ID 直接按不存在处理，不触库。
func TestGetAttachmentRejectsInvalidID(t *testing.T) {
	env := newMediaTestEnv(t)
	for _, id := range []int64{0, -1} {
		_, err := env.svc.getAttachment(context.Background(), id)
		requireKind(t, err, apperr.KindNotFound)
	}
	if got := len(env.stub.recorded()); got != 0 {
		t.Fatalf("非法 ID 不应触库，实际查询 %d 次", got)
	}
}

// TestGetAttachmentDatabasePaths 覆盖记录不存在（pgx.ErrNoRows 映射）与数据库错误。
func TestGetAttachmentDatabasePaths(t *testing.T) {
	t.Run("记录不存在", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(noAttachmentResult())
		_, err := env.svc.getAttachment(context.Background(), 42)
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("数据库错误", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(dbErrorResult("connection reset"))
		_, err := env.svc.getAttachment(context.Background(), 42)
		requireKind(t, err, apperr.KindInternal)
	})
}

// TestRequireAttachmentAccess 覆盖附件可见性规则的全部判定分支。
func TestRequireAttachmentAccess(t *testing.T) {
	postID := int64(9)
	postRow := testAttachmentRow(42, 7)
	postRow.PostID = &postID

	publicPost := model.Post{ID: postID, AuthorID: 3, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic}

	cases := []struct {
		name    string
		row     func() repo.AttachmentRow
		viewer  model.Viewer
		content func(*stubContent)
		want    apperr.Kind
		wantNil bool
	}{
		{
			name: "附件已失效",
			row: func() repo.AttachmentRow {
				row := testAttachmentRow(42, 7)
				row.Status = model.AttachmentDeleted
				return row
			},
			viewer: humanViewer(7),
			want:   apperr.KindNotFound,
		},
		{
			name: "头像附件公开可读",
			row: func() repo.AttachmentRow {
				row := testAttachmentRow(42, 7)
				row.Purpose = domain.PurposeAvatar
				return row
			},
			viewer:  guestViewer(),
			wantNil: true,
		},
		{
			name:   "未挂帖附件游客不可读",
			row:    func() repo.AttachmentRow { return testAttachmentRow(42, 7) },
			viewer: guestViewer(),
			want:   apperr.KindNotFound,
		},
		{
			name:    "未挂帖附件上传者本人可读",
			row:     func() repo.AttachmentRow { return testAttachmentRow(42, 7) },
			viewer:  humanViewer(7),
			wantNil: true,
		},
		{
			name:    "未挂帖附件管理员可读",
			row:     func() repo.AttachmentRow { return testAttachmentRow(42, 7) },
			viewer:  adminViewer(1),
			wantNil: true,
		},
		{
			name:   "继承帖子可见性（公开帖游客可读）",
			row:    func() repo.AttachmentRow { return postRow },
			viewer: guestViewer(),
			content: func(f *stubContent) {
				f.visiblePosts = map[int64]model.Post{postID: publicPost}
			},
			wantNil: true,
		},
		{
			name:   "帖子不可见按附件不存在处理",
			row:    func() repo.AttachmentRow { return postRow },
			viewer: guestViewer(),
			want:   apperr.KindNotFound,
		},
		{
			name:   "可见性校验数据库错误映射为内部错误",
			row:    func() repo.AttachmentRow { return postRow },
			viewer: guestViewer(),
			content: func(f *stubContent) {
				f.visibleErr = errors.New("content 不可用")
			},
			want: apperr.KindInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newMediaTestEnv(t)
			if tc.content != nil {
				tc.content(env.content)
			}
			err := env.svc.requireAttachmentAccess(context.Background(), tc.viewer, tc.row())
			if tc.wantNil {
				if err != nil {
					t.Fatalf("期望允许访问，实际错误 %v", err)
				}
				return
			}
			requireKind(t, err, tc.want)
			if got := len(env.stub.recorded()); got != 0 {
				t.Fatalf("可见性校验不应直接触库，实际查询 %d 次", got)
			}
		})
	}
}

// TestOpenContentSuccess 覆盖原文件成功读取：鉴权通过后按存储键打开真实文件。
func TestOpenContentSuccess(t *testing.T) {
	env := newMediaTestEnv(t)
	payload := testPNG(t, 8, 8)
	key := env.saveBytes(payload)
	row := testAttachmentRow(42, 7)
	row.StorageKey = key
	env.expect(attachmentResult(row))

	result, err := env.svc.OpenContent(context.Background(), humanViewer(7), 42)
	if err != nil {
		t.Fatalf("OpenContent 返回错误: %v", err)
	}
	defer result.File.Close()
	if result.Attachment.ID != 42 || result.Attachment.StorageKey != key {
		t.Fatalf("返回附件 = %+v，期望 ID=42 且存储键为测试键", result.Attachment)
	}
	content, err := io.ReadAll(result.File)
	if err != nil {
		t.Fatalf("读取附件内容失败: %v", err)
	}
	if string(content) != string(payload) {
		t.Fatalf("附件内容长度 = %d，期望 %d", len(content), len(payload))
	}
}

// TestOpenContentDeniedAndMissing 覆盖鉴权失败与物理文件缺失（不泄露存储路径）。
func TestOpenContentDeniedAndMissing(t *testing.T) {
	t.Run("游客读取未挂帖附件", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)))
		_, err := env.svc.OpenContent(context.Background(), guestViewer(), 42)
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("物理文件缺失", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)))
		_, err := env.svc.OpenContent(context.Background(), humanViewer(7), 42)
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("附件记录不存在", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(noAttachmentResult())
		_, err := env.svc.OpenContent(context.Background(), humanViewer(7), 42)
		requireKind(t, err, apperr.KindNotFound)
	})
}

// TestOpenDerivativeInvalidType 非法衍生类型直接 404，不触库。
func TestOpenDerivativeInvalidType(t *testing.T) {
	env := newMediaTestEnv(t)
	for _, derivativeType := range []string{"", "poster", "thumbnail2"} {
		_, err := env.svc.OpenDerivative(context.Background(), guestViewer(), 42, derivativeType)
		requireKind(t, err, apperr.KindNotFound)
	}
	if got := len(env.stub.recorded()); got != 0 {
		t.Fatalf("非法衍生类型不应触库，实际查询 %d 次", got)
	}
}

// TestOpenDerivative 覆盖衍生文件读取的鉴权、状态机与物理文件缺失。
func TestOpenDerivative(t *testing.T) {
	t.Run("附件不存在", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(noAttachmentResult())
		_, err := env.svc.OpenDerivative(context.Background(), guestViewer(), 42, model.DerivativeThumbnail)
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("鉴权失败", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)))
		_, err := env.svc.OpenDerivative(context.Background(), guestViewer(), 42, model.DerivativeThumbnail)
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("衍生记录不存在", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), noDerivativeResult())
		_, err := env.svc.OpenDerivative(context.Background(), humanViewer(7), 42, model.DerivativeThumbnail)
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("衍生记录读取错误", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), dbErrorResult("boom"))
		_, err := env.svc.OpenDerivative(context.Background(), humanViewer(7), 42, model.DerivativeThumbnail)
		requireKind(t, err, apperr.KindInternal)
	})
	t.Run("衍生文件尚未就绪", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), derivativeResult(repo.Derivative{
			ID: 5, AttachmentID: 42, DerivativeType: model.DerivativeThumbnail,
			Status: model.DerivativePending, CreatedAt: testNow, UpdatedAt: testNow,
		}))
		_, err := env.svc.OpenDerivative(context.Background(), humanViewer(7), 42, model.DerivativeThumbnail)
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("就绪但存储键为空", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), derivativeResult(repo.Derivative{
			ID: 5, AttachmentID: 42, DerivativeType: model.DerivativeThumbnail,
			Status: model.DerivativeReady, CreatedAt: testNow, UpdatedAt: testNow,
		}))
		_, err := env.svc.OpenDerivative(context.Background(), humanViewer(7), 42, model.DerivativeThumbnail)
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("就绪但物理文件缺失", func(t *testing.T) {
		env := newMediaTestEnv(t)
		env.expect(attachmentResult(testAttachmentRow(42, 7)), derivativeResult(repo.Derivative{
			ID: 5, AttachmentID: 42, DerivativeType: model.DerivativeThumbnail,
			Status: model.DerivativeReady, StorageKey: testStorageKey(7),
			CreatedAt: testNow, UpdatedAt: testNow,
		}))
		_, err := env.svc.OpenDerivative(context.Background(), humanViewer(7), 42, model.DerivativeThumbnail)
		requireKind(t, err, apperr.KindNotFound)
	})
	t.Run("成功读取", func(t *testing.T) {
		env := newMediaTestEnv(t)
		payload := testJPEG(t, 16, 16)
		key := env.saveBytes(payload)
		env.expect(
			attachmentResult(testAttachmentRow(42, 7)),
			derivativeResult(repo.Derivative{
				ID: 5, AttachmentID: 42, DerivativeType: model.DerivativeThumbnail,
				Status: model.DerivativeReady, StorageKey: key,
				CreatedAt: testNow, UpdatedAt: testNow,
			}),
		)
		result, err := env.svc.OpenDerivative(context.Background(), humanViewer(7), 42, model.DerivativeThumbnail)
		if err != nil {
			t.Fatalf("OpenDerivative 返回错误: %v", err)
		}
		defer result.File.Close()
		if result.DerivativeType != model.DerivativeThumbnail {
			t.Fatalf("衍生类型 = %q，期望 %q", result.DerivativeType, model.DerivativeThumbnail)
		}
		content, err := io.ReadAll(result.File)
		if err != nil || string(content) != string(payload) {
			t.Fatalf("衍生文件内容 = %q/%v，期望 %q", content, err, payload)
		}
	})
}
