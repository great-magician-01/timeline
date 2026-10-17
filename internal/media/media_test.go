package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/settings"
	"github.com/great-magician-01/timeline/internal/storage"
)

// TestScaleBilinearDimensions 验证等比缩放与“只缩小不放大”。
func TestScaleBilinearDimensions(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		maxEdge       int
		wantW, wantH  int
	}{
		{"横图缩小", 1024, 512, 512, 512, 256},
		{"竖图缩小", 1000, 3000, 512, 171, 512},
		{"小图不放大", 100, 50, 512, 100, 50},
		{"正方形边界不变", 512, 512, 512, 512, 512},
		{"单像素", 1, 1, 512, 1, 1},
		{"极端长条不小于1", 3, 7, 2, 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := image.NewRGBA(image.Rect(0, 0, tc.width, tc.height))
			scaled, err := scaleBilinear(source, tc.maxEdge)
			if err != nil {
				t.Fatalf("缩放失败: %v", err)
			}
			bounds := scaled.Bounds()
			if bounds.Dx() != tc.wantW || bounds.Dy() != tc.wantH {
				t.Fatalf("尺寸 = %dx%d，期望 %dx%d", bounds.Dx(), bounds.Dy(), tc.wantW, tc.wantH)
			}
		})
	}
}

// TestScaleBilinearSolidColor 验证纯色图缩放后颜色不变且完全不透明。
func TestScaleBilinearSolidColor(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 40, 30))
	fill := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			source.Set(x, y, fill)
		}
	}
	scaled, err := scaleBilinear(source, 16)
	if err != nil {
		t.Fatalf("缩放失败: %v", err)
	}
	if scaled.Bounds().Dx() != 16 || scaled.Bounds().Dy() != 12 {
		t.Fatalf("尺寸 = %dx%d，期望 16x12", scaled.Bounds().Dx(), scaled.Bounds().Dy())
	}
	for y := 0; y < scaled.Bounds().Dy(); y++ {
		for x := 0; x < scaled.Bounds().Dx(); x++ {
			r, g, b, a := scaled.At(x, y).RGBA()
			if r>>8 != 10 || g>>8 != 20 || b>>8 != 30 || a>>8 != 255 {
				t.Fatalf("像素 (%d,%d) = (%d,%d,%d,%d)，期望 (10,20,30,255)", x, y, r>>8, g>>8, b>>8, a>>8)
			}
		}
	}
}

// TestScaleBilinearAlphaOverWhite 验证带 alpha 的像素与白底合成。
func TestScaleBilinearAlphaOverWhite(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 128})
		}
	}
	scaled, err := scaleBilinear(source, 1)
	if err != nil {
		t.Fatalf("缩放失败: %v", err)
	}
	if scaled.Bounds().Dx() != 1 || scaled.Bounds().Dy() != 1 {
		t.Fatalf("尺寸 = %dx%d，期望 1x1", scaled.Bounds().Dx(), scaled.Bounds().Dy())
	}
	r, g, b, a := scaled.At(0, 0).RGBA()
	// 红色 alpha=128 与白色合成后约为 (255,127,127)，JS 端 8 位表示如下。
	if r>>8 != 255 || a>>8 != 255 {
		t.Fatalf("红/alpha = (%d,%d)，期望 (255,255)", r>>8, a>>8)
	}
	if g>>8 < 125 || g>>8 > 129 || b>>8 < 125 || b>>8 > 129 {
		t.Fatalf("绿/蓝 = (%d,%d)，期望约 127", g>>8, b>>8)
	}

	transparent := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	scaledTransparent, err := scaleBilinear(transparent, 1)
	if err != nil {
		t.Fatalf("缩放失败: %v", err)
	}
	r, g, b, a = scaledTransparent.At(0, 0).RGBA()
	if r>>8 != 255 || g>>8 != 255 || b>>8 != 255 || a>>8 != 255 {
		t.Fatalf("全透明像素 = (%d,%d,%d,%d)，期望白底 (255,255,255,255)", r>>8, g>>8, b>>8, a>>8)
	}
}

// TestScaleBilinearRejectsInvalidInput 验证边界情况返回错误而不是 panic。
func TestScaleBilinearRejectsInvalidInput(t *testing.T) {
	if _, err := scaleBilinear(nil, 512); err == nil {
		t.Fatal("空图像应当返回错误")
	}
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if _, err := scaleBilinear(empty, 512); err == nil {
		t.Fatal("0 尺寸源图应当返回错误")
	}
	valid := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if _, err := scaleBilinear(valid, 0); err == nil {
		t.Fatal("maxEdge 为 0 应当返回错误")
	}
	if _, err := scaleBilinear(valid, -1); err == nil {
		t.Fatal("maxEdge 为负数应当返回错误")
	}
}

