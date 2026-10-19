// Package contracts 是目标架构中模块之间的跨模块发布语言（publish language）。
//
// 目标架构规则（见 CODE_QUALITY_REFACTOR_PLAN.md §0 第 8 条、§3.3）：
//   - modules/x 不得 import modules/y；跨模块调用一律通过本包定义的接口与输入结构体；
//   - 生产者模块的 app 类型结构化实现本包接口，消费者只依赖本包；组合根（internal/app）
//     负责把生产者实现注入消费者；
//   - 本包只允许 import 标准库与 internal/shared/model：不出现 gin、pgx、platform/*、
//     modules/*，也不含任何 SQL/IO/事务代码。
//
// 时间约定：所有写操作的输入都显式携带 At/RunAt 等应用时钟时间，由调用方（app 用例）
// 从 platform/clock 取一次 now 后传入，保证同一事务内多个模块写入使用同一个时间。
//
// 事务约定：实现方通过 platform/db 从 ctx 解析当前事务（db.WithTx 传播），因此消费者在
// 自己的 WithTx 内调用这些方法即可跨模块共享同一事务；接口签名不出现 pgx.Tx。
//
// 消费者 → 接口速查（详细消费者标注在各接口定义处）：
//
//	agent    → ContentService、SocialService、AIService、AuthService/UserReader、NoticeService、JobsQueue、SettingsReader
//	admin    → AuthService/UserWriter、ContentService、SocialService、MediaService、JOBS、AuditWriter、SettingsReader/Writer、AIService/SecretCipher、AgentService
//	content  → AuthService/UserReader、MediaService、SocialService/RelationReader、NoticeService、JobsQueue、AuditWriter、SettingsReader
//	social   → ContentService、AuthService/UserReader、NoticeService、AuditWriter
//	notice   → ContentService、SocialService、AuthService/UserReader
//	dm       → AuthService/UserReader、SocialService/RelationReader（私信不向其它模块发布能力）
//	media    → AuthService/UserReader+UserWriter、ContentService、JobsQueue、AuditWriter、SettingsReader
//	ai       → AuthService/UserReader、SettingsReader、AuditWriter
//	auth     → SettingsReader、AuditWriter
//
// 与 platform/* 的关系：platform/audit、platform/jobs、platform/settings 已经提供技术实现，
// 本包为它们定义「模块可见的窄端口」（AuditWriter、JobsQueue、SettingsReader/Writer）与
// 跨模块数据结构。组合根在 wire 时用轻量适配器把 platform 实现适配到本包接口（签名不同：
// platform 类型 vs contracts 类型），或在后续重构中让 platform 直接以本包类型为准。
package contracts
