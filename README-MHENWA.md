# New API Mhenwa 定制仓库

本仓库是 Mhenwa 中转站 New API 定制版的源码仓库。后续渠道状态页面、管理页、探测逻辑和其他定制功能都从本仓库的分支及工作树修改、测试和构建；不要在旧的 `.tmp-newapi-rc44`、`.tmp-newapi-rc47` 临时目录继续开发。

## 基线

- 当前整合版本：New API `v1.0.0-rc.47`，官方提交 `e835b1b`
- RC44 定制起点：上游标签 `v1.0.0-rc.44`，上游提交 `633b4c5`
- 定制代码包含提交：`8f3d5c5`、`2127ebf`
- Mhenwa 基线标签：`mhenwa-rc44-channel-status-20261028`
- RC44 已部署镜像：`local/new-api:rc44-group-status-20261028-96a7b6b`
- RC44 已部署镜像 ID：`sha256:abd1980d6dcdfcc3254e2cd9bdf6f4938948618d499ebdd3cd8180ac22a28e5e`
- RC44 部署源码归档 SHA256：`96a7b6b12b884f062c1f02f4f16d310bb978ba3883f967df8ee73d1ff6617f52`

建立本仓库时，已将 RC44 源码与实际部署构建冻结清单中的 2,648 个文件逐项比对，内容哈希全部一致。本次从该定制版本直接合并官方 RC47 标签，未采用之前丢失部分状态功能的 RC47 试验分支。

本次唯一文本冲突位于 `relay/responses_websocket.go`：保留 Mhenwa 的 `requestID` 观测关联，同时保留 RC47 在请求返回后收集上游地址、生成错误信息的逻辑。RC47 的 WebSocket 写入预算改动也保留。

整合验证还补齐了两个测试环境：API Key 过期时间测试所需的路由上下文，以及 SQLite 测试连接的关闭清理；这两项均不修改产品行为。

`VERSION` 固定为 `v1.0.0-rc.47`，使本仓库直接构建时也有明确版本；官方发布工作流原本会在 CI 中注入版本。

`main` 已整合 RC47，并合入 `codex/channel-status-active-lease` 的活跃请求租约修复。并行开发请使用独立 Git 工作树，分支本身无法隔离同一目录中的未提交修改。

## 渠道状态功能

- 用户侧渠道状态页、管理设置页和历史观测柱状图。
- 监控指定分组和模型，优先用真实请求形成观测；空闲达到配置间隔后再补流式探测。
- 活跃请求按进程实例隔离，每 30 秒续租；停止续租满 2 分钟后，调度器自动回收遗留标记，恢复空闲探测资格。租约使用 Redis 时钟，持续续租的长请求会保留；混跑期间旧格式标记仍保留原有 24 小时宽限。
- 状态数据由 Redis 保存并通过 API 提供；按当前 RC44 代码中的实现工作。
- 状态卡片展示有效倍率；API Key 分组选择器展示分组状态和观测成功率，并提供状态页入口。
- 状态在弹窗打开时按 30 秒刷新，弹窗关闭后停止该查询。

## 开发和构建

后端使用仓库根目录的 Go module；前端使用 Bun。完整容器镜像通过仓库根目录的 Dockerfile 构建：

```powershell
docker build --platform linux/amd64 -t new-api-mhenwa:rc47 .
```

定向检查：

```powershell
go test ./pkg/channel_status ./controller
cd web
bun run test -- src/features/channel-status src/features/keys/components/__tests__/api-key-group-combobox.test.tsx src/features/keys/components/__tests__/api-keys-mutate-drawer.test.tsx
bun run typecheck
```

RC47 整合分支为 `integrate/rc47-preserve-rc44-channel-status`。`upstream` 指向 New API 官方仓库；升级时先在新分支评估上游变更，验证通过后再合并回 `main`。构建或部署生产镜像前应先运行相关测试并核对目标版本。
