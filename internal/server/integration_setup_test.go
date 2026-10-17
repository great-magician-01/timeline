// Package server_test 是目标架构下的 HTTP 集成测试（外部测试包）：
// 通过 internal/testkit 构造真实组合根（app.New）+ 隔离测试 schema + httptest 服务，
// 只做黑盒 HTTP 断言，不依赖 production 包内部结构。
//
// 本文件是测试共用的 harness/兼容层：共享构造入口（setupIntegrationServer）与
// 旧测试沿用的小工具（client 包装、断言、后缀等）都在这里统一提供，
// 具体实现全部委托 internal/testkit；其他测试文件不要重复定义这些名字。
//
// 集成测试默认跳过；设置 TIMELINE_INTEGRATION=1 后连接开发实例中的
// timeline_test / user_test 隔离 schema 运行。
package server_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// 共享测试账号约定值（旧测试名保持兼容）。
const (
	integrationAdminUsername = testkit.IntegrationAdminUsername
	integrationAdminEmail    = testkit.IntegrationAdminEmail
	integrationAdminPassword = testkit.IntegrationAdminPassword
	integrationModUsername   = testkit.IntegrationModUsername
	integrationModEmail      = testkit.IntegrationModEmail
)

// IntegrationEnabled 报告是否显式开启集成测试（供需要自行 Skip 的用例使用）。
func IntegrationEnabled() bool { return testkit.IntegrationEnabled() }

// integrationClient 是 testkit.Client 的兼容别名。
type integrationClient = testkit.Client

// setupIntegrationServer 在共享测试 schema（timeline_test/user_test）上构造完整服务：
// 迁移 + 注册开关恢复 + 幂等测试管理员 + app.New + httptest。
// 调用方负责在未设置 TIMELINE_INTEGRATION=1 时先 Skip（testkit 内部也会 Skip）。
func setupIntegrationServer(t *testing.T) (*pgxpool.Pool, *testkit.Server, string) {
	t.Helper()
	srv := testkit.NewIntegrationServer(t)
	sharedIntegrationBaseURL = srv.BaseURL
	return srv.Pool, srv, srv.BaseURL
}

// newIntegrationClient 创建带独立 Cookie jar 的测试客户端。
// base 为空时使用共享集成服务的地址（setupIntegrationServer 的第三个返回值）。
func newIntegrationClient(t *testing.T, base string) *integrationClient {
	t.Helper()
	if base == "" {
		base = sharedIntegrationBaseURL
	}
	return testkit.NewClient(t, base)
}

// sharedIntegrationBaseURL 由 setupIntegrationServer 记录，供未显式传 base 的用例使用。
var sharedIntegrationBaseURL string

// newIntegrationClientFor 创建绑定到给定服务的客户端（新测试推荐用法）。
func newIntegrationClientFor(t *testing.T, srv *testkit.Server) *integrationClient {
	t.Helper()
	return srv.Client(t)
}

// registerIntegrationUser 注册并登录：注册接口只创建账号，不下发会话，
// 后续写操作需要登录后的会话 CSRF 令牌。
func registerIntegrationUser(t *testing.T, client *integrationClient, username, email string) {
	t.Helper()
	client.RegisterAndLogin(username, email)
}

// loginIntegrationUser 登录并记录会话 CSRF 令牌。
func loginIntegrationUser(t *testing.T, client *integrationClient, identifier, password string) {
	t.Helper()
	client.LoginAndSetCSRF(identifier, password)
}

// integrationSuffix 生成用于隔离本次测试数据的唯一后缀。
func integrationSuffix() string { return testkit.IntegrationSuffix() }

// integrationShortSuffix 返回较短的后缀，用于受 32 位长度限制的用户名。
func integrationShortSuffix() string { return testkit.IntegrationShortSuffix() }

// integrationDSN 在开发实例 DSN 上覆盖 search_path 到隔离测试 schema。
func integrationDSN(productionDSN, timelineSchema, userSchema string) (string, error) {
	return testkit.IntegrationDSN(productionDSN, timelineSchema, userSchema)
}

// ===== JSON 取值与断言（委托 testkit） =====

// expectIntegrationStatus 断言 HTTP 状态码符合预期。
func expectIntegrationStatus(t *testing.T, got, want int, raw string) {
	t.Helper()
	testkit.ExpectStatus(t, got, want, raw)
}

// integrationCSRF 从响应中读取 csrf_token 并写入客户端。
func integrationCSRF(payload map[string]any, t *testing.T, client *integrationClient) {
	t.Helper()
	testkit.MustSetCSRF(t, payload, client)
}

// integrationUser 读取响应中的 user 对象。
func integrationUser(payload map[string]any, t *testing.T) map[string]any {
	t.Helper()
	return testkit.MustUser(t, payload)
}

// integrationNested 读取响应中的嵌套对象。
func integrationNested(payload map[string]any, t *testing.T, key string) map[string]any {
	t.Helper()
	return testkit.MustNested(t, payload, key)
}

// integrationString 读取响应中的非空字符串字段。
func integrationString(payload map[string]any, t *testing.T, key string) string {
	t.Helper()
	return testkit.MustString(t, payload, key)
}

// integrationErrorCode 读取统一错误响应中的 code。
func integrationErrorCode(payload map[string]any) string { return testkit.ErrorCode(payload) }

// integrationListContains 判断列表响应 items 中是否存在某字段等于 want 的项。
func integrationListContains(payload map[string]any, key, want string) bool {
	return testkit.ListContains(payload, key, want)
}

// integrationListHasType 判断列表响应中是否存在指定 type。
func integrationListHasType(payload map[string]any, want string) bool {
	return testkit.ListHasType(payload, want)
}

// integrationListHasStatus 判断列表响应中是否存在指定 status。
func integrationListHasStatus(payload map[string]any, want string) bool {
	return testkit.ListHasStatus(payload, want)
}

// integrationListHasKind 判断列表响应中是否存在指定 kind。
func integrationListHasKind(payload map[string]any, want string) bool {
	return testkit.ListHasKind(payload, want)
}

// ===== HTTP 附件/二进制辅助 =====

// uploadIntegrationPNG 上传一张 8x8 的合法 PNG，返回附件 ID。
func uploadIntegrationPNG(t *testing.T, client *integrationClient) string {
	t.Helper()
	return client.Upload()
}

// rawIntegrationGET 发起原始 GET，返回状态码、内容类型与前 16 字节。
func rawIntegrationGET(t *testing.T, client *integrationClient, path string) (int, string, []byte) {
	t.Helper()
	return client.RawGET(path)
}

// substituteRouteParams 把 Gin 路径参数替换为占位值，便于直接发起请求。
func substituteRouteParams(path string) string { return testkit.SubstituteRouteParams(path) }
