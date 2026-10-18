package app

import (
	"context"
	"testing"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/page"
)

// TestFetchTimelineCandidatesPassesFollowingOnlyToReposts 防回归：关注时间线的纯转发分支
// 必须把 FollowingOnly=true 与关注集合一起传给 social 端口；关注集合为空也必须传，
// 由 social 端用 ANY(空数组) 过滤出空结果，不能退化为不过滤（refactor 回归）。
//
// includePosts=false 时 content repo 的帖子分支会直接返回空，用例只验证 social 端口入参。
func TestFetchTimelineCandidatesPassesFollowingOnlyToReposts(t *testing.T) {
	cases := []struct {
		name         string
		followingIDs []int64
	}{
		{"空关注集合", []int64{}},
		{"非空关注集合", []int64{2, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []contracts.RepostTimelineQuery
			svc := newTestService(t, Deps{Social: fakeSocial{repostCalls: &calls}})
			candidates, err := svc.fetchTimelineCandidates(
				context.Background(), humanViewer(1),
				timelineParams{includeReposts: true, followingOnly: true},
				tc.followingIDs, nil, page.TimeCursor{}, 20,
			)
			if err != nil {
				t.Fatalf("fetchTimelineCandidates 返回错误: %v", err)
			}
			if len(candidates) != 0 {
				t.Fatalf("假 social 未返回转发事件时应无候选，实际 %d", len(candidates))
			}
			if len(calls) != 1 {
				t.Fatalf("social 转发查询调用 = %d，期望 1", len(calls))
			}
			query := calls[0]
			if !query.FollowingOnly {
				t.Fatalf("关注时间线必须传 FollowingOnly=true，实际 %+v", query)
			}
			if query.FollowingIDs == nil || len(query.FollowingIDs) != len(tc.followingIDs) {
				t.Fatalf("FollowingIDs 未按关注集合透传: %#v", query.FollowingIDs)
			}
			for i := range tc.followingIDs {
				if query.FollowingIDs[i] != tc.followingIDs[i] {
					t.Fatalf("FollowingIDs[%d] = %d，期望 %d", i, query.FollowingIDs[i], tc.followingIDs[i])
				}
			}
			if query.Limit != 20 {
				t.Fatalf("Limit = %d，期望 20", query.Limit)
			}
		})
	}
}

// TestFetchTimelineCandidatesGlobalTimelineDoesNotFilterFollowing 防回归：
// 全站/个人主页时间线不得设置 FollowingOnly，仍按全量纯转发查询。
func TestFetchTimelineCandidatesGlobalTimelineDoesNotFilterFollowing(t *testing.T) {
	var calls []contracts.RepostTimelineQuery
	svc := newTestService(t, Deps{Social: fakeSocial{repostCalls: &calls}})
	_, err := svc.fetchTimelineCandidates(
		context.Background(), guestViewer(),
		timelineParams{includeReposts: true},
		nil, nil, page.TimeCursor{}, 20,
	)
	if err != nil {
		t.Fatalf("fetchTimelineCandidates 返回错误: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("social 转发查询调用 = %d，期望 1", len(calls))
	}
	if calls[0].FollowingOnly {
		t.Fatalf("全站时间线不应设置 FollowingOnly: %+v", calls[0])
	}
}
