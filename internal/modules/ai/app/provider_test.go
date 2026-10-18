package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/platform/aiclient"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
)

// 说明：provider.go 中触库部分（列表/名称冲突/读取预设）在单测中不可达；
// 这里覆盖纯解析、掩码、错误脱敏、限频与事务缺失时的错误传播。

// TestParseProviderIDValue 校验预设 ID 解析与非法值文案。
func TestParseProviderIDValue(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{name: "正常 ID", raw: "123", want: 123},
		{name: "带空白", raw: " 456 ", want: 456},
		{name: "最大正整数", raw: "9223372036854775807", want: 9223372036854775807},
		{name: "空串", raw: "", wantErr: true},
		{name: "仅空白", raw: "   ", wantErr: true},
		{name: "非数字", raw: "abc", wantErr: true},
		{name: "浮点", raw: "1.5", wantErr: true},
		{name: "零", raw: "0", wantErr: true},
		{name: "负数", raw: "-5", wantErr: true},
		{name: "溢出", raw: "9223372036854775808", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProviderIDValue(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("应返回解析错误，实际 %d", got)
				}
				if err.Error() != "请选择有效的模型接口预设" {
					t.Fatalf("错误文案错误: %q", err.Error())
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parseProviderIDValue(%q) = (%d, %v)，期望 %d", tc.raw, got, err, tc.want)
			}
		})
	}
}

// TestTruncateRunes 校验按字符截断（不破坏多字节）与省略号行为。
func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{name: "短于上限", value: "abc", limit: 5, want: "abc"},
		{name: "等于上限", value: "abcde", limit: 5, want: "abcde"},
		{name: "超过上限", value: "abcdef", limit: 5, want: "abcde…"},
		{name: "上限零", value: "abc", limit: 0, want: "…"},
		{name: "中文按字符", value: "一二三四五六", limit: 3, want: "一二三…"},
		{name: "emoji 按字符", value: "🔑🔑🔑", limit: 2, want: "🔑🔑…"},
		{name: "空串", value: "", limit: 3, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateRunes(tc.value, tc.limit); got != tc.want {
				t.Fatalf("truncateRunes(%q, %d) = %q，期望 %q", tc.value, tc.limit, got, tc.want)
			}
		})
	}
}

// TestDuplicateNameError 校验预设重名 409 的形状。
func TestDuplicateNameError(t *testing.T) {
	e := requireKind(t, duplicateNameError(), apperr.KindConflict)
	if e.Code != "duplicate" {
		t.Fatalf("错误码应为 duplicate，实际 %q", e.Code)
	}
	if e.Message != "同名模型接口预设已存在" {
		t.Fatalf("重名文案错误: %q", e.Message)
	}
	details, ok := e.Details.(map[string]string)
	if !ok || details["name"] != "名称已被占用" {
		t.Fatalf("重名详情错误: %#v", e.Details)
	}
}

// TestProviderTestErrorMessage 校验连接测试错误映射与密钥脱敏。
func TestProviderTestErrorMessage(t *testing.T) {
	apiKey := "sk-very-secret-key"
	cases := []struct {
		name string
		err  error
		key  string
		want string
	}{
		{
			name: "超时",
			err:  fmt.Errorf("请求失败: %w", context.DeadlineExceeded),
			key:  apiKey,
			want: "连接超时（超过 15 秒）",
		},
		{
			name: "401 认证失败",
			err:  &aiclient.APIError{Provider: "openai", StatusCode: 401, Message: "invalid key"},
			key:  apiKey,
			want: "连接失败：认证被拒绝，请检查 API Key",
		},
		{
			name: "403 无权限",
			err:  &aiclient.APIError{Provider: "openai", StatusCode: 403, Message: "forbidden"},
			key:  apiKey,
			want: "连接失败：认证被拒绝，请检查 API Key",
		},
		{
			name: "404 地址错误",
			err:  &aiclient.APIError{Provider: "openai", StatusCode: 404, Message: "not found"},
			key:  apiKey,
			want: "连接失败：接口地址不正确（404）",
		},
		{
			name: "429 上游限流",
			err:  &aiclient.APIError{Provider: "openai", StatusCode: 429, Message: "rate limited"},
			key:  apiKey,
			want: "连接失败：请求被上游限流（429）",
		},
		{
			name: "500 上游错误",
			err:  &aiclient.APIError{Provider: "openai", StatusCode: 500, Message: "boom"},
			key:  apiKey,
			want: "连接失败：上游服务错误（500）",
		},
		{
			name: "502 上游错误",
			err:  &aiclient.APIError{Provider: "openai", StatusCode: 502, Message: "bad gateway"},
			key:  apiKey,
			want: "连接失败：上游服务错误（502）",
		},
		{
			name: "418 其它 HTTP",
			err:  &aiclient.APIError{Provider: "openai", StatusCode: 418, Message: "teapot"},
			key:  apiKey,
			want: "连接失败：HTTP 418",
		},
		{
			name: "0 无法访问且密钥脱敏",
			err:  &aiclient.APIError{Provider: "openai", StatusCode: 0, Message: "dial failed with " + apiKey},
			key:  apiKey,
			want: "连接失败：无法访问接口地址（callable: openai request failed: dial failed with ***）",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := providerTestErrorMessage(tc.err, tc.key)
			if got != tc.want {
				t.Fatalf("错误映射不符:\n got=%q\nwant=%q", got, tc.want)
			}
			if strings.Contains(got, tc.key) {
				t.Fatalf("错误提示泄露了 API Key: %q", got)
			}
		})
	}
}

