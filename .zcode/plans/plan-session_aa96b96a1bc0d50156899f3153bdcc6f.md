## 发帖表情选取功能 — 实施计划

### 一、分析结论（已核实）

- **后端零改动**：帖子正文存 `TEXT` 且无长度上限（仅请求体 1MiB 防御上限）；`internal/shared/richtext` 清洗只转义 `& < > " '`，无非 ASCII 过滤；提及正则仅匹配 ASCII 用户名、`#…#` 话题允许 emoji（现有语义）；pg_trgm 搜索对 UTF-8 安全；AI 发帖路径走同一清洗管线。emoji 插入后全链路天然兼容。
- **前端落点**：发帖编辑器是 `web/src/components/RichTextEditor.vue`（contenteditable + 工具栏，仅被 `PostComposer.vue` 使用）。按你的选择，本次覆盖**全部文本输入框**共 5 处：
  1. 发帖/编辑帖编辑器工具栏（`RichTextEditor.vue`）
  2. 首页快速发布 textarea（`HomeView.vue` ~182）
  3. 评论框 textarea（`PostDetailView.vue` ~566）
  4. 转发/引用弹窗 textarea（`RepostDialog.vue` ~139，注意它是 z-[60] 模态）
  5. 私信输入框 textarea（`MessageThreadView.vue` ~355）
- **文档红线**：`docs/DESIGN_SYSTEM.md` 第 268 行现写明「编辑框无表情」，本次为解除排除项的功能扩展，须同步文档。

### 二、选型：emoji-picker-element ^1.29

对比过的三个候选中选定 **emoji-picker-element**（v1.29.1，已查 registry + README）：

- **零运行时依赖**、TS 类型齐全、作者活跃维护（对比：vue3-emoji-picker 带 idb+popper 两个依赖、社区较小；picmo unpacked 765KB 更重）。
- **原生 emoji 渲染**，不下载图片/字体资源，无 CDN 依赖（符合图标/资源红线）。
- **内置「常用」分区**：点击自动计数，IndexedDB 持久化（满足「最近使用」要求，零额外代码）。
- **i18n 全量可译**：`i18n` 属性传入中文分类名（笑脸与情感/人物与身体/动物与自然/食物与饮料/旅行与地点/活动/物品/符号/旗帜）。
- **30+ CSS 自定义属性**（`--background`、`--border-color`、`--input-*` 等），可映射到 `tokens.css` 令牌，浅色/深色/EVA 主题自动跟随。
- 已知取舍：内置搜索框只匹配英文关键词/shortcodes（不支持中文搜索）；库不支持隐藏搜索框，保留作为顺带能力。

**集成方式**：不用模板标签 `<emoji-picker>`（可避免 vite `isCustomElement` 配置与 vue-tsc 模板校验问题），而是在封装组件内 `await import('emoji-picker-element')` 懒加载（首开才拉取，Vite 自动分包，主包不受影响）后 `new Picker({ locale: 'zh', i18n: zhI18n })` 命令式创建并挂载，`addEventListener('emoji-click')` 取 `event.detail.unicode`。

### 三、实施步骤（纯前端 + 文档）

**1. 依赖**：`web/` 下 `npm install emoji-picker-element`（唯一新依赖）。

**2. 图标**：`web/src/components/icons.ts` 的 `definitions` 新增 `smile`（24×24、stroke 1.8、圆头线框：圆脸 + 双眼 + 微笑弧线），`IconName` 类型自动收窄。

**3. 共享组件 `web/src/components/EmojiPicker.vue`**（新建）：
- Props：`disabled?`、`align?: 'left' | 'right'`（面板对齐侧）、`label?`（aria）；**`#trigger` 具名插槽**——各处按钮样式不同（编辑器 30px 幽灵钮 / 操作行小钮），由使用方自绘，组件只负责面板与开关逻辑。
- 面板：**fixed 定位**（编辑器容器有 `overflow-hidden`，absolute 会被裁剪；参照 RepostDialog 已有 fixed 先例），锚定触发按钮 `getBoundingClientRect()`，空间不足时向上翻，`z-[70]`（高于 RepostDialog 的 z-[60]）。`align` 决定左/右对齐。
- 交互：document 级 `pointerdown` 点外关闭（面板在 root 内，shadow DOM 重定向后的 target 仍被 `contains` 覆盖，无需 composedPath）；Esc 关闭并 **emit `open-change`**（供模态宿主协调）；document 级 `scroll`（capture）重算位置；首次打开才动态 import 并创建 Picker。
- 主题：面板容器以 Tailwind 任意属性注入 CSS 变量映射（如 `[--background:var(--surface)] [--border-color:var(--border)] [--input-font-color:var(--ink)] [--button-hover-background:var(--hover)]`），不写字面量颜色、不新增 scoped 样式块。
- 中文 i18n 对象内联在组件或 `web/src/components/emoji-i18n.ts`。

