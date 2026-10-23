# SSE 实时推送实现计划

> 状态：已实施（2026-10-23，全部验证通过：默认与集成测试、`go vet`、架构守卫、`npm run build`）　|　创建：2026-10-23　|　前置分析：已完成（见下文「方案概述」）

## 方案概述

为通知中心与私信增加 SSE（Server-Sent Events）实时推送，替代当前前端 30s/10s 轮询的延迟。**核心设计决策：事件是"提示信号"而非"内容载荷"**——流里只推接收者 ID + 事件种类（如"通知有更新"），客户端收到后复用现有 `refresh()` 全量拉取。这样规避了三个问题：流内事件的可见性过滤（红线"非公开内容不得泄露"）、载荷组装的跨模块依赖、以及断线补偿（重连后全量 refresh 兜底，不需要 Last-Event-ID 持久化游标）。

**结论：架构方向不需要调整**（四层模块、contracts 端口、组合根、声明式路由全部兼容），需要的是新增两块基建（进程内事件总线、SSE 传输辅助）+ 一处语义级调整（事件必须事务提交后发布）+ 文档边界补充。无数据库迁移，不需要 Redis/消息队列/WebSocket。

已核实的关键前提：

- `http.Server`（`cmd/server/main.go:114`）只设 `ReadHeaderTimeout`，无 WriteTimeout——长连接不会被服务器杀掉，现成可用。
- `Recovery` 已处理"响应已写出不再覆盖"（`internal/transport/middleware/recovery.go`），SSE 中途 panic 不破坏流。
- 路由 Policy 表（`internal/transport/router/policies.go`）加一条 `Login` GET 路由零阻力；EventSource 是 GET 且不能带自定义头，Login 策略无 CSRF，天然兼容；项目无 CORS 头，同源策略挡住跨站 EventSource 读取。
- 部署形态是单实例 Go 应用（文档既定前提），进程内 hub 成立。
- 通知写入集中在 `contracts.NoticeService.CreateNotification`（`internal/modules/notice/app/write.go:14`），是天然的唯一通知事件发布点；私信独立路径（`internal/modules/dm/app/message.go:14` SendMessage）。
- **唯一语义问题**：`TxManager.WithTx`（`internal/platform/db/tx.go`）无 after-commit 钩子，而通知写入发生在调用方事务内部（如 social 的 `createCommentFor` 在 `WithTx` 内调 `notifyComment`）。写库后立即 publish 会出现"客户端收到推送但事务未提交、列表为空"的竞态。

## 1. 设计决策

| 决策点 | 选择 | 理由 |
| --- | --- | --- |
| 事件语义 | 提示信号，不推内容载荷 | 规避流内可见性过滤与跨模块载荷组装；断线重连全量拉取兜底 |
| 发布时序 | `TxManager` 增加 after-commit 机制，事件在事务提交后发布 | 唯一能根治竞态的位置；调用点零心智负担（发布方不用关心自己在不在事务内） |
| 事件总线 | `internal/platform/eventbus`（进程内、无业务语义） | 符合 platform"技术基座"定位；单实例部署前提下不需要外部队列 |
| 跨模块发布语言 | `shared/contracts` 定义 `RealtimePublisher` 窄端口 + 事件结构 | 与现有 audit/jobs 适配器模式一致（`wire.go` 已有先例） |
| SSE handler 归属 | 新增极小模块 `internal/modules/realtime`（仅 app + http，无 domain/repo/SQL） | 事件源横跨 notice/dm 两个域，放 notice/http 会造成跨域归属；无 SQL 所以无 repo；守卫规则按模式扫描，需跑一遍验证 |
| 幂等去重的幽灵信号 | `NotificationRepo.Insert` 改为返回 `(inserted bool, err error)`（`CommandTag.RowsAffected()`） | 目前 repo 丢弃了 RowsAffected（`notification_repo.go:46`）；notice 模块内自洽修改，避免幂等跳过时发幽灵红点 |
| 轮询去留 | 保留现有 30s 轮询作为降级兜底，间隔不变 | EventSource 断线自动重连是内置的；轮询天然兜底连接失败/代理不支持场景，v1 不做复杂降级切换 |
| 配置 | v1 不加新环境变量，连接上限/心跳间隔等用 eventbus 内常量 | 避免文档/配置面扩散；后续有需要再提为配置 |