// TestProviderTestErrorMessageTruncates 校验超长底层错误被截断，且密钥先脱敏再截断。
func TestProviderTestErrorMessageTruncates(t *testing.T) {
	apiKey := "sk-" + strings.Repeat("k", 40)
	long := strings.Repeat("x", 400) + apiKey
	got := providerTestErrorMessage(errors.New(long), apiKey)
	if strings.Contains(got, apiKey) {
		t.Fatal("截断前必须先脱敏，提示中不得出现完整密钥")
	}
	body := strings.TrimPrefix(got, "连接失败：")
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("超长错误应带省略号: %q", body)
	}
	if runes := []rune(body); len(runes) != 301 {
		t.Fatalf("截断后应为 300 字符 + 省略号，实际 %d", len(runes))
	}
	// 空密钥时不做替换，但仍应截断。
	got = providerTestErrorMessage(errors.New(long), "")
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("空密钥时仍应截断: %q", got)
	}
}

// TestMaskProviderKey 校验预设密钥掩码与损坏密文降级。
func TestMaskProviderKey(t *testing.T) {
	svc := testService(Deps{})
	cipher, err := encryptSecret(testSecretKey, "sk-abcdefgh")
	if err != nil {
		t.Fatalf("构造测试密文失败: %v", err)
	}
	if got := svc.maskProviderKey(domain.Provider{ID: 1, APIKeyCipher: cipher}); got != "sk-...efgh" {
		t.Fatalf("密钥掩码错误: %q", got)
	}
	if got := svc.maskProviderKey(domain.Provider{ID: 1}); got != "" {
		t.Fatalf("未设置密钥时应返回空串，实际 %q", got)
	}
	if got := svc.maskProviderKey(domain.Provider{ID: 1, APIKeyCipher: "v1:broken"}); got != "****" {
		t.Fatalf("密文损坏时应降级为 ****，实际 %q", got)
	}

	item := svc.providerItem(domain.Provider{ID: 5, Name: "预设", APIKeyCipher: cipher}, 3)
	if item.AICount != 3 || item.Provider.ID != 5 || item.APIKeyMasked != "sk-...efgh" {
		t.Fatalf("预设数据视图组装错误: %+v", item)
	}
}

// TestProviderTestLimiterThresholds 固定连接测试限频阈值，防止被误改放宽。
func TestProviderTestLimiterThresholds(t *testing.T) {
	if providerTestRateLimit != 10 {
		t.Fatalf("连接测试限频阈值应为 10，实际 %d", providerTestRateLimit)
	}
	if providerTestRateWindow != time.Minute {
		t.Fatalf("连接测试限频窗口应为 1 分钟，实际 %v", providerTestRateWindow)
	}
}

