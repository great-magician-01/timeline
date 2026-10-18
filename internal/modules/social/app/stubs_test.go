package app

// 本文件提供 social/app 单元测试共用的 fake 端口与固定装配。
// 约定：
//   - 所有 fake 都内嵌对应端口接口，只覆写测试路径实际调用的方法，避免为每个方法写样板；
//     未被覆写的方法若被意外调用会 nil panic，正好暴露用例越界依赖。
//   - 复用 review_fix_test.go 已声明的 fixIDs / fixClock / fixTx 语义等价物，
//     这里只补充带错误注入与调用记录的增强版本，类型名不与既有声明重复。
//   - 时间一律来自 platform/clock 的 Fake，仓库内无真实 sleep。

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/audit"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/ratelimit"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// stubNow 是全部用例共享的固定业务时间，用于断言写入时间、通知时间与幂等键。
var stubNow = time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)

// 测试账号 ID 约定：1=访问者，2=帖子作者，3=其他用户，4=停用账号，5=AI，99=管理员。
const (
	stubViewerID    int64 = 1
	stubAuthorID    int64 = 2
	stubOtherID     int64 = 3
	stubSuspendedID int64 = 4
	stubAIID        int64 = 5
	stubAdminID     int64 = 99

	// stubPostID 是默认可见帖子的 ID。
	stubPostID int64 = 20
)

// stubViewer 返回默认的已登录人类访问者。
func stubViewer() model.Viewer {
	return model.ViewerFrom(model.User{ID: stubViewerID, Status: model.StatusActive})
}

// stubAuthorViewer 返回帖子作者身份的访问者。
func stubAuthorViewer() model.Viewer {
	return model.ViewerFrom(model.User{ID: stubAuthorID, Status: model.StatusActive})
}

// stubAdminViewer 返回管理员访问者。
func stubAdminViewer() model.Viewer {
	return model.ViewerFrom(model.User{ID: stubAdminID, Role: model.RoleAdmin, Status: model.StatusActive})
}

// stubSuspendedViewer 返回已停用账号的访问者。
func stubSuspendedViewer() model.Viewer {
	return model.ViewerFrom(model.User{ID: stubSuspendedID, Status: model.StatusSuspended})
}

// stubGuestViewer 返回游客访问者。
func stubGuestViewer() model.Viewer { return model.GuestViewer() }

// stubPublicPost 返回默认的公开已发布帖子（作者为 stubAuthorID）。
func stubPublicPost() model.Post {
	return model.Post{
		ID:           stubPostID,
		AuthorID:     stubAuthorID,
		Status:       model.PostStatusPublished,
		Visibility:   model.VisibilityPublic,
		LikeCount:    3,
		CommentCount: 4,
		RepostCount:  5,
		CreatedAt:    stubNow.Add(-time.Hour),
		UpdatedAt:    stubNow.Add(-time.Hour),
	}
}

// ===== 通用工具 =====

// requireOK 断言没有错误。
func requireOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际错误: %v", err)
	}
}

// requireKind 断言错误链上是期望的领域错误 Kind，并返回该领域错误供继续断言。
func requireKind(t *testing.T, err error, want apperr.Kind) *apperr.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("期望领域错误 %s，实际为 nil", want)
	}
	appErr, ok := apperr.AsKind(err)
	if !ok {
		t.Fatalf("期望领域错误 %s，实际错误 %v", want, err)
	}
	if appErr.Kind != want {
		t.Fatalf("错误 Kind = %s，期望 %s（错误 %v）", appErr.Kind, want, err)
	}
	return appErr
}

// errIDs 让 ID 生成返回指定错误。
type errIDs struct{ err error }

func (e errIDs) Next() (int64, error) { return 0, e.err }

// zeroIDs 返回非正 ID，覆盖 newID 的兜底分支。
type zeroIDs struct{}

func (zeroIDs) Next() (int64, error) { return 0, nil }

// errTx 让事务入口直接返回指定错误。
type errTx struct{ err error }

func (e errTx) WithTx(context.Context, func(context.Context) error) error { return e.err }

// ===== auth 端口 fake =====

