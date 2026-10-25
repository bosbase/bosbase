# BosBase MCP 使用指南

> English version: [mcp-guide.md](./mcp-guide.md)

BosBase 内置远程 MCP（Model Context Protocol）服务。AI（Claude Code、Claude Desktop、Cursor、自研 Agent 等）带上 BosBase 的 token 连接后，可以：

- **长期记忆**：把记忆存进 PostgreSQL + pgvector，按项目区分，支持语义检索和增删改查
- **知识库（类 Obsidian）**：Markdown 笔记、文件夹、`[[双链]]`、反向链接、`#标签`、frontmatter 属性、日记、改名自动更新链接、版本历史，知识点之间可以做图查询
- **记得自己做过什么**：每次工具调用都会记录，新会话开始时自动带上最近的操作摘要
- **对象存储**：配置多个 S3 兼容存储（AWS S3、Cloudflare R2、Google Cloud Storage、MinIO、Backblaze B2…），上传图片、视频等文件，上传历史记在 Postgres
- **大模型中转**：配置多个大模型渠道和 key（OpenAI 兼容 / Anthropic），通过 BosBase 调用，自动统计用量
- **错误诊断**（超级用户）：健康检查、错误日志查询（数据库 + 磁盘文件）、生成错误报告
- **调用全部 HTTP API**：集合、记录、认证、文件、批量、GraphQL、SQL、设置、日志、备份等

所有相关数据表都由系统迁移在**首次启动时自动创建**，不需要手动建表。

---

## 1. 准备

### 1.1 启动 BosBase

```sh
export SASSPB_POSTGRES_URL="postgres://user:pass@host:5432/db?sslmode=disable"   # 需要已安装 pgvector
export BS_ENCRYPTION_KEY="<32 位随机字符串>"                                       # 建议设置：用于加密存储的密钥

./bosbase serve
```

`BS_ENCRYPTION_KEY` 必须正好 32 个字符。没有设置或格式不对时，BosBase 照常运行，密钥改为不加密保存（会打印警告，`diagnostics_health` 也会提示）。

