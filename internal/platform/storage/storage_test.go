package storage

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// testNow 是固定的写入时间，保证存储键中的日期目录可预测（不依赖当前真实时间）。
var testNow = time.Date(2026, 10, 18, 15, 4, 5, 0, time.FixedZone("CST", 8*60*60))

// storageKeyRegexp 是测试侧独立声明的存储键形状断言，避免直接复用生产正则导致“自证”。
var storageKeyRegexp = regexp.MustCompile(`^[0-9]{4}/[0-9]{2}/[0-9]{2}/[0-9a-f]{32}\.[a-z0-9]{1,8}$`)

// pngPayload 返回一段以 PNG 魔数开头的合法内容（嗅探按实际字节判定）。
func pngPayload() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), []byte("timeline-storage-test")...)
}

// newTestStore 在 t.TempDir() 下构造存储，测试不接触仓库内路径。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}
	return store
}

// errReader 模拟不会读到 EOF 的失败读取。
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestNewRejectsBlankRoot(t *testing.T) {
	for _, root := range []string{"", "   ", "\t\n"} {
		if _, err := New(root); err == nil || !strings.Contains(err.Error(), "上传目录不能为空") {
			t.Errorf("空上传目录应被拒绝，root=%q，err=%v", root, err)
		}
	}
}

func TestNewCreatesRootDirectory(t *testing.T) {
	// 多级不存在目录应被自动创建，且 Root 返回绝对路径。
	root := filepath.Join(t.TempDir(), "data", "uploads", "attachments")
	store, err := New(root)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if !filepath.IsAbs(store.Root()) {
		t.Fatalf("Root 应为绝对路径: %q", store.Root())
	}
	if store.Root() != root {
		t.Fatalf("Root = %q，期望 %q", store.Root(), root)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("上传目录未创建: info=%v err=%v", info, err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o750 {
			t.Errorf("上传目录权限 = %o，期望 750", got)
		}
	}
}

func TestNewFailsWhenRootIsRegularFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "uploads")
	if err := os.WriteFile(blocker, []byte("占位文件"), 0o600); err != nil {
		t.Fatalf("准备占位文件失败: %v", err)
	}
	if _, err := New(blocker); err == nil || !strings.Contains(err.Error(), "创建上传目录失败") {
		t.Fatalf("根路径是普通文件时应返回创建目录错误，实际 %v", err)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	store := newTestStore(t)
	payload := pngPayload()

	key, size, mediaType, err := store.Save(bytes.NewReader(payload), 1<<20, testNow)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if size != int64(len(payload)) {
		t.Fatalf("返回字节数 = %d，期望 %d", size, len(payload))
	}
	if mediaType != "image/png" {
		t.Fatalf("嗅探媒体类型 = %q，期望 image/png", mediaType)
	}
	if !storageKeyRegexp.MatchString(key) {
		t.Fatalf("存储键形状不合规: %q", key)
	}
	if want := "2026/10/18/"; !strings.HasPrefix(key, want) {
		t.Fatalf("存储键应包含注入时间对应的日期目录 %q，实际 %q", want, key)
	}
	if ext := filepath.Ext(key); ext != ".png" {
		t.Fatalf("扩展名应由实际内容推导为 .png，实际 %q", ext)
	}
	if base := filepath.Base(key); len(strings.TrimSuffix(base, ".png")) != 32 {
		t.Fatalf("文件名应为 32 位十六进制随机串，实际 %q", base)
	}

	full := filepath.Join(store.Root(), filepath.FromSlash(key))
	info, err := os.Stat(full)
	if err != nil {
		t.Fatalf("保存后的文件不存在: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o640 {
			t.Errorf("文件权限 = %o，期望 640", got)
		}
	} else if info.Mode().Perm()&0o200 == 0 {
		t.Errorf("Windows 上保存的文件应可写，实际权限 %o", info.Mode().Perm())
	}
	dateDir, err := os.Stat(filepath.Dir(full))
	if err != nil || !dateDir.IsDir() {
		t.Fatalf("日期目录不存在: info=%v err=%v", dateDir, err)
	}
	if runtime.GOOS != "windows" {
		if got := dateDir.Mode().Perm(); got != 0o750 {
			t.Errorf("日期目录权限 = %o，期望 750", got)
		}
	}

	// Save 写出的文件必须能通过 Open 读回且内容一致。
	file, err := store.Open(key)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("读回内容与写入不一致: got=%d bytes want=%d bytes", len(got), len(payload))
	}
	if !store.Exists(key) {
		t.Fatal("保存后的存储键应存在")
	}
}

