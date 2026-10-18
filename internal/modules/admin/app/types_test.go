package app

import (
	"io"
	"log/slog"
	"testing"
)

func TestNewUsesProvidedLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := New(Deps{Log: logger})
	if service.Logger() != logger {
		t.Fatal("New 应复用注入的日志器")
	}
}

func TestNewFallsBackToDefaultLogger(t *testing.T) {
	service := New(Deps{})
	if service.Logger() != slog.Default() {
		t.Fatal("未注入日志器时应回退 slog.Default")
	}
}
