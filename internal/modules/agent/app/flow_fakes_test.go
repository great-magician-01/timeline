package app

// 本文件为 agent/app 用例测试补充的 fake 能力：在 review_fix_test.go 已有 fake 基础上
// 通过嵌入扩展（不重复声明已有方法），并补充 Counters/Notices/Tx 等端口 fake。
// 只用于测试，不进入生产代码。

import (
	"context"
	"errors"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== 内容端口扩展 =====

type flowContent struct {
	*fakeContent

	createdPosts     []contracts.ActorPostInput
	createdPost      model.Post
	createPostErr    error
	postsBatchErr    error
	getPost          map[int64]model.Post
	getPostErr       error
	excerpts         map[int64]string
	excerptErr       error
	candidates       []contracts.PostCandidate
	candidateErr     error
	candidateQueries []contracts.CandidateQuery
	draftList        []contracts.DraftSummary
	draftErr         error
	draftQueries     []contracts.DraftListQuery
	publishedDrafts  []contracts.PublishPostForActorInput
	publishErr       error
	updatedDrafts    []contracts.UpdateDraftForActorInput
	updateDraftErr   error
}

func (f *flowContent) CreatePostForActor(_ context.Context, in contracts.ActorPostInput) (model.Post, error) {
	f.createdPosts = append(f.createdPosts, in)
	if f.createPostErr != nil {
		return model.Post{}, f.createPostErr
	}
	if f.createdPost.ID > 0 {
		return f.createdPost, nil
	}
	return publicPost(7001, in.AuthorID), nil
}

func (f *flowContent) GetPosts(ctx context.Context, postIDs []int64) (map[int64]model.Post, error) {
	if f.postsBatchErr != nil {
		return nil, f.postsBatchErr
	}
	return f.fakeContent.GetPosts(ctx, postIDs)
}

func (f *flowContent) GetPost(_ context.Context, postID int64) (model.Post, error) {
	if f.getPostErr != nil {
		return model.Post{}, f.getPostErr
	}
	if post, ok := f.getPost[postID]; ok {
		return post, nil
	}
	if post, ok := f.posts[postID]; ok {
		return post, nil
	}
	return model.Post{}, notFoundError("帖子不存在")
}

func (f *flowContent) GetPostExcerpt(_ context.Context, postID int64) (string, error) {
	if f.excerptErr != nil {
		return "", f.excerptErr
	}
	return f.excerpts[postID], nil
}

func (f *flowContent) ListCandidatePosts(_ context.Context, query contracts.CandidateQuery) ([]contracts.PostCandidate, error) {
	f.candidateQueries = append(f.candidateQueries, query)
	if f.candidateErr != nil {
		return nil, f.candidateErr
	}
	return f.candidates, nil
}

func (f *flowContent) ListDraftsByAuthor(_ context.Context, query contracts.DraftListQuery) ([]contracts.DraftSummary, error) {
	f.draftQueries = append(f.draftQueries, query)
	if f.draftErr != nil {
		return nil, f.draftErr
	}
	return f.draftList, nil
}

func (f *flowContent) PublishPostForActor(_ context.Context, in contracts.PublishPostForActorInput) error {
	f.publishedDrafts = append(f.publishedDrafts, in)
	return f.publishErr
}

func (f *flowContent) UpdateDraftForActor(_ context.Context, in contracts.UpdateDraftForActorInput) error {
	f.updatedDrafts = append(f.updatedDrafts, in)
	return f.updateDraftErr
}

// ===== 社交端口扩展 =====

type flowSocial struct {
	*fakeSocial

	commentInputs  []contracts.ActorCommentInput
	createdComment model.Comment
	commentErr     error
	commentGetErr  error
	commentsErr    error

	likeInputs   []contracts.LikeInput
	likeActive   bool
	likeCount    int
	likeErr      error
	unlikeActive bool
	unlikeCount  int
	unlikeErr    error

	followState   string
	followErr     error
	unfollowState string
	unfollowErr   error

	repostActive bool
	repostCount  int
	repostErr    error

	recentComments    []contracts.CommentBrief
	recentCommentsErr error
	newComments       []model.Comment
	newCommentsErr    error
	likedRefs         []contracts.LikedPostRef
	followeeRefs      []contracts.FolloweeRef
}

func (f *flowSocial) GetComment(ctx context.Context, commentID int64) (model.Comment, error) {
	if f.commentGetErr != nil {
		return model.Comment{}, f.commentGetErr
	}
	return f.fakeSocial.GetComment(ctx, commentID)
}

func (f *flowSocial) GetComments(ctx context.Context, commentIDs []int64) (map[int64]model.Comment, error) {
	if f.commentsErr != nil {
		return nil, f.commentsErr
	}
	return f.fakeSocial.GetComments(ctx, commentIDs)
}

func (f *flowSocial) CreateCommentForActor(_ context.Context, in contracts.ActorCommentInput) (model.Comment, error) {
	f.commentInputs = append(f.commentInputs, in)
	if f.commentErr != nil {
		return model.Comment{}, f.commentErr
	}
	if f.createdComment.ID > 0 {
		return f.createdComment, nil
	}
	comment := model.Comment{ID: 8001, PostID: in.PostID, AuthorID: in.ActorID, Status: model.CommentStatusPublished}
	return comment, nil
}

func (f *flowSocial) LikeForActor(_ context.Context, in contracts.LikeInput) (bool, int, error) {
	f.likeInputs = append(f.likeInputs, in)
	if f.likeErr != nil {
		return false, 0, f.likeErr
	}
	return f.likeActive, f.likeCount, nil
}

func (f *flowSocial) UnlikeForActor(_ context.Context, in contracts.LikeInput) (bool, int, error) {
	f.likeInputs = append(f.likeInputs, in)
	if f.unlikeErr != nil {
		return false, 0, f.unlikeErr
	}
	return f.unlikeActive, f.unlikeCount, nil
}

func (f *flowSocial) FollowForActor(_ context.Context, in contracts.FollowInput) (string, error) {
	if f.followErr != nil {
		return "", f.followErr
	}
	if f.followState == "" {
		return model.FollowStateFollowing, nil
	}
	return f.followState, nil
}

func (f *flowSocial) UnfollowForActor(_ context.Context, in contracts.FollowInput) (string, error) {
	if f.unfollowErr != nil {
		return "", f.unfollowErr
	}
	if f.unfollowState == "" {
		return model.FollowStateNone, nil
	}
	return f.unfollowState, nil
}

func (f *flowSocial) RepostForActor(_ context.Context, in contracts.RepostInput) (bool, int, error) {
	if f.repostErr != nil {
		return false, 0, f.repostErr
	}
	return f.repostActive, f.repostCount, nil
}

func (f *flowSocial) ListRecentComments(_ context.Context, _ contracts.CommentListQuery) ([]contracts.CommentBrief, error) {
	if f.recentCommentsErr != nil {
		return nil, f.recentCommentsErr
	}
	return f.recentComments, nil
}

func (f *flowSocial) ListNewCommentsOnAuthorPosts(_ context.Context, _ contracts.CommentTriggerQuery) ([]model.Comment, error) {
	if f.newCommentsErr != nil {
		return nil, f.newCommentsErr
	}
	return f.newComments, nil
}

func (f *flowSocial) ListRecentLikedPosts(_ context.Context, _ int64, _ int) ([]contracts.LikedPostRef, error) {
	return f.likedRefs, nil
}

func (f *flowSocial) ListRecentFollowees(_ context.Context, _ int64, _ int) ([]contracts.FolloweeRef, error) {
	return f.followeeRefs, nil
}

// ===== AI 端口扩展 =====

type flowAI struct {
	*fakeAI

	longTerm               []contracts.Memory
	longTermErr            error
	longTermInputs         []contracts.SearchLongTermInput
	touched                [][]int64
	touchErr               error
	recentCount            int
	recentCountErr         error
	recentLimit            []int
	recentErr              error
	enabledAIIDs           []int64
	enabledErr             error
	runtimeErr             error
	runtimeErrFrom         int
	runtimeCalls           int
	memorySources          []contracts.MemorySourceRef
	memorySourcesErr       error
	activeRefsErr          error
	activeRefsLimits       []int
	activeRefsAfters       []contracts.MemorySourceRef
	invalidateErr          error
	invalidateBySourcesErr error
	createMemoryErr        error
	addSourcesErr          error
	listSourcesErr         error
}

func (f *flowAI) RuntimeByAI(_ context.Context, aiID int64) (contracts.Runtime, error) {
	f.runtimeCalls++
	if f.runtimeErr != nil && (f.runtimeErrFrom == 0 || f.runtimeCalls >= f.runtimeErrFrom) {
		return contracts.Runtime{}, f.runtimeErr
	}
	runtime := f.runtime
	runtime.AIID = aiID
	return runtime, nil
}

func (f *flowAI) EnabledAIIDs(_ context.Context) ([]int64, error) {
	if f.enabledErr != nil {
		return nil, f.enabledErr
	}
	return f.enabledAIIDs, nil
}

func (f *flowAI) RecentMemories(ctx context.Context, aiID int64, limit int) ([]contracts.Memory, error) {
	f.recentLimit = append(f.recentLimit, limit)
	if f.recentErr != nil {
		return nil, f.recentErr
	}
	return f.fakeAI.RecentMemories(ctx, aiID, limit)
}

func (f *flowAI) SearchLongTerm(_ context.Context, in contracts.SearchLongTermInput) ([]contracts.Memory, error) {
	f.longTermInputs = append(f.longTermInputs, in)
	if f.longTermErr != nil {
		return nil, f.longTermErr
	}
	return f.longTerm, nil
}

func (f *flowAI) TouchMemories(_ context.Context, memoryIDs []int64, _ time.Time) error {
	f.touched = append(f.touched, memoryIDs)
	return f.touchErr
}

func (f *flowAI) CountRecentMemories(_ context.Context, _ int64) (int, error) {
	if f.recentCountErr != nil {
		return 0, f.recentCountErr
	}
	return f.recentCount, nil
}

func (f *flowAI) ListMemorySources(ctx context.Context, memoryIDs []int64) ([]contracts.MemorySourceRef, error) {
	if f.listSourcesErr != nil {
		return nil, f.listSourcesErr
	}
	return f.fakeAI.ListMemorySources(ctx, memoryIDs)
}

func (f *flowAI) ListActiveMemorySourceRefs(
	_ context.Context,
	_ int64,
	after contracts.MemorySourceRef,
	limit int,
) ([]contracts.MemorySourceRef, error) {
	f.activeRefsLimits = append(f.activeRefsLimits, limit)
	f.activeRefsAfters = append(f.activeRefsAfters, after)
	if f.activeRefsErr != nil {
		return nil, f.activeRefsErr
	}
	return pageMemorySourceRefs(f.fakeAI.activeSourceRefs, after, limit), nil
}

func (f *flowAI) CreateMemory(ctx context.Context, in contracts.CreateMemoryInput) (contracts.Memory, error) {
	if f.createMemoryErr != nil {
		return contracts.Memory{}, f.createMemoryErr
	}
	return f.fakeAI.CreateMemory(ctx, in)
}

func (f *flowAI) AddMemorySources(ctx context.Context, memoryID int64, sources []contracts.MemorySourceInput) error {
	if f.addSourcesErr != nil {
		return f.addSourcesErr
	}
	return f.fakeAI.AddMemorySources(ctx, memoryID, sources)
}

func (f *flowAI) InvalidateBySource(ctx context.Context, in contracts.InvalidateBySourceInput) error {
	if f.invalidateErr != nil {
		return f.invalidateErr
	}
	return f.fakeAI.InvalidateBySource(ctx, in)
}

func (f *flowAI) InvalidateMemoriesBySources(ctx context.Context, in contracts.InvalidateMemoriesBySourcesInput) (int, error) {
	if f.invalidateBySourcesErr != nil {
		return 0, f.invalidateBySourcesErr
	}
	return f.fakeAI.InvalidateMemoriesBySources(ctx, in)
}

// ===== 任务仓储扩展 =====

type taskResultRecord struct {
	taskID int64
	update repo.TaskResultUpdate
}

type statusRecord struct {
	taskID int64
	status string
	errMsg string
	at     *time.Time
}

type flowTasks struct {
	*fakeTasks

	claimAttempts int
	claimed       bool
	claimErr      error
	claims        []int64

	insertErr  error
	duplicate  bool
	inserted   []repo.TaskInsert
	resultErrs error
	results    []taskResultRecord
	statusErrs error
	statuses   []statusRecord

	memoryRefs    map[int64][]repo.MemoryRef
	addedRefs     map[int64][]repo.MemoryRef
	addRefsErr    error
	memoryRefsErr error

	listFilters []repo.TaskFilter
	listErr     error
	listResult  []repo.Task
	listMore    bool
	pendingErr  error
	getTaskErr  error
}

func (f *flowTasks) InsertTask(_ context.Context, in repo.TaskInsert) (bool, error) {
	// 与唯一幂等键索引一致：同键已存在时不再插入（返回 inserted=false）。
	for _, existing := range f.inserted {
		if existing.IdempotencyKey == in.IdempotencyKey {
			return false, nil
		}
	}
	f.inserted = append(f.inserted, in)
	if f.insertErr != nil {
		return false, f.insertErr
	}
	if f.duplicate {
		return false, nil
	}
	if f.tasks == nil {
		f.tasks = map[int64]repo.Task{}
	}
	f.tasks[in.ID] = repo.Task{
		ID:             in.ID,
		AIID:           in.AIID,
		TriggerType:    in.TriggerType,
		TriggerRefType: in.TriggerRefType,
		TriggerRefID:   in.TriggerRefID,
		ActionType:     in.ActionType,
		Status:         "pending",
		IdempotencyKey: in.IdempotencyKey,
		ScheduledAt:    in.ScheduledAt,
		CreatedAt:      in.CreatedAt,
	}
	return true, nil
}

func (f *flowTasks) ClaimTask(_ context.Context, taskID int64, _ time.Time, _ time.Duration) (repo.Task, bool, error) {
	f.claims = append(f.claims, taskID)
	if f.claimErr != nil {
		return repo.Task{}, false, f.claimErr
	}
	if !f.claimed {
		return repo.Task{}, false, nil
	}
	task, ok := f.tasks[taskID]
	if !ok {
		task = repo.Task{ID: taskID}
	}
	task.Attempts = f.claimAttempts
	return task, true, nil
}

func (f *flowTasks) UpdateTaskResult(ctx context.Context, in repo.TaskResultUpdate) error {
	f.results = append(f.results, taskResultRecord{taskID: in.TaskID, update: in})
	if f.resultErrs != nil {
		return f.resultErrs
	}
	return f.fakeTasks.UpdateTaskResult(ctx, in)
}

func (f *flowTasks) SetTaskStatus(ctx context.Context, taskID int64, status, errorMessage string, finishedAt *time.Time) error {
	f.statuses = append(f.statuses, statusRecord{taskID: taskID, status: status, errMsg: errorMessage, at: finishedAt})
	if f.statusErrs != nil {
		return f.statusErrs
	}
	return f.fakeTasks.SetTaskStatus(ctx, taskID, status, errorMessage, finishedAt)
}

func (f *flowTasks) AddMemoryRefs(_ context.Context, taskID int64, refs []repo.MemoryRef, _ int64) error {
	if f.addRefsErr != nil {
		return f.addRefsErr
	}
	if f.addedRefs == nil {
		f.addedRefs = map[int64][]repo.MemoryRef{}
	}
	f.addedRefs[taskID] = append(f.addedRefs[taskID], refs...)
	return nil
}

func (f *flowTasks) ListMemoryRefs(_ context.Context, taskID int64) ([]repo.MemoryRef, error) {
	if f.memoryRefsErr != nil {
		return nil, f.memoryRefsErr
	}
	return f.memoryRefs[taskID], nil
}

func (f *flowTasks) ListMemoryRefsByTaskIDs(_ context.Context, taskIDs []int64) (map[int64][]repo.MemoryRef, error) {
	if f.memoryRefsErr != nil {
		return nil, f.memoryRefsErr
	}
	refs := make(map[int64][]repo.MemoryRef, len(taskIDs))
	for _, taskID := range taskIDs {
		if taskRefs, ok := f.memoryRefs[taskID]; ok {
			refs[taskID] = taskRefs
		}
	}
	return refs, nil
}

func (f *flowTasks) GetTask(ctx context.Context, taskID int64) (repo.Task, error) {
	if f.getTaskErr != nil {
		return repo.Task{}, f.getTaskErr
	}
	return f.fakeTasks.GetTask(ctx, taskID)
}

func (f *flowTasks) ListTasks(ctx context.Context, filter repo.TaskFilter) ([]repo.Task, bool, error) {
	f.listFilters = append(f.listFilters, filter)
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	if f.listResult != nil {
		return f.listResult, f.listMore, nil
	}
	if filter.Status == domain.TaskStatusPending {
		result := make([]repo.Task, 0, len(f.fakeTasks.pending))
		for _, task := range f.fakeTasks.pending {
			if filter.AIID > 0 && task.AIID != filter.AIID {
				continue
			}
			if f.fakeTasks.statuses[task.ID] == domain.TaskStatusCanceled {
				continue
			}
			result = append(result, task)
		}
		return result, false, nil
	}
	return f.fakeTasks.ListTasks(ctx, filter)
}

func (f *flowTasks) ListPendingTasks(ctx context.Context, limit int) ([]repo.Task, error) {
	if f.pendingErr != nil {
		return nil, f.pendingErr
	}
	return f.fakeTasks.ListPendingTasks(ctx, limit)
}

// ===== 计数仓储 fake =====

type counterBump struct {
	id       int64
	aiID     int64
	action   string
	dayStart time.Time
	at       time.Time
}

type flowCounters struct {
	CounterStore

	bumps        []counterBump
	actionCounts map[string]int
	totals       map[int64]int
	last         map[string]*time.Time
	bumpErr      error
	countErr     error
	totalErr     error
	lastErr      error
}

func (f *flowCounters) BumpActionCounter(_ context.Context, id, aiID int64, actionType string, day, at time.Time) error {
	f.bumps = append(f.bumps, counterBump{id: id, aiID: aiID, action: actionType, dayStart: day, at: at})
	return f.bumpErr
}

func (f *flowCounters) CountActions(_ context.Context, _ int64, actionType string, _ time.Time) (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.actionCounts[actionType], nil
}

func (f *flowCounters) TotalActions(_ context.Context, aiID int64, _ time.Time) (int, error) {
	if f.totalErr != nil {
		return 0, f.totalErr
	}
	return f.totals[aiID], nil
}

func (f *flowCounters) TotalActionsByAIIDs(_ context.Context, aiIDs []int64, _ time.Time) (map[int64]int64, error) {
	if f.totalErr != nil {
		return nil, f.totalErr
	}
	counts := make(map[int64]int64, len(aiIDs))
	for _, aiID := range aiIDs {
		if total, ok := f.totals[aiID]; ok {
			counts[aiID] = int64(total)
		}
	}
	return counts, nil
}

func (f *flowCounters) LastSucceededAt(_ context.Context, _ int64, triggerType string) (*time.Time, error) {
	if f.lastErr != nil {
		return nil, f.lastErr
	}
	return f.last[triggerType], nil
}

// ===== 作业队列扩展 =====

type flowJobs struct {
	*fakeJobs

	enqueueCalls []contracts.JobEnqueue
	enqueueErr   error
	retried      []int64
	retryErr     error
	refQueries   []contracts.JobRefQuery
	refErr       error
}

func (f *flowJobs) Enqueue(ctx context.Context, in contracts.JobEnqueue) error {
	f.enqueueCalls = append(f.enqueueCalls, in)
	if f.enqueueErr != nil {
		return f.enqueueErr
	}
	return f.fakeJobs.Enqueue(ctx, in)
}

func (f *flowJobs) ListByRef(ctx context.Context, query contracts.JobRefQuery) ([]contracts.JobSnapshot, error) {
	f.refQueries = append(f.refQueries, query)
	if f.refErr != nil {
		return nil, f.refErr
	}
	return f.fakeJobs.ListByRef(ctx, query)
}

func (f *flowJobs) RetryJob(_ context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	f.retried = append(f.retried, in.JobID)
	if f.retryErr != nil {
		return contracts.JobSnapshot{}, f.retryErr
	}
	return contracts.JobSnapshot{ID: in.JobID, Status: contracts.JobStatusPending}, nil
}

// ===== 调用记录扩展 =====

type flowCallLogs struct {
	*fakeCallLogs

	inserts     []repo.CallLogInsert
	insertErr   error
	listResult  []repo.CallLog
	listMore    bool
	listErr     error
	listFilters []repo.CallLogFilter
	getResult   repo.CallLog
	getErr      error
	markCounts  []int
	markErr     error
	cutoffs     []time.Time
}

func (f *flowCallLogs) InsertCallLog(ctx context.Context, in repo.CallLogInsert) error {
	f.inserts = append(f.inserts, in)
	if f.insertErr != nil {
		return f.insertErr
	}
	return f.fakeCallLogs.InsertCallLog(ctx, in)
}

func (f *flowCallLogs) ListCallLogs(_ context.Context, filter repo.CallLogFilter) ([]repo.CallLog, bool, error) {
	f.listFilters = append(f.listFilters, filter)
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	return f.listResult, f.listMore, nil
}

func (f *flowCallLogs) GetCallLog(_ context.Context, _ int64) (repo.CallLog, error) {
	if f.getErr != nil {
		return repo.CallLog{}, f.getErr
	}
	return f.getResult, nil
}

func (f *flowCallLogs) MarkDeletedBefore(_ context.Context, cutoff time.Time, _ int) (int, error) {
	f.cutoffs = append(f.cutoffs, cutoff)
	if f.markErr != nil {
		return 0, f.markErr
	}
	if len(f.markCounts) == 0 {
		return 0, nil
	}
	count := f.markCounts[0]
	f.markCounts = f.markCounts[1:]
	return count, nil
}

// ===== 设置/审计/通知/账号端口扩展 =====

type flowSettings struct {
	*fakeSettings

	settingsFn  func() contracts.SystemSettings
	settingsErr error
	setErr      error
	setCalls    []contracts.SettingsWrite
}

func (f *flowSettings) Settings(ctx context.Context) (contracts.SystemSettings, error) {
	if f.settingsFn != nil {
		return f.settingsFn(), nil
	}
	if f.settingsErr != nil {
		return contracts.SystemSettings{}, f.settingsErr
	}
	return f.fakeSettings.Settings(ctx)
}

func (f *flowSettings) Set(ctx context.Context, in contracts.SettingsWrite) error {
	f.setCalls = append(f.setCalls, in)
	if f.setErr != nil {
		return f.setErr
	}
	return f.fakeSettings.Set(ctx, in)
}

type flowAudit struct {
	*fakeAudit

	err     error
	entries []contracts.AuditEntry
}

func (f *flowAudit) Write(ctx context.Context, entry contracts.AuditEntry) error {
	f.entries = append(f.entries, entry)
	if f.err != nil {
		return f.err
	}
	return f.fakeAudit.Write(ctx, entry)
}

type flowUsers struct {
	*fakeUsers

	err     error
	errOnID int64
	calls   []int64
}

func (f *flowUsers) GetUser(ctx context.Context, userID int64) (model.User, error) {
	f.calls = append(f.calls, userID)
	if f.err != nil && (f.errOnID == 0 || f.errOnID == userID) {
		return model.User{}, f.err
	}
	return f.fakeUsers.GetUser(ctx, userID)
}

type flowNotices struct {
	contracts.NoticeService

	mentions []contracts.MentionRef
	err      error
	queries  []contracts.MentionQuery
}

func (f *flowNotices) ListActiveMentions(_ context.Context, query contracts.MentionQuery) ([]contracts.MentionRef, error) {
	f.queries = append(f.queries, query)
	if f.err != nil {
		return nil, f.err
	}
	return f.mentions, nil
}

// ===== 事务/ID fake =====

type fakeTx struct {
	calls int
	err   error
}

func (f *fakeTx) WithTx(ctx context.Context, fn func(context.Context) error) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return fn(ctx)
}

