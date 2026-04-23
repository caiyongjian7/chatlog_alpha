# chatlog_alpha

微信 4.x 聊天记录本地查询工具，支持 `macOS` 与 `Windows`。

> 警告：Windows 平台当前未完成实机测试，可能无法正常运行。请优先在测试环境验证后再用于正式数据。

## 更新日志（近期）

### 2026-04-23

- 新增“实验性功能”页面，承载 `GLM 语义检索与重排序` 全量入口（配置、连通性测试、索引管理、语义搜索、会话问答、主题趋势、联系人画像）。
- 语义能力改为索引就绪后可用；前端动作按钮会按索引状态自动禁用/启用。
- 安全调整：`semantic api_key` 无默认值，`GET /api/v1/semantic/config` 不回显真实 key，仅返回 `has_api_key`；保存时留空会保留已存 key。
- 索引状态增强：新增 `last_incremental_at / last_incremental_added / last_incremental_error` 与 `last_rerank_at / last_rerank_applied / last_rerank_error`。
- 增量索引机制升级：除了搜索触发外，服务端新增后台自动监控（检测会话 `NOrder` 变化时自动触发增量构建）。
- 主题趋势与联系人画像升级为图表展示（时间窗支持：今天、近 7 天、近 1 月、全部）。
- 历史/检索接口口径修正：`history/search` 新增并统一 `total_count + limit + offset`，过滤改为“先过滤后分页”，修复小时过滤统计不一致问题。

### 2026-04-22

- 微信关键词推送支持 Hermes Agent Weixin Channel，前端可读取/保存 Hermes 微信配置并做配置可用性检查。
- 新增 Hermes Agent QQ 推送渠道，支持读取/保存 `QQ_APP_ID`、`QQ_CLIENT_SECRET`、`QQBOT_HOME_CHANNEL` 并通过 Hermes `QQAdapter` 发送文本与媒体。
- 推送页面能力整合：支持关键词推送、实时全部转发、指定联系人转发、指定群聊转发，并展示各推送方式结果。

### 2026-04-21

- 朋友圈媒体代理解密增强：对齐参考实现修复 `keystream reverse` 与解密校验策略，降低“返回成功但媒体不可播放”概率。
- 新增官方 WASM 优先解密链路（失败回退本地实现），提升视频号样本兼容性。

## 平台与能力

- 数据库 Key 获取：内置扫描流程（兼容 `all_keys.json`）
- 图片 Key 获取：内置扫描与校验流程
- 数据查询：HTTP + MCP（wx-cli 风格接口）
- 数据源：内置 `wcdb_api` 兼容查询链路（非外部 DLL）
- 全局搜索：支持跨所有数据库快速搜索 / 深度搜索
- 朋友圈媒体：支持图片、视频、实况图代理解密
- 关键词推送：前端/TUI 同步配置，支持 MCP 主动推送与 POST 通知
  - 也支持推送到 Hermes Agent 的微信 home channel
  - 也支持推送到 Hermes Agent 的 QQ home channel
- 推送事件：支持持久化保存、启动恢复与一键清理


## GitHub 自动构建产物

当前 `Release` 工作流会自动构建以下平台与架构：

- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

发布时会在 `dist/` 生成对应压缩包与二进制文件（Windows 为 `.exe`）。

## 快速开始

### 本地运行

```bash
go run .
```

或：

```bash
go build -o chatlog ./cmd/chatlog
./chatlog
```

### 常用命令（CLI）

```bash
chatlog http list
```

```bash
chatlog http call --endpoint history --query chat=<会话ID> --query limit=100 --query format=json
```

### HTTP 接口命令行调用（全接口）

```bash
# 列出所有可调用 HTTP 接口别名
chatlog http list

# 按别名调用（示例：聊天记录）
chatlog http call --endpoint history --query chat=<会话ID> --query limit=100 --query format=json

# 按原始路径调用（示例：执行 SQL）
chatlog http call --path /api/v1/db/query --query group=message --query file=message_0.db --query sql='select count(*) c from MSG'

# 全局搜索（quick / deep）
chatlog http call --path /api/v1/db/search --query keyword=朋友圈 --query mode=deep --query limit=100

# 媒体接口（模板路径参数）
chatlog http call --endpoint image --path-param key=<image_key>

# 朋友圈媒体代理解密
chatlog http call --path /api/v1/sns/media/proxy --query key=<enc_key> --query url='<sns_media_url>'
```

Skill 文档：`skills/chatlog-http-cli/SKILL.md`

## macOS 权限说明（务必阅读）

