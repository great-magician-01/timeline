package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 说明：本包测试只覆盖纯函数、字段校验、错误映射与「触库前」即可返回的分支。
// ai/app 的 Service 内部直连 *repo.Store，无法 fake 数据库；因此这里用 &Service{deps: ...}
// 直接构造（包内可见），不调用 New（New 会读连接池）。凡是校验之后必然访问 repo 的用例，
// 统一让事务管理器缺失（deps.Tx == nil）在进入事务前返回，从而不触库。

// testSecretKey 是 32 字节固定测试密钥，与任何真实密钥无关。
var testSecretKey = []byte("0123456789abcdef0123456789abcdef")

// testNow 是测试统一使用的应用时间。
var testNow = time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)

// testService 构造只用于纯逻辑与校验路径的 Service（repo 保持 nil）。
func testService(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = clock.NewFake(testNow)
	}
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	if deps.SecretKey == nil {
		deps.SecretKey = testSecretKey
	}
	return &Service{deps: deps}
}

// fakeUsers 是 contracts.UserReader 的测试实现，只覆写用例需要的方法；
// 未覆写的方法会因嵌入 nil 接口而 panic，提醒测试不要越界触库。
type fakeUsers struct {
	contracts.UserReader
	user    model.User
	getErr  error
	ids     []int64
	idsErr  error
	byName  model.User
	nameErr error
}

func (f fakeUsers) GetUser(context.Context, int64) (model.User, error) {
	return f.user, f.getErr
}

func (f fakeUsers) GetUserByUsername(context.Context, string) (model.User, error) {
	return f.byName, f.nameErr
}

func (f fakeUsers) ListUserIDsByAIFlag(context.Context, bool) ([]int64, error) {
	return f.ids, f.idsErr
}

// fakeIDs 是 IDGenerator 的测试实现。
type fakeIDs struct {
	id    int64
	err   error
	calls int
}

func (f *fakeIDs) Next() (int64, error) {
	f.calls++
	return f.id, f.err
}

// fakeAudit 记录审计写入。
type fakeAudit struct {
	entries []contracts.AuditEntry
	err     error
}

func (f *fakeAudit) Write(_ context.Context, entry contracts.AuditEntry) error {
	f.entries = append(f.entries, entry)
	return f.err
}

// fakeSettings 只实现全局网页搜索设置读取。
type fakeSettings struct {
	webSearch contracts.WebSearchSettings
	err       error
}

func (f fakeSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	return contracts.SystemSettings{}, nil
}

func (f fakeSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	return f.webSearch, f.err
}

// fakePgError 实现 SQLState，用于唯一冲突判断。
type fakePgError struct{ code string }

func (e fakePgError) Error() string { return "pg error " + e.code }

func (e fakePgError) SQLState() string { return e.code }

// aiUser 返回一个 AI 身份账号。
func aiUser(id int64) model.User {
	return model.User{ID: id, Username: "ai_" + formatID(id), Nickname: "AI", IsAI: true}
}

// humanUser 返回一个人类账号。
func humanUser(id int64) model.User {
	return model.User{ID: id, Username: "user_" + formatID(id), Nickname: "用户"}
}

// requireKind 断言错误是期望 Kind 的领域错误，并返回它便于继续断言。
func requireKind(t *testing.T, err error, kind apperr.Kind) *apperr.Error {
	t.Helper()
	e, ok := apperr.AsKind(err)
	if !ok {
		t.Fatalf("期望领域错误 %s，实际: %v", kind, err)
	}
	if e.Kind != kind {
		t.Fatalf("期望 Kind=%s，实际 Kind=%s（%v）", kind, e.Kind, err)
	}
	return e
}

// 指针辅助函数。
func strPtr(value string) *string { return &value }

func boolPtr(value bool) *bool { return &value }

func intPtr(value int) *int { return &value }

func floatPtr(value float64) *float64 { return &value }

// validationDetails 从校验错误中取出字段级详情。
func validationDetails(t *testing.T, err error) map[string]string {
	t.Helper()
	e := requireKind(t, err, apperr.KindValidation)
	details, ok := e.Details.(map[string]string)
	if !ok {
		t.Fatalf("校验错误应携带 map[string]string 详情，实际: %#v", e.Details)
	}
	return details
}
