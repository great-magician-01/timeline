// Package migrations 内嵌 SQL 迁移文件。
// 命名规范：NNNN_描述.sql（按文件名排序执行，只允许追加，不修改已应用的文件）。
package migrations

import "embed"

// 嵌入本目录全部文件（当前目录的 README.md 作为恒存锚点）。
//
// 为什么不是 "all:*.sql"：go:embed 要求**每个** pattern 至少匹配一个文件，
// 目录里一个 .sql 都没有时会编译失败（"pattern all:*.sql: no matching files found"）。
// 用 all:* 则永远至少匹配到 README.md；运行时用 fs.Glob(FS, "*.sql") 取迁移文件，
// 数量为零也完全正常。
//
//go:embed all:*
var FS embed.FS