### 1) 推荐用 `sudo` 运行

macOS 内存读取依赖 `task_for_pid`，建议以 root 启动程序。

### 2) 若使用 setuid 方案（可执行文件自动 root）

请在每次重新编译后执行：

```bash
BIN_PATH="/你的实际路径/chatlog"
sudo chown root:wheel "$BIN_PATH"
sudo chmod 4755 "$BIN_PATH"
ls -l "$BIN_PATH"
```

看到 `-rwsr-xr-x` 表示生效。

### 3) SIP

在多数机器上，仅 root 仍可能不足以读取微信进程内存。  
如需稳定扫描 Key，通常还需要关闭 SIP（System Integrity Protection）。

## Windows 权限说明

- 请使用“管理员权限”启动程序，否则可能无法读取微信进程内存。

## HTTP 接口（摘要）

基础：

- `GET /health`
- `GET /api/v1/ping`

媒体：

- `GET /image/*key`
- `GET /video/*key`
- `GET /file/*key`
- `GET /voice/*key`
- `GET /data/*path`
- `GET /api/v1/sns/media/proxy`

查询（wx-cli 风格）：

- `GET /api/v1/sessions`
- `GET /api/v1/history`
- `GET /api/v1/search`
- `GET /api/v1/unread`
- `GET /api/v1/members`
- `GET /api/v1/new_messages`
- `GET /api/v1/stats`
- `GET /api/v1/favorites`
- `GET /api/v1/sns_notifications`
- `GET /api/v1/sns_feed`
- `GET /api/v1/sns_search`
- `GET /api/v1/contacts`
- `GET /api/v1/chatrooms`

数据库调试：

- `GET /api/v1/db`
- `GET /api/v1/db/search`
- `GET /api/v1/db/tables`
- `GET /api/v1/db/data`
- `GET /api/v1/db/query`
- `POST /api/v1/cache/clear`

语义检索（GLM Embedding + Rerank）：

- `GET /api/v1/semantic/config`
- `POST /api/v1/semantic/config`
- `POST /api/v1/semantic/test`
- `GET /api/v1/semantic/index/status`
- `POST /api/v1/semantic/index/rebuild`
- `POST /api/v1/semantic/index/clear`
- `GET /api/v1/semantic/search`
- `POST /api/v1/semantic/qa`
- `GET /api/v1/semantic/topics`
- `GET /api/v1/semantic/profiles`

关键词推送（前端“关键词推送”页面与 TUI 同步）：

- `GET /api/v1/hook/config`
- `POST /api/v1/hook/config`
- `GET /api/v1/hook/status`
- `GET /api/v1/hook/events`
- `POST /api/v1/hook/events/clear`
- `GET /api/v1/hook/stream`（SSE 实时事件）
- `GET /api/v1/hook/hermes/weixin`
- `POST /api/v1/hook/hermes/weixin`
- `GET /api/v1/hook/hermes/qq`
- `POST /api/v1/hook/hermes/qq`

输出格式：

- 默认 `YAML`
- 可选 `JSON`（`format=json`）

查询接口口径（最新）：

- `GET /api/v1/history`
  - 新增可选过滤参数：`hour`（0-23）、`is_self`（`1/0`）、`sub_type`、`has_media`（`1/0`）
  - `hour` 不传或留空表示“全部小时”
  - 过滤顺序为“先过滤，再分页”，避免先 `limit` 截断导致统计错位
  - 返回字段包含：
    - `total_count`：过滤后的总条数
    - `count`：当前页条数
    - `limit` / `offset`
- `GET /api/v1/search`
  - 支持 `offset`
  - 结果流程为“先聚合排序，再分页”
  - 返回字段包含 `total_count` / `count` / `limit` / `offset`
- `GET /api/v1/stats`
  - 现在为实时计算（已移除服务端缓存）
  - 返回口径字段：`query_since` / `query_until` / `query_range_label`
  - 群聊 `active_senders` 为真实去重发言人数（非 TopN 长度）
- `GET /api/v1/contacts`
  - 默认 `limit=500`
  - 支持 `is_friend` 筛选（`1/0/true/false`）
- `GET /api/v1/chatrooms`
  - 默认 `limit=500`

YAML 可读性优化：

- `history/search/stats` 已改为结构化输出（固定字段顺序，避免 map 随机顺序）
- 合并转发/笔记中的媒体内容，当 host 缺失时不再生成 `http:///...` 空链接，而是回退为文本占位

## 全局搜索