## 2. 后端改动（按依赖序）

### 2.1 `internal/platform/db/tx.go` — AfterCommit 机制

- 新增 `func AfterCommit(ctx context.Context, fn func())`：
  - `InTx(ctx)` 为真：把 fn 追加到随事务创建的回调队列（`withTx` 在 Begin 时附带 `*txCallbacks`，嵌套 `WithTx` 复用外层事务即共享同一队列）；
  - 无事务：立即执行（调用方不在事务内时的自然语义）。
- `WithTx` 在 `tx.Commit` 成功后按注册顺序执行回调，ctx 用 `context.WithoutCancel(ctx)`（提交后请求 ctx 可能随即取消，发布必须能完成）；回滚路径不执行。
- 回调契约写进注释：**不得阻塞、不得 panic**；eventbus 的 `Publish` 自身 recover 兜底。

### 2.2 `internal/platform/eventbus/broker.go` — 进程内事件总线（新包）

- `type Event struct { Kind string; ConversationID int64 }`（Kind：`notification` / `dm`；无业务包依赖）。
- `Broker`：按 userID 的订阅表（`map[int64]map[*subscription]struct{}` + RWMutex）；`Subscribe(ctx, userID) (<-chan Event, cancel func(), err error)`；`Publish(userID int64, ev Event)` 非阻塞（select default）。
- 缓冲 channel（16）；**慢消费者策略：缓冲满直接丢弃该事件**（信号本就有轮询兜底，丢弃无害），debug 级计数日志。
- 上限常量：每用户并发连接 ≤3（新标签页场景）、全局 ≤2000；`Subscribe` 超限返回错误，由 handler 映射 503。
- `Close()`：取消全部订阅（优雅关闭广播断开用）。
- 纯 Go 无 gin，goroutine 由调用方 ctx 取消驱动，hub 本身无常驻 goroutine。

### 2.3 `internal/shared/contracts/realtime.go` — 发布语言（新文件）

- `type RealtimePublisher interface { PublishNotification(recipientID int64); PublishDM(recipientID, conversationID int64) }`——方法化而非裸 Publish(Event)，调用方不接触事件结构细节。
- `internal/app/adapters.go` 增加 `realtimeAdapter{broker *eventbus.Broker}`（编译期断言实现接口），模式同现有 `auditAdapter`/`jobsAdapter`。

### 2.4 `internal/modules/notice` — 通知事件发布点

| 文件 | 改动 |
| --- | --- |
| `repo/notification_repo.go:46` | `Insert` 返回 `(bool, error)`（`tag.RowsAffected() > 0`）；app 层接口 `port.go` 同步 |
| `app/write.go:14` `CreateNotification` | Insert 成功（非幂等跳过）时 `db.AfterCommit(ctx, func(){ publisher.PublishNotification(recipientID) })`；接收者无效/停用/AI/幂等跳过均不发 |
| `app/service.go` Deps | 加 `Realtime contracts.RealtimePublisher`（可选注入，nil 时静默跳过——既有测试 fake 不强制改） |

调用方（social/content/agent）**零改动**：它们经 `noticeLazy` 端口进入同一 `CreateNotification`。

### 2.5 `internal/modules/dm` — 私信事件发布点

| 文件 | 改动 |
| --- | --- |
| `app/message.go:14` `SendMessage` | 事务内拿到 recipient 后注册 `db.AfterCommit`，`PublishDM(peerID, convID)`；同时发给发送者自己（多标签页同步，未读数拉取消耗可忽略） |
| `app/message.go` 撤回用例 | 同模式发布 `PublishDM`（双方），撤回的可见性变化靠对话页局部刷新及时呈现 |
| `app/service.go` Deps | 加 `Realtime contracts.RealtimePublisher`（nil 容忍） |

