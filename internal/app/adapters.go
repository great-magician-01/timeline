package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	platformaudit "github.com/great-magician-01/timeline/internal/platform/audit"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/platform/eventbus"
	platformjobs "github.com/great-magician-01/timeline/internal/platform/jobs"
	platformsettings "github.com/great-magician-01/timeline/internal/platform/settings"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// 编译期断言：组合根提供的适配器满足模块依赖的 contracts 端口。
var (
	_ contracts.SettingsReader    = settingsAdapter{}
	_ contracts.SettingsWriter    = settingsAdapter{}
	_ contracts.SettingsService   = settingsServiceAdapter{}
	_ contracts.AuditWriter       = auditAdapter{}
	_ contracts.JobsQueue         = jobsAdapter{}
	_ contracts.RealtimePublisher = realtimeAdapter{}
)

// ===== platform/settings → contracts.SettingsReader / SettingsWriter =====

type settingsAdapter struct {
	store *platformsettings.Store
}

func (a settingsAdapter) Settings(ctx context.Context) (contracts.SystemSettings, error) {
	values, err := a.store.Load(ctx)
	if err != nil {
		return contracts.SystemSettings{}, err
	}
	return contracts.SystemSettings{
		RegistrationEnabled:     values.RegistrationEnabled,
		AIGlobalEnabled:         values.AIGlobalEnabled,
		MaxUploadBytes:          values.MaxUploadBytes,
		MaxRequestBytes:         values.MaxRequestBytes,
		StorageQuotaBytes:       values.StorageQuotaBytes,
		PostRateLimit:           values.PostRateLimit,
		PostRateWindow:          values.PostRateWindow,
		UploadRateLimit:         values.UploadRateLimit,
		UploadRateWindow:        values.UploadRateWindow,
		TempAttachmentRetention: values.TempAttachmentRetention,
		CallLogMaxBytes:         values.CallLogMaxBytes,
		CallLogRetentionDays:    values.CallLogRetentionDays,
	}, nil
}

func (a settingsAdapter) WebSearch(ctx context.Context) (contracts.WebSearchSettings, error) {
	values, err := a.store.LoadWebSearch(ctx)
	if err != nil {
		return contracts.WebSearchSettings{}, err
	}
	return contracts.WebSearchSettings{BaseURL: values.BaseURL, APIKeyCipher: values.APIKeyCipher}, nil
}

func (a settingsAdapter) Set(ctx context.Context, in contracts.SettingsWrite) error {
	return a.store.Set(ctx, in.Key, in.Value, in.AdminID, in.At)
}

// settingsServiceAdapter 同时暴露读写能力（admin 使用 contracts.SettingsService）。
type settingsServiceAdapter struct{ settingsAdapter }

// ===== platform/audit → contracts.AuditWriter =====

type auditAdapter struct {
	writer platformaudit.Writer
}

func (a auditAdapter) Write(ctx context.Context, entry contracts.AuditEntry) error {
	return a.writer.Write(ctx, platformaudit.Entry{
		AdminID:    entry.AdminID,
		Action:     entry.Action,
		TargetType: entry.TargetType,
		TargetID:   entry.TargetID,
		Detail:     entry.Detail,
		At:         entry.At,
	})
}

// ===== platform/eventbus → contracts.RealtimePublisher =====

// realtimeAdapter 把进程内事件总线适配为模块依赖的实时发布端口。
// Publish 自身 recover，满足 db.AfterCommit 回调「不得 panic」的契约。
type realtimeAdapter struct {
	broker *eventbus.Broker
}

func (a realtimeAdapter) PublishNotification(recipientID int64) {
	if a.broker == nil {
		return
	}
	a.broker.Publish(recipientID, eventbus.Event{Kind: eventbus.KindNotification})
}

func (a realtimeAdapter) PublishDM(userID, conversationID int64) {
	if a.broker == nil {
		return
	}
	a.broker.Publish(userID, eventbus.Event{Kind: eventbus.KindDM, ConversationID: conversationID})
}

func (a realtimeAdapter) PublishGroup(userID, groupID int64) {
	if a.broker == nil {
		return
	}
	a.broker.Publish(userID, eventbus.Event{Kind: eventbus.KindGroup, ConversationID: groupID})
}

// ===== platform/jobs → contracts.JobsQueue =====
//
// Enqueue/CancelPending 走 platform/jobs 的既有方法（ctx 携带事务时自动复用）；
// 人工处置（Pause/Retry/Cancel/Reactivate/SetJobStatus/Get/ListByRef）已下沉到
// platform/jobs，组合根不做任何 SQL。
type jobsAdapter struct {
	pool   *pgxpool.Pool
	runner *platformjobs.Runner
}

func (a jobsAdapter) Enqueue(ctx context.Context, in contracts.JobEnqueue) error {
	return a.runner.Enqueue(ctx, db.ExecutorFrom(ctx, a.pool), platformjobs.EnqueueParams{
		Type:           in.Type,
		RefType:        in.RefType,
		RefID:          in.RefID,
		IdempotencyKey: in.IdempotencyKey,
		Payload:        in.Payload,
		RunAt:          in.RunAt,
		MaxAttempts:    in.MaxAttempts,
		At:             in.At,
	})
}

func (a jobsAdapter) CancelPending(ctx context.Context, in contracts.CancelPendingJobsInput) error {
	return a.runner.CancelPending(ctx, db.ExecutorFrom(ctx, a.pool), in.JobType, in.RefType, in.RefID)
}

func (a jobsAdapter) PausePending(ctx context.Context, in contracts.PausePendingJobsInput) (int, error) {
	return a.runner.PausePending(ctx, in)
}

func (a jobsAdapter) PauseAllPending(ctx context.Context, in contracts.PauseAllPendingInput) (int, error) {
	return a.runner.PauseAllPending(ctx, in)
}

func (a jobsAdapter) RetryJob(ctx context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	return a.runner.RetryJob(ctx, in)
}

func (a jobsAdapter) CancelJob(ctx context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	return a.runner.CancelJob(ctx, in)
}

func (a jobsAdapter) Reactivate(
	ctx context.Context, idempotencyKey string, runAt, at time.Time,
) (bool, error) {
	return a.runner.Reactivate(ctx, idempotencyKey, runAt, at)
}

func (a jobsAdapter) SetJobStatus(ctx context.Context, jobID int64, status string, at time.Time) error {
	return a.runner.SetJobStatus(ctx, jobID, status, at)
}

func (a jobsAdapter) GetJob(ctx context.Context, jobID int64) (contracts.JobSnapshot, error) {
	return a.runner.GetJob(ctx, jobID)
}

func (a jobsAdapter) ListByRef(ctx context.Context, query contracts.JobRefQuery) ([]contracts.JobSnapshot, error) {
	return a.runner.ListByRef(ctx, query)
}