// TestTargetSize 验证目标尺寸计算（只缩小不放大且至少 1 像素）。
func TestTargetSize(t *testing.T) {
	cases := []struct {
		width, height, maxEdge int
		wantW, wantH           int
	}{
		{2000, 1000, 512, 512, 256},
		{1000, 3000, 512, 171, 512},
		{50, 50, 512, 50, 50},
		{3, 7, 2, 1, 2},
	}
	for _, tc := range cases {
		gotW, gotH := targetSize(tc.width, tc.height, tc.maxEdge)
		if gotW != tc.wantW || gotH != tc.wantH {
			t.Errorf("targetSize(%d,%d,%d) = %d,%d，期望 %d,%d",
				tc.width, tc.height, tc.maxEdge, gotW, gotH, tc.wantW, tc.wantH)
		}
	}
}

// TestSanitizeFilename 验证文件名净化：只保留安全形式用于展示。
func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"POSIX 路径", "/tmp/upload/photo.jpg", "photo.jpg"},
		{"Windows 路径", `C:\Users\evil\报告.pdf`, "报告.pdf"},
		{"目录穿越", "../../etc/passwd", "passwd"},
		{"Windows 穿越", `..\..\secret.txt`, "secret.txt"},
		{"控制字符", "a\x00b\nc\r.jpg", "abc.jpg"},
		{"空文件名", "", defaultFilename},
		{"只有路径", "///", defaultFilename},
		{"上级目录", "..", defaultFilename},
		{"保留中文与空格", "  假期 照片.png  ", "假期 照片.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeFilename(tc.raw); got != tc.want {
				t.Fatalf("sanitizeFilename(%q) = %q，期望 %q", tc.raw, got, tc.want)
			}
		})
	}

	long := strings.Repeat("字", 300) + ".jpg"
	got := sanitizeFilename(long)
	if runes := []rune(got); len(runes) != maxOriginalFilenameRunes {
		t.Fatalf("超长文件名截断后长度 = %d，期望 %d", len(runes), maxOriginalFilenameRunes)
	}
	if strings.ContainsAny(got, "/\\") {
		t.Fatalf("净化后的文件名不应含路径分隔符，得到 %q", got)
	}
}

// TestDerivativeTypeForKind 验证附件种类到衍生文件类型的映射。
func TestDerivativeTypeForKind(t *testing.T) {
	cases := map[string]string{
		storage.KindImage: model.DerivativeThumbnail,
		storage.KindVideo: model.DerivativeVideoCover,
		storage.KindFile:  "",
		"":                "",
		"unknown":         "",
	}
	for kind, want := range cases {
		if got := derivativeTypeForKind(kind); got != want {
			t.Errorf("derivativeTypeForKind(%q) = %q，期望 %q", kind, got, want)
		}
	}
}

// TestDerivativeJobKey 验证幂等键格式。
func TestDerivativeJobKey(t *testing.T) {
	if got := derivativeJobKey(12345, model.DerivativeThumbnail); got != "media_derivative:12345:thumbnail" {
		t.Fatalf("幂等键 = %q", got)
	}
}

// TestContentDisposition 验证下载头的拼装与非 ASCII 文件名编码。
func TestContentDisposition(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		filename string
		want     string
	}{
		{"ASCII 文件名", storage.KindFile, "report.txt", `attachment; filename="report.txt"`},
		{
			"中文文件名走 RFC 5987",
			storage.KindFile,
			"报告.pdf",
			`attachment; filename="download"; filename*=UTF-8''%E6%8A%A5%E5%91%8A.pdf`,
		},
		{
			"含引号的文件名走 RFC 5987",
			storage.KindFile,
			`a"b.txt`,
			`attachment; filename="download"; filename*=UTF-8''a%22b.txt`,
		},
		{"图片内联", storage.KindImage, "照片.jpg", "inline"},
		{"视频内联", storage.KindVideo, "视频.mp4", "inline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := contentDisposition(tc.kind, tc.filename); got != tc.want {
				t.Fatalf("contentDisposition(%q, %q) = %q，期望 %q", tc.kind, tc.filename, got, tc.want)
			}
		})
	}
}

// TestCleanupTimeComparison 验证清理任务的时间比较判定与保留期来源。
func TestCleanupTimeComparison(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.Local)
	retention := cleanupRetention(settings.Values{})
	if retention != settings.DefaultTempRetention {
		t.Fatalf("空设置应回退默认保留期，得到 %s", retention)
	}
	custom := cleanupRetention(settings.Values{TempAttachmentRetention: 48 * time.Hour})
	if custom != 48*time.Hour {
		t.Fatalf("应使用设置中的保留期，得到 %s", custom)
	}

	if got := cleanupCutoff(now, retention); !got.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("截止时间 = %s，期望 7 天前", got)
	}
	if !expiredUnattached(now.Add(-8*24*time.Hour), now, retention) {
		t.Fatal("8 天前的临时附件应当过期")
	}
	if expiredUnattached(now.Add(-6*24*time.Hour), now, retention) {
		t.Fatal("6 天前的临时附件不应过期")
	}
	if expiredUnattached(now.Add(-7*24*time.Hour), now, retention) {
		t.Fatal("恰好 7 天的临时附件按“早于截止时间”判定不应过期")
	}
	if !expiredUnattached(now.Add(-7*24*time.Hour-time.Second), now, retention) {
		t.Fatal("超过 7 天的临时附件应当过期")
	}
	if !expiredUnattached(now.Add(-49*time.Hour), now, custom) {
		t.Fatal("48 小时保留期下 49 小时前应当过期")
	}
	if expiredUnattached(now.Add(-47*time.Hour), now, custom) {
		t.Fatal("48 小时保留期下 47 小时前不应过期")
	}
}