### 2.6 `internal/modules/realtime` — SSE 通道模块（新目录）

- `app/service.go`：持有 `*eventbus.Broker`，提供 `Subscribe(ctx, userID) (<-chan eventbus.Event, cancel, error)`（含每用户连接计数）与 `Close()`。
- `http/handler.go`：`GET /api/v1/events` handler——
  - 响应头：`Content-Type: text/event-stream`、`Cache-Control: no-cache`、`X-Accel-Buffering: no`（防 nginx 默认缓冲吞流）、`Connection: keep-alive`；
  - 循环：select 订阅 channel / 25s 心跳 ticker（写 `: ping\n\n`）/ `r.Context().Done()`（客户端断开即 unsubscribe，无泄漏）；每事件写 `event: <kind>\ndata: {...}\n\n` 后 `Flush`；
  - `data` JSON 字段 `snake_case`、雪花 ID 字符串化（如 `{"conversation_id":"123"}`），与全站 API 约定一致；
  - 未登录由 Login 策略拦（401），超限 503。
- 无 domain/repo（无 SQL、无状态机）；守卫若有假设按报错最小处理（预期无）。

### 2.7 路由与生命周期

| 文件 | 改动 |
| --- | --- |
| `internal/transport/router/routes.go` | 注册 `GET /api/v1/events`（Policy: Login，handler 由 `router.Deps.Realtime` 注入）；`routes_test.go:12` 基线 132→133 |
| `internal/app/wire.go` | 构造 `eventbus.NewBroker()` → `realtimeAdapter` 注入 notice/dm/realtime 三处；`Deps` 加 `Realtime` handler 组 |
| `internal/app/app.go` | `newApp` 的 `onClose` 回调里加 `broker.Close()`——`App.Close()`（`app.go:104`）已支持外部回调，优雅关闭时先断全部 SSE 连接，`httpSrv.Shutdown(10s)` 不再被长连接拖满 |

### 2.8 明确不改

- `main.go`/`http.Server`：无 WriteTimeout，SSE 兼容，不动。
- `AccessLog`：SSE 连接在关闭时记一条访问日志（无时长字段）——v1 接受。
- 数据库：零迁移；`DATA_MODEL.md`/`migrations/README.md` 不动。
- 时间线/帖子流不做实时（后续扩展，同一总线加 Kind 即可）；AI/agent 不订阅；管理后台不加实时。

## 3. 前端改动

| 文件 | 改动 |
| --- | --- |
| `web/src/composables/realtime.ts`（新） | 模块级单例 EventSource(`/api/v1/events`)：`startRealtime()` / `stopRealtime()`；`onmessage` 按 `event` 字段分发——`notification` → 通知 store `refresh()`，`dm` → 私信 store `refresh()` + 通过简单回调注册表通知对话页（不引第三方事件库）；`onerror`/401 关闭连接不再自动重连（等下次登录态变化重启），轮询继续兜底 |
| `web/src/App.vue:27-31` | 登录 watcher 里在两个轮询启动旁启动 `startRealtime()`；登出/401 时 `stopRealtime()` |
| `web/src/views/MessageThreadView.vue:30` | 10s 会话内轮询保留；订阅 realtime 的 `dm` 事件，`conversation_id` 匹配时立即 `pollNew()`，实现秒级收信与撤回呈现 |

`document.hidden` 时 SSE 保持连接（服务端心跳检测死连接，成本可忽略）；现有两个 30s 轮询不动（降级兜底），后续可选在连接健康时拉长间隔——不在本次范围。

## 4. 测试

