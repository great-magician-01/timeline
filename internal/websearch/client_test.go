package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSearchURL(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com":             "https://api.deepseek.com/v1/tools/search",
		"https://api.deepseek.com/":            "https://api.deepseek.com/v1/tools/search",
		"https://api.deepseek.com/v1":          "https://api.deepseek.com/v1/tools/search",
		"https://api.deepseek.com/v1/":         "https://api.deepseek.com/v1/tools/search",
		"https://gateway.example.com/deepseek": "https://gateway.example.com/deepseek/v1/tools/search",
		"https://gateway.example.com/x/v1/":    "https://gateway.example.com/x/v1/tools/search",
	}
	for input, want := range cases {
		got, err := SearchURL(input)
		if err != nil {
			t.Fatalf("%s: 不应报错: %v", input, err)
		}
		if got != want {
			t.Errorf("%s: got %s, want %s", input, got, want)
		}
	}
}

func TestNormalizeBaseURLRejectsUnsafeValues(t *testing.T) {
	invalid := []string{
		"",
		"ftp://api.deepseek.com",
		"api.deepseek.com",
		"https://user:pass@api.deepseek.com",
		"http://:8080",
		"https://api.deepseek.com?x=1",
		"https://api.deepseek.com#fragment",
	}
	for _, input := range invalid {
		if _, err := NormalizeBaseURL(input); err == nil {
			t.Errorf("%q 应被拒绝", input)
		}
	}
	got, err := NormalizeBaseURL("  https://api.deepseek.com/v1/  ")
	if err != nil || got != "https://api.deepseek.com/v1" {
		t.Fatalf("规范化结果不符: %q, %v", got, err)
	}
}

func TestSearchRequestAndTruncation(t *testing.T) {
	const apiKey = "sk-global-search-secret"
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/tools/search" {
			t.Errorf("请求方法与路径不符: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			t.Errorf("Authorization 不符: %q", got)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &requestBody)
		results := make([]map[string]any, 0, 6)
		for index := 0; index < 6; index++ {
			summary := strings.Repeat("摘要", 100)
			if index == 0 {
				summary = strings.Repeat("摘要", 500)
			}
			results = append(results, map[string]any{
				"title":     "标题",
				"url":       "https://example.com/" + string(rune('a'+index)),
				"summary":   summary,
				"content":   "不应下发",
				"site_name": "Example",
				"date":      "2026-10-17T00:00:00Z",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	output, err := client.Search(context.Background(), server.URL, apiKey, "  DeepSeek 搜索  ")
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if requestBody["query"] != "DeepSeek 搜索" || requestBody["count"] != float64(5) || requestBody["need_content"] != false {
		t.Fatalf("请求体不符: %#v", requestBody)
	}
	if len(requestBody) != 3 {
		t.Fatalf("请求体只能包含 query/count/need_content: %#v", requestBody)
	}
	if len(output.Results) != 5 || !output.Truncated || !output.Untrusted {
		t.Fatalf("结果裁剪不符: %+v", output)
	}
	for _, result := range output.Results {
		if utf8.RuneCountInString(result.Summary) > maxSummaryRunes {
			t.Fatalf("summary 未截断: %d", utf8.RuneCountInString(result.Summary))
		}
	}
	encoded, _ := json.Marshal(output)
	if strings.Contains(string(encoded), "不应下发") || strings.Contains(string(encoded), apiKey) {
		t.Fatalf("工具结果泄漏正文或密钥: %s", encoded)
	}
}

func TestSearchEmptyResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()
	output, err := NewClient(time.Second).Search(context.Background(), server.URL, "key", "query")
	if err != nil {
		t.Fatalf("空结果不应报错: %v", err)
	}
	if !output.Untrusted || output.Results == nil || len(output.Results) != 0 {
		t.Fatalf("空结果输出不符: %+v", output)
	}
}

func TestSearchRetriesTransientErrors(t *testing.T) {
	var (
		attempts int
		sleeps   []time.Duration
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		switch attempts {
		case 1:
			w.WriteHeader(http.StatusRequestTimeout)
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			_, _ = w.Write([]byte(`{"results":[]}`))
		}
	}))
	defer server.Close()
	client := NewClient(time.Second)
	client.sleep = func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}
	if _, err := client.Search(context.Background(), server.URL, "key", "query"); err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if attempts != 3 || len(sleeps) != 2 || sleeps[0] != 3*time.Second || sleeps[1] != 9*time.Second {
		t.Fatalf("重试次数或退避不符: attempts=%d sleeps=%v", attempts, sleeps)
	}
}

func TestSearchDoesNotRetryClientErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"sk-secret-should-not-leak"}`))
	}))
	defer server.Close()
	client := NewClient(time.Second)
	client.sleep = func(context.Context, time.Duration) error {
		t.Fatal("4xx 不应重试")
		return nil
	}
	_, err := client.Search(context.Background(), server.URL, "sk-secret-should-not-leak", "query")
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	if attempts != 1 {
		t.Fatalf("401 只能请求一次，实际 %d", attempts)
	}
	if strings.Contains(err.Error(), "sk-secret") || strings.Contains(err.Error(), "sk-secret-should-not-leak") {
		t.Fatalf("错误文本泄漏密钥或上游正文: %v", err)
	}
	var searchErr *Error
	if !errors.As(err, &searchErr) || searchErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误类型或状态不符: %#v", err)
	}
}

func TestSearchDoesNotFollowRedirect(t *testing.T) {
	followed := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed = true
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	_, err := NewClient(time.Second).Search(context.Background(), server.URL, "key", "query")
	if err == nil {
		t.Fatal("重定向应返回错误")
	}
	if followed {
		t.Fatal("不应跟随重定向")
	}
}

func TestSearchDoesNotRetryNotImplemented(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer server.Close()
	client := NewClient(time.Second)
	client.sleep = func(context.Context, time.Duration) error {
		t.Fatal("501 不应触发重试")
		return nil
	}
	_, err := client.Search(context.Background(), server.URL, "key", "query")
	if err == nil || attempts != 1 {
		t.Fatalf("501 应只请求一次: attempts=%d err=%v", attempts, err)
	}
}

func TestSearchRetriesClientTimeout(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()
	client := NewClient(10 * time.Millisecond)
	client.sleep = func(context.Context, time.Duration) error { return nil }
	_, err := client.Search(context.Background(), server.URL, "key", "query")
	if err == nil {
		t.Fatal("持续超时应最终返回错误")
	}
	if attempts != maxSearchAttempts {
		t.Fatalf("单次超时应可重试，实际 %d 次", attempts)
	}
	var searchErr *Error
	if !errors.As(err, &searchErr) || searchErr.Code != "web_search_timeout" {
		t.Fatalf("超时应归类为 web_search_timeout: %#v", err)
	}
}

func TestSearchValidatesInputBeforeHTTP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	client := NewClient(time.Second)
	if _, err := client.Search(context.Background(), server.URL, "", "query"); err == nil {
		t.Fatal("空密钥应报错")
	}
	if _, err := client.Search(context.Background(), server.URL, "key", "   "); err == nil {
		t.Fatal("空查询应报错")
	}
	if _, err := client.Search(context.Background(), server.URL, "key", strings.Repeat("搜", QueryRuneLimit+1)); err == nil {
		t.Fatal("超长查询应报错")
	}
	if requests != 0 {
		t.Fatalf("校验失败不应发起 HTTP，实际 %d 次", requests)
	}
}