type stuckIDs struct {
	value int64
	err   error
}

func (f *stuckIDs) Next() (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.value, nil
}

func notFoundError(message string) error {
	return apperr.NotFound(message)
}

// ===== 组合夹具 =====

type flowFixture struct {
	clk         *clock.Fake
	ids         *fakeIDs
	tx          *fakeTx
	content     *flowContent
	social      *flowSocial
	ai          *flowAI
	users       *flowUsers
	tasks       *flowTasks
	counters    *flowCounters
	jobs        *flowJobs
	callLogs    *flowCallLogs
	settings    *flowSettings
	audit       *flowAudit
	notices     *flowNotices
	groups      *flowGroups
	groupWriter *flowGroupWriter
	svc         *Service
}

func newFlowFixture() *flowFixture {
	f := &flowFixture{
		clk: testClock(),
		ids: &fakeIDs{next: 1000},
		tx:  &fakeTx{},
		content: &flowContent{
			fakeContent: &fakeContent{posts: map[int64]model.Post{}},
			excerpts:    map[int64]string{},
			getPost:     map[int64]model.Post{},
		},
		social: &flowSocial{fakeSocial: &fakeSocial{
			comments:      map[int64]model.Comment{},
			depths:        map[int64]int{},
			likedPosts:    map[int64]bool{},
			likedComments: map[int64]bool{},
			following:     map[int64]bool{},
		}},
		ai:    &flowAI{fakeAI: &fakeAI{}},
		users: &flowUsers{fakeUsers: &fakeUsers{users: map[int64]model.User{}}},
		tasks: &flowTasks{
			fakeTasks:  &fakeTasks{statuses: map[int64]string{}, tasks: map[int64]repo.Task{}},
			memoryRefs: map[int64][]repo.MemoryRef{},
			addedRefs:  map[int64][]repo.MemoryRef{},
		},
		counters: &flowCounters{actionCounts: map[string]int{}, totals: map[int64]int{}, last: map[string]*time.Time{}},
		jobs:     &flowJobs{fakeJobs: &fakeJobs{}},
		callLogs: &flowCallLogs{fakeCallLogs: &fakeCallLogs{}},
		settings: &flowSettings{fakeSettings: &fakeSettings{values: contracts.SystemSettings{AIGlobalEnabled: true}}},
		audit:    &flowAudit{fakeAudit: &fakeAudit{}},
		notices:  &flowNotices{},
		groups: &flowGroups{
			members:   map[string]bool{},
			snapshots: map[int64]contracts.GroupSnapshot{},
			groupsOf:  map[int64][]int64{},
		},
		groupWriter: &flowGroupWriter{},
	}
	f.svc = New(Deps{
		Clock:          f.clk,
		IDs:            f.ids,
		Tx:             f.tx,
		Log:            testLogger(),
		Tasks:          f.tasks,
		CallLogs:       f.callLogs,
		Counters:       f.counters,
		Content:        f.content,
		Social:         f.social,
		AI:             f.ai,
		Notices:        f.notices,
		Users:          f.users,
		Groups:         f.groups,
		GroupWriter:    f.groupWriter,
		Jobs:           f.jobs,
		Audit:          f.audit,
		Settings:       f.settings,
		SettingsWriter: f.settings,
	})
	return f
}