- 前端页面：访问根页面 `http://127.0.0.1:5030/`，切换到“全局搜索”标签页。
- 接口：`GET /api/v1/db/search`
- 参数：
  - `keyword`：搜索关键词
  - `mode`：`quick` 或 `deep`
  - `limit`：结果总数上限，默认 `100`，最大 `500`
- 返回内容包含命中的：
  - 数据库组
  - 数据库文件
  - 表名
  - 列名
  - 行标识
  - 命中内容预览

说明：

- `quick`：优先性能，适合前端实时搜索。
- `deep`：覆盖更全，会额外尝试解析压缩消息体和部分二进制字段，速度更慢。

## GLM 语义能力（Embedding-3 + Rerank）

前端入口：

- 根页面 `http://127.0.0.1:5030/` 的“实验性功能”标签页中使用“GLM 语义检索与重排序”面板。

配置与测试：

- 支持配置 `api_key`、`base_url`、`embedding_model`、`rerank_model`、`embedding_dimension`、`recall_k`、`top_n`、`similarity_threshold`。
- 支持配置 `index_workers`（并发索引线程数，默认 4，最大 32）。
- 语义能力属于实验性固定能力（前端不可关闭）；仅在“连通性通过 + 索引就绪”后可使用检索/问答等动作。
- `api_key` 无默认值；`GET /api/v1/semantic/config` 不回显真实 key，仅返回 `has_api_key` 标记。
- `POST /api/v1/semantic/config` 时若 `api_key` 留空，将保留已保存 key（不会清空）。
- 默认模型：
  - embedding：`embedding-3`
  - rerank：`rerank`

向量索引：

- 实时状态：`GET /api/v1/semantic/index/status`
  - 状态字段包含：
    - 基础构建状态：`indexed_count` / `processed` / `failed` / `pending` / `total` / `progress_pct`
    - 增量状态：`last_incremental_at` / `last_incremental_added` / `last_incremental_error`
    - 重排序状态：`last_rerank_at` / `last_rerank_applied` / `last_rerank_error`
- 重建索引：`POST /api/v1/semantic/index/rebuild`
  - `reset=0`（默认）：断点续传，继续上次中断进度
  - `reset=1`：从头重建（先清空索引）
- 删除索引：`POST /api/v1/semantic/index/clear`
- 本地索引库路径：`<WorkDir>/.chatlog_semantic/vector_index.db`

已接入能力（6项）：

1. 语义全局检索：`GET /api/v1/semantic/search?query=...&chat=...`
2. 检索精排：`semantic/search` 默认开启 rerank（可配置关闭）
3. 语义告警触发：关键词钩子在字面未命中时，会按配置尝试语义匹配（`enable_semantic_push`）
4. 会话级问答（RAG 检索证据）：`POST /api/v1/semantic/qa`
5. 主题聚类/趋势（轻量统计版）：`GET /api/v1/semantic/topics`
6. 联系人/发送者语义画像（关键词聚合）：`GET /api/v1/semantic/profiles`

说明：

- 当前 5/6 项以“可追溯证据检索 + 轻量主题词统计”为主，适合先在线验证；后续可替换为更强聚类算法或 LLM 生成摘要。
- `realtime_index` 开启时，服务运行期间会根据会话 `NOrder` 变化自动触发增量建索引；语义检索前也会再做一次兜底增量。

## 朋友圈媒体代理解密

- 接口：`GET /api/v1/sns/media/proxy`
- 典型参数：
  - `url`：朋友圈图片 / 视频 / 实况图资源地址
  - `key`：对应消息 XML 中的 `<enc key="...">`
- 行为：
  - 图片：优先按 `reversed` keystream 解密，失败再尝试 `raw`
  - 视频：按前 `128 KB` 做 XOR 解密，并使用 `reversed` keystream
  - 文件头校验优先，不会只因为响应头里的 `Content-Type` 看起来像图片/视频就跳过解密
  - 优先使用微信官方 `wasm_video_decode.wasm/js` 生成 keystream，失败时回退到本地 Go 实现
- `sns_feed` / `sns_search` 返回的 `media_list` 已带代理地址，可直接访问。

示例：

```bash
curl "http://127.0.0.1:5030/api/v1/sns_feed?limit=5"
curl "http://127.0.0.1:5030/api/v1/sns/media/proxy?key=2503144471&url=https%3A%2F%2F..."
```

自动补 key：

- 如果 `sns_feed` / `sns_search` 已经解析过对应朋友圈消息，服务端会缓存媒体 `url -> key` 映射。
- 之后请求 `/api/v1/sns/media/proxy?key=0&url=...` 时，会优先尝试从缓存里自动补上真实 key。
- 如果该消息从未被解析过，且请求里又没有提供正确 `key`，则无法解密。