**4. 五处接入**：
- **`RichTextEditor.vue`**：工具栏在「插入链接」前新增「插入表情」按钮（样式与现有工具钮一致，`@mousedown.prevent`）；panel `select` 后 `editor.focus()` + `document.execCommand('insertText', false, emoji)` + `syncFromDom()`（与 `onPaste` 同款，保留撤销栈；打开面板时顺手保存 `getSelection()` range，插入前恢复，双保险）。
- **新增 `web/src/utils/emoji.ts`**：`insertAtSelection(textarea, text)` —— 记录 `selectionStart/End` → `setRangeText(text, …, 'end')` → `dispatchEvent(new Event('input', { bubbles: true }))`（触发 v-model 更新）→ `focus()` + `setSelectionRange` 复位光标。
- **`HomeView.vue`**：快速发布操作行（附件按钮旁）加小号表情钮 → `insertAtSelection`。
- **`PostDetailView.vue`**：评论框操作行加表情钮 → 同上。
- **`RepostDialog.vue`**：引用 textarea 旁加表情钮；**Esc 协调**——对话框现有 document 级 Esc 关闭逻辑改为「emoji 面板打开时忽略 Esc」（由 `open-change` 驱动），避免一次 Esc 同时关面板和弹窗。
- **`MessageThreadView.vue`**：发送按钮旁加表情钮 → 同上（DM 后端 2000 rune 上限行为不变，emoji 计 1 rune）。

**5.（建议）后端保险用例**：`internal/shared/richtext` 现有测试表追加 emoji 用例（单 emoji 😀、ZWJ 组合 👨‍👩‍👧、emoji 夹 `#话题#`），断言清洗后原样保留——把「后端安全」结论固化进测试。不改任何实现代码。

**6. 文档同步**（按 AGENTS.md §7 顺序）：
- `docs/REQUIREMENTS.md`：发帖/评论/私信/转发引用输入能力补表情选取（0.28.0 → 0.29.0 + 变更记录）。
- `docs/PAGES_AND_PERMISSIONS.md`、`docs/WORKFLOWS.md`：对应页面/流程补一句能力描述。
- `docs/DESIGN_SYSTEM.md`：删除第 268 行「编辑框无表情」排除声明，新增表情选取器组件规范（触发钮、fixed 面板、令牌映射、第三方组件说明）（0.3.0 → 0.4.0）。
- `docs/ACCEPTANCE.md`：新增 EMOJI 组用例（编辑器插入并发帖可见、快速发布/评论/私信/引用插入、深色与 EVA 主题面板外观、弹窗层级、Esc 行为）。
- `docs/README.md`（索引版本）与根 `AGENTS.md`（§1 功能清单补一句、版本 0.2.8 + 变更记录）。
- `docs/API.md`、`docs/DATA_MODEL.md` 无影响，不动。

### 四、验证

- `cd web && npm run build`（vue-tsc 严格检查 + Vite 构建，前端唯一门禁）。
- `go test ./...`（richtext 新用例；其余无改动应全绿）。
- 手动冒烟（`go run ./cmd/server` + `npm run dev`）：五处插入 emoji → 发布后详情/时间线渲染正常；切换深色/EVA 主题面板配色正确；RepostDialog 中面板层级与 Esc 正确；首开懒加载分包生效。

### 五、风险与说明

- 原生 emoji 渲染依赖系统字体，过旧系统会缺字（库自动隐藏不支持的字符），可接受。
- 「常用」数据存浏览器 IndexedDB（库自管理），设备本地、随源共享，清除站点数据即重置；隐私模式下降级为不持久化，不影响功能。
- 搜索框仅英文关键词有效，属已知取舍；未来要中文搜索可换 picmo + emojibase 中文数据包，面板封装隔离了这层替换成本。
