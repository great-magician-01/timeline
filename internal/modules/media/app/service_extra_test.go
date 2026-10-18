package app

// 用例层基础设施：依赖回退、执行器选择、设置读取回退与错误包装。

import (
	"context"
	"errors"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/settings"
)

// TestNewFallsBackToSystemClock 未传时钟时回退系统时钟，其他依赖保持注入值。
func TestNewFallsBackToSystemClock(t *testing.T) {
	svc := New(Deps{})
	if svc.clk == nil {
		t.Fatal("nil Clock 应回退系统时钟")
	}
	if svc.uploadLimiter == nil {
		t.Fatal("上传限频器应始终初始化")
	}
	if svc.store != nil || svc.jobs != nil || svc.auth != nil {
		t.Fatal("未注入的依赖不应被自动补齐")
	}

	injected := clock.NewFake(testNow)
	svc = New(Deps{Clock: injected})
	if svc.clk != injected {
		t.Fatal("注入的时钟应被保留")
	}
}

// TestExecWithoutPools 未装配连接池或连接池未就绪时，repo 空输入早退分支不会 panic。
func TestExecWithoutPools(t *testing.T) {
	ctx := context.Background()
	cases := map[string]*Service{
		"未装配连接池": {log: discardLogger()},
		"连接池未就绪": {pools: testPoolProvider{}, log: discardLogger()},
	}
	for name, svc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := svc.AttachmentsByIDs(ctx, nil)
			if err != nil || len(got) != 0 {
				t.Fatalf("空集合读取 = %v/%v，期望空 map 与 nil 错误", got, err)
			}
		})
	}
}

// TestSettingsOrDefaults 覆盖未装配设置、设置读取失败回退默认值与正常读取。
func TestSettingsOrDefaults(t *testing.T) {
	ctx := context.Background()

	t.Run("未装配设置回退默认值", func(t *testing.T) {
		svc := &Service{log: discardLogger()}
		got := svc.settingsOrDefaults(ctx)
		want := defaultSystemSettings()
		if got.MaxUploadBytes != want.MaxUploadBytes || got.UploadRateLimit != want.UploadRateLimit {
			t.Fatalf("默认设置 = %+v，期望 %+v", got, want)
		}
	})
	t.Run("设置读取失败回退默认值", func(t *testing.T) {
		svc := &Service{
			settings: &stubSettings{err: errors.New("settings 不可用")},
			log:      discardLogger(),
		}
		got := svc.settingsOrDefaults(ctx)
		want := defaultSystemSettings()
		if got.StorageQuotaBytes != want.StorageQuotaBytes {
			t.Fatalf("读取失败回退设置 = %+v，期望 %+v", got, want)
		}
	})
	t.Run("正常读取注入值", func(t *testing.T) {
		values := testSystemSettings()
		values.MaxUploadBytes = 4321
		svc := &Service{settings: &stubSettings{values: values}, log: discardLogger()}
		if got := svc.settingsOrDefaults(ctx); got.MaxUploadBytes != 4321 {
			t.Fatalf("设置读取 = %d，期望 4321", got.MaxUploadBytes)
		}
	})
}

// TestDefaultSystemSettings 默认设置必须与 platform/settings 的默认值保持一致。
func TestDefaultSystemSettings(t *testing.T) {
	defaults := settings.Defaults()
	got := defaultSystemSettings()
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"MaxUploadBytes", got.MaxUploadBytes, defaults.MaxUploadBytes},
		{"MaxRequestBytes", got.MaxRequestBytes, defaults.MaxRequestBytes},
		{"StorageQuotaBytes", got.StorageQuotaBytes, defaults.StorageQuotaBytes},
		{"UploadRateLimit", got.UploadRateLimit, defaults.UploadRateLimit},
		{"UploadRateWindow", got.UploadRateWindow, defaults.UploadRateWindow},
		{"TempAttachmentRetention", got.TempAttachmentRetention, defaults.TempAttachmentRetention},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s 默认值 = %v，期望 %v", tc.name, tc.got, tc.want)
		}
	}
}

// TestWrapPreservesDomainErrors 领域错误原样返回，未知错误包装为内部错误。
func TestWrapPreservesDomainErrors(t *testing.T) {
	svc := &Service{log: discardLogger()}
	if err := svc.wrap("空错误", nil); err != nil {
		t.Fatalf("nil 错误应返回 nil，实际 %v", err)
	}

	original := apperr.Validation("字段非法")
	if err := svc.wrap("包装", original); err != original {
		t.Fatalf("领域错误应原样返回，实际 %v", err)
	}

	err := svc.wrap("失败", errors.New("数据库连接失败"))
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, err) {
		t.Fatal("包装错误应实现 errors.Is")
	}
}

// TestInternalErrorMapping 内部错误统一 Kind 与对外文案。
func TestInternalErrorMapping(t *testing.T) {
	svc := &Service{log: discardLogger()}
	err := svc.internal("写入失败", errors.New("底层原因"))
	domainErr, ok := apperr.AsKind(err)
	if !ok || domainErr.Kind != apperr.KindInternal {
		t.Fatalf("内部错误 Kind 不正确: %v", err)
	}
	if domainErr.Message == "" || domainErr.Message == "底层原因" {
		t.Fatalf("内部错误不应泄露底层原因，实际 %q", domainErr.Message)
	}
}
