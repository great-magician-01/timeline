# Timeline 前端

基于 Vue 3、TypeScript、Vite 和 Tailwind CSS 4，构建产物由 Go 服务嵌入并托管。

## 常用命令

```bash
npm ci
npm run dev       # Vite 开发服务器，/api 等路径代理到 localhost:5070
npm test          # Vitest 单元测试
npm run build     # vue-tsc 类型检查并构建到 dist
npm run preview   # 预览构建产物
```

开发时需从仓库根目录另外启动 `go run ./cmd/server`。要让 Go 二进制托管最新前端，先执行 `npm run build`，再重新编译并启动服务。

## 样式约定

- `src/styles/app.css`：唯一全局样式入口，接入 Tailwind theme/utilities，不启用 Preflight。
- `src/styles/tokens.css`：明暗主题和设计颜色的唯一来源；组件不得新增字面量颜色。
- `src/styles/ui.ts`：跨组件复用的完整静态 utility 组合。
- Vue 模板和静态 TS 字符串使用 Tailwind utility，不新增 SFC `scoped` 样式块，也不运行时拼接类名片段。
- `shell-down:` 对应设计要求的 `max-width: 880px`；主题仍通过 `<html data-theme>` 和 `localStorage` 的 `tl-theme` 切换。

完整设计基线见 [`docs/DESIGN_SYSTEM.md`](../docs/DESIGN_SYSTEM.md)。
