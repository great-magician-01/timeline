package domain

import (
	"testing"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

func TestActionAllowedForTriggerCommentSemantics(t *testing.T) {
	// 只开启被动回复：提及/评论触发的 comment 应允许，主动评论应拒绝。
	passiveOnly := contracts.BehaviorSettings{AllowPassiveReply: true}
	if !ActionAllowedForTrigger(passiveOnly, TriggerMention, ActionComment) {
		t.Fatal("提及触发的 comment 应按 allow_passive_reply 允许")
	}
	if !ActionAllowedForTrigger(passiveOnly, TriggerComment, ActionComment) {
		t.Fatal("收到评论触发的 comment 应按 allow_passive_reply 允许")
	}
	if ActionAllowedForTrigger(passiveOnly, TriggerProactive, ActionComment) {
		t.Fatal("主动互动触发的 comment 应按 allow_active_comment 判断")
	}
	if !ActionAllowedForTrigger(passiveOnly, TriggerMention, ActionReply) {
		t.Fatal("reply 应按 allow_passive_reply 允许")
	}

	// 只开启主动评论：主动互动 comment 允许，提及触发的 comment 仍按被动回复拒绝。
	activeOnly := contracts.BehaviorSettings{AllowActiveComment: true}
	if !ActionAllowedForTrigger(activeOnly, TriggerProactive, ActionComment) {
		t.Fatal("主动互动 comment 应按 allow_active_comment 允许")
	}
	if ActionAllowedForTrigger(activeOnly, TriggerMention, ActionComment) {
		t.Fatal("提及触发的 comment 不应受 allow_active_comment 影响")
	}
	if ActionAllowedForTrigger(activeOnly, TriggerProactive, ActionReply) {
		t.Fatal("reply 仍应按 allow_passive_reply 判断")
	}
}

func TestProactiveAllowedIncludesActiveComment(t *testing.T) {
	if !ProactiveAllowed(contracts.BehaviorSettings{AllowActiveComment: true}) {
		t.Fatal("只开启主动评论时应允许主动互动调度")
	}
	if ProactiveAllowed(contracts.BehaviorSettings{AllowPassiveReply: true}) {
		t.Fatal("只开启被动回复不应参与主动互动调度")
	}
}

func TestValidCandidateScope(t *testing.T) {
	for _, scope := range []string{ScopeGlobalPublic, ScopeFollowed, ScopeTags, ScopeAuthors} {
		if !ValidCandidateScope(scope) {
			t.Fatalf("候选范围 %q 应有效", scope)
		}
	}
	for _, scope := range []string{"", ScopeNone, "unknown"} {
		if ValidCandidateScope(scope) {
			t.Fatalf("候选范围 %q 应无效（未配置不得主动互动）", scope)
		}
	}
}
