// Package id 提供雪花 ID 生成器。
// 约定见 docs/DATA_MODEL.md 第 3 章：BIGINT、后端生成、worker id 来自配置、时钟回拨防护。
package id

import (
	"fmt"
	"sync"
	"time"
)

const (
	// epochMS 纪元：2024-01-01T00:00:00+08:00（北京时间，与全系统时区约定一致）。
	epochMS = int64(1704038400000)

	workerBits = 10
	seqBits    = 12
	maxWorker  = int64(1)<<workerBits - 1 // 1023
	maxSeq     = int64(1)<<seqBits - 1    // 4095

	// 时钟回拨容忍上限：小于等于该值时等待追平，超过则拒绝生成。
	maxClockDriftMS = int64(5)
)

type Generator struct {
	mu     sync.Mutex
	worker int64
	lastMS int64
	seq    int64
}

// NewGenerator 创建生成器；worker 取值 0-1023（单机部署默认 1）。
func NewGenerator(worker int64) (*Generator, error) {
	if worker < 0 || worker > maxWorker {
		return nil, fmt.Errorf("雪花 ID worker 必须在 0-%d 之间，当前值: %d", maxWorker, worker)
	}
	return &Generator{worker: worker}, nil
}

// Next 生成下一个雪花 ID（近似时间递增）。
func (g *Generator) Next() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().UnixMilli()
	if now < g.lastMS {
		drift := g.lastMS - now
		if drift > maxClockDriftMS {
			return 0, fmt.Errorf("检测到时钟回拨 %dms，拒绝生成 ID", drift)
		}
		time.Sleep(time.Duration(drift) * time.Millisecond)
		now = time.Now().UnixMilli()
	}

	if now == g.lastMS {
		g.seq = (g.seq + 1) & maxSeq
		if g.seq == 0 { // 同毫秒序列耗尽，等待下一毫秒
			for now <= g.lastMS {
				now = time.Now().UnixMilli()
			}
		}
	} else {
		g.seq = 0
	}
	g.lastMS = now

	return ((now - epochMS) << (workerBits + seqBits)) | (g.worker << seqBits) | g.seq, nil
}