// ===== 群聊端口 fake（contracts.GroupReader / contracts.GroupMessageWriter）=====

type flowGroups struct {
	contracts.GroupReader
	members   map[string]bool                   // "groupID:userID" → 是否有效成员
	snapshots map[int64]contracts.GroupSnapshot // groupID → 快照
	groupsOf  map[int64][]int64                 // userID → 群 ID 列表
}

func (f *flowGroups) IsMemberActive(_ context.Context, groupID, userID int64) (bool, error) {
	return f.members[apiID(groupID)+":"+apiID(userID)], nil
}

func (f *flowGroups) Snapshot(_ context.Context, groupID, _ int64, _ int) (contracts.GroupSnapshot, error) {
	snapshot, ok := f.snapshots[groupID]
	if !ok {
		return contracts.GroupSnapshot{}, apperr.NotFound("群聊不存在")
	}
	return snapshot, nil
}

func (f *flowGroups) ListActiveGroupIDs(_ context.Context, userID int64) ([]int64, error) {
	return f.groupsOf[userID], nil
}

type flowGroupWriter struct {
	contracts.GroupMessageWriter
	writes []contracts.GroupMessageInput
	nextID int64
	err    error
}

func (f *flowGroupWriter) WriteFromAI(_ context.Context, in contracts.GroupMessageInput) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.nextID++
	f.writes = append(f.writes, in)
	return f.nextID, nil
}

// enableAI 准备一个启用中的 AI 账号与运行时配置。
func (f *flowFixture) enableAI(aiID int64) contracts.Runtime {
	f.users.users[aiID] = model.User{ID: aiID, Username: "ai_1", Status: model.StatusActive, IsAI: true}
	rt := contracts.Runtime{
		AIID:     aiID,
		Username: "ai_1",
		Nickname: "小助手",
		Profile:  contracts.Profile{IsEnabled: true, Topics: "科技"},
		Provider: contracts.ProviderConfig{
			ID: 1, BaseURL: "http://127.0.0.1:0", APIKey: "k", DefaultModel: "test-model", IsEnabled: true,
		},
		Model:    "test-model",
		Timeout:  5 * time.Second,
		Behavior: contracts.BehaviorSettings{AllowOriginalPost: true, AllowLike: true, AllowFollow: true, MaxReplyDepth: 2},
	}
	f.ai.runtime = rt
	return rt
}

var errBoom = errors.New("fake 基础设施错误")
