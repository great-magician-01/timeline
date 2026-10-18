package audit

import "testing"

// fakeIDGenerator 生成连续 ID，供审计写入器单测使用。
type fakeIDGenerator struct{ next int64 }

func (g *fakeIDGenerator) Next() (int64, error) {
	g.next++
	return g.next, nil
}

// TestNewWriter 校验生产构造入口返回可用的 Writer；pool 只用于解析执行器，
// nil 连接池不会在构造阶段 panic（调用方由 ready 中间件保证业务请求不进未就绪状态）。
func TestNewWriter(t *testing.T) {
	w := NewWriter(nil, &fakeIDGenerator{})
	if w == nil {
		t.Fatal("NewWriter 不应返回 nil")
	}
	if _, ok := w.(*writer); !ok {
		t.Fatalf("NewWriter 应返回 *writer，实际 %T", w)
	}
}
