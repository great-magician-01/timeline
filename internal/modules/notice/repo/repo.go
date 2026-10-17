// Package repo 是通知域唯一包含 SQL 的层：notifications 与 mentions 两张表。
//
// 约定：写库时间由 app 通过 CreatedAt 参数传入，SQL 中不出现 now()/CURRENT_DATE；
// 列表过滤使用静态片段 + $n 占位符拼装；repo 通过 db.ExecutorFrom 自动加入 ctx 中的事务。
// 账号、帖子、评论数据分别由 auth/content/social 模块拥有，本包不写它们的 SQL。
package repo
