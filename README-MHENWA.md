# New API Mhenwa 定制仓库

本仓库是 Mhenwa 中转站 New API 定制版的源码仓库。后续渠道状态页面、管理页、探测逻辑和其他定制功能都从本仓库的分支及工作树修改、测试和构建；不要在旧的 `.tmp-newapi-rc44`、`.tmp-newapi-rc47` 临时目录继续开发。

## 基线

- 当前整合版本：New API 官方 `main` 截至 2026-11-09 的提交 `bd6bb53019fb7e21804b31ab7365c9b2097f4b6c`（包含 `v1.0.0-rc.49` 及之后的官方更新）。
- 本次定制基线：`d04056fdde6604d628517e9e5ba7b75ab98e3307`；本地构建版本标记为 `v1.0.0-rc.49-mhenwa.20261109`，不冒充精确的官方 RC49 发布构建。
- RC44 定制起点：上游标签 `v1.0.0-rc.44`，上游提交 `633b4c5`
- 定制代码包含提交：`8f3d5c5`、`2127ebf`
- Mhenwa 基线标签：`mhenwa-rc44-channel-status-20261028`
- RC44 已部署镜像：`local/new-api:rc44-group-status-20261028-96a7b6b`
- RC44 已部署镜像 ID：`sha256:abd1980d6dcdfcc3254e2cd9bdf6f4938948618d499ebdd3cd8180ac22a28e5e`
- RC44 部署源码归档 SHA256：`96a7b6b12b884f062c1f02f4f16d310bb978ba3883f967df8ee73d1ff6617f52`

建立本仓库时，已将 RC44 源码与实际部署构建冻结清单中的 2,648 个文件逐项比对，内容哈希全部一致。RC47 整合从该定制版本直接合并官方标签，未采用之前丢失部分状态功能的 RC47 试验分支；随后合入活跃请求租约、请求审计和失败 Responses 计费修复。

RC47 整合时的 `relay/responses_websocket.go` 冲突保留了 Mhenwa 的 `requestID` 观测关联，以及官方收集上游地址、生成错误信息和 WebSocket 写入预算的逻辑。

整合验证还补齐了两个测试环境：API Key 过期时间测试所需的路由上下文，以及 SQLite 测试连接的关闭清理；这两项均不修改产品行为。

2026-11-09 的官方更新合并保留全部 79 个原有定制文件。SSE scanner 采用官方精确匹配 `[DONE]` 的逻辑，并保留渠道状态观测与 relay audit；WebSocket 测试保留双方新增场景；七种语言合并双方翻译键。

失败 Responses 的既定规则继续保留：明确失败且缺少上游有效 usage 时，不以本地估算 token 向用户收费；上游有效 usage 的既有结算逻辑仍然单独验证。官方新增端到端测试中的 `failed-estimated-output` 预期按此定制规则调整，不更改生产计费实现。

并行开发请使用独立 Git 工作树，分支本身无法隔离同一目录中的未提交修改。升级代码不等于部署；生产运行版本以实际镜像 revision 和部署回执为准。

## 渠道状态功能

- 用户侧渠道状态页、管理设置页和历史观测柱状图。
- 监控指定分组和模型，优先用真实请求形成观测；空闲达到配置间隔后再补流式探测。
- 活跃请求按进程实例隔离，每 30 秒续租；停止续租满 2 分钟后，调度器自动回收遗留标记，恢复空闲探测资格。租约使用 Redis 时钟，持续续租的长请求会保留；混跑期间旧格式标记仍保留原有 24 小时宽限。
- 状态数据由 Redis 保存并通过 API 提供；保留 RC44 定制及后续活跃请求租约修复。
- 状态卡片展示有效倍率；API Key 分组选择器展示分组状态和观测成功率，并提供状态页入口。
- 状态在弹窗打开时按 30 秒刷新，弹窗关闭后停止该查询。

## 开发和构建

后端使用仓库根目录的 Go module；前端使用 Bun。完整容器镜像通过仓库根目录的 Dockerfile 构建：

```powershell
docker build --platform linux/amd64 -t new-api-mhenwa:rc49-mhenwa-20261109 .
```

定向检查：

```powershell
go test ./pkg/channel_status ./controller
cd web
bun run test -- src/features/channel-status src/features/keys/components/__tests__/api-key-group-combobox.test.tsx src/features/keys/components/__tests__/api-keys-mutate-drawer.test.tsx
bun run typecheck
```

RC47 历史整合分支为 `integrate/rc47-preserve-rc44-channel-status`；2026-11-09 升级分支为 `codex/upstream-20261109-preserve-custom`。`upstream` 指向 New API 官方仓库，`origin` 指向 Mhenwa 定制仓库；升级时先在独立工作树评估和合并上游变更，验证通过后再合并回 `main`。构建或部署生产镜像前应先运行相关测试并核对目标版本。

## 请求故障审计与上游对账

`codex/relay-request-audit` 在现有日志中增加关联事实，不改变重试、超时、断连、余额、预扣费或结算规则。默认启用；`RELAY_AUDIT_ENABLED=false` 可关闭。无需开启 `DEBUG`，也不修改数据库结构。