type stubUsers struct {
	contracts.UserReader

	byID      map[int64]model.User
	byName    map[string]model.User
	briefs    map[int64]model.UserBrief
	suspended map[int64]bool
	aiIDs     []int64

	usersErr, nameErr, listErr, briefErr, briefsErr, suspendedErr, aiErr error

	lastBriefIDs []int64
}

func newStubUsers() *stubUsers {
	active := func(id int64, username string, ai bool) model.User {
		return model.User{ID: id, Username: username, Status: model.StatusActive, IsAI: ai}
	}
	s := &stubUsers{
		byID: map[int64]model.User{
			stubViewerID:    active(stubViewerID, "alice", false),
			stubAuthorID:    active(stubAuthorID, "bob", false),
			stubOtherID:     active(stubOtherID, "carol", false),
			stubSuspendedID: {ID: stubSuspendedID, Username: "dave", Status: model.StatusSuspended},
			stubAIID:        active(stubAIID, "aibot", true),
			stubAdminID:     {ID: stubAdminID, Username: "root", Role: model.RoleAdmin, Status: model.StatusActive},
		},
		byName:    map[string]model.User{},
		briefs:    map[int64]model.UserBrief{},
		suspended: map[int64]bool{stubSuspendedID: true},
	}
	for id, user := range s.byID {
		s.byName[user.Username] = user
		s.briefs[id] = model.UserBriefFromUser(user)
	}
	return s
}

func (s *stubUsers) GetUser(_ context.Context, userID int64) (model.User, error) {
	if s.usersErr != nil {
		return model.User{}, s.usersErr
	}
	if user, ok := s.byID[userID]; ok {
		return user, nil
	}
	return model.User{}, apperr.NotFound("用户不存在")
}

func (s *stubUsers) GetUserByUsername(_ context.Context, username string) (model.User, error) {
	if s.nameErr != nil {
		return model.User{}, s.nameErr
	}
	if user, ok := s.byName[username]; ok {
		return user, nil
	}
	return model.User{}, apperr.NotFound("用户不存在")
}

func (s *stubUsers) GetUsers(_ context.Context, userIDs []int64) (map[int64]model.User, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	result := make(map[int64]model.User, len(userIDs))
	for _, id := range userIDs {
		if user, ok := s.byID[id]; ok {
			result[id] = user
		}
	}
	return result, nil
}

func (s *stubUsers) GetUserBrief(_ context.Context, userID int64) (model.UserBrief, error) {
	if s.briefErr != nil {
		return model.UserBrief{}, s.briefErr
	}
	if brief, ok := s.briefs[userID]; ok {
		return brief, nil
	}
	return model.UserBrief{}, apperr.NotFound("用户不存在")
}

func (s *stubUsers) GetUserBriefs(_ context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	s.lastBriefIDs = append([]int64{}, userIDs...)
	if s.briefsErr != nil {
		return nil, s.briefsErr
	}
	result := make(map[int64]model.UserBrief, len(userIDs))
	for _, id := range userIDs {
		if brief, ok := s.briefs[id]; ok {
			result[id] = brief
		}
	}
	return result, nil
}

func (s *stubUsers) BriefsByIDs(ctx context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	return s.GetUserBriefs(ctx, userIDs)
}

func (s *stubUsers) IsSuspended(_ context.Context, userID int64) (bool, error) {
	if s.suspendedErr != nil {
		return false, s.suspendedErr
	}
	return s.suspended[userID], nil
}

