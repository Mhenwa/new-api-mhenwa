# New API Mhenwa 定制仓库

本仓库是 Mhenwa 中转站 New API 定制版的源码主目录。后续渠道状态页面、管理页、探测逻辑和其他定制功能都从这里修改、测试和构建；不要在旧的临时源码目录或 RC47 集成目录继续开发。

## 基线

- 当前定制版本：New API `v1.0.0-rc.44`
- 上游基线标签：`v1.0.0-rc.44`，上游提交 `633b4c5`
- 定制代码包含提交：`8f3d5c5`、`2127ebf`
- Mhenwa 基线标签：`mhenwa-rc44-channel-status-20261028`
- 部署镜像：`local/new-api:rc44-group-status-20261028-96a7b6b`
- 部署镜像 ID：`sha256:abd1980d6dcdfcc3254e2cd9bdf6f4938948618d499ebdd3cd8180ac22a28e5e`
- 部署源码归档 SHA256：`96a7b6b12b884f062c1f02f4f16d310bb978ba3883f967df8ee73d1ff6617f52`

建立本仓库时，已将当前 RC44 源码与实际部署构建冻结清单中的 2,648 个文件逐项比对，内容哈希全部一致。RC47 试验版不属于本仓库基线。

## 渠道状态功能

- 用户侧渠道状态页、管理设置页和历史观测柱状图。
- 监控指定分组和模型，优先用真实请求形成观测；空闲达到配置间隔后再补流式探测。
- 状态数据由 Redis 保存并通过 API 提供；按当前 RC44 代码中的实现工作。
- API Key 分组选择器展示分组状态、有效倍率和观测成功率，并提供状态页入口。
- 状态在弹窗打开时按 30 秒刷新，弹窗关闭后停止该查询。

## 开发和构建

后端使用仓库根目录的 Go module；前端使用 Bun。完整容器镜像通过仓库根目录的 Dockerfile 构建：

```powershell
docker build --platform linux/amd64 -t new-api-mhenwa:rc44 .
```

定向检查：

```powershell
go test ./pkg/channel_status ./controller
cd web
bun run test -- src/features/channel-status src/features/keys/components/__tests__/api-key-group-combobox.test.tsx src/features/keys/components/__tests__/api-keys-mutate-drawer.test.tsx
bun run typecheck
```

当前分支为 `main`。`upstream` 仅指向 New API 官方仓库；将来需要升级基线时，先在新分支评估上游变更，再合并回 `main`，不直接覆盖 Mhenwa 定制代码。构建或部署生产镜像前应先运行相关测试并核对目标版本。