// TestTestProviderRateLimited 校验单管理员超过阈值后返回带 RetryAfter 的限频错误。
func TestTestProviderRateLimited(t *testing.T) {
	svc := testService(Deps{})
	svc.providerTestLimiter = ratelimit.New(providerTestRateLimit, providerTestRateWindow)
	ctx := context.Background()
	for i := 0; i < providerTestRateLimit; i++ {
		if allowed, _ := svc.providerTestLimiter.Consume("admin:9"); !allowed {
			t.Fatalf("第 %d 次连接测试不应被限频", i+1)
		}
	}
	_, err := svc.TestProvider(ctx, 9, 1)
	e := requireKind(t, err, apperr.KindRateLimited)
	if e.RetryAfter <= 0 {
		t.Fatalf("限频错误应带正的 RetryAfter，实际 %v", e.RetryAfter)
	}
	if e.Code != "rate_limited" {
		t.Fatalf("限频错误码错误: %q", e.Code)
	}
}

// TestCreateProviderValidation 校验预设字段校验在触库前返回。
func TestCreateProviderValidation(t *testing.T) {
	svc := testService(Deps{})
	_, err := svc.CreateProvider(context.Background(), 1, CreateProviderInput{})
	details := validationDetails(t, err)
	for _, key := range []string{"name", "base_url", "default_model"} {
		if _, ok := details[key]; !ok {
			t.Errorf("空输入应返回 %s 字段错误，实际 %v", key, details)
		}
	}

	longName := strings.Repeat("名", domain.MaxProviderNameRunes+1)
	_, err = svc.CreateProvider(context.Background(), 1, CreateProviderInput{
		Name:         longName,
		BaseURL:      "https://api.example.com/v1",
		DefaultModel: "gpt-demo",
	})
	details = validationDetails(t, err)
	if _, ok := details["name"]; !ok {
		t.Fatalf("超长名称应返回 name 错误，实际 %v", details)
	}

	_, err = svc.CreateProvider(context.Background(), 1, CreateProviderInput{
		Name:         "预设",
		BaseURL:      "ftp://api.example.com",
		DefaultModel: "gpt-demo",
	})
	details = validationDetails(t, err)
	if _, ok := details["base_url"]; !ok {
		t.Fatalf("非 http 地址应返回 base_url 错误，实际 %v", details)
	}
}

// TestProviderWritePathsRequireTransaction 校验合法输入在事务管理器缺失时返回明确错误而不是触库。
func TestProviderWritePathsRequireTransaction(t *testing.T) {
	ctx := context.Background()
	svc := testService(Deps{})

	_, err := svc.CreateProvider(ctx, 1, CreateProviderInput{
		Name:         "预设",
		BaseURL:      "https://api.example.com/v1",
		APIKey:       "sk-create-secret",
		DefaultModel: "gpt-demo",
	})
	requireTransactionMissing(t, err)

	_, err = svc.UpdateProvider(ctx, 1, 5, UpdateProviderInput{Name: strPtr("新名称")})
	requireTransactionMissing(t, err)

	requireTransactionMissing(t, svc.DeleteProvider(ctx, 1, 5))
}

// TestCreateProviderEncryptFailure 校验加密密钥长度错误被映射为内部错误。
func TestCreateProviderEncryptFailure(t *testing.T) {
	svc := testService(Deps{SecretKey: []byte("short")})
	_, err := svc.CreateProvider(context.Background(), 1, CreateProviderInput{
		Name:         "预设",
		BaseURL:      "https://api.example.com/v1",
		APIKey:       "sk-x",
		DefaultModel: "gpt-demo",
	})
	e := requireKind(t, err, apperr.KindInternal)
	if !strings.Contains(e.Error(), "加密密钥长度必须为 32 字节") {
		t.Fatalf("应保留加密失败底因，实际 %v", e)
	}
}

// TestProviderListTypesZeroValue 确认列表视图类型零值可用（结构防漂移）。
func TestProviderListTypesZeroValue(t *testing.T) {
	var list ProviderList
	if list.Items != nil || list.LastID != 0 || list.HasMore {
		t.Fatalf("ProviderList 零值异常: %+v", list)
	}
	var result ProviderTestResult
	if result.OK || result.LatencyMS != 0 || result.Error != "" || result.Models != nil {
		t.Fatalf("ProviderTestResult 零值异常: %+v", result)
	}
}

// requireTransactionMissing 断言错误来自「事务管理器未初始化」，证明已越过字段校验且未触库。
func requireTransactionMissing(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "事务管理器未初始化") {
		t.Fatalf("应返回事务管理器未初始化错误，实际 %v", err)
	}
}
