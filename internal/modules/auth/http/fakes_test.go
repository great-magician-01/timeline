package http

// 本文提供 auth/http 单元测试的固定装配：用 app.New(fake Deps) 构造 *app.Service，
// 再用 gin 引擎挂上与 transport/router 策略一致的中间件链，覆盖 handler 的参数校验、
// 错误映射、Cookie 属性与中间件状态映射。全部 fake 不访问数据库/网络。
//
// 复用已有测试文件中的 newTestContext/newJSONContext/errorCode/zeroService；
// 端口 fake 通过内嵌接口只实现用例实际调用到的方法。

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

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/great-magician-01/timeline/internal/modules/auth/app"
	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// fakePortErr 是注入端口错误的统一底层错误。
var fakePortErr = errors.New("fake 端口错误")

// testNow 是全部 HTTP 用例共用的固定业务时间。
var testNow = time.Date(2026, 10, 18, 9, 30, 0, 0, clock.AsiaShanghai)

// cheapPasswordHash 生成最低成本 bcrypt 哈希，避免 cost=12 拖慢用例。
func cheapPasswordHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成低成本测试哈希失败: %v", err)
	}
	return string(hash)
}

// ===== 账号仓储 fake =====

type fakeUsers struct {
	app.UserRepo
	users map[int64]model.User
	seq   int64

	byIDErr              error
	byIDForUpdateErr     error
	byUsernameErr        error
	byLoginIdentifierErr error
	insertErr            error
	updateProfileErr     error
	updateFollowErr      error
	updateRoleErr        error
	updateStatusErr      error
	updatePasswordErr    error
	updateAvatarErr      error
	searchErr            error
	countHumanErr        error
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{users: map[int64]model.User{}}
}

func (f *fakeUsers) nextID() int64 {
	f.seq++
	return f.seq
}

func (f *fakeUsers) Insert(_ context.Context, user model.User) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	for _, existing := range f.users {
		if strings.EqualFold(existing.Username, user.Username) {
			return apperr.Conflict("duplicate", "用户名已存在")
		}
		if existing.Email != "" && strings.EqualFold(existing.Email, user.Email) {
			return apperr.Conflict("duplicate", "邮箱已存在")
		}
	}
	f.users[user.ID] = user
	return nil
}

func (f *fakeUsers) ByID(_ context.Context, id int64) (model.User, error) {
	if f.byIDErr != nil {
		return model.User{}, f.byIDErr
	}
	user, ok := f.users[id]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return user, nil
}

func (f *fakeUsers) ByIDForUpdate(ctx context.Context, id int64) (model.User, error) {
	if f.byIDForUpdateErr != nil {
		return model.User{}, f.byIDForUpdateErr
	}
	return f.ByID(ctx, id)
}

func (f *fakeUsers) ByUsername(_ context.Context, username string) (model.User, error) {
	if f.byUsernameErr != nil {
		return model.User{}, f.byUsernameErr
	}
	for _, user := range f.users {
		if strings.EqualFold(user.Username, username) {
			return user, nil
		}
	}
	return model.User{}, apperr.NotFound("用户不存在")
}

func (f *fakeUsers) ByLoginIdentifier(_ context.Context, identifier string) (model.User, error) {
	if f.byLoginIdentifierErr != nil {
		return model.User{}, f.byLoginIdentifierErr
	}
	for _, user := range f.users {
		if strings.EqualFold(user.Username, identifier) ||
			(user.Email != "" && strings.EqualFold(user.Email, identifier)) {
			return user, nil
		}
	}
	return model.User{}, apperr.NotFound("用户不存在")
}

func (f *fakeUsers) UpdateProfile(_ context.Context, id int64, nickname, bio, avatarURL *string, at time.Time) (model.User, error) {
	if f.updateProfileErr != nil {
		return model.User{}, f.updateProfileErr
	}
	user, ok := f.users[id]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	if nickname != nil {
		user.Nickname = *nickname
	}
	if bio != nil {
		user.Bio = *bio
	}
	if avatarURL != nil {
		user.AvatarURL = *avatarURL
	}
	user.UpdatedAt = at
	f.users[id] = user
	return user, nil
}

