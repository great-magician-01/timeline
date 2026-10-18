package app

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 说明：以下 fake 通过内嵌端口接口满足完整方法集，只覆写测试路径实际调用的方法；
// 未被覆写的方法在测试路径不应被调用（调用会 nil panic，能暴露意外的额外依赖）。

type fixClock struct{ at time.Time }

func (f fixClock) Now() time.Time { return f.at }

type fixIDs struct{ next int64 }

func (f *fixIDs) Next() (int64, error) { f.next++; return f.next, nil }

type fixTx struct{}

func (fixTx) WithTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fixUsers struct {
	contracts.UserReader
	user model.User
}

func (f *fixUsers) GetUser(context.Context, int64) (model.User, error) { return f.user, nil }

type fixPosts struct {
	contracts.ContentService
	notFound bool
	postIDs  []int64
	posts    map[int64]model.Post
}

func (f *fixPosts) GetPost(context.Context, int64) (model.Post, error) {
	if f.notFound {
		return model.Post{}, apperr.NotFound("内容不存在")
	}
	return model.Post{}, nil
}

func (f *fixPosts) ListPostIDsByAuthor(context.Context, int64) ([]int64, error) {
	return f.postIDs, nil
}

func (f *fixPosts) GetPosts(context.Context, []int64) (map[int64]model.Post, error) {
	return f.posts, nil
}

type fixComments struct {
	CommentRepo
	listCalled  bool
	listExclude int64
	listPostIDs []int64
	listItems   []model.Comment
	getNotFound bool
}

func (f *fixComments) ListByPostIDsSince(
	_ context.Context, postIDs []int64, excludeAuthorID int64, _ time.Time, _ int,
) ([]model.Comment, error) {
	f.listCalled = true
	f.listExclude = excludeAuthorID
	f.listPostIDs = append([]int64{}, postIDs...)
	return f.listItems, nil
}

func (f *fixComments) Get(context.Context, int64) (model.Comment, error) {
	if f.getNotFound {
		return model.Comment{}, apperr.NotFound("评论不存在或无权访问")
	}
	return model.Comment{}, nil
}

type fixLikes struct {
	LikeRepo
	changed bool
}

func (f *fixLikes) Deactivate(context.Context, int64, string, int64, time.Time) (int64, bool, error) {
	return 0, f.changed, nil
}

type fixReposts struct {
	RepostRepo
	changed bool
}

func (f *fixReposts) Deactivate(context.Context, int64, int64, time.Time) (int64, bool, error) {
	return 0, f.changed, nil
}

func newFixService(posts *fixPosts, comments *fixComments, likes *fixLikes, reposts *fixReposts) *Service {
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	return New(Deps{
		Clock:    fixClock{at: at},
		IDs:      &fixIDs{},
		Tx:       fixTx{},
		Users:    &fixUsers{user: model.User{ID: 1, Status: model.StatusActive}},
		Posts:    posts,
		Comments: comments,
		Likes:    likes,
		Reposts:  reposts,
	})
}

// TestListNewCommentsOnAuthorPostsExcludesSelfComment 验证评论触发扫描：
// 只查询作者自己的公开已发布帖子，并把 ExcludeAuthorID 透传给 repo（旧实现排除自评论）。
func TestListNewCommentsOnAuthorPostsExcludesSelfComment(t *testing.T) {
	posts := &fixPosts{
		postIDs: []int64{7, 8, 9},
		posts: map[int64]model.Post{
			7: {ID: 7, AuthorID: 100, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic},
			8: {ID: 8, AuthorID: 100, Status: model.PostStatusPublished, Visibility: model.VisibilityFollowers},
			9: {ID: 9, AuthorID: 100, Status: model.PostStatusDraft, Visibility: model.VisibilityPublic},
		},
	}
	comments := &fixComments{listItems: []model.Comment{{ID: 5, PostID: 7, AuthorID: 200}}}
	svc := newFixService(posts, comments, nil, nil)

	got, err := svc.ListNewCommentsOnAuthorPosts(context.Background(), contracts.CommentTriggerQuery{
		AuthorID:        100,
		ExcludeAuthorID: 100,
		Since:           time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC),
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("ListNewCommentsOnAuthorPosts 返回错误: %v", err)
	}
	if len(got) != 1 || got[0].ID != 5 {
		t.Fatalf("返回评论 = %+v，期望仅评论 5", got)
	}
	if !comments.listCalled {
		t.Fatal("应调用评论 repo 查询新评论")
	}
	if comments.listExclude != 100 {
		t.Fatalf("repo 收到 ExcludeAuthorID = %d，期望 100（排除自评论）", comments.listExclude)
	}
	if len(comments.listPostIDs) != 1 || comments.listPostIDs[0] != 7 {
		t.Fatalf("repo 收到帖子过滤 = %v，期望仅公开已发布帖子 7", comments.listPostIDs)
	}
}

// TestUnlikeForActorMissingPostIsIdempotent 取消点赞一个从未存在的帖子应幂等成功（计数 0）。
func TestUnlikeForActorMissingPostIsIdempotent(t *testing.T) {
	svc := newFixService(&fixPosts{notFound: true}, &fixComments{}, &fixLikes{changed: false}, nil)

	active, count, err := svc.UnlikeForActor(context.Background(), contracts.LikeInput{
		ActorID: 1, TargetType: "post", TargetID: 999,
	})
	if err != nil {
		t.Fatalf("取消不存在帖子的点赞应幂等成功，实际错误: %v", err)
	}
	if active || count != 0 {
		t.Fatalf("结果 = (%v, %d)，期望 (false, 0)", active, count)
	}
}

// TestUnlikeForActorMissingCommentIsIdempotent 取消点赞一个从未存在的评论应幂等成功（计数 0）。
func TestUnlikeForActorMissingCommentIsIdempotent(t *testing.T) {
	comments := &fixComments{getNotFound: true}
	svc := newFixService(&fixPosts{}, comments, &fixLikes{changed: false}, nil)

	active, count, err := svc.UnlikeForActor(context.Background(), contracts.LikeInput{
		ActorID: 1, TargetType: "comment", TargetID: 999,
	})
	if err != nil {
		t.Fatalf("取消不存在评论的点赞应幂等成功，实际错误: %v", err)
	}
	if active || count != 0 {
		t.Fatalf("结果 = (%v, %d)，期望 (false, 0)", active, count)
	}
}

// TestUnrepostForActorMissingPostIsIdempotent 取消转发一个从未存在的帖子应幂等成功（计数 0）。
func TestUnrepostForActorMissingPostIsIdempotent(t *testing.T) {
	svc := newFixService(&fixPosts{notFound: true}, &fixComments{}, nil, &fixReposts{changed: false})

	active, count, err := svc.UnrepostForActor(context.Background(), contracts.RepostInput{
		ActorID: 1, PostID: 999,
	})
	if err != nil {
		t.Fatalf("取消不存在帖子的转发应幂等成功，实际错误: %v", err)
	}
	if active || count != 0 {
		t.Fatalf("结果 = (%v, %d)，期望 (false, 0)", active, count)
	}
}
