# 个人主页横幅图（banner）实现计划

## 方案概述

完全复刻头像（avatar）既有链路：banner 是 `attachments` 表中 `purpose='banner'` 的图片附件，通过现有 `POST /attachments` 上传，media 在同一事务内经 `contracts.UserWriter` 端口把 URL 写回 `users.banner_url` / `users.banner_attachment_id`（users 表仍由 auth 独有，符合 ARCHITECTURE §4 数据所有权）；banner 附件**公开可读**（与头像一致，主页横幅本就是公开展示内容）；服务端**不做裁剪**，前端用 `object-cover` 自适应；支持**移除横幅**（用户已确认）。

## 1. 迁移 `migrations/0024_user_banner.sql`

```sql
ALTER TABLE "user".users ADD COLUMN banner_url TEXT NOT NULL DEFAULT '';
ALTER TABLE "user".users ADD COLUMN banner_attachment_id BIGINT;
ALTER TABLE timeline.attachments DROP CONSTRAINT attachments_purpose_check;
ALTER TABLE timeline.attachments ADD CONSTRAINT attachments_purpose_check CHECK (purpose IN ('post', 'avatar', 'banner'));
```

- 字段/约束名沿用 0003/0012 惯例；**不建** `banner_attachment_id` 索引（无任何等值查询方；0012 头像索引是专供清理侧查询的，SQL 最简原则）。
- 头注释说明：清理候选 SQL 固定 `purpose='post'`（`media/repo/attachment.go:151`），banner 附件天然不受临时附件清理影响，无需回填、无需排除参数——**清理链路零改动**。

## 2. 后端改动（按依赖序）

| 文件 | 改动 |
| --- | --- |
| `internal/shared/model/model.go` | `User` 加 `BannerURL string`（镜像 `AvatarURL`；附件 ID 不进共享模型，与头像一致） |
| `internal/modules/media/domain/attachment.go` | 加 `PurposeBanner = "banner"`；`NormalizePurpose` 放行 banner，错误文案同步（“purpose 只能是 post、avatar 或 banner”） |
| `internal/shared/contracts/auth.go` | 加 `BannerUpdateInput{UserID, AttachmentID, BannerURL, At}`；`UserWriter` 加 `SetBanner` |
| `internal/modules/auth/repo/user_repo.go` | `userColumns`/`scanUser`/`Insert` 加 `banner_url`；新增 `UpdateBanner(ctx, id, attachmentID *int64, bannerURL string, at)`（镜像 `UpdateAvatar:178`） |
| `internal/modules/auth/app/user_admin.go` | `Service.SetBanner`（镜像 `SetAvatar:197`）；新增 `Service.ClearBanner`（attachmentID=nil、bannerURL=''，供移除用） |
| `internal/modules/media/app/upload.go` | 头像分支（:214-223）旁加 banner 分支：`purpose==banner` 时校验 `kind==KindImage`（非图片 400 `invalid_request`），同事务调 `s.auth.SetBanner` 写回 |
| `internal/modules/auth/app/`（profile 用例）+ `internal/modules/auth/http/` | 新增移除横幅用例与 `DeleteBanner` handler（`POST /users/me/banner/delete`，返回 userEnvelope 便于前端直接同步） |
| `internal/transport/router/routes.go` | 注册上述路由（HumanWrite）；`routes_test.go` `baselineRouteCount` 131→132 |
| `internal/modules/media/app/read.go` | `requireAttachmentAccess` 放行 `purpose='banner'` 公开可读（:49，注释同步） |
| `internal/shared/view/view.go` | `UserResponse` 加 `BannerURL`（json `banner_url`，恒返回、空串=未设置）；`PublicUserResponse` 填充，`PrivateUserResponse` 自动继承 |

无需改动：媒体清理（见上）、admin 附件列表（purpose 为字符串透传，验证即可）、`UserBrief`（列表摘要不带横幅）。

## 3. 测试

