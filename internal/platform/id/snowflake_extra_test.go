package id

import (
	"strings"
	"testing"
	"time"
)

// TestNextEncodesWorkerAndTimestamp 校验雪花 ID 位布局：序列位、worker 位与时间位
// 与 docs/DATA_MODEL.md 的约定一致（worker 可配置、时间近似当前时间）。
func TestNextEncodesWorkerAndTimestamp(t *testing.T) {
	generator, err := NewGenerator(maxWorker)
	if err != nil {
		t.Fatalf("NewGenerator(%d): %v", maxWorker, err)
	}
	value, err := generator.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if worker := (value >> seqBits) & maxWorker; worker != maxWorker {
		t.Fatalf("worker 位 = %d，期望 %d", worker, maxWorker)
	}
	if seq := value & maxSeq; seq < 0 || seq > maxSeq {
		t.Fatalf("序列位越界: %d", seq)
	}
	millis := (value >> (workerBits + seqBits)) + epochMS
	if drift := time.Now().UnixMilli() - millis; drift < -5_000 || drift > 5_000 {
		t.Fatalf("ID 时间位与当前时间偏差过大: %dms", drift)
	}
}

// TestDifferentWorkersProduceDistinctIDs 校验不同 worker 生成的 ID 互不相同，
// 多实例部署时只需保证 worker 唯一即可避免冲突。
func TestDifferentWorkersProduceDistinctIDs(t *testing.T) {
	first, err := NewGenerator(0)
	if err != nil {
		t.Fatalf("NewGenerator(0): %v", err)
	}
	second, err := NewGenerator(1)
	if err != nil {
		t.Fatalf("NewGenerator(1): %v", err)
	}
	a, err := first.Next()
	if err != nil {
		t.Fatalf("first.Next: %v", err)
	}
	b, err := second.Next()
	if err != nil {
		t.Fatalf("second.Next: %v", err)
	}
	if a == b {
		t.Fatal("不同 worker 的 ID 不应相同")
	}
	if (a>>seqBits)&maxWorker == (b>>seqBits)&maxWorker {
		t.Fatalf("worker 位未正确编码: a=%d b=%d", a, b)
	}
}

// TestNewGeneratorErrorMentionsBounds 校验 worker 越界错误信息包含允许范围，便于配置排障。
func TestNewGeneratorErrorMentionsBounds(t *testing.T) {
	_, err := NewGenerator(maxWorker + 1)
	if err == nil {
		t.Fatal("越界 worker 应报错")
	}
	if !strings.Contains(err.Error(), "0-1023") {
		t.Fatalf("错误信息应包含允许范围 0-1023: %v", err)
	}
}