- `platform/db`：`AfterCommit` 表驱动——无事务立即执行、提交后按序执行、回滚不执行、嵌套事务在外层提交后执行、回调 ctx 不随请求取消（复用现有 `txBeginner` fake）。
- `platform/eventbus`：订阅/发布/缓冲满丢弃/每用户上限/全局上限/Close 广播/并发订阅与取消。
- `notice/app`：fake publisher 捕获——插入成功发布、幂等跳过不发布、接收者无效/AI 不发布；不在事务内时立即发布。
- `dm/app`：SendMessage/撤回发布给双方；策略拒绝不发布。
- `realtime/http`：`httptest.Server` 真流读取——响应头断言、心跳间隔、事件格式（ID 字符串化）、每用户第 4 条连接 503、`broker.Close()` 后连接结束；未登录 401。
- 集成测试（`internal/server`，testkit 需加流式读 helper）：`TestRealtimeIntegration`——A 连接 SSE → B 点赞 A 帖子 → A 超时窗内读到 `event: notification`；B 发私信 → A 收到 `event: dm`（conversation_id 为字符串）；游客连接 401；连接断开后重连成功。
- 路由基线 132→133；`go test ./...`、`go vet ./...`、`go test ./internal/architecture`、`cd web && npm run build`、`TIMELINE_INTEGRATION=1 go test -p 1 ./...` 全绿。

## 5. 文档同步（依赖序，均更新版本号/日期/文末变更记录）

1. `docs/REQUIREMENTS.md` 0.31.0：通知中心与私信章节改为「SSE 实时推送 + 轮询降级」；安全红线补「SSE 只推接收者私有提示信号、不推内容载荷」与单实例边界。
2. `docs/API.md` 0.12.0：新增 `GET /api/v1/events`（Login 策略、`text/event-stream`、事件种类 `notification`/`dm`、心跳与重连语义、无 CORS）。
3. `docs/ARCHITECTURE.md` 0.5.0：platform/eventbus、modules/realtime、`db.AfterCommit`、单实例边界与未来扩展路径（LISTEN/NOTIFY 或 Redis 换 Broker 实现）。
4. `docs/DEPLOYMENT.md` 0.4.5：反代注意事项——应用已设 `X-Accel-Buffering: no`，`proxy_read_timeout` 需大于 25s 心跳间隔，不支持多实例负载均衡下的 SSE。
5. `docs/PAGES_AND_PERMISSIONS.md` 0.1.26：通知中心/私信实时推送（Login）。
6. `docs/WORKFLOWS.md` 0.12.0：通知与私信流程补事件发布点（提交后发布）。
7. `docs/ACCEPTANCE.md` 0.8.0：新增 RT 组——RT-01 登录后实时收到通知红点、RT-02 私信秒级到达、RT-03 撤回及时呈现、RT-04 游客 401、RT-05 断网重连后数据正确、RT-06 服务重启后前端自动重连。
8. `docs/README.md` 0.1.18 与 `AGENTS.md` 0.3.0（§1 功能清单、§3 目录、§5 补 AfterCommit/eventbus 约定、§6 测试清单、§10 变更记录）。

## 6. 实施顺序

1. `platform/db` AfterCommit + 单测（独立可合入）
2. `platform/eventbus` + 单测
3. `contracts` 端口 + `app/adapters.go` 适配器
4. notice（Insert 返回值 + 发布点）与 dm（发布点）+ 单测
5. `modules/realtime` + 路由 + handler 测试 + 基线更新
6. `App.Close`/`wire.go` 生命周期接线
7. 前端 composable + App.vue/MessageThreadView 接线 + `npm run build`
8. testkit 流式 helper + `TestRealtimeIntegration`
9. 文档同步批次（§5 全部）

## 7. 明确不做（边界）

- 不推内容载荷、不做流内可见性过滤（信号语义从根上规避）。
- 不做时间线/帖子流/评论流的实时推送（后续按同一总线加 Kind）。
- 不做多实例广播（单实例前提写入文档；未来换 Broker 实现即可，端口抽象已留）。
- 不做 WebSocket 降级、不做 Last-Event-ID 离线补偿（重连全量 refresh）。
- 不加新环境变量/系统设置项（上限与心跳用常量）。
- 不缩短现有轮询（作为降级路径原样保留）。