- 管理员消费/错误日志的 `other.admin_info.relay_audit` 保存生成日志时的快照；普通用户看不到此管理员字段。现有 `upstream_request_id` 继续供详情页显示和查询。
- 应用日志中以 `relay_audit ` 开头的 JSON 事件记录收到上游响应头（`upstream_headers`）、上游传输结束（`upstream_end`）、处理器退出（`request_end`）。使用本地 `request_id` 关联。消费日志可能早于最后一帧写出，最终传输情况以 `request_end` 为准；`upstream_end` 本身不表示模型成功完成。
- 每次 HTTP 调用或 WebSocket 握手保留独立 attempt、渠道、上游 origin、状态、精确 UTC 观察时间、连接/首包耗时、响应 ID 头白名单、响应对象 ID、流结束原因和协议结果。失败尝试不会被后续重试覆盖。HTTP/2 内部重放另记 `transport_writes`；它不代表已拿到每个内部重放的独立供应商 ID。
- ID 头白名单为 `X-Oneapi-Request-Id`、`X-Request-Id`、`Request-Id`、`OpenAI-Request-Id`、`X-Goog-Request-Id`、`Apim-Request-Id`、`X-Amzn-RequestId`、`X-Amz-Request-Id`、`X-Correlation-Id`。保留头名称和值；超过现有 128 字符列上限的 ID 只完整保存于审计 JSON，不截断，不写入该列。`resp_...` 等响应对象 ID 单独保存，不能冒充上游后台 UUID。
- 区分下游请求读取/取消、下游写入/flush、DNS、连接、TLS、上游请求写入、响应读取、本地流超时与处理器错误。清理关闭 body 后的读取错误单独标记。只记录错误类型和分类，不记录原始错误消息、URL 查询、认证头、Cookie、请求正文或模型输出。
- 字节数是应用实际读取/成功交给 writer 的正文数量，并非客户端收到的字节或网络抓包流量；上游请求字节数包含内部重放。WebSocket 帧仅对成功的 `WriteMessage` 计数，失败帧可能已部分传输，因此不推定其实际送达量。
- 每请求最多保存 32 个 attempt，每层最多 32 个观察事件，超出有 dropped 计数。仅从不超过 512 KiB 的 JSON/SSE 事件提取响应对象 ID、终止类型和 usage 对象存在标记，不留存内容。usage 对象存在、HTTP 200、`completed` 或本地结算均不是供应商扣费凭据。

普通 HTTP 的公共 `doRequest`、SSE scanner、Responses WebSocket 的逻辑请求和复用连接已接入。Responses WebSocket 同一连接的每条 `response.create` 有独立 `-ws-N` 本地 ID，复用握手不会伪造新的供应商 UUID；Realtime WebSocket 当前仅覆盖握手。绕过公共传输的 AWS SDK、个别专有适配器及插件请求不保证具有同样的完整上游证据。

需要上游随响应返回其后台可查询的 ID。供应商只在后台内部生成且不返回的 UUID 无法由本程序还原；需对方提供可关联字段。收到响应头前即失败也可能拿不到 ID。对账时同时使用 ID、渠道/账户、UTC 时间与请求协议，在上游账单中独立确认是否扣费。

### 入口 Nginx 配合（示例，尚未部署）

应用看到的下游可能是 A/HK 或 stream guard。`client_gone` 只能证明直接下游 context 被取消，不能独自证明顾客主动退出。要区分链路各跳，还需每个入口的 access/error 日志；极近事件的观察先后也不等于因果。

在 Nginx `http` 块定义专用格式：

```nginx
log_format newapi_relay_audit escape=json
    '{"time":"$time_iso8601","node":"$hostname",'
    '"edge_request_id":"$request_id","incoming_id":"$http_x_request_id",'
    '"app_request_id":"$upstream_http_x_oneapi_request_id",'
    '"connection":"$connection","connection_requests":"$connection_requests",'
    '"method":"$request_method","uri":"$uri","status":$status,'
    '"request_completion":"$request_completion","request_time":"$request_time",'
    '"request_length":"$request_length","bytes_sent":"$bytes_sent",'
    '"upstream_addr":"$upstream_addr","upstream_status":"$upstream_status",'
    '"upstream_connect_time":"$upstream_connect_time",'
    '"upstream_header_time":"$upstream_header_time",'
    '"upstream_response_time":"$upstream_response_time",'
    '"upstream_bytes_sent":"$upstream_bytes_sent",'
    '"upstream_bytes_received":"$upstream_bytes_received"}';
```

在对应代理 `location` 加入（保留该 location 已有的其他 `proxy_set_header`、流式和 Upgrade 配置）：

```nginx
proxy_set_header X-Request-Id $request_id;
access_log /var/log/nginx/newapi-relay-audit.log newapi_relay_audit;
```

每跳生成自己的 ID，记录传入 ID 与返回的应用 ID；应用审计中的 `edge_request_id_untrusted` 与最近一跳发送的 ID 对应。该字段仅用于关联，不用于认证。中间代理必须传递此头才能关联；未收到应用响应头时，仍可按逐跳 ID 追溯。Nginx WebSocket access 日志在连接关闭后生成，只描述连接；多条逻辑请求仍应查应用 `-ws-N` 记录。按磁盘空间配置轮转和保留期限，不开启包含请求正文的调试日志。

本地验证使用 `httptest` HTTP/HTTP2/WebSocket 服务，覆盖失败后重试不串 ID、握手成功/拒绝、请求体重放、真正上游读失败与 cleanup 区分、完成/取消竞态、WebSocket 逻辑请求隔离与末尾写失败、字节计数、flush/deadline、隐私过滤和停用开关。没有调用收费上游。