func (f *fakeUsers) UpdateFollowApproval(_ context.Context, id int64, enabled bool, at time.Time) (bool, error) {
	if f.updateFollowErr != nil {
		return false, f.updateFollowErr
	}
	user, ok := f.users[id]
	if !ok {
		return false, apperr.NotFound("用户不存在")
	}
	user.RequireFollowApproval = enabled
	user.UpdatedAt = at
	f.users[id] = user
	return enabled, nil
}

func (f *fakeUsers) UpdateRole(_ context.Context, id int64, role string, at time.Time) (model.User, error) {
	if f.updateRoleErr != nil {
		return model.User{}, f.updateRoleErr
	}
	user, ok := f.users[id]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	user.Role = role
	user.UpdatedAt = at
	f.users[id] = user
	return user, nil
}

func (f *fakeUsers) UpdatePassword(_ context.Context, id int64, hash string, tokenVersion int64, at time.Time) error {
	if f.updatePasswordErr != nil {
		return f.updatePasswordErr
	}
	user, ok := f.users[id]
	if !ok {
		return apperr.NotFound("用户不存在")
	}
	user.PasswordHash = hash
	user.TokenVersion = tokenVersion
	user.UpdatedAt = at
	f.users[id] = user
	return nil
}

func (f *fakeUsers) UpdateAvatar(_ context.Context, id int64, _ *int64, avatarURL string, at time.Time) error {
	if f.updateAvatarErr != nil {
		return f.updateAvatarErr
	}
	user, ok := f.users[id]
	if !ok {
		return apperr.NotFound("用户不存在")
	}
	user.AvatarURL = avatarURL
	user.UpdatedAt = at
	f.users[id] = user
	return nil
}

func (f *fakeUsers) Search(_ context.Context, keyword string, _ int64, limit int) ([]model.User, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	result := make([]model.User, 0, limit)
	for _, user := range f.users {
		if strings.Contains(strings.ToLower(user.Username), keyword) ||
			strings.Contains(strings.ToLower(user.Nickname), keyword) {
			result = append(result, user)
		}
	}
	return result, nil
}

func (f *fakeUsers) CountHumanUsers(_ context.Context) (int64, error) {
	if f.countHumanErr != nil {
		return 0, f.countHumanErr
	}
	var count int64
	for _, user := range f.users {
		if !user.IsAI {
			count++
		}
	}
	return count, nil
}

// ===== 会话仓储 fake =====

type fakeSessionEntry struct {
	user    model.User
	session model.Session
}

type fakeSessions struct {
	app.SessionRepo
	users        *fakeUsers
	sessions     map[string]fakeSessionEntry
	lastInsertAt *time.Time

	insertErr          error
	lookupErr          error
	revokeErr          error
	revokeAllErr       error
	revokeAllExceptErr error
	updateVersionErr   error
}

func newFakeSessions(users *fakeUsers) *fakeSessions {
	return &fakeSessions{users: users, sessions: map[string]fakeSessionEntry{}}
}

func (f *fakeSessions) Insert(_ context.Context, session model.Session, tokenHash string, createdAt time.Time) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	user, ok := f.users.users[session.UserID]
	if !ok {
		return apperr.NotFound("用户不存在")
	}
	at := createdAt
	f.lastInsertAt = &at
	f.sessions[tokenHash] = fakeSessionEntry{user: user, session: session}
	return nil
}

