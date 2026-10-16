// Package logging 提供双通道日志：标准输出 + 按天目录的文件。
// 文件通道写入「LOG_FILE 所在目录/yyyyMMdd/文件名」，按北京时间每天新建目录，跨天自动切换
// （见 docs/REQUIREMENTS.md §17.6 与 docs/DEPLOYMENT.md §5）。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// dailyWriter 按日期目录写入日志文件的 io.Writer。
type dailyWriter struct {
	mu   sync.Mutex
	path string
	loc  *time.Location
	day  string
	file *os.File
}

func newDailyWriter(path string, loc *time.Location) *dailyWriter {
	return &dailyWriter{path: path, loc: loc}
}

func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	day := time.Now().In(w.loc).Format("20060102")
	if w.file == nil || day != w.day {
		if err := w.rotate(day); err != nil {
			// 文件通道失败不阻断进程：回退到仅标准输出，由调用方日志可见
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *dailyWriter) rotate(day string) error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	target := filepath.Join(filepath.Dir(w.path), day, filepath.Base(w.path))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}
	w.file = f
	w.day = day
	return nil
}

func (w *dailyWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// Init 初始化日志：级别由 level 控制，格式支持 text/json，文件通道按天分目录。
// 返回的 closer 必须在进程退出前调用。
func Init(level, format, filePath string, loc *time.Location) (*slog.Logger, io.Closer, error) {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "info":
		lv = slog.LevelInfo
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		return nil, nil, fmt.Errorf("未知日志级别: %q", level)
	}

	w := newDailyWriter(filePath, loc)
	// 启动时立即建立当日目录，尽早暴露权限问题（如卷未赋权）
	if err := w.rotate(time.Now().In(loc).Format("20060102")); err != nil {
		return nil, nil, err
	}

	multi := io.MultiWriter(os.Stdout, w)
	opts := &slog.HandlerOptions{Level: lv}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(multi, opts)
	} else {
		handler = slog.NewTextHandler(multi, opts)
	}
	return slog.New(handler), w, nil
}