// TestSaveGeneratesUniqueRandomKeys 验证同一内容重复保存会得到不同存储键（随机文件名）。
func TestSaveGeneratesUniqueRandomKeys(t *testing.T) {
	store := newTestStore(t)
	payload := pngPayload()
	first, _, _, err := store.Save(bytes.NewReader(payload), 1<<20, testNow)
	if err != nil {
		t.Fatalf("第一次保存失败: %v", err)
	}
	second, _, _, err := store.Save(bytes.NewReader(payload), 1<<20, testNow)
	if err != nil {
		t.Fatalf("第二次保存失败: %v", err)
	}
	if first == second {
		t.Fatalf("两次保存得到相同存储键（随机文件名未生效）: %q", first)
	}
	for _, key := range []string{first, second} {
		if !store.Exists(key) {
			t.Fatalf("存储键 %q 不存在", key)
		}
	}
}

// TestSaveRejectsTooLarge 覆盖大小上限：超过上限时必须返回 ErrTooLarge 且不产生存储键。
func TestSaveRejectsTooLarge(t *testing.T) {
	store := newTestStore(t)
	payload := pngPayload() // 内容长度大于 8 字节上限
	key, size, mediaType, err := store.Save(bytes.NewReader(payload), 8, testNow)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("超过上限应返回 ErrTooLarge，实际 %v", err)
	}
	if key != "" || size != 0 || mediaType != "" {
		t.Fatalf("失败时不应返回任何结果: key=%q size=%d mediaType=%q", key, size, mediaType)
	}
}

// TestSaveUsesDefaultMaxBytesWhenNonPositive 覆盖 maxBytes <= 0 回退默认上限。
func TestSaveUsesDefaultMaxBytesWhenNonPositive(t *testing.T) {
	if DefaultMaxUploadBytes != 100<<20 {
		t.Fatalf("默认上传上限常量被修改: %d", DefaultMaxUploadBytes)
	}
	store := newTestStore(t)
	for _, maxBytes := range []int64{0, -1} {
		key, size, _, err := store.Save(bytes.NewReader(pngPayload()), maxBytes, testNow)
		if err != nil {
			t.Fatalf("maxBytes=%d 时应使用默认上限并保存成功: %v", maxBytes, err)
		}
		if size != int64(len(pngPayload())) || !store.Exists(key) {
			t.Fatalf("maxBytes=%d 保存结果异常: key=%q size=%d", maxBytes, key, size)
		}
	}
}

// TestSaveRejectsEmptyAndUnsupportedContent 覆盖空内容与不可保存类型的拒绝路径。
func TestSaveRejectsEmptyAndUnsupportedContent(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"空内容", nil},
		{"HTML 会存储型 XSS", []byte("<!DOCTYPE html><html><body>x</body></html>")},
		{"XML 可内嵌脚本", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			key, size, mediaType, err := store.Save(bytes.NewReader(tc.payload), 1<<20, testNow)
			if !errors.Is(err, ErrUnsupportedType) {
				t.Fatalf("应返回 ErrUnsupportedType，实际 %v", err)
			}
			if key != "" || size != 0 || mediaType != "" {
				t.Fatalf("失败时不应返回任何结果: key=%q size=%d mediaType=%q", key, size, mediaType)
			}
		})
	}
}