// TestCleanupSQLGuards 验证清理 SQL 的引用保护：只清理帖子用途、未被草稿关联、
// 未被头像引用的临时附件，并且不执行物理删除。
func TestCleanupSQLGuards(t *testing.T) {
	guards := []string{
		"post_id IS NULL",
		"status = 'available'",
		"purpose = 'post'",
		"post_draft_attachments",
		"da.is_active",
		"users",
		"avatar_url",
	}
	for _, guard := range guards {
		if !strings.Contains(cleanupAttachmentsSQL, guard) {
			t.Errorf("清理 SQL 缺少保护条件 %q", guard)
		}
	}
	lower := strings.ToLower(cleanupAttachmentsSQL)
	if strings.Contains(lower, "delete from") {
		t.Fatal("清理任务不得物理删除记录")
	}
	if !strings.Contains(cleanupAttachmentsSQL, "SET status = 'deleted'") {
		t.Fatal("清理任务应只做逻辑失效")
	}
}

// TestExceedsStorageQuota 验证存储配额边界：包含逻辑删除记录的总和不得超过配额。
func TestExceedsStorageQuota(t *testing.T) {
	cases := []struct {
		name             string
		used, incoming   int64
		quota            int64
		wantExceedsQuota bool
	}{
		{name: "远低于配额", used: 100, incoming: 50, quota: 1000},
		{name: "恰好等于配额", used: 950, incoming: 50, quota: 1000},
		{name: "超过配额", used: 960, incoming: 50, quota: 1000, wantExceedsQuota: true},
		{name: "已用空间占满", used: 1000, incoming: 0, quota: 1000},
		{name: "已用空间超出", used: 1200, incoming: 0, quota: 1000, wantExceedsQuota: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exceedsStorageQuota(tc.used, tc.incoming, tc.quota); got != tc.wantExceedsQuota {
				t.Fatalf("exceedsStorageQuota(%d,%d,%d) = %v，期望 %v",
					tc.used, tc.incoming, tc.quota, got, tc.wantExceedsQuota)
			}
		})
	}
}

// TestGenerateThumbnail 验证缩略图流水线（解码 PNG、缩放、编码 JPEG、另存衍生文件），
// 不需要数据库，只使用临时存储目录。
func TestGenerateThumbnail(t *testing.T) {
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 200, G: 100, B: 50, A: 200})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("编码测试图片失败: %v", err)
	}
	key, _, mediaType, err := store.Save(bytes.NewReader(encoded.Bytes()), storage.DefaultMaxUploadBytes, time.Now())
	if err != nil {
		t.Fatalf("保存测试图片失败: %v", err)
	}
	if mediaType != "image/png" {
		t.Fatalf("媒体类型 = %q，期望 image/png", mediaType)
	}

	service := &Service{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	thumbnailKey, err := service.generateThumbnail(model.Attachment{
		ID:         1,
		Kind:       storage.KindImage,
		StorageKey: key,
	})
	if err != nil {
		t.Fatalf("生成缩略图失败: %v", err)
	}
	if thumbnailKey == key {
		t.Fatal("缩略图必须是新的存储键，不能覆盖原文件")
	}
	if !store.Exists(key) {
		t.Fatal("原文件必须保留")
	}

	file, err := store.Open(thumbnailKey)
	if err != nil {
		t.Fatalf("打开缩略图失败: %v", err)
	}
	defer file.Close()
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		t.Fatalf("解析缩略图失败: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("缩略图格式 = %q，期望 jpeg", format)
	}
	if config.Width != 512 || config.Height != 256 {
		t.Fatalf("缩略图尺寸 = %dx%d，期望 512x256", config.Width, config.Height)
	}

	// 非图片附录无法生成缩略图，应当返回错误而不 panic。
	if _, err := service.generateThumbnail(model.Attachment{
		ID:         2,
		Kind:       storage.KindFile,
		StorageKey: key,
	}); err == nil {
		t.Fatal("文件类附件不应生成缩略图")
	}
}

// TestTruncateRunes 验证按字符截断不会切断 UTF-8。
func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("你好世界", 2); got != "你好" {
		t.Fatalf("截断结果 = %q", got)
	}
	if got := truncateRunes("abc", 5); got != "abc" {
		t.Fatalf("短文本不应被修改，得到 %q", got)
	}
	if got := truncateRunes("abc", 0); got != "" {
		t.Fatalf("零长度截断应返回空串，得到 %q", got)
	}
	if got := truncateRunes("abc", -3); got != "" {
		t.Fatalf("负数长度截断应返回空串，得到 %q", got)
	}
}
