// Package storage 负责附件原文件与衍生文件在本地持久化卷中的安全读写。
// 约定（docs/REQUIREMENTS.md 7、docs/WORKFLOWS.md 6）：
//   - 存储键由服务端生成（日期目录 + 随机文件名 + 由实际内容推导的扩展名），
//     绝不使用上传者提供的文件名参与路径；
//   - 只校验并写入，不提供删除能力：所有附件与衍生文件一律逻辑删除，不删除物理文件；
//   - 类型判定以实际字节为准（嗅探），不信任客户端声明的 Content-Type。
package storage

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrTooLarge 表示文件超过允许的上传上限。
	ErrTooLarge = errors.New("文件超过大小限制")
	// ErrUnsupportedType 表示实际内容类型不允许上传。
	ErrUnsupportedType = errors.New("不支持的文件类型")
)

// Store 是本地卷存储。root 为 UPLOAD_DIR。
type Store struct {
	root string
}

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("上传目录不能为空")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析上传目录失败: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, fmt.Errorf("创建上传目录失败: %w", err)
	}
	return &Store{root: absolute}, nil
}

// Root 返回存储根目录（日志与排障使用）。
func (s *Store) Root() string { return s.root }

// storageKeyPattern 限定存储键形状，读取时据此阻断目录穿越。
var storageKeyPattern = regexp.MustCompile(`^[0-9]{4}/[0-9]{2}/[0-9]{2}/[0-9a-f]{32}\.[a-z0-9]{1,8}$`)

// Save 把内容写入持久化卷，返回存储键、字节数与嗅探得到的媒体类型。
// 只有完整写入成功才会返回存储键；写入中断时残留的分片文件不会被业务引用。
func (s *Store) Save(r io.Reader, maxBytes int64, now time.Time) (string, int64, string, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxUploadBytes
	}
	limited := io.LimitReader(r, maxBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return "", 0, "", fmt.Errorf("读取上传内容失败: %w", err)
	}
	if int64(len(payload)) > maxBytes {
		return "", 0, "", ErrTooLarge
	}
	if len(payload) == 0 {
		return "", 0, "", ErrUnsupportedType
	}

	_, mediaType, extension := Detect(payload)
	if extension == "" {
		return "", 0, "", ErrUnsupportedType
	}

	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", 0, "", fmt.Errorf("生成存储键失败: %w", err)
	}
	key := path.Join(
		now.Format("2006"), now.Format("01"), now.Format("02"),
		hex.EncodeToString(random)+"."+extension,
	)
	full, err := s.fullPath(key)
	if err != nil {
		return "", 0, "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", 0, "", fmt.Errorf("创建附件目录失败: %w", err)
	}
	if err := os.WriteFile(full, payload, 0o640); err != nil {
		return "", 0, "", fmt.Errorf("写入附件失败: %w", err)
	}
	return key, int64(len(payload)), mediaType, nil
}