// TestSavePropagatesReadError 覆盖读取上传内容失败的分支。
func TestSavePropagatesReadError(t *testing.T) {
	store := newTestStore(t)
	readErr := errors.New("读取中断")
	key, _, _, err := store.Save(errReader{err: readErr}, 1<<20, testNow)
	if !errors.Is(err, readErr) {
		t.Fatalf("读取错误应被包装返回，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "读取上传内容失败") {
		t.Fatalf("错误信息应说明读取失败: %v", err)
	}
	if key != "" {
		t.Fatalf("失败时不应返回存储键: %q", key)
	}
}

// TestSaveFailsWhenDateDirectoryCannotBeCreated 覆盖创建日期目录失败的包装错误。
func TestSaveFailsWhenDateDirectoryCannotBeCreated(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	store, err := New(root)
	if err != nil {
		t.Fatalf("创建存储失败: %v", err)
	}
	// 把根目录替换为普通文件：后续 MkdirAll 日期目录必然失败。
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("删除目录失败: %v", err)
	}
	if err := os.WriteFile(root, []byte("占位文件"), 0o600); err != nil {
		t.Fatalf("写占位文件失败: %v", err)
	}
	_, _, _, err = store.Save(bytes.NewReader(pngPayload()), 1<<20, testNow)
	if err == nil || !strings.Contains(err.Error(), "创建附件目录失败") {
		t.Fatalf("日期目录创建失败应返回包装错误，实际 %v", err)
	}
}

// TestOpenRejectsIllegalKeys 覆盖目录穿越与形状不符的存储键在读取时被拒绝。
func TestOpenRejectsIllegalKeys(t *testing.T) {
	store := newTestStore(t)
	illegal := []string{
		"",
		"../2026/10/18/0123456789abcdef0123456789abcdef.png",
		"2026/10/18/../../0123456789abcdef0123456789abcdef.png",
		"2026/10/18/",
		"2026/10/18/0123456789abcdef0123456789abcdef",
		"2026/10/18/0123456789abcdef0123456789abcdef.png/evil",
		"2026/10/18/0123456789abcdef0123456789abcdef.PNG",
		"2026/10/18/0123456789abcdef0123456789abcde.png",
		"2026/1/18/0123456789abcdef0123456789abcdef.png",
		"2026/10/18/0123456789abcdef0123456789abcdef.a.b.c.d.e.f",
		`2026\10\18\0123456789abcdef0123456789abcdef.png`,
		"2026/10/18/0123456789ABCDEF0123456789abcdef.png",
	}
	for _, key := range illegal {
		if _, err := store.Open(key); err == nil || !strings.Contains(err.Error(), "非法存储键") {
			t.Errorf("Open 应拒绝非法存储键 %q，实际 err=%v", key, err)
		}
		if store.Exists(key) {
			t.Errorf("Exists 应拒绝非法存储键 %q", key)
		}
	}
}

// TestOpenMissingValidKeyReturnsNotExist 覆盖形状合法但文件不存在的情况。
func TestOpenMissingValidKeyReturnsNotExist(t *testing.T) {
	store := newTestStore(t)
	key := "2026/10/18/0123456789abcdef0123456789abcdef.png"
	if _, err := store.Open(key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("不存在的合法存储键应返回 ErrNotExist，实际 %v", err)
	}
	if store.Exists(key) {
		t.Fatal("不存在的存储键不应 Exists")
	}
}

// TestExistsFalseForDirectory 覆盖同路径是目录时 Exists 返回 false。
func TestExistsFalseForDirectory(t *testing.T) {
	store := newTestStore(t)
	key := "2026/10/18/0123456789abcdef0123456789abcdef.png"
	if err := os.MkdirAll(filepath.Join(store.Root(), filepath.FromSlash(key)), 0o750); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	if store.Exists(key) {
		t.Fatal("目录不应被视为已存在的附件")
	}
}

