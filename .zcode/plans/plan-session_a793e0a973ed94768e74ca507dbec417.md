# 群聊功能实现计划

## 已确认的产品决策

1. **独立群聊入口**：新增「群聊」页面与导航项，徽章与私信分开；后端独立 group 域。
2. **直接拉人 + 策略保护**：群主拉人即入群；`dm_policy=none`（关闭私信）的用户不可被拉入；成员可随时退出/隐藏会话。
3. **两级角色**：群主（改名/拉人/踢人/转让/解散）+ 成员；群主退出前必须先转让或解散。
4. **AI 参与群聊**：AI 账号可被拉入群；行为面 = 被 @ 回复（复用 `allow_passive_reply`）+ 自主发言（新开关 `allow_group_message`，默认关闭）。

默认参数（可后续调整）：群名 1–30 字；成员上限 200；每人建群上限 20；消息规则与私信一致（纯文本、2000 字、64KiB 源上限、2 分钟撤回、逻辑删除）；发送/拉人共享限频 60 次/10 分钟（与私信同参数）。

## 一、数据库迁移 `0025_group_chat.sql`

无外键、无时间默认值、逻辑删除，迁移器 v2 分句兼容（无函数体语句）：

- `timeline.group_conversations`：`id`、`name`、`owner_id`、`last_message_id`（NULL=无消息）、`status`（`active`/`dissolved`）、`created_at`/`updated_at`。
- `timeline.group_members`：`id`、`group_id`、`user_id`、`role`（`owner`/`member`）、`last_read_message_id`（已读游标）、`hidden_message_id`（隐藏指针，群有更新的新消息时列表自动重现，避免群发消息时全量 UPDATE 成员行）、`joined_at`、`left_at`（退出/被踢，逻辑删除）；`UNIQUE (group_id, user_id)`；部分索引 `(user_id) WHERE left_at IS NULL`（我的群列表）与 `(group_id) WHERE left_at IS NULL`（成员分页）。
- `timeline.group_messages`：结构与 `dm_messages` 同构（去掉 recipient）；索引 `(group_id, id DESC)`。
- `ALTER TABLE timeline.ai_behavior_settings ADD COLUMN allow_group_message BOOLEAN NOT NULL DEFAULT FALSE`。

未读模型与 DM 不同：群消息 N 个接收者不能逐条标记，采用成员级游标——未读数 = `id > last_read_message_id AND sender_id != me AND status='published'`，列表未读一条 GROUP BY 批量查询。

## 二、共享层与实时推送扩展

- `internal/shared/model`：`GroupConversation/GroupMember/GroupMessage` 行结构与枚举。
- 新增 `internal/shared/chat`（纯常量+校验：2000 字上限、64KiB 源上限、2 分钟撤回窗、无图片校验、预览截断），dm 与 group 的 domain 共同引用（dm/domain 改为薄引用，属受测小重构）。
- `internal/shared/contracts`：`RealtimePublisher` 新增 `PublishGroup(userID, groupID)`；新增 AI 触发端口 `GroupAITrigger.OnGroupMention(aiID, groupID, messageID)` 与 agent 侧所需的 `GroupReader`（群名+最近消息）、`GroupMessageWriter`（AI 代发）端口；`BehaviorSettings` 增加 `AllowGroupMessage`。
- `internal/platform/eventbus`：新增 `KindGroup = "group"`（载荷复用 `ConversationID` 字段）。
- `internal/modules/realtime/http/handler.go`：携带 `conversation_id` 的特判从 `KindDM` 扩展为 `KindDM|KindGroup`。

## 三、后端新模块 `internal/modules/group/`（四层）

- **domain/rules.go**：群名校验、`CanBeInvited`（停用拒绝、`dm_policy=none` 拒绝、AI 允许）、群主专属动作判定、发言资格（成员且群 active）、撤回窗（引用 shared/chat）、踢人不可踢群主、群主退出须先转让/解散。
- **app**：`service.go`（Deps：Clock/IDs/Tx/Users(auth)/repos/Realtime/AITrigger/限频器）+ `conversation.go`（建群校验成员名单与上限、我的群列表、详情、成员分页、改名、拉人、踢人、转让、退出、解散、隐藏）+ `message.go`（发送：事务内重校成员资格→插入→touch last_message_id→`db.AfterCommit` 向全部活跃成员发 group 信号、发送时 `richtext.ExtractMentions` 提及成员 AI 则 AfterCommit 触发 AI 任务；消息列表 cursor+after_id；已读=推进游标至群最新消息；撤回；未读总数）。
- **repo**：conversation/member/message 三仓储，SQL 全在本层；成员资格校验走 `GetForUpdate`。
- **http**：handler + view（DTO，ID 字符串化、role、unread_count、UserBrief 复用）。

## 四、路由与装配

- `internal/transport/router`：GET(Login)：`/groups`、`/groups/unread-count`、`/groups/:id`、`/groups/:id/members`、`/groups/:id/messages`；POST(HumanWrite)：`/groups`、`/groups/:id/{rename,invite,transfer,leave,dissolve,read,hide}`、`/groups/:id/members/:uid/remove`、`/groups/:id/messages`、`/groups/messages/:id/recall`。全部 GET/POST + 语义化 action 路径。
- `internal/app/wire.go`：装配 group 模块，注入 authApp/realtimePublish/AI 触发适配（agent app 实现组合根注入，构造环用 lazy 端口同现有模式）。
- 管理后台不注册任何群聊路由（与私信一致，完全不可见；AI 群聊任务与调用记录仍按既有 AI 管理面可见，属现有设计）。