func (f *fakeSessions) ByTokenHash(_ context.Context, tokenHash string) (model.User, model.Session, error) {
	if f.lookupErr != nil {
		return model.User{}, model.Session{}, f.lookupErr
	}
	entry, ok := f.sessions[tokenHash]
	if !ok {
		return model.User{}, model.Session{}, apperr.NotFound("会话不存在")
	}
	// 重新读取账号行，模拟 JOIN 拿到最新状态（停用/角色变更即时生效）。
	if user, ok := f.users.users[entry.user.ID]; ok {
		entry.user = user
	}
	return entry.user, entry.session, nil
}

func (f *fakeSessions) Revoke(_ context.Context, sessionID int64, at time.Time) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	for hash, entry := range f.sessions {
		if entry.session.ID == sessionID {
			entry.session.RevokedAt = &at
			f.sessions[hash] = entry
		}
	}
	return nil
}

func (f *fakeSessions) RevokeAllForUser(_ context.Context, userID int64, at time.Time) error {
	if f.revokeAllErr != nil {
		return f.revokeAllErr
	}
	for hash, entry := range f.sessions {
		if entry.session.UserID == userID {
			entry.session.RevokedAt = &at
			f.sessions[hash] = entry
		}
	}
	return nil
}

func (f *fakeSessions) RevokeAllForUserExcept(_ context.Context, userID, exceptSessionID int64, at time.Time) error {
	if f.revokeAllExceptErr != nil {
		return f.revokeAllExceptErr
	}
	for hash, entry := range f.sessions {
		if entry.session.UserID == userID && entry.session.ID != exceptSessionID {
			entry.session.RevokedAt = &at
			f.sessions[hash] = entry
		}
	}
	return nil
}

func (f *fakeSessions) UpdateTokenVersion(_ context.Context, sessionID, tokenVersion int64) error {
	if f.updateVersionErr != nil {
		return f.updateVersionErr
	}
	for hash, entry := range f.sessions {
		if entry.session.ID == sessionID {
			entry.session.TokenVersion = tokenVersion
			f.sessions[hash] = entry
		}
	}
	return nil
}

// ===== 其余端口 fake =====

type fakeTx struct{}

func (fakeTx) WithTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeIDs struct{ next int64 }

func (f *fakeIDs) Next() (int64, error) {
	f.next++
	return f.next, nil
}

type fakeLocker struct{}

func (fakeLocker) AcquireBootstrapLock(context.Context) error { return nil }

type fakeSettings struct {
	enabled bool
	readErr error
}

func (f *fakeSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	if f.readErr != nil {
		return contracts.SystemSettings{}, f.readErr
	}
	return contracts.SystemSettings{RegistrationEnabled: f.enabled}, nil
}

func (f *fakeSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	return contracts.WebSearchSettings{}, nil
}

type fakeAudit struct {
	entries []contracts.AuditEntry
	err     error
}

func (f *fakeAudit) Write(_ context.Context, entry contracts.AuditEntry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, entry)
	return nil
}

type fakeRelations struct {
	state string
	err   error
}

func (f *fakeRelations) IsFollowing(context.Context, int64, int64) (bool, error) {
	return f.state == model.FollowStateFollowing, f.err
}

func (f *fakeRelations) FollowingSet(context.Context, int64) (map[int64]bool, error) {
	return map[int64]bool{}, f.err
}

func (f *fakeRelations) FollowState(context.Context, int64, int64) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.state == "" {
		return model.FollowStateNone, nil
	}
	return f.state, nil
}

type fakeStats struct {
	value app.ProfileStats
	err   error
}

func (f *fakeStats) ProfileStats(context.Context, int64) (app.ProfileStats, error) {
	return f.value, f.err
}

// ===== 测试装配 =====

// harness 是一次 HTTP 测试的完整装配（每个用例独立构造，避免跨用例状态污染）。
type harness struct {
	svc       *app.Service
	handler   *Handler
	users     *fakeUsers
	sessions  *fakeSessions
	settings  *fakeSettings
	audit     *fakeAudit
	relations *fakeRelations
	stats     *fakeStats
	clock     *clock.Fake
}

