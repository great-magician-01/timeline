// Package repo 是社交域唯一包含 SQL 的层：关注关系与申请、点赞、评论、纯转发。
//
// 约定：
//   - 所有写库时间由 app 通过 now 参数传入，SQL 中不出现 now()/CURRENT_DATE/interval；
//   - 计数更新为原子增量，不用 GREATEST 夹取（CHECK 约束兜底暴露不一致）；
//   - 取消类操作只置失效标记（is_active=FALSE / status='deleted'），不物理删除；
//   - repo 通过 db.ExecutorFrom 自动加入 ctx 中的事务。
//
// 账号（users）与帖子（posts）分别由 auth、content 模块拥有，本包不写它们的 SQL。
package repo

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// notFound 把 pgx.ErrNoRows 映射为领域 not_found，避免存储层错误泄漏到 app。
func notFound(err error, message string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound(message)
	}
	return err
}
