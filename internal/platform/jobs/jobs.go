// Package jobs 是单实例部署下的通用后台作业运行器（目标架构 platform/jobs）：
//   - 作业持久化在 timeline.background_jobs，应用重启后继续执行；
//   - 领取作业使用 UPDATE ... FOR UPDATE SKIP LOCKED，原子且不会重复执行；
//   - 失败按有限次数与指数退避重试，超过上限进入 failed 等待人工重试；
//   - 只更新状态，不物理删除作业记录。
//
// 时间约定：运行期时间一律来自注入的 clock.Clock，SQL 中不出现 now()/CURRENT_DATE/
// interval 等数据库时间函数。领取时间、完成时间、恢复截止时间与重试时间都由应用
// 计算后作为显式参数传入，便于 fake clock 测试与多实例时钟治理。
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/platform/db"
)

// 作业状态（与 background_jobs.status 约束一致）。
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusPaused    = "paused"
	StatusCanceled  = "canceled"
)

// 作业类型。
const (
	TypePostPublish       = "post_publish"
	TypeMediaDerivative   = "media_derivative"
	TypeAttachmentCleanup = "attachment_cleanup"
	TypeAISchedule        = "ai_schedule"
	TypeAITask            = "ai_task"
	TypeMemoryConsolidate = "memory_consolidation"
	TypeCallLogCleanup    = "ai_call_log_cleanup"
)

// Job 是领取到的作业快照。
type Job struct {
	ID          int64
	Type        string
	RefType     string
	RefID       *int64
	Payload     []byte
	Attempts    int
	MaxAttempts int
	// RunAt 是作业的计划执行时间。处理器可用它识别“被领取后计划时间又被改写”的
	// 过期作业（例如作者修改了定时发布时间），从而放弃执行。
	RunAt time.Time
}

// PayloadJSON 解析作业负载。
func (j Job) PayloadJSON(dst any) error {
	if len(j.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(j.Payload, dst)
}

// Handler 执行作业。返回错误表示本次执行失败，由运行器决定重试。
type Handler func(ctx context.Context, job Job) error

// ErrHandled 表示处理器已经自行写入作业终态（例如账号停用把定时发布改为 paused），
// 运行器不得再改动该作业的状态。
var ErrHandled = errors.New("作业已由处理器写入终态")

// PoolProvider 提供连接池；连接池未就绪时返回 nil，运行器据此空转等待。
// 直接复用 platform/db 的连接池抽象，组合根可注入 db.Readiness 等同一对象。
type PoolProvider = db.PoolProvider

// IDGenerator 生成雪花 ID；与 platform/id 同形，避免包之间相互依赖。
type IDGenerator interface {
	Next() (int64, error)
}

// Querier 是连接池与事务共用的执行接口。
// 对应设计文档中的 db.Executor，保留别名以维持旧调用方 Enqueue/CancelPending 的
// 签名形状（当调用方在事务内入队时由它传入事务执行器）。
type Querier = db.Executor

// 编译期断言：连接池与事务都满足 Querier。
var (
	_ Querier = (*pgxpool.Pool)(nil)
	_ Querier = (pgx.Tx)(nil)
)