// newHarness 构造装配；registrationEnabled 控制注册开关初始状态。
func newHarness(t *testing.T, registrationEnabled bool) *harness {
	t.Helper()
	users := newFakeUsers()
	sessions := newFakeSessions(users)
	h := &harness{
		users:     users,
		sessions:  sessions,
		settings:  &fakeSettings{enabled: registrationEnabled},
		audit:     &fakeAudit{},
		relations: &fakeRelations{},
		stats:     &fakeStats{},
		clock:     clock.NewFake(testNow),
	}
	h.svc = app.New(app.Deps{
		Users:        users,
		Sessions:     sessions,
		Locks:        fakeLocker{},
		Tx:           fakeTx{},
		IDs:          &fakeIDs{next: 900000},
		Clock:        h.clock,
		Settings:     h.settings,
		Audit:        h.audit,
		Relations:    h.relations,
		Stats:        h.stats,
		CookieSecure: true,
	})
	h.handler = New(h.svc)
	return h
}

// seedUser 写入一个测试账号并补全默认字段。
func (h *harness) seedUser(t *testing.T, user model.User) model.User {
	t.Helper()
	if user.ID == 0 {
		user.ID = h.users.nextID()
	}
	if user.Nickname == "" {
		user.Nickname = user.Username
	}
	if user.Role == "" {
		user.Role = model.RoleUser
	}
	if user.Status == "" {
		user.Status = model.StatusActive
	}
	if user.TokenVersion == 0 {
		user.TokenVersion = 1
	}
	if user.CreatedAt.IsZero() {
		user.CreatedAt = testNow
		user.UpdatedAt = testNow
	}
	h.users.users[user.ID] = user
	return user
}

// seedSession 直接写入服务端会话，返回会话 ID；rawToken 为浏览器 Cookie 明文。
func (h *harness) seedSession(user model.User, rawToken, csrfToken string) model.Session {
	session := model.Session{
		ID:           user.ID*10 + 1,
		UserID:       user.ID,
		CSRFToken:    csrfToken,
		TokenVersion: user.TokenVersion,
		ExpiresAt:    testNow.Add(domain.SessionTTL),
	}
	h.sessions.sessions[domain.HashSessionToken(rawToken)] = fakeSessionEntry{user: user, session: session}
	return session
}

// engine 挂载与 transport/router 策略一致的最小中间件链。
func (h *harness) engine() *gin.Engine {
	engine := gin.New()
	engine.Use(h.handler.ResolveSession)
	engine.GET("/api/v1/auth/csrf", h.handler.CSRF)
	engine.GET("/api/v1/auth/config", h.handler.Config)
	engine.POST("/api/v1/auth/register", h.handler.GuestCSRF, h.handler.Register)
	engine.POST("/api/v1/auth/login", h.handler.GuestCSRF, h.handler.Login)
	engine.GET("/api/v1/auth/me", h.handler.RequireLogin, h.handler.Me)
	engine.POST("/api/v1/auth/logout", h.handler.RequireLogin, h.handler.RequireCSRF, h.handler.Logout)
	engine.POST("/api/v1/auth/password", h.handler.RequireLogin, h.handler.RequireCSRF, h.handler.ChangePassword)
	engine.POST("/api/v1/users/me", h.handler.RequireLogin, h.handler.RequireCSRF, h.handler.UpdateProfile)
	engine.GET("/api/v1/users/me/follow-settings", h.handler.RequireLogin, h.handler.FollowSettings)
	engine.POST("/api/v1/users/me/follow-settings", h.handler.RequireLogin, h.handler.RequireCSRF, h.handler.UpdateFollowSettings)
	engine.GET("/api/v1/users/search", h.handler.SearchUsers)
	engine.GET("/api/v1/users/:username", h.handler.GetProfile)
	engine.POST("/api/v1/admin/users/:id/grant-admin",
		h.handler.RequireLogin, h.handler.RequireAdmin, h.handler.RequireCSRF, h.handler.RequireSuperAdmin, h.handler.GrantAdmin)
	engine.POST("/api/v1/admin/users/:id/revoke-admin",
		h.handler.RequireLogin, h.handler.RequireAdmin, h.handler.RequireCSRF, h.handler.RequireSuperAdmin, h.handler.RevokeAdmin)
	engine.GET("/api/v1/public/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return engine
}

// ===== 请求 helper =====

type requestOption func(*http.Request)

func withCookie(name, value string) requestOption {
	return func(req *http.Request) { req.AddCookie(&http.Cookie{Name: name, Value: value}) }
}

func withHeader(name, value string) requestOption {
	return func(req *http.Request) { req.Header.Set(name, value) }
}

func withRemoteAddr(ip string) requestOption {
	return func(req *http.Request) { req.RemoteAddr = ip + ":54321" }
}

// guestCSRFHeaders 让 GuestCSRF 双提交校验通过（Cookie 与请求头一致）。
func guestCSRFHeaders(options ...requestOption) []requestOption {
	const token = "guest-csrf-token"
	return append([]requestOption{
		withCookie(GuestCSRFCookieName, token),
		withHeader(CSRFHeader, token),
	}, options...)
}

// perform 发送一次 HTTP 请求并返回响应记录器。
func (h *harness) perform(t *testing.T, engine *gin.Engine, method, path, body string, options ...requestOption) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, option := range options {
		option(req)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)
	return recorder
}

