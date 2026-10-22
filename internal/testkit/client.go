package testkit

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Client 是带 Cookie jar 与 CSRF 令牌的测试 HTTP 客户端。
// 每个 Client 模拟一个浏览器会话；登录/注册/写操作都通过它发起。
type Client struct {
	t      *testing.T
	base   string
	client *http.Client
	csrf   string
}

// NewClient 创建测试客户端；base 是 httptest 服务根地址（可带或不带尾斜杠）。
func NewClient(t *testing.T, base string) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{t: t, base: strings.TrimSuffix(base, "/"), client: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

// BaseURL 返回客户端请求的服务根地址。
func (c *Client) BaseURL() string { return c.base }

// CSRF 返回当前客户端持有的 CSRF 令牌。
func (c *Client) CSRF() string { return c.csrf }

// SetCSRF 更新当前客户端的 CSRF 令牌。
func (c *Client) SetCSRF(token string) { c.csrf = token }

// HTTPClient 暴露底层 http.Client，供需要自定义请求（如 multipart）的测试使用。
func (c *Client) HTTPClient() *http.Client { return c.client }

// RefreshCSRF 调用 GET /api/v1/auth/csrf 获取当前会话（或游客）的 CSRF 令牌。
func (c *Client) RefreshCSRF() {
	c.t.Helper()
	status, _, payload, raw := c.Call(http.MethodGet, "/api/v1/auth/csrf", false, nil)
	ExpectStatus(c.t, status, http.StatusOK, raw)
	MustSetCSRF(c.t, payload, c)
}

// Call 发起一次 JSON 请求。withCSRF 为 true 时附带 X-CSRF-Token 请求头。
// 返回状态码、响应头、解析后的 JSON（空响应为空 map）与原始响应体。
func (c *Client) Call(
	method, path string,
	withCSRF bool,
	body any,
) (status int, header http.Header, payload map[string]any, raw string) {
	c.t.Helper()
	status, header, payload, raw, err := c.callRaw(method, path, withCSRF, body)
	if err != nil {
		c.t.Fatal(err)
	}
	return status, header, payload, raw
}

// CallRaw 与 Call 相同，但把请求/解析错误作为返回值，供 goroutine 中的并发用例使用。
func (c *Client) CallRaw(
	method, path string,
	withCSRF bool,
	body any,
) (status int, header http.Header, payload map[string]any, raw string, err error) {
	return c.callRaw(method, path, withCSRF, body)
}

func (c *Client) callRaw(
	method, path string,
	withCSRF bool,
	body any,
) (status int, header http.Header, payload map[string]any, raw string, err error) {
	var reader io.Reader
	if body != nil {
		encoded, encodeErr := json.Marshal(body)
		if encodeErr != nil {
			return 0, nil, nil, "", encodeErr
		}
		reader = bytes.NewReader(encoded)
	}
	req, reqErr := http.NewRequest(method, c.base+path, reader)
	if reqErr != nil {
		return 0, nil, nil, "", reqErr
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withCSRF {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, doErr := c.client.Do(req)
	if doErr != nil {
		return 0, nil, nil, "", doErr
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return 0, nil, nil, "", readErr
	}
	payload = make(map[string]any)
	if len(data) > 0 {
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			return resp.StatusCode, resp.Header, nil, string(data), unmarshalErr
		}
	}
	return resp.StatusCode, resp.Header, payload, string(data), nil
}

// Register 通过游客 CSRF 调用注册接口；password 为空时使用默认测试密码。
func (c *Client) Register(username, email, password string) (status int, header http.Header, payload map[string]any, raw string) {
	c.t.Helper()
	if password == "" {
		password = DefaultUserPassword
	}
	c.RefreshCSRF()
	return c.Call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": username,
		"email":    email,
		"password": password,
	})
}

// RegisterAndLogin 注册并登录（注册接口只创建账号，不下发会话），返回 user 对象。
func (c *Client) RegisterAndLogin(username, email string) map[string]any {
	c.t.Helper()
	status, _, payload, raw := c.Register(username, email, DefaultUserPassword)
	ExpectStatus(c.t, status, http.StatusCreated, raw)
	user := MustUser(c.t, payload)
	c.LoginAndSetCSRF(username, DefaultUserPassword)
	return user
}

// Login 调用登录接口，返回状态、响应头、JSON 与原始响应体。
func (c *Client) Login(identifier, password string) (status int, header http.Header, payload map[string]any, raw string) {
	c.t.Helper()
	c.RefreshCSRF()
	return c.Call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": identifier,
		"password":   password,
	})
}

// LoginAndSetCSRF 登录并断言 200，同时把响应中的 CSRF 令牌写入客户端。
func (c *Client) LoginAndSetCSRF(identifier, password string) map[string]any {
	c.t.Helper()
	status, _, payload, raw := c.Login(identifier, password)
	ExpectStatus(c.t, status, http.StatusOK, raw)
	MustSetCSRF(c.t, payload, c)
	return payload
}

// Upload 上传一张 8x8 的合法 PNG，返回附件 ID；失败时 Fatal。
func (c *Client) Upload() string {
	c.t.Helper()
	status, _, payload, raw := c.UploadWithHeaders()
	ExpectStatus(c.t, status, http.StatusCreated, raw)
	return MustString(c.t, MustNested(c.t, payload, "attachment"), "id")
}