// TestDetect 表驱动覆盖魔数嗅探、回退分类与明确拒绝的类型。
func TestDetect(t *testing.T) {
	cases := []struct {
		name      string
		payload   []byte
		wantKind  string
		wantMedia string
		wantExt   string
	}{
		{"JPEG", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}, KindImage, "image/jpeg", "jpg"},
		{"PNG", []byte("\x89PNG\r\n\x1a\nmore"), KindImage, "image/png", "png"},
		{"GIF87a", []byte("GIF87a-data"), KindImage, "image/gif", "gif"},
		{"GIF89a", []byte("GIF89a-data"), KindImage, "image/gif", "gif"},
		{"WEBP", []byte("RIFF\x00\x00\x00\x00WEBPmore"), KindImage, "image/webp", "webp"},
		{"AVI", []byte("RIFF\x00\x00\x00\x00AVI more"), KindVideo, "video/x-msvideo", "avi"},
		{"WebM", []byte("\x1a\x45\xdf\xa3data"), KindVideo, "video/webm", "webm"},
		{"MP4 isom", []byte("\x00\x00\x00\x18ftypisomdata"), KindVideo, "video/mp4", "mp4"},
		{"MP4 avc1", []byte("\x00\x00\x00\x18ftypavc1data"), KindVideo, "video/mp4", "mp4"},
		{"QuickTime", []byte("\x00\x00\x00\x14ftypqt  data"), KindVideo, "video/quicktime", "mov"},
		{"MP3", []byte("ID3fake-mp3-data"), KindFile, "audio/mpeg", "mp3"},
		{"PDF", []byte("%PDF-1.7 data"), KindFile, "application/pdf", "pdf"},
		{"ZIP", []byte("PK\x03\x04data"), KindFile, "application/zip", "zip"},
		{"GZIP", []byte("\x1f\x8b\x08data"), KindFile, "application/gzip", "gz"},
		{"BMP 回退", []byte("BM\x00\x00\x00\x00data"), KindImage, "image/bmp", "bmp"},
		{"ICO 回退为 bin", []byte("\x00\x00\x01\x00data"), KindImage, "image/x-icon", "bin"},
		{"纯文本", []byte("hello timeline\n"), KindFile, "text/plain; charset=utf-8", "txt"},
		{"音频回退为 bin", []byte("FORM\x00\x00\x00\x00AIFFdata"), KindFile, "audio/aiff", "bin"},
		{"未知二进制", []byte{0x00, 0x01, 0x02, 0x03, 0x04}, KindFile, "application/octet-stream", "bin"},
		{"HTML 明确拒绝", []byte("<!DOCTYPE html><html><body>x</body></html>"), "", "", ""},
		{"XML 明确拒绝", []byte(`<?xml version="1.0"?><data/>`), "", "", ""},
		{"空内容按文本回退", nil, KindFile, "text/plain; charset=utf-8", "txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, mediaType, extension := Detect(tc.payload)
			if kind != tc.wantKind || mediaType != tc.wantMedia || extension != tc.wantExt {
				t.Fatalf("Detect() = (%q, %q, %q)，期望 (%q, %q, %q)",
					kind, mediaType, extension, tc.wantKind, tc.wantMedia, tc.wantExt)
			}
		})
	}
}

// TestDetectOnlySniffsFirst512Bytes 验证超长内容只嗅探头部，不影响开头魔数的识别。
func TestDetectOnlySniffsFirst512Bytes(t *testing.T) {
	payload := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 2048)...)
	if kind, _, ext := Detect(payload); kind != KindImage || ext != "png" {
		t.Fatalf("超长 PNG 内容应仍被识别，实际 kind=%q ext=%q", kind, ext)
	}
}

// TestExtensionForMediaType 覆盖衍生文件扩展名映射。
func TestExtensionForMediaType(t *testing.T) {
	cases := map[string]string{
		"image/jpeg": "jpg",
		"image/png":  "png",
		"image/webp": "webp",
		"image/gif":  "",
		"":           "",
	}
	for mediaType, want := range cases {
		if got := ExtensionForMediaType(mediaType); got != want {
			t.Errorf("ExtensionForMediaType(%q) = %q，期望 %q", mediaType, got, want)
		}
	}
}