func (s *stubUsers) ListUserIDsByAIFlag(_ context.Context, isAI bool) ([]int64, error) {
	if s.aiErr != nil {
		return nil, s.aiErr
	}
	if s.aiIDs != nil {
		return s.aiIDs, nil
	}
	ids := make([]int64, 0, len(s.byID))
	for id, user := range s.byID {
		if user.IsAI == isAI {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ===== content 端口 fake =====

type stubPosts struct {
	contracts.ContentService

	getPost         model.Post
	visiblePost     model.Post
	posts           map[int64]model.Post
	publicPostIDs   []int64
	publicPostLimit int
	plainTexts      map[int64]string
	counterValue    int

	counters []contracts.PostCounterInput

	getPostErr, visibleErr, postsErr, publicPostIDsErr, textsErr, counterErr error
}

func newStubPosts() *stubPosts {
	post := stubPublicPost()
	return &stubPosts{
		getPost:     post,
		visiblePost: post,
		posts:       map[int64]model.Post{post.ID: post},
		plainTexts:  map[int64]string{post.ID: "公开帖子正文"},
	}
}

func (s *stubPosts) GetPost(_ context.Context, postID int64) (model.Post, error) {
	if s.getPostErr != nil {
		return model.Post{}, s.getPostErr
	}
	post := s.getPost
	if post.ID == 0 || (postID != post.ID && len(s.posts) > 0) {
		if stored, ok := s.posts[postID]; ok {
			return stored, nil
		}
		return model.Post{}, apperr.NotFound("内容不存在或无权访问")
	}
	return post, nil
}

func (s *stubPosts) GetVisiblePost(_ context.Context, _ model.Viewer, _ int64) (model.Post, error) {
	if s.visibleErr != nil {
		return model.Post{}, s.visibleErr
	}
	return s.visiblePost, nil
}

func (s *stubPosts) GetPosts(_ context.Context, postIDs []int64) (map[int64]model.Post, error) {
	if s.postsErr != nil {
		return nil, s.postsErr
	}
	result := make(map[int64]model.Post, len(postIDs))
	for _, id := range postIDs {
		if post, ok := s.posts[id]; ok {
			result[id] = post
		}
	}
	return result, nil
}

func (s *stubPosts) BumpPostCounter(_ context.Context, in contracts.PostCounterInput) (int, error) {
	if s.counterErr != nil {
		return 0, s.counterErr
	}
	s.counters = append(s.counters, in)
	return s.counterValue, nil
}

func (s *stubPosts) ListPublicPublishedPostIDsByAuthor(_ context.Context, _ int64, limit int) ([]int64, error) {
	if s.publicPostIDsErr != nil {
		return nil, s.publicPostIDsErr
	}
	s.publicPostLimit = limit
	return s.publicPostIDs, nil
}

func (s *stubPosts) GetPostPlainTexts(_ context.Context, postIDs []int64) (map[int64]string, error) {
	if s.textsErr != nil {
		return nil, s.textsErr
	}
	result := make(map[int64]string, len(postIDs))
	for _, id := range postIDs {
		if text, ok := s.plainTexts[id]; ok {
			result[id] = text
		}
	}
	return result, nil
}

// ===== notice 端口 fake =====

type stubDeactivateMention struct {
	sourceType string
	sourceID   int64
	at         time.Time
}

type stubNotices struct {
	contracts.NoticeService

	notifications []contracts.NotificationInput
	publications  []contracts.PublishMentionsInput
	deactivated   []stubDeactivateMention

	createErr, publishErr, deactivateErr error
}

func (s *stubNotices) CreateNotification(_ context.Context, in contracts.NotificationInput) error {
	s.notifications = append(s.notifications, in)
	return s.createErr
}

func (s *stubNotices) PublishMentions(_ context.Context, in contracts.PublishMentionsInput) error {
	s.publications = append(s.publications, in)
	return s.publishErr
}

func (s *stubNotices) DeactivateMentions(_ context.Context, sourceType string, sourceID int64, at time.Time) error {
	s.deactivated = append(s.deactivated, stubDeactivateMention{sourceType: sourceType, sourceID: sourceID, at: at})
	return s.deactivateErr
}

// ===== 审计 writer fake =====

type stubAudit struct {
	audit.Writer

	entries []audit.Entry
	err     error
}

func (s *stubAudit) Write(_ context.Context, entry audit.Entry) error {
	s.entries = append(s.entries, entry)
	return s.err
}

// ===== 关注关系端口 fake =====

type stubFollowInsert struct {
	id, followerID, followeeID int64
	at                         time.Time
}

type stubFollowDeactivate struct {
	followerID, followeeID int64
	at                     time.Time
}

type stubFollowList struct {
	userID, cursor int64
	limit          int
}

// stubFollowState 是 FollowState 三项关系状态的预置值。
type stubFollowState struct {
	following, pending, incoming bool
}

type stubFollows struct {
	FollowRepo

	following    map[[2]int64]bool
	followingSet map[int64]map[int64]bool
	states       map[[2]int64]stubFollowState

	insertRowID       int64
	insertActiveAt    time.Time
	insertChanged     bool
	deactivateChanged bool
	followerRows      []FollowRow
	followingRows     []FollowRow
	recentRows        []FollowRow

	inserts     []stubFollowInsert
	deactivates []stubFollowDeactivate
	listCalls   map[string]stubFollowList

	followingErr, followStateErr, followingSetErr, insertErr, deactivateErr,
	followersErr, followingListErr, recentErr error
}

func newStubFollows() *stubFollows {
	return &stubFollows{
		following:         map[[2]int64]bool{},
		followingSet:      map[int64]map[int64]bool{},
		states:            map[[2]int64]stubFollowState{},
		insertChanged:     true,
		deactivateChanged: true,
		listCalls:         map[string]stubFollowList{},
	}
}

func (s *stubFollows) Following(_ context.Context, followerID, followeeID int64) (bool, error) {
	if s.followingErr != nil {
		return false, s.followingErr
	}
	return s.following[[2]int64{followerID, followeeID}], nil
}

func (s *stubFollows) FollowState(
	_ context.Context, viewerID, targetID int64,
) (bool, bool, bool, error) {
	if s.followStateErr != nil {
		return false, false, false, s.followStateErr
	}
	state := s.states[[2]int64{viewerID, targetID}]
	return state.following, state.pending, state.incoming, nil
}

func (s *stubFollows) FollowingSet(_ context.Context, followerID int64) (map[int64]bool, error) {
	if s.followingSetErr != nil {
		return nil, s.followingSetErr
	}
	result := map[int64]bool{}
	for id, active := range s.followingSet[followerID] {
		result[id] = active
	}
	return result, nil
}

func (s *stubFollows) InsertActive(
	_ context.Context, id, followerID, followeeID int64, at time.Time,
) (int64, time.Time, bool, error) {
	if s.insertErr != nil {
		return 0, time.Time{}, false, s.insertErr
	}
	s.inserts = append(s.inserts, stubFollowInsert{id: id, followerID: followerID, followeeID: followeeID, at: at})
	rowID := s.insertRowID
	if rowID == 0 {
		rowID = id
	}
	activeAt := s.insertActiveAt
	if activeAt.IsZero() {
		activeAt = at
	}
	return rowID, activeAt, s.insertChanged, nil
}

func (s *stubFollows) Deactivate(
	_ context.Context, followerID, followeeID int64, at time.Time,
) (bool, error) {
	if s.deactivateErr != nil {
		return false, s.deactivateErr
	}
	s.deactivates = append(s.deactivates, stubFollowDeactivate{followerID: followerID, followeeID: followeeID, at: at})
	return s.deactivateChanged, nil
}

func (s *stubFollows) ListFollowers(_ context.Context, followeeID, cursor int64, limit int) ([]FollowRow, error) {
	if s.followersErr != nil {
		return nil, s.followersErr
	}
	s.listCalls["followers"] = stubFollowList{userID: followeeID, cursor: cursor, limit: limit}
	return s.followerRows, nil
}

func (s *stubFollows) ListFollowing(_ context.Context, followerID, cursor int64, limit int) ([]FollowRow, error) {
	if s.followingListErr != nil {
		return nil, s.followingListErr
	}
	s.listCalls["following"] = stubFollowList{userID: followerID, cursor: cursor, limit: limit}
	return s.followingRows, nil
}

func (s *stubFollows) ListRecentFollowees(_ context.Context, followerID int64, limit int) ([]FollowRow, error) {
	if s.recentErr != nil {
		return nil, s.recentErr
	}
	s.listCalls["recent"] = stubFollowList{userID: followerID, limit: limit}
	return s.recentRows, nil
}

// ===== 关注申请端口 fake =====

type stubRequestUpdate struct {
	id         int64
	status     string
	resolvedAt time.Time
}

type stubRequestList struct {
	userID, cursor int64
	limit          int
}

type stubRequests struct {
	FollowRequestRepo

	rows         map[int64]domain.RequestRow
	incomingRows []domain.RequestRow
	outgoingRows []domain.RequestRow

	insertChanged bool
	cancelChanged bool

	updates []stubRequestUpdate
	calls   map[string]stubRequestList

	inserts []stubRequestInsert

	getErr, insertErr, updateErr, incomingErr, outgoingErr, cancelErr error
}

type stubRequestInsert struct {
	id, requesterID, targetID int64
	at                        time.Time
}

func newStubRequests() *stubRequests {
	return &stubRequests{
		rows:          map[int64]domain.RequestRow{},
		insertChanged: true,
		cancelChanged: true,
		calls:         map[string]stubRequestList{},
	}
}

func (s *stubRequests) InsertPending(
	_ context.Context, id, requesterID, targetID int64, at time.Time,
) (int64, bool, error) {
	if s.insertErr != nil {
		return 0, false, s.insertErr
	}
	s.inserts = append(s.inserts, stubRequestInsert{id: id, requesterID: requesterID, targetID: targetID, at: at})
	return id, s.insertChanged, nil
}

func (s *stubRequests) GetForUpdate(_ context.Context, id int64) (RequestRow, error) {
	if s.getErr != nil {
		return RequestRow{}, s.getErr
	}
	if row, ok := s.rows[id]; ok {
		return row, nil
	}
	return RequestRow{}, apperr.NotFound("申请不存在")
}

func (s *stubRequests) UpdateStatus(_ context.Context, id int64, status string, resolvedAt time.Time) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updates = append(s.updates, stubRequestUpdate{id: id, status: status, resolvedAt: resolvedAt})
	return nil
}

func (s *stubRequests) ListPendingIncoming(_ context.Context, targetID, cursor int64, limit int) ([]RequestRow, error) {
	if s.incomingErr != nil {
		return nil, s.incomingErr
	}
	s.calls["incoming"] = stubRequestList{userID: targetID, cursor: cursor, limit: limit}
	return s.incomingRows, nil
}

func (s *stubRequests) ListPendingOutgoing(_ context.Context, requesterID, cursor int64, limit int) ([]RequestRow, error) {
	if s.outgoingErr != nil {
		return nil, s.outgoingErr
	}
	s.calls["outgoing"] = stubRequestList{userID: requesterID, cursor: cursor, limit: limit}
	return s.outgoingRows, nil
}

func (s *stubRequests) CancelPending(_ context.Context, requesterID, targetID int64, _ time.Time) (bool, error) {
	if s.cancelErr != nil {
		return false, s.cancelErr
	}
	s.calls["cancel"] = stubRequestList{userID: requesterID, cursor: targetID}
	return s.cancelChanged, nil
}

// ===== 点赞端口 fake =====

type stubLikeCall struct {
	id, userID int64
	targetType string
	targetID   int64
	at         time.Time
}

type stubLikes struct {
	LikeRepo

	activeStatesMap map[int64]bool
	activateID      int64
	activateChanged bool
	deactivateID    int64

	deactivateChanged bool
	isActive          bool
	likedIDs          []int64

	activates   []stubLikeCall
	deactivates []stubLikeCall

	lastStatesType string
	lastStatesIDs  []int64

	lastIsActiveUserID, lastIsActiveTargetID int64
	lastIsActiveType                         string

	activateErr, deactivateErr, statesErr, isActiveErr, likedIDsErr error
}

func newStubLikes() *stubLikes {
	return &stubLikes{
		activeStatesMap:   map[int64]bool{},
		activateChanged:   true,
		deactivateChanged: true,
	}
}

func (s *stubLikes) Activate(
	_ context.Context, id, userID int64, targetType string, targetID int64, at time.Time,
) (int64, bool, error) {
	if s.activateErr != nil {
		return 0, false, s.activateErr
	}
	s.activates = append(s.activates, stubLikeCall{id: id, userID: userID, targetType: targetType, targetID: targetID, at: at})
	return s.activateID, s.activateChanged, nil
}

func (s *stubLikes) Deactivate(
	_ context.Context, userID int64, targetType string, targetID int64, at time.Time,
) (int64, bool, error) {
	if s.deactivateErr != nil {
		return 0, false, s.deactivateErr
	}
	s.deactivates = append(s.deactivates, stubLikeCall{userID: userID, targetType: targetType, targetID: targetID, at: at})
	return s.deactivateID, s.deactivateChanged, nil
}

func (s *stubLikes) ActiveStates(
	_ context.Context, _ int64, targetType string, targetIDs []int64,
) (map[int64]bool, error) {
	if s.statesErr != nil {
		return nil, s.statesErr
	}
	s.lastStatesType = targetType
	s.lastStatesIDs = append([]int64{}, targetIDs...)
	result := make(map[int64]bool, len(targetIDs))
	for _, id := range targetIDs {
		result[id] = s.activeStatesMap[id]
	}
	return result, nil
}

func (s *stubLikes) IsActive(_ context.Context, userID int64, targetType string, targetID int64) (bool, error) {
	if s.isActiveErr != nil {
		return false, s.isActiveErr
	}
	s.lastIsActiveUserID = userID
	s.lastIsActiveType = targetType
	s.lastIsActiveTargetID = targetID
	return s.isActive, nil
}

func (s *stubLikes) LikedPostIDs(_ context.Context, _ int64, _ int) ([]int64, error) {
	if s.likedIDsErr != nil {
		return nil, s.likedIDsErr
	}
	return s.likedIDs, nil
}

// ===== 评论端口 fake =====

type stubAdjust struct {
	id    int64
	delta int
	at    time.Time
}

type stubMarkDeleted struct {
	id                              int64
	byAdmin, byAuthor, byPostAuthor bool
	at                              time.Time
}

type stubCommentList struct {
	postID, rootID, cursor int64
	limit                  int
}

type stubByPostIDs struct {
	postIDs         []int64
	excludeAuthorID int64
	since           time.Time
	limit           int
}

type stubComments struct {
	CommentRepo

	comments        map[int64]model.Comment
	byIDsMap        map[int64]model.Comment
	topLevelRows    []model.Comment
	replyRows       []model.Comment
	previewsMap     map[int64][]model.Comment
	parentIDs       map[int64]*int64
	recentRows      []model.Comment
	byAuthorRows    []model.Comment
	byPostIDsRows   []model.Comment
	replyCountValue int
	likeCountValue  int

	markDeletedChanged bool

	inserts          []model.Comment
	adjustReplies    []stubAdjust
	adjustLikes      []stubAdjust
	markDeletedCalls []stubMarkDeleted
	listCalls        map[string]stubCommentList
	byPostIDsCalls   []stubByPostIDs

	getErr, getForUpdateErr, byIDsErr, insertErr, topLevelErr, repliesErr, previewsErr,
	adjustReplyErr, adjustLikeErr, markDeletedErr, parentErr, recentErr, byAuthorErr, byPostIDsErr error
}

func newStubComments() *stubComments {
	return &stubComments{
		comments:           map[int64]model.Comment{},
		byIDsMap:           map[int64]model.Comment{},
		previewsMap:        map[int64][]model.Comment{},
		parentIDs:          map[int64]*int64{},
		markDeletedChanged: true,
		listCalls:          map[string]stubCommentList{},
	}
}

func (s *stubComments) Get(_ context.Context, id int64) (model.Comment, error) {
	if s.getErr != nil {
		return model.Comment{}, s.getErr
	}
	if comment, ok := s.comments[id]; ok {
		return comment, nil
	}
	return model.Comment{}, apperr.NotFound("评论不存在或无权访问")
}

func (s *stubComments) GetForUpdate(_ context.Context, id int64) (model.Comment, error) {
	if s.getForUpdateErr != nil {
		return model.Comment{}, s.getForUpdateErr
	}
	if comment, ok := s.comments[id]; ok {
		return comment, nil
	}
	return model.Comment{}, apperr.NotFound("评论不存在或无权访问")
}

func (s *stubComments) ByIDs(_ context.Context, _ []int64) (map[int64]model.Comment, error) {
	if s.byIDsErr != nil {
		return nil, s.byIDsErr
	}
	return s.byIDsMap, nil
}

func (s *stubComments) Insert(_ context.Context, comment model.Comment) (model.Comment, error) {
	if s.insertErr != nil {
		return model.Comment{}, s.insertErr
	}
	s.inserts = append(s.inserts, comment)
	return comment, nil
}

func (s *stubComments) ListTopLevel(_ context.Context, postID, cursor int64, limit int) ([]model.Comment, error) {
	if s.topLevelErr != nil {
		return nil, s.topLevelErr
	}
	s.listCalls["top"] = stubCommentList{postID: postID, cursor: cursor, limit: limit}
	return s.topLevelRows, nil
}

func (s *stubComments) ListReplies(_ context.Context, rootID, cursor int64, limit int) ([]model.Comment, error) {
	if s.repliesErr != nil {
		return nil, s.repliesErr
	}
	s.listCalls["replies"] = stubCommentList{rootID: rootID, cursor: cursor, limit: limit}
	return s.replyRows, nil
}

func (s *stubComments) ReplyPreviews(_ context.Context, _ []int64, _ int) (map[int64][]model.Comment, error) {
	if s.previewsErr != nil {
		return nil, s.previewsErr
	}
	return s.previewsMap, nil
}

func (s *stubComments) AdjustReplyCount(_ context.Context, id int64, delta int, at time.Time) (int, error) {
	if s.adjustReplyErr != nil {
		return 0, s.adjustReplyErr
	}
	s.adjustReplies = append(s.adjustReplies, stubAdjust{id: id, delta: delta, at: at})
	return s.replyCountValue, nil
}

func (s *stubComments) AdjustLikeCount(_ context.Context, id int64, delta int, at time.Time) (int, error) {
	if s.adjustLikeErr != nil {
		return 0, s.adjustLikeErr
	}
	s.adjustLikes = append(s.adjustLikes, stubAdjust{id: id, delta: delta, at: at})
	return s.likeCountValue, nil
}

func (s *stubComments) MarkDeleted(
	_ context.Context, id int64, byAdmin, byAuthor, byPostAuthor bool, at time.Time,
) (bool, error) {
	if s.markDeletedErr != nil {
		return false, s.markDeletedErr
	}
	s.markDeletedCalls = append(s.markDeletedCalls, stubMarkDeleted{
		id: id, byAdmin: byAdmin, byAuthor: byAuthor, byPostAuthor: byPostAuthor, at: at,
	})
	return s.markDeletedChanged, nil
}

func (s *stubComments) ParentID(_ context.Context, commentID int64) (*int64, error) {
	if s.parentErr != nil {
		return nil, s.parentErr
	}
	return s.parentIDs[commentID], nil
}

func (s *stubComments) RecentByPost(_ context.Context, postID int64, limit int) ([]model.Comment, error) {
	if s.recentErr != nil {
		return nil, s.recentErr
	}
	s.listCalls["recent"] = stubCommentList{postID: postID, limit: limit}
	return s.recentRows, nil
}

func (s *stubComments) ListByAuthor(
	_ context.Context, authorID int64, afterAt time.Time, afterID int64, limit int,
) ([]model.Comment, error) {
	if s.byAuthorErr != nil {
		return nil, s.byAuthorErr
	}
	s.listCalls["author"] = stubCommentList{postID: authorID, cursor: afterID, limit: limit}
	return s.byAuthorRows, nil
}

func (s *stubComments) ListByPostIDsSince(
	_ context.Context, postIDs []int64, excludeAuthorID int64, since time.Time, limit int,
) ([]model.Comment, error) {
	if s.byPostIDsErr != nil {
		return nil, s.byPostIDsErr
	}
	s.byPostIDsCalls = append(s.byPostIDsCalls, stubByPostIDs{
		postIDs: append([]int64{}, postIDs...), excludeAuthorID: excludeAuthorID, since: since, limit: limit,
	})
	return s.byPostIDsRows, nil
}

// ===== 转发端口 fake =====

type stubRepostCall struct {
	id, userID, postID int64
	authorIsAI         bool
	at                 time.Time
}

type stubReposts struct {
	RepostRepo

	activateID        int64
	activateAt        time.Time
	activateChanged   bool
	deactivateID      int64
	deactivateChanged bool
	isActive          bool
	activeStatesMap   map[int64]bool
	events            []domain.RepostEvent

	activates   []stubRepostCall
	deactivates []stubRepostCall
	lastQuery   domain.RepostEventQuery

	lastIsActiveUserID, lastIsActivePostID int64

	activateErr, deactivateErr, isActiveErr, statesErr, listErr error
}

func newStubReposts() *stubReposts {
	return &stubReposts{
		activateChanged:   true,
		deactivateChanged: true,
		activeStatesMap:   map[int64]bool{},
	}
}

func (s *stubReposts) Activate(
	_ context.Context, id, userID, postID int64, authorIsAI bool, at time.Time,
) (int64, time.Time, bool, error) {
	if s.activateErr != nil {
		return 0, time.Time{}, false, s.activateErr
	}
	s.activates = append(s.activates, stubRepostCall{
		id: id, userID: userID, postID: postID, authorIsAI: authorIsAI, at: at,
	})
	repostID := s.activateID
	if repostID == 0 {
		repostID = id
	}
	repostedAt := s.activateAt
	if repostedAt.IsZero() {
		repostedAt = at
	}
	return repostID, repostedAt, s.activateChanged, nil
}

func (s *stubReposts) Deactivate(_ context.Context, userID, postID int64, at time.Time) (int64, bool, error) {
	if s.deactivateErr != nil {
		return 0, false, s.deactivateErr
	}
	s.deactivates = append(s.deactivates, stubRepostCall{userID: userID, postID: postID, at: at})
	return s.deactivateID, s.deactivateChanged, nil
}

func (s *stubReposts) IsActive(_ context.Context, userID, postID int64) (bool, error) {
	if s.isActiveErr != nil {
		return false, s.isActiveErr
	}
	s.lastIsActiveUserID = userID
	s.lastIsActivePostID = postID
	return s.isActive, nil
}

func (s *stubReposts) ActiveStates(_ context.Context, _ int64, postIDs []int64) (map[int64]bool, error) {
	if s.statesErr != nil {
		return nil, s.statesErr
	}
	result := make(map[int64]bool, len(postIDs))
	for _, id := range postIDs {
		result[id] = s.activeStatesMap[id]
	}
	return result, nil
}

func (s *stubReposts) ListEvents(_ context.Context, query RepostEventQuery) ([]RepostEvent, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.lastQuery = query
	return s.events, nil
}

// ===== 固定装配 =====

// stubSet 汇总全部 fake 端口，默认构造一个可直接调用读路径的 Service。
type stubSet struct {
	clk      clock.Clock
	ids      IDGenerator
	tx       TxRunner
	users    *stubUsers
	posts    *stubPosts
	notices  *stubNotices
	audit    *stubAudit
	follows  *stubFollows
	requests *stubRequests
	likes    *stubLikes
	comments *stubComments
	reposts  *stubReposts

	commentLimiter *ratelimit.Limiter
	likeLimiter    *ratelimit.Limiter
	followLimiter  *ratelimit.Limiter
}

// newStubSet 构造带默认假数据的装配；业务时钟固定为 stubNow。
func newStubSet() *stubSet {
	return &stubSet{
		clk:      clock.NewFake(stubNow),
		ids:      &fixIDs{next: 100},
		tx:       fixTx{},
		users:    newStubUsers(),
		posts:    newStubPosts(),
		notices:  &stubNotices{},
		audit:    &stubAudit{},
		follows:  newStubFollows(),
		requests: newStubRequests(),
		likes:    newStubLikes(),
		comments: newStubComments(),
		reposts:  newStubReposts(),
	}
}

func (s *stubSet) deps() Deps {
	return Deps{
		Clock:    s.clk,
		IDs:      s.ids,
		Tx:       s.tx,
		Users:    s.users,
		Posts:    s.posts,
		Notices:  s.notices,
		Audit:    s.audit,
		Follows:  s.follows,
		Requests: s.requests,
		Likes:    s.likes,
		Comments: s.comments,
		Reposts:  s.reposts,

		CommentLimiter: s.commentLimiter,
		LikeLimiter:    s.likeLimiter,
		FollowLimiter:  s.followLimiter,
	}
}

// service 按当前 fake 状态构造被测服务。
func (s *stubSet) service() *Service { return New(s.deps()) }

// serviceWith 允许用例覆盖单个依赖（如错误注入 IDs/Tx/限频器）后构造服务。
func (s *stubSet) serviceWith(mutate func(*Deps)) *Service {
	deps := s.deps()
	mutate(&deps)
	return New(deps)
}

// newFixedLimiter 构造小阈值限频器；窗口使用 1 小时，保证同一用例内不会自然过期，
// 又不需要真实 sleep。
func newFixedLimiter(limit int) *ratelimit.Limiter {
	return ratelimit.New(limit, time.Hour)
}