然后由超级用户配置向量模型供应商（无需重启，见[第 4 节](#4-向量模型供应商超级用户)）。没有配置时，记忆仍然可以保存，检索会退化为关键词全文检索。

### 1.2 获取 token

```sh
# 普通用户
curl -X POST https://your-host/api/collections/users/auth-with-password \
  -H 'Content-Type: application/json' -d '{"identity":"me@example.com","password":"..."}'

# 超级用户为 Agent 签发长期 token（有效期 1 年）
curl -X POST https://your-host/api/collections/users/impersonate/<用户ID> \
  -H "Authorization: <超级用户token>" -H 'Content-Type: application/json' -d '{"duration":31536000}'
```

### 1.3 连接 MCP

Claude Code：

```sh
claude mcp add --transport http bosbase https://your-host/api/mcp \
  --header "Authorization: Bearer <token>"
```

Claude Desktop / Cursor 等（`mcp.json`）：

```json
{
  "mcpServers": {
    "bosbase": {
      "type": "http",
      "url": "https://your-host/api/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

协议说明：Streamable HTTP（无状态，`POST /api/mcp`），支持协议版本 `2025-06-18`、`2025-03-26`、`2024-11-05`。

---

## 2. 隐私与权限

所有内容**默认私有**，不同用户之间完全隔离：别人无法搜索、读取、列出、关联或修改你的记忆和笔记，`[[双链]]` 也只会在你自己的知识库内解析。

- **个人记忆是私有的**：没有对所有人公开的记忆，用 `shared: true` 保存记忆会被拒绝。
- **共享记忆库**让多个用户共用记忆：加入需要创建者同意，每条记忆都记录作者（见 [3.3](#33-共享记忆库)）。
- **知识库**默认私有。**公共知识库**由超级用户用 `note_write` 加 `shared: true` 写入，所有用户可读，结果里会标注 `shared: true`。

| 数据 | 普通用户 | 超级用户 |
| --- | --- | --- |
| 记忆 | 自己的（私有）+ 已加入的共享记忆库 | 可用 `ownerId` 管理任意用户的，用 `space` 进入任意共享记忆库（管理员） |
| 知识库笔记 / 关系 | 自己的私有笔记 + 只读公共知识库 | 可用 `ownerId` 管理任意用户的；用 `shared: true` 写公共知识库 |
| 操作日志 | 自己的 | 自己的，或用 `ownerId` 查看他人 |
| 存储配置 / 大模型渠道 | 自己的 + 使用共享的 | 可创建共享配置（`shared: true`）或替他人创建（`ownerId`） |
| 上传历史 / 大模型调用记录 | 自己的 | 全部（`all: true`） |
| HTTP API（`api_request`） | 与直接调用 HTTP 完全相同的权限和集合规则 | 同左 |
| 向量模型供应商、诊断工具、`memory_stats`、`collections_list`、`collection_get` | — | ✓ |

普通用户连接后看到 59 个工具，超级用户看到 70 个。

---

## 3. 记忆

| 工具 | 作用 |
| --- | --- |
| `memory_store` | 保存记忆（自动生成向量），可同时用 `relations` 关联已有记忆；`pinned: true` 设为核心记忆；`dedupe: true` 遇到相似记忆时更新而不是重复保存 |
| `memory_search` | 语义检索 + 关键词补全；`expandDepth` 可同时返回图上的相邻记忆；`folder` 限定笔记目录 |
| `memory_get` / `memory_list` | 读取 / 列出 |
| `memory_update` / `memory_delete` | 修改（自动重新生成向量并保留历史版本）/ 删除 |
| `memory_link` / `memory_unlink` | 建立 / 删除有类型的有向关系，如 `depends_on`、`part_of`、`supersedes` |
| `memory_graph` | 从若干记忆出发按层展开（最多 5 层，可按关系类型和方向过滤） |
| `memory_path` | 两条记忆之间的最短关系路径 |
| `memory_projects` / `memory_project_rename` / `memory_project_delete` | 项目列表 / 改名合并 / 删除（需 `confirm: true`） |

示例：

```json
{"name":"memory_store","arguments":{"project":"bosbase","title":"部署约定","content":"生产环境使用 #docker 部署，数据库见 [[PostgreSQL]]","tags":["ops"]}}
{"name":"memory_search","arguments":{"query":"生产环境怎么部署","project":"bosbase","expandDepth":1}}
```

记忆内容同样是 Markdown：`#标签` 和 `[[双链]]` 会被自动识别。

### 3.1 跨会话记住上下文

| 功能 | 作用 |
| --- | --- |
| 自动带上下文 | 每次新建 MCP 会话时，服务端说明里会自动带上 AI 记得的内容：置顶记忆、最近的会话摘要和未完成的待办（约 3000 字符以内；设置 `MCP_MEMORY_AUTO_CONTEXT=false` 可关闭） |
| `memory_context` | 一次调用拿到当前任务的上下文：置顶记忆、和问题相关的记忆、最近会话、未完成待办、最近更新的记忆、最近的操作 |
| `memory_remember` | 保存本次对话：会话摘要，以及事实、决定（标签 `decision`）和待办（标签 `todo`）；和已有记忆相似时更新原记忆，不重复保存 |
| `memory_extract` | 服务端用大模型渠道从对话记录中提取记忆（自动排除密钥等敏感信息），再像 `memory_remember` 一样保存 |
| 置顶记忆 | 在 `memory_store` / `memory_update` 中设置 `pinned: true`，每次会话都会带给 AI（偏好、身份、项目关键信息） |
| 待办 | 带 `todo` 标签的记忆会一直出现在上下文里，直到加上 `done` 标签 |
| Prompt / Resource | Prompt `recall`、`remember`，Resource `bosbase://memory/context`、`bosbase://memory/pinned`（在 Claude Code 里是 `/mcp__bosbase__recall`、`/mcp__bosbase__remember`、`@bosbase:bosbase://memory/context`） |

```json
{"name":"memory_context","arguments":{"query":"修复结账的 bug","project":"shop"}}
{"name":"memory_remember","arguments":{"project":"shop","title":"结账重复扣款","summary":"定位并修复了结账时重复扣款的问题。","facts":["结账重试必须使用幂等键"],"decisions":["所有支付调用都使用 Stripe 幂等键"],"todos":["为重复扣款补一个回归测试"]}}
{"name":"memory_store","arguments":{"content":"用户希望回答简洁，并使用中文","pinned":true}}
```

推荐流程：对话开始时调用 `memory_context`，对话中用 `memory_store`（配合 `dedupe`、`pinned`）记录，任务结束时调用 `memory_remember`。

### 3.2 长期对话记忆

| 工具 | 作用 |
| --- | --- |
| `conversation_save` | 保存对话轮次（自动生成向量）。不传 `conversationId` 会新建对话，之后用返回的 id 继续追加。每新增 40 轮（`MCP_CONVERSATION_SUMMARIZE_AFTER`）且配置了大模型渠道时，自动生成摘要 |
| `conversation_recall` | 按语义搜索所有历史对话，返回命中的轮次及其前后文（"上个月我们说过 X 什么？"） |
| `conversation_list` / `conversation_get` | 列出对话及其摘要 / 读取对话内容 |
| `conversation_summarize` | 把新轮次压缩进滚动摘要（每个对话一条会话记忆，原地更新），并提取事实、决定和待办 |
| `memory_consolidate` | 把一个项目的会话摘要合并成长期**项目摘要**，每次会话都会带上 |
| `conversation_delete` | 删除对话（从中提取的记忆保留） |

摘要使用对话本身的语言。`memory_context` 会带上项目摘要和相关的历史对话片段。对话永远是私有的。

```json
{"name":"conversation_save","arguments":{"project":"travel","messages":[{"role":"user","content":"我对海鲜过敏"},{"role":"assistant","content":"好的，我会避开海鲜餐厅。"}]}}
{"name":"conversation_recall","arguments":{"query":"用户有什么饮食禁忌"}}
{"name":"memory_consolidate","arguments":{"project":"travel"}}
```

### 3.3 共享记忆库

多个用户可以通过各自的 AI 共用一个记忆库（比如一个团队）：

1. 用户创建记忆库：`space` `{"action":"create","name":"产品团队","listed":true}`，创建者即所有者。
2. 其他人发现（`discover`）并申请加入：`{"action":"join","space":"产品团队","message":"..."}`。申请处于**待审核**状态，所有者下次会话开始时会收到提醒。
3. 创建者（所有者）审核，只有所有者能批准、拒绝、移除成员：`space_member` `{"action":"approve","space":"产品团队","memberId":"<用户 id>"}`（或 `reject` 拒绝）。
4. 成员在记忆和笔记工具中传入 `space` 读写共享记忆。每条记忆都记录**作者**（`author`、`authorName`、`updatedBy`）；个人搜索也会包含已加入的记忆库（`includeSpaces`，结果里有 `spaceName` 和 `authorName`）。

| 规则 | |
| --- | --- |
| 访问 | 只有已通过审核的成员可以访问（待审核、被拒绝的用户不能访问）；个人记忆仍然私有 |
| 写入 | 成员可以新增记忆，笔记可以多人协作编辑 |
| 修改 / 删除 | 别人的记忆：只有作者或所有者、管理员可以修改和删除；删除笔记：作者或所有者、管理员 |
| 角色 | `owner` 所有者即创建者：审批加入申请、移除成员、设置角色（用 `set_role owner` 转让）；`admin` 管理员：管理内容；`member` 成员 |
| HTTP API | `/api/memory/spaces/...` 管理记忆库、成员和共享记忆（[接口文档](../apidocs/api-reference/11-memory-spaces.md)） |

---

### 3.4 AI 群聊

1. 超级用户为每个 AI 创建账号：`user_create` `{"name":"Planner"}`（或 HTTP `POST /api/admin/users`），返回 `email`、`password`（不传则自动生成）和 `token`，这个 token 就是该 AI 的 MCP Bearer token。
2. 一个 AI 创建群聊并拉其他成员：`chat_room` `{"action":"create","name":"发布作战室","members":["coder@agents.test"]}`。
3. AI 用 `chat_send` 发消息，用 `chat_read` 收消息（`after` 传上次返回的 cursor，`wait` 最多 60 秒，有人发言会立即返回）。
4. 应用或人也可以通过实时订阅 `chat_messages` 集合（`/api/realtime`）同步看到群聊，成员只会收到自己所在群的消息。

```json
{"name":"chat_send","arguments":{"room":"发布作战室","content":"后端已完成，@Reviewer 请审查","mentions":["reviewer@agents.test"]}}
{"name":"chat_read","arguments":{"room":"发布作战室","after":"2026-10-25 03:10:04.151Z|01a1...","wait":30}}
```

接口文档见 [`apidocs/api-reference/12-chat-accounts.md`](../apidocs/api-reference/12-chat-accounts.md)。

---

## 4. 向量模型供应商（超级用户）

记忆和笔记由存在数据库里的供应商生成向量，支持所有 OpenAI 兼容的 `/embeddings` 接口（OpenAI、OpenRouter、Azure 网关、Ollama、vLLM、Jina、Voyage 等）。修改后立即生效，不需要重启服务。

- **默认和优先级**：先用默认供应商，再按 `priority` 从小到大使用其他已启用的供应商。
- **自动切换**：某个供应商调用失败时自动换下一个，错误记录在该供应商的 `lastError` 上，`diagnostics_health` 也会提示。
- **更换模型**：不同模型的向量不能直接比较，所以语义检索只比较和查询同一模型生成的记忆（其他记忆仍可通过关键词找到）。更换默认模型后，运行 `embedding_reindex` 重新生成向量。
- API key 加密保存，接口不会返回明文。

| 工具 | 作用 |
| --- | --- |
| `embedding_provider` | `list` / `get` / `create` / `update` / `delete` / `test` |
| `embedding_status` | 供应商列表、调用顺序、当前主模型，以及缺少向量或使用其他模型的记忆数量 |
| `embedding_reindex` | 用主供应商重新生成向量（`mode`：默认 `mismatched`，或 `missing`、`all`），`remaining > 0` 时继续调用 |

```json
{"name":"embedding_provider","arguments":{"action":"create","name":"openrouter","baseUrl":"https://openrouter.ai/api/v1","apiKey":"sk-or-...","model":"openai/text-embedding-3-small","isDefault":true}}
{"name":"embedding_provider","arguments":{"action":"create","name":"local-ollama","baseUrl":"http://ollama:11434/v1","model":"nomic-embed-text","priority":10}}
{"name":"embedding_provider","arguments":{"action":"test","id":"openrouter"}}
{"name":"embedding_reindex","arguments":{}}
```

HTTP 接口：`/api/embedding/providers`（增删改查，以及 `/{id}/test`）、`/api/embedding/status`、`/api/embedding/embed`、`/api/embedding/reindex`，详见 [`apidocs/api-reference/10-storage-llm-diagnostics.md`](../apidocs/api-reference/10-storage-llm-diagnostics.md#embedding-providers)。

没有启用任何供应商时，会退回使用环境变量 `MCP_EMBEDDING_API_KEY` / `MCP_EMBEDDING_BASE_URL` / `MCP_EMBEDDING_MODEL`。

---

## 5. 知识库（类 Obsidian）

一个项目就是一个 vault，每条笔记有唯一的相对路径，如 `projects/bosbase.md`。

| 功能 | 说明 |
| --- | --- |
| frontmatter 属性 | 开头的 YAML 块解析为 `properties`，支持 `title`、`tags`、`aliases` |
| 标题 | 显式 `title` > frontmatter `title` > 第一个 `# 标题` > 文件名 |
| 标签 | frontmatter 标签 + 正文 `#标签`、`#嵌套/标签`（忽略 `#123` 和代码块） |
| 双链 | `[[笔记]]`、`[[目录/笔记]]`、`[[笔记\|别名]]`、`[[笔记#标题]]`、`![[嵌入]]`、`[文字](note.md)`；按路径、文件名、标题、别名解析，自动生成 `links_to` 图关系；目标笔记以后创建时自动补上 |
| 反向链接 | 链接到本笔记的笔记，外加"未链接提及" |
| 改名 | `note_move` 会自动改写其他笔记里指向它的链接 |
| 版本历史 | 每次修改都会保留旧版本（最近 50 个），可以查看和恢复 |

| 工具 | 作用 |
| --- | --- |
| `note_write` | 创建或修改笔记；`mode`：`overwrite`（默认）、`create`（已存在则报错）、`append`、`prepend`（frontmatter 保持在最前） |
| `note_read` | 读取笔记，附带属性、标签、出链（是否已解析）、反向链接 |
| `note_list` | 浏览目录（笔记 + 子目录），可按标签、属性过滤（如 `{"status":"done"}`） |
| `note_move` / `note_delete` | 移动或改名 / 删除 |
| `note_backlinks` | 反向链接 + 未链接提及 |
| `note_daily` | 日记 `daily/YYYY-MM-DD.md`，`append` 会追加一条带时间的记录 |
| `note_tags` | 标签统计 |
| `note_history` / `note_restore` | 版本列表或查看某个版本 / 恢复到某个版本 |

示例：

```json
{"name":"note_write","arguments":{"project":"kb","path":"tech/pgvector.md","content":"---\naliases: [向量扩展]\nstatus: active\n---\n# pgvector\nPostgreSQL 的向量扩展，被 [[BosBase]] 使用 #数据库"}}
{"name":"note_move","arguments":{"project":"kb","path":"tech/pgvector.md","newPath":"db/pgvector.md"}}
{"name":"memory_path","arguments":{"source":"<笔记A id>","target":"<笔记B id>"}}
```

### 5.1 知识图谱与联想检索

| 工具 | 作用 |
| --- | --- |
| `knowledge_associate` | 联想检索：即使和问题字面不相关，也能找到相关知识。从最匹配的结果出发，沿双链、反向链接、类型化关系、语义相近的笔记和共同标签扩散（默认 2 跳），每条结果都说明联想路径（`via`：从哪条笔记、通过什么关系联想到） |
| `note_graph` | 知识库图谱视图（类似 Obsidian）：节点、连线、核心节点、孤立笔记、连通分组 |
| `knowledge_suggest_links` | 找出内容相关但还没建立链接的笔记；`apply: true` 会自动建立 `related_to` 关系 |

```json
{"name":"knowledge_associate","arguments":{"query":"支付扣款为什么会被拒绝","project":"kb"}}
```

结果示例：`规则引擎（2 跳，经由 风控服务：links to）`、`退款流程（经由 支付服务：共同标签 #payment）`。`memory_context` 也会带上"Related through the knowledge graph"（通过知识图谱关联）一节。

---

## 6. 让 AI 记得自己做过什么

- 每次工具调用都会自动记录到 `_mcp_activity`，包括工具名、项目、参数（密码、token、密钥会脱敏，大字段会截断）、是否成功、摘要和耗时。
- 新会话 `initialize` 时，服务端返回的说明末尾会附上**最近 10 条写操作**。
- `activity_recent`：按时间、工具、项目、关键词查询历史操作；`writesOnly: true` 只看写操作。
- `activity_log`：AI 主动写一条工作日志（做了什么、做了哪些决定、下一步是什么）。
- 历史默认保留 180 天（环境变量 `MCP_ACTIVITY_RETENTION_DAYS`）。

建议给 AI 的系统提示（服务端说明里已经包含同样的指引）：

> 对话开始时用用户的问题调用 `memory_context`；遇到值得长期记住的信息用 `memory_store` 保存（`dedupe: true`，稳定的偏好加 `pinned: true`）；任务结束时调用 `memory_remember`，写上摘要、事实、决定和待办。

---

## 7. 对象存储

| 工具 | 作用 |
| --- | --- |
| `storage_provider` | 管理存储配置：`list` / `get` / `create` / `update` / `delete` / `test`（上传一个探测文件再读、再删，验证连通性） |
| `storage_upload` | 上传文件（base64 或文本），记录到上传历史，并返回可以直接在公网访问的 `url` |
| `storage_presign_upload` | 大文件（视频等）获取预签名 PUT 地址，客户端直传，完成后调用 `storage_objects` 的 `complete` |
| `storage_objects` | 上传历史：`list`（按存储、状态、前缀、类型、关键词过滤）、`get`（附带 1 小时有效的下载地址）、`complete`、`delete`（删除远端文件，历史保留为 deleted） |

常用配置：

| 服务 | type | endpoint | region |
| --- | --- | --- | --- |
| AWS S3 | `aws` | 可留空（自动生成） | 如 `ap-northeast-1` |
| Cloudflare R2 | `r2` | `https://<account_id>.r2.cloudflarestorage.com` | `auto`（默认） |
| Google Cloud Storage（HMAC 密钥） | `gcs` | 可留空（`https://storage.googleapis.com`） | `auto` |
| MinIO / 自建 | `minio` | `http://minio:9000`，并设置 `forcePathStyle: true` | — |

```json
{"name":"storage_provider","arguments":{"action":"create","name":"r2","type":"r2","endpoint":"https://xxx.r2.cloudflarestorage.com","bucket":"media","accessKey":"...","secretKey":"...","publicUrl":"https://media.example.com","isDefault":true}}
{"name":"storage_upload","arguments":{"filename":"cover.png","contentBase64":"iVBORw0...","prefix":"images"}}
```

公网链接：每次上传都会返回 `url`，`urlType` 为 `public`（永久链接，存储配置了 `publicUrl` 或 `publicRead: true` 时）或 `presigned`（预签名链接，默认 7 天有效，可用 `urlExpiresSeconds` 指定）。

超级用户可以配置多个共享存储（`shared: true`）并设置默认存储；用户自己没有默认存储时，上传到共享的默认存储。设置 `STORAGE_ALLOW_USER_PROVIDERS=false` 后，只有超级用户可以配置存储。

HTTP 接口同样可用：`POST /api/storage/objects` 支持 multipart 多文件上传，详见 [`apidocs/api-reference/10-storage-llm-diagnostics.md`](../apidocs/api-reference/10-storage-llm-diagnostics.md)。

---

## 8. 大模型中转

| 工具 | 作用 |
| --- | --- |
| `llm_channel` | 管理渠道：`list` / `get` / `create` / `update` / `delete` / `test` |
| `llm_chat` | 通过渠道对话（`prompt` 或 `messages`，可选 `system`、`model`、`temperature`、`maxTokens`） |
| `llm_embed` | 通过渠道生成向量 |
| `llm_usage` | 按渠道和模型统计用量（`usage`），或查看调用明细（`requests`） |

渠道类型：

- `openai`：所有 OpenAI 兼容接口，如 OpenAI、OpenRouter、DeepSeek、通义千问、Groq、Gemini 的 OpenAI 兼容端点、Ollama 或 vLLM 的 `/v1`
- `anthropic`：Anthropic Messages API

渠道选择顺序：显式指定（`X-Bosbase-Channel` 请求头、`channel` 参数或请求体字段）> 渠道 `models` 列表里包含所请求的模型 > 默认渠道（自己的优先，其次是共享的）。

**任何 OpenAI SDK 都可以直接把 BosBase 当中转用**：

```python
from openai import OpenAI
client = OpenAI(base_url="https://your-host/api/llm/v1", api_key="<BosBase token>")
client.chat.completions.create(model="openai/gpt-4o-mini", messages=[{"role":"user","content":"你好"}])
```

支持流式输出（SSE 透传）；如果需要统计流式请求的用量，请带上 `"stream_options": {"include_usage": true}`。

---

## 9. 错误诊断（超级用户）

- 所有 ERROR 级别的日志除了写入数据库，还会**写到磁盘**：`{dataDir}/logs/errors-YYYY-MM-DD.jsonl`。数据库不可用时也能排查。相关环境变量：`BOSBASE_ERROR_LOG_LEVEL`、`BOSBASE_ERROR_LOG_MAX_DAYS`（默认 30）、`BOSBASE_ERROR_LOG_MAX_MB`（默认 100）、`BOSBASE_ERROR_LOG_FILE=false`（关闭）。
- 请求日志里的密码、token、密钥等字段会自动脱敏。
- 如果数据库里的设置是加密保存的，而现在 key 丢失、不对或格式错误，BosBase 仍会用默认设置启动，记录一条错误日志，并禁止保存设置（避免覆盖加密的原设置）。恢复原来的 key 后重启即可；也可以设置 `BOSBASE_ALLOW_SETTINGS_RESET=true` 覆盖原设置。

| 工具 | 作用 |
| --- | --- |
| `diagnostics_health` | 健康快照：版本、运行时、数据库和 pgvector、连接池、迁移、设置、加密密钥状态、近期错误数、各集成状态（包括调用失败的向量模型供应商），以及自动发现的问题清单 |
| `diagnostics_errors` | 错误日志（按消息、状态码、错误聚合 + 最新明细），可按时间、状态码（如 `5xx`）、关键词过滤；数据库不可用时自动读磁盘文件 |
| `diagnostics_log_files` | 列出或读取磁盘上的错误日志文件 |
| `diagnostics_report` | 生成 Markdown 错误报告并保存到 `{dataDir}/logs/reports/`，可直接附在 bug 报告里 |

推荐排查流程：`diagnostics_health` → `diagnostics_errors {"since":"24h","status":"5xx"}` → `diagnostics_report`。

---

## 10. 调用全部 HTTP API

| 工具 | 作用 |
| --- | --- |
| `api_endpoints` | 当前 token 能调用的接口列表（实时读取路由，包含自定义路由），标注权限级别 |
| `api_docs` | 查阅 API 文档（按路径，或按主题如 `overview`） |
| `api_request` | 用当前 token 在进程内调用任意接口，支持 query、JSON body、文件上传 |
| `records_*` / `record_*` | 记录增删改查的快捷工具（遵守集合 API 规则） |
| `collections_list` / `collection_get` | 查看集合结构（超级用户） |

流式接口（realtime、pubsub、SSE、WebSocket）不支持通过 MCP 调用。

---

## 11. 环境变量汇总

| 变量 | 作用 | 默认 |
| --- | --- | --- |
| `MCP_EMBEDDING_API_KEY` / `_BASE_URL` / `_MODEL` / `_DIMENSIONS` | 备用向量模型，仅在没有启用任何供应商时使用 | 回退到 `OPENAI_*`；模型 `text-embedding-3-small` |
| `MCP_ACTIVITY_RETENTION_DAYS` | 操作日志保留天数 | 180 |
| `MCP_MEMORY_AUTO_CONTEXT` | 在会话初始化说明中自动带上记忆上下文 | true |
| `MCP_CONVERSATION_SUMMARIZE_AFTER` | 新增多少轮后自动生成对话摘要 | 40 |
| `MCP_CONVERSATION_RETENTION_DAYS` | 对话轮次保留天数（0 表示永久） | 0 |
| `BS_ENCRYPTION_KEY`（或 `BOSBASE_ENCRYPTION_KEY`） | 加密设置和密钥用的 key（正好 32 个字符） | 未设置或格式不对：不加密保存，服务照常运行 |
| `BOSBASE_ALLOW_SETTINGS_RESET` | 允许覆盖用已丢失的 key 加密过的设置 | false |
| `BOSBASE_ALLOW_PRIVATE_UPSTREAMS` | 允许普通用户的配置访问内网地址 | false |
| `STORAGE_MAX_UPLOAD_MB` | 单次上传大小上限 | 512 |
| `STORAGE_URL_EXPIRES_SECONDS` | 上传返回的预签名链接有效期 | 604800（7 天） |
| `STORAGE_ALLOW_USER_PROVIDERS` | 普通用户是否可以配置自己的存储 | true |
| `LLM_RELAY_TIMEOUT_SECONDS` | 大模型中转超时 | 600 |
| `BOSBASE_ERROR_LOG_FILE` / `_LEVEL` / `_MAX_DAYS` / `_MAX_MB` | 错误日志落盘 | 开启 / ERROR / 30 / 100 |

## 12. 数据表

| 表 | 用途 |
| --- | --- |
| `_mcp_memories` | 记忆和笔记（内容、标签、属性、链接、向量） |
| `_mcp_memory_edges` | 记忆之间的关系（`links_to` 由双链自动维护） |
| `_mcp_memory_versions` | 历史版本 |
| `_mcp_activity` | AI 操作日志 |
| `_storage_providers` / `_storage_objects` | 存储配置 / 上传历史 |
| `_llm_channels` / `_llm_requests` | 大模型渠道 / 调用记录 |
| `_embedding_providers` | 向量模型供应商 |
| `_mcp_conversations` / `_mcp_messages` | 对话 / 对话轮次 |
| `_mcp_spaces` / `_mcp_space_members` | 共享记忆库 / 成员 |
| `chat_rooms` / `chat_messages` | 群聊（普通集合，支持实时订阅） |