// performAs 以已登录会话身份发送请求（自动带上会话 Cookie 与 CSRF 头）。
func (h *harness) performAs(
	t *testing.T, engine *gin.Engine, method, path, body, rawToken, csrfToken string,
) *httptest.ResponseRecorder {
	t.Helper()
	return h.perform(t, engine, method, path, body,
		withCookie(SessionCookieName, rawToken), withHeader(CSRFHeader, csrfToken))
}

// login 调用真实登录接口，返回响应记录器、会话 Cookie 明文与 CSRF 令牌。
func (h *harness) login(t *testing.T, engine *gin.Engine, identifier, password, ip string) (*httptest.ResponseRecorder, string, string) {
	t.Helper()
	body := `{"identifier":"` + identifier + `","password":"` + password + `"}`
	recorder := h.perform(t, engine, http.MethodPost, "/api/v1/auth/login", body, guestCSRFHeaders(withRemoteAddr(ip))...)
	if recorder.Code != http.StatusOK {
		t.Fatalf("登录失败: 状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeJSONBody(t, recorder, &response)
	var sessionToken string
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == SessionCookieName {
			sessionToken = cookie.Value
		}
	}
	if sessionToken == "" || response.CSRFToken == "" {
		t.Fatalf("登录响应缺少会话 Cookie 或 CSRF: token=%q csrf=%q", sessionToken, response.CSRFToken)
	}
	return recorder, sessionToken, response.CSRFToken
}

func decodeJSONBody(t *testing.T, recorder *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), dst); err != nil {
		t.Fatalf("解析 JSON 响应失败: %v，body=%s", err, recorder.Body.String())
	}
}

// bodyMap 把响应体解析为 map，便于断言 JSON 形状。
func bodyMap(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	decodeJSONBody(t, recorder, &body)
	return body
}

// errorBody 解析统一错误响应 {error:{code,message,details}}。
func errorBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	body := bodyMap(t, recorder)
	errValue, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("错误响应缺少 error 对象: %s", recorder.Body.String())
	}
	return errValue
}

// errorDetails 读取错误响应中的字段详情。
func errorDetails(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	details, ok := errorBody(t, recorder)["details"].(map[string]any)
	if !ok {
		t.Fatalf("错误响应缺少 details: %s", recorder.Body.String())
	}
	return details
}

// responseCookie 从响应 Set-Cookie 中按名字取出 Cookie。
func responseCookie(t *testing.T, recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