// UploadWithHeaders 上传 PNG 并返回状态码、响应头、JSON 与原始响应体（用于配额等失败断言）。
func (c *Client) UploadWithHeaders() (int, http.Header, map[string]any, string) {
	return c.uploadMultipart("integration.png", "post", integrationPNGBytes)
}

// UploadWithPurpose 上传 PNG 并声明用途，返回附件对象（头像/横幅等 purpose 用例）。
func (c *Client) UploadWithPurpose(purpose string) map[string]any {
	c.t.Helper()
	status, _, payload, raw := c.uploadMultipart("integration.png", purpose, integrationPNGBytes)
	ExpectStatus(c.t, status, http.StatusCreated, raw)
	attachment, ok := payload["attachment"].(map[string]any)
	if !ok {
		c.t.Fatalf("附件上传响应缺少 attachment: %s", raw)
	}
	return attachment
}

// UploadRawWithPurpose 上传任意内容并声明用途，返回状态码、JSON 与原始响应体
// （用于非图片拒绝等失败断言）。
func (c *Client) UploadRawWithPurpose(purpose, filename string, content []byte) (int, map[string]any, string) {
	c.t.Helper()
	status, _, payload, raw := c.uploadMultipart(filename, purpose, content)
	return status, payload, raw
}

func (c *Client) uploadMultipart(filename, purpose string, content []byte) (int, http.Header, map[string]any, string) {
	c.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		c.t.Fatal(err)
	}
	if err := writer.WriteField("purpose", purpose); err != nil {
		c.t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		c.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, c.base+"/api/v1/attachments", &body)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", c.csrf)
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		c.t.Fatal(err)
	}
	payload := make(map[string]any)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &payload); err != nil {
			c.t.Fatalf("接口返回非 JSON: %s", string(data))
		}
	}
	return resp.StatusCode, resp.Header, payload, string(data)
}

// RawGET 发起原始 GET，返回状态码、内容类型与响应体前 16 字节（用于二进制/缓存头断言）。
func (c *Client) RawGET(path string) (int, string, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(resp.Body, head)
	return resp.StatusCode, resp.Header.Get("Content-Type"), head[:n]
}

// DefaultUserPassword 是集成测试普通用户密码。
const DefaultUserPassword = "user-password-123"

// integrationPNGBytes 是一张 8x8 的合法 PNG（与旧集成测试一致）。
var integrationPNGBytes = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x08,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x4b, 0x6d, 0x29,
	0xdc, 0x00, 0x00, 0x00, 0x13, 0x49, 0x44, 0x41,
	0x54, 0x08, 0xd7, 0x63, 0xfc, 0xcf, 0xc0, 0xf0,
	0x9f, 0x81, 0x81, 0x01, 0x00, 0x00, 0x00, 0xff,
	0xff, 0x03, 0x00, 0x0f, 0x2c, 0x03, 0x8b, 0xd8,
	0x4e, 0x4c, 0x51, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// 断言与 JSON 取值辅助。

// ExpectStatus 断言 HTTP 状态码符合预期。
func ExpectStatus(t *testing.T, got, want int, raw string) {
	t.Helper()
	if got != want {
		t.Fatalf("HTTP 状态不符: got=%d want=%d body=%s", got, want, raw)
	}
}

// MustSetCSRF 从响应 JSON 中读取 csrf_token 并写入 client。
func MustSetCSRF(t *testing.T, payload map[string]any, client *Client) {
	t.Helper()
	token, ok := payload["csrf_token"].(string)
	if !ok || token == "" {
		t.Fatalf("响应缺少 csrf_token: %v", payload)
	}
	client.SetCSRF(token)
}

// MustUser 读取响应中的 user 对象。
func MustUser(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	user, ok := payload["user"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 user: %v", payload)
	}
	return user
}

// MustNested 读取响应中的嵌套对象。
func MustNested(t *testing.T, payload map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := payload[key].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 %s: %v", key, payload)
	}
	return value
}

// MustString 读取响应中的非空字符串字段。
func MustString(t *testing.T, payload map[string]any, key string) string {
	t.Helper()
	value, ok := payload[key].(string)
	if !ok || value == "" {
		t.Fatalf("响应缺少字符串字段 %s: %v", key, payload)
	}
	return value
}

// ErrorCode 读取统一错误响应中的 code。
func ErrorCode(payload map[string]any) string {
	if errObj, ok := payload["error"].(map[string]any); ok {
		if code, ok := errObj["code"].(string); ok {
			return code
		}
	}
	return ""
}

// ListContains 判断列表响应 items 中是否存在某字段等于 want 的项。
func ListContains(payload map[string]any, key, want string) bool {
	items, _ := payload["items"].([]any)
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if value, ok := entry[key].(string); ok && value == want {
			return true
		}
	}
	return false
}

// ListHasType 判断列表响应中是否存在指定 type。
func ListHasType(payload map[string]any, want string) bool {
	return ListContains(payload, "type", want)
}

// ListHasStatus 判断列表响应中是否存在指定 status。
func ListHasStatus(payload map[string]any, want string) bool {
	return ListContains(payload, "status", want)
}

// ListHasKind 判断列表响应中是否存在指定 kind。
func ListHasKind(payload map[string]any, want string) bool {
	return ListContains(payload, "kind", want)
}

// PathEscape 转义用户名等路径片段（保持测试请求路径与直接拼接语义一致）。
func PathEscape(value string) string { return url.PathEscape(value) }