运行前提：

- 若要使用官方 WASM 解密路径，运行环境需要安装 `node`。
- 没有 `node` 时，程序会自动回退到内置 Go 实现，但兼容性可能略差。

## 关键词推送与持久化

- 前端页面：访问根页面 `http://127.0.0.1:5030/`，切换到“关键词推送”标签页。
- 配置项与 TUI 一致：
  - `keywords`（多个用 `｜` 分隔）
  - `notify_mode`（`mcp` / `post` / `both` / `weixin` / `qq` / `all`，也支持 `mcp,weixin`、`mcp,qq` 这类组合值）
  - `post_url`
  - `before_count` / `after_count`
- MCP 主动推送方法名：`notifications/chatlog/keyword_hit`
- 前端“关键词推送”页面会展示所有触发事件，不受 `notify_mode` 影响。
- Weixin Channel 推送：
  - 自动读取 Hermes Agent 的 `HERMES_HOME` 或默认 `~/.hermes`
  - 优先从 `.env` 读取 `WEIXIN_HOME_CHANNEL`、`WEIXIN_ACCOUNT_ID`、`WEIXIN_TOKEN`、`WEIXIN_BASE_URL`
  - 同时支持读取 `config.yaml` 的 `platforms.weixin.token`
  - 若 `.env` 未提供 token / base_url，会继续读取 `weixin/accounts/<account_id>.json`
  - 也会尝试读取 `config.yaml` 的 `platforms.weixin.extra`（如 `account_id/token/base_url/cdn_base_url`）
  - 前端可直接读取并修改上述微信配置，保存时会写回 Hermes Home 下的 `.env`
  - 启用 `weixin` 模式时，会校验 Hermes Agent 已安装，且微信渠道已配置完成
  - iLink 接口限制提醒：
    - 该接口存在会话态限制，长时间未交互后，主动推送可能被拒绝或无效。
    - 经验值：先主动给 `clawbot` 发送一条消息后，通常可连续主动推送约 `10` 次（实际次数会随账号状态与接口策略波动）。
- QQ Channel 推送：
  - 自动读取 Hermes Agent 的 `HERMES_HOME` 或默认 `~/.hermes`
  - 优先从 `.env` 读取 `QQ_APP_ID`、`QQ_CLIENT_SECRET`、`QQBOT_HOME_CHANNEL`、`QQBOT_HOME_CHANNEL_NAME`
  - 也支持读取 `config.yaml` 的 `platforms.qqbot.extra.app_id/client_secret` 与 `platforms.qqbot.home_channel`
  - 兼容读取 `config.yaml` 的 `platforms.qq` 段
  - 前端可直接读取并修改上述 QQ 配置，保存时会写回 Hermes Home 下的 `.env`
  - 启用 `qq` 模式时，会校验 Hermes Agent 已安装，且 QQ 渠道已配置完成
  - home channel 默认按私聊处理；若要主动推送到群聊或频道，请使用 `group:group_openid` 或 `channel:channel_id` 前缀
- 事件持久化文件：
  - 优先：`<DataDir>/chatlog_hook_events.json`
  - 回退：`<WorkDir>/chatlog_hook_events.json`
- 清理方式：
  - 前端“清空事件”按钮
  - 或调用 `POST /api/v1/hook/events/clear`

## MCP

端点：

- `ANY /mcp`
- `ANY /mcp/`
- `ANY /sse`
- `ANY /message`

### Hermes Agent 接入

本项目可作为 Hermes 的 HTTP MCP Server 使用。

1. 先确保 chatlog HTTP 服务已启动（默认 `127.0.0.1:5030`）。

2. 在 `~/.hermes/config.yaml` 增加 MCP 配置：

```yaml
mcp_servers:
  chatlog:
    url: "http://127.0.0.1:5030/mcp"
    enabled: true
    connect_timeout: 60
    timeout: 120
    tools:
      resources: false
      prompts: false
```

3. 或使用 Hermes CLI 直接添加：

```bash
hermes mcp add chatlog --url http://127.0.0.1:5030/mcp
hermes mcp test chatlog
```

4. 在 Hermes 会话中执行：

```text
/reload-mcp
```

加载后，工具名称会以 `mcp_chatlog_` 前缀出现。

## 安全与隐私

- 所有处理在本地完成
- 请妥善保管解密数据与密钥文件

## 免责声明

详见 [DISCLAIMER.md](./DISCLAIMER.md)