- 更新实现 `contracts.AuthService/UserWriter` 的测试 fake（`media/app/helpers_extra_test.go`、`media/http/fakes_extra_test.go`、`admin/app/service_test.go`、`admin/http/fakes_test.go`）补 `SetBanner` stub。
- domain：`NormalizePurpose` 表驱动用例加 banner（含非法值文案断言）。
- media/app：上传 purpose=banner 成功写回（fake 捕获调用）、替换覆盖、非图片拒绝。
- auth/app + auth/http：`SetBanner`/`ClearBanner` 契约用例、`DeleteBanner` handler 用例（镜像既有 avatar/profile 用例）。
- 集成测试：`internal/server` 新增 `TestBannerIntegration`（镜像 `TestBookmarkIntegration` 组织方式）：上传→`GET /users/:username` 含 `banner_url`→游客读附件 200（公开性）→替换→移除后回空串→非图片 400→停用账号禁止上传。
- 验证：`go test ./...`、`go vet ./...`、`cd web && npm run build`、`TIMELINE_INTEGRATION=1 go test -p 1 ./...`（含在开发库应用 0024）。

## 4. 前端改动

- `web/src/api/types.ts`：`PublicUser` 加 `banner_url: string`（恒返回，空串=未设置）。
- `web/src/api/media.ts`：`UploadPurpose` 加 `'banner'`；新增 `updateBanner(file)`（镜像 `updateAvatar`）。
- `web/src/api/users.ts`：新增 `deleteBanner()` → `POST /users/me/banner/delete`。
- `web/src/views/settings/SettingsProfileView.vue`：仿「头像」卡片新增「主页横幅」卡片——横幅预览（object-cover）+ 无横幅占位、「更换横幅」隐藏 file input（accept="image/*"、前端校验 image/*）、「移除横幅」按钮（无横幅时禁用）；成功后 `me()` + `setCurrentUser` 同步全站。
- `web/src/views/UserProfileView.vue`：资料卡（:296-366）顶部加横幅区——卡片结构调整为「横幅全宽区 + 内容区 cardPad」：有横幅时 `<img>` object-cover（如高约 h-32~40，880px 断点下略降），无横幅或图片加载失败时用现有 `--media-bg` 令牌占位；样式只用现有令牌/utility，不写字面量颜色、不加 scoped 样式。

## 5. 文档同步（依赖顺序，均更新版本号/日期/文末变更记录）

1. `docs/REQUIREMENTS.md` 0.29.0：§5 用户资料条目扩写（昵称、头像、主页横幅和简介；横幅公开可读）。
2. `migrations/README.md`：概览表加 0024。
3. `docs/DATA_MODEL.md` 0.13.0：§6.1 users 加两列、§6.9 attachments purpose 枚举、§4.5 迁移说明。
4. `docs/ARCHITECTURE.md` 0.4.3：§4 数据所有权补 banner 经 auth 端口写回、§9 迁移状态到 0024。
5. `docs/API.md` 0.11.0：§2.8 user 对象加 `banner_url`、§4 加 `POST /users/me/banner/delete` 与 `GET /users/:username` 说明、§11 purpose 加 banner 与“横幅附件公开可读、上传后自动写回”。
6. `docs/PAGES_AND_PERMISSIONS.md` 0.1.24：P-09 个人主页展示横幅、U-08 资料设置加横幅编辑。
7. `docs/ACCEPTANCE.md` 0.6.0：MEDIA 组追加 MEDIA-07/08（横幅上传展示与公开读取、移除/替换/非图片拒绝/停用拒绝）。
8. `docs/DESIGN_SYSTEM.md` 0.4.0：§9 个人主页头部横幅结构要点、§10 审计表加行。
9. `docs/README.md` 0.1.16 与 `AGENTS.md` 0.2.8（§1 功能清单、§6 测试清单、§10 变更记录）。
10. 顺带把 API/DATA_MODEL/PAGES/ACCEPTANCE/ARCHITECTURE 头部滞留的“需求基线 0.27.0”拉齐到 0.29.0。

## 6. 明确不做（记录为边界）

- 服务端裁剪或固定宽高比校验（前端 object-cover 自适应；复用全局单文件大小/配额限制）。
- `UserBrief` 不带 banner_url；帖子列表/导航不出横幅。
- AI 账号无上传入口（HumanWrite 策略天然隔离），管理后台不加 AI 横幅管理（如有需要另立需求）。
- 换横幅后旧附件不失效（与头像语义一致：仅失去引用，配额口径不变）。