// Open 按存储键读取文件；存储键形状不合规时直接拒绝。
func (s *Store) Open(key string) (*os.File, error) {
	full, err := s.fullPath(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// Exists 用于衍生文件是否已生成的快速判断。
func (s *Store) Exists(key string) bool {
	full, err := s.fullPath(key)
	if err != nil {
		return false
	}
	info, err := os.Stat(full)
	return err == nil && !info.IsDir()
}

func (s *Store) fullPath(key string) (string, error) {
	if !storageKeyPattern.MatchString(key) {
		return "", fmt.Errorf("非法存储键")
	}
	full := filepath.Join(s.root, filepath.FromSlash(key))
	// 双保险：最终路径必须仍位于根目录内
	if !strings.HasPrefix(full, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("非法存储键")
	}
	return full, nil
}

// 附件类型。
const (
	KindImage = "image"
	KindVideo = "video"
	KindFile  = "file"
)

// DefaultMaxUploadBytes 是单文件的默认上限（可由系统设置覆盖）。
const DefaultMaxUploadBytes = 100 << 20 // 100 MiB

type signature struct {
	match     func(header []byte) bool
	kind      string
	mediaType string
	extension string
}

// signatures 按顺序匹配；顺序敏感，先匹配更具体的类型。
var signatures = []signature{
	{hasPrefix("\xff\xd8\xff"), KindImage, "image/jpeg", "jpg"},
	{hasPrefix("\x89PNG\r\n\x1a\n"), KindImage, "image/png", "png"},
	{hasPrefix("GIF87a"), KindImage, "image/gif", "gif"},
	{hasPrefix("GIF89a"), KindImage, "image/gif", "gif"},
	{func(b []byte) bool {
		return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP"))
	}, KindImage, "image/webp", "webp"},
	{func(b []byte) bool {
		return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("AVI "))
	}, KindVideo, "video/x-msvideo", "avi"},
	{hasPrefix("\x1a\x45\xdf\xa3"), KindVideo, "video/webm", "webm"},
	{func(b []byte) bool {
		if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
			return false
		}
		brand := string(b[8:12])
		switch brand {
		case "qt  ":
			return false // 由下一条 quicktime 规则处理
		case "isom", "iso2", "mp41", "mp42", "avc1", "dash", "M4V ", "3gp4", "3gp5":
			return true
		}
		return false
	}, KindVideo, "video/mp4", "mp4"},
	{func(b []byte) bool {
		return len(b) >= 12 && bytes.Equal(b[4:8], []byte("ftyp")) && string(b[8:12]) == "qt  "
	}, KindVideo, "video/quicktime", "mov"},
	{hasPrefix("ID3"), KindFile, "audio/mpeg", "mp3"},
	{hasPrefix("%PDF-"), KindFile, "application/pdf", "pdf"},
	{hasPrefix("PK\x03\x04"), KindFile, "application/zip", "zip"},
	{hasPrefix("\x1f\x8b"), KindFile, "application/gzip", "gz"},
}

func hasPrefix(prefix string) func([]byte) bool {
	return func(header []byte) bool { return bytes.HasPrefix(header, []byte(prefix)) }
}

// Detect 以实际字节判定附件种类、媒体类型与存储扩展名。
// 返回空扩展名表示该内容不允许上传。
func Detect(payload []byte) (kind, mediaType, extension string) {
	header := payload
	if len(header) > 512 {
		header = header[:512]
	}
	for _, sig := range signatures {
		if sig.match(header) {
			return sig.kind, sig.mediaType, sig.extension
		}
	}
	return classifyFallback(payload)
}

// classifyFallback 处理文本类与未知二进制：文本允许作为普通文件保存，
// 但明确拒绝会在浏览器中直接执行的 HTML/SVG 等类型，避免存储型 XSS。
func classifyFallback(payload []byte) (string, string, string) {
	sniffed := http.DetectContentType(payload)
	switch sniffed {
	case "text/html; charset=utf-8", "text/xml; charset=utf-8", "image/svg+xml":
		return "", "", ""
	}
	if strings.HasPrefix(sniffed, "text/") {
		return KindFile, "text/plain; charset=utf-8", "txt"
	}
	if strings.HasPrefix(sniffed, "image/") {
		// 浏览器能嗅探出但不在白名单内的图片（如 bmp、ico、tiff）仍按图片保存
		switch sniffed {
		case "image/bmp":
			return KindImage, sniffed, "bmp"
		case "image/vnd.microsoft.icon":
			return KindImage, sniffed, "ico"
		case "image/tiff":
			return KindImage, sniffed, "tiff"
		}
		return KindImage, sniffed, "bin"
	}
	if strings.HasPrefix(sniffed, "audio/") {
		return KindFile, sniffed, "bin"
	}
	return KindFile, "application/octet-stream", "bin"
}

// ExtensionForMediaType 供衍生文件生成时选择扩展名。
func ExtensionForMediaType(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	}
	return ""
}