## 五、AI 行为面（modules/agent + modules/ai）

- **被 @ 回复**：群消息发送时提取提及，命中成员 AI 经 `GroupAITrigger` 端口在 AfterCommit 投递任务（幂等键 `group_mention:<msgID>:<aiID>`），比轮询扫描更精确。
- **自主发言**：`scheduler.go` 新增 `scheduleGroupMessage`（schedule 键 `group_message_interval_minutes`，默认 60 分钟、0 关闭，幂等桶），投递 proactive 触发的 `group_message` 动作任务。
- **任务执行**（`task.go`）：`ActionGroupMessage` 加入 `domain` 白名单与决策 schema 分支；执行时二次校验 AI 仍是成员且群 active（任务执行时重新授权原则）；经 `GroupReader` 注入群名+最近 N 条消息（含 @ 者昵称）为即时上下文（**首期不写入分层记忆**，控制范围）；产出经 `GroupMessageWriter` 代发，走与人类一致的校验与 AI 账号限频；审计与调用记录照旧。
- **管理端**：`ai_behavior_settings` 读写与 AI 配置页（后端 admin http + 前端 aiAdmin.ts/对应组件）增加「群聊发言」开关与间隔。
- 需求红线同步改述：「AI 只能使用公开内容、自身数据与**自身参与的群聊内容**」。

## 六、前端（web/src）

- `api/groups.ts` + `types.ts` 新 DTO；`stores/groups.ts`（30s 轮询未读徽章 + SSE 信号刷新）；`composables/realtime.ts` 监听 `group` 事件、新增 `onGroupSignal` 分发（与 dm 并列）。
- `views/GroupsView.vue`（我的群列表：建群表单[群名+用户名逐个添加]、未读高亮、隐藏）；`views/GroupThreadView.vue`（对话页，复用 MessageThreadView 模式：10s 增量轮询+SSE、撤回、已读、emoji 面板、消息带发送者昵称、群头部链接到成员管理）；`views/GroupMembersView.vue`（成员分页、拉人、踢人、转让、改名、解散、退出，按角色显隐）。
- 路由：`/groups`、`/groups/:id`、`/groups/:id/members`；导航新增「群聊」入口带徽章（UserAvatar 无群头像，首期用群名首字/群图标占位）。

## 七、测试

- group domain 表驱动单测；group app fake 端口单测（拉人策略矩阵/AI 可入群/dm_policy=none 拒绝/转让/解散/踢人/已读游标/隐藏指针/提及触发端口）。
- agent 侧：动作白名单、调度间隔、群消息任务执行（fake GroupReader/Writer/模型客户端）单测。
- `internal/server/group_integration_test.go` `TestGroupIntegration`（testkit）：建群→拉人（含拒绝矩阵）→发言→未读→已读→撤回→隐藏重现→退出/转让/踢人/解散只读→非成员 404→SSE group 信号（conversation_id 字符串化）→AI 被 @ 回复经 mock-llm 生成落群。
- 前端 Vitest：groups store、realtime group 信号；`npm test` + `npm run build`。

## 八、文档同步（按 AGENTS.md §7 顺序）

REQUIREMENTS.md（§10.7 移除「不提供群聊」、新增 §10.8；§3 决策表/§22 验收项/红线改述）→ API.md（新增群聊章，后续章顺延；SSE 章补 group 事件）→ DATA_MODEL.md（6.33–6.35 + behavior 新列 + 索引说明）→ PAGES_AND_PERMISSIONS.md、WORKFLOWS.md（群生命周期与 AI 群聊任务状态机）→ ACCEPTANCE.md（GROUP-01～xx + SEC 项：非成员 404/停用拒绝/后台不可见/AI 成本与默认关闭）→ ARCHITECTURE.md（modules 补 group、KindGroup、新契约端口）→ DESIGN_SYSTEM.md（群头像占位/成员列表模式）→ migrations/README.md、docs/README.md、AGENTS.md（§1/§3/§4/§6/§10 变更记录）。

## 九、实施顺序（每阶段保持测试绿）

1. 迁移 0025 + shared 层（model/chat 抽取/contracts/eventbus 扩展）
2. group 模块四层 + 路由 + 装配 + realtime handler 扩展 + 单测
3. `TestGroupIntegration`（人类路径）
4. 前端全量（api/store/三个视图/导航/realtime）+ Vitest
5. AI 行为面（agent 调度与执行 + ai/admin 配置）+ 单测 + 集成
6. 文档全套同步 + AGENTS.md
7. 全量验证：`go test ./...`、`go vet ./...`、`go test ./internal/architecture`、`TIMELINE_INTEGRATION=1 go test -p 1 -count=1 ./...`（需本地 PG 测试 schema 与 mock-llm）、`cd web && npm test && npm run build`

## 风险与边界说明

- SSE 新事件 kind 对旧客户端无影响（未监听即忽略）；单实例前提不变。
- AI 自主发言默认关闭，成本由管理员显式开启承担；调度幂等桶保证每 AI 每 interval 至多一条自主群消息，被 @ 回复每条消息至多一次。
- `dm_policy` 语义从「私信接收策略」扩展为同时约束「被拉群」，需求文档明确写入。
- 群消息不进 AI 分层记忆（首期），只作任务期即时上下文。