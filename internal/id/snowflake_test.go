package id

import "testing"

func TestNextMonotonicAndUnique(t *testing.T) {
	g, err := NewGenerator(1)
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}

	const n = 20000
	seen := make(map[int64]struct{}, n)
	var last int64
	for i := range n {
		v, err := g.Next()
		if err != nil {
			t.Fatalf("Next #%d: %v", i, err)
		}
		if v <= last {
			t.Fatalf("ID 非严格递增: %d 之后出现 %d", last, v)
		}
		if _, dup := seen[v]; dup {
			t.Fatalf("ID 重复: %d", v)
		}
		seen[v] = struct{}{}
		last = v
	}
}

func TestWorkerBounds(t *testing.T) {
	if _, err := NewGenerator(-1); err == nil {
		t.Fatal("worker=-1 应报错")
	}
	if _, err := NewGenerator(1024); err == nil {
		t.Fatal("worker=1024 应报错")
	}
	if _, err := NewGenerator(1023); err != nil {
		t.Fatalf("worker=1023 应通过: %v", err)
	}
}
