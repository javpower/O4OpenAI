# O4OpenAI - OpenAI / Anthropic Compatible API Gateway

一个可扩展的 API 转换平台，对外同时提供 **OpenAI 兼容**和 **Anthropic Messages API 兼容**的接口，内部调用第三方大模型 API（目前已接入 Agnes AI）。

## 特性

-  **OpenAI API 兼容** — Chat Completions、Images、Videos、Models 接口
-  **Anthropic Messages API 兼容** — 支持 `POST /v1/messages`，兼容 Anthropic SDK
-  **插件化 Provider 架构** — 轻松添加新的第三方平台适配器
-  **流式传输支持** — OpenAI SSE 和 Anthropic SSE 两种格式
-  **客户端 Key 透传** — 支持 `Authorization: Bearer` 和 `x-api-key` 两种认证方式
-  **模型映射** — 可以将外部模型名映射到任意 Provider 的模型名
-  **错误透传** — 上游 HTTP 状态码（401/422 等）原样返回给客户端
-  **Thinking 模式** — 支持 Agnes 原生的 Thinking 能力（Anthropic 和 OpenAI 两种格式）
-  **工具调用** — 支持完整的 function calling / tool_use 工作流
-  **图生图、首尾帧视频** — 走 Agnes 同一接口，OpenAI 风格封装
-  **Multipart 表单支持** — 图片编辑和视频生成支持 `multipart/form-data`，兼容 OpenAI Python SDK
-  **多图上传** — 图生图支持一次上传最多 16 张图片

## 支持的 OpenAI 接口

| 接口 | 方法 | 路径 | 说明 |
|------|------|------|------|
| Chat Completions | POST | `/v1/chat/completions` | 支持流式和非流式 |
| Image Generation | POST | `/v1/images/generations` | 文生图 |
| Image Edit | POST | `/v1/images/edits` | 图生图（image + prompt） |
| Image Variation | POST | `/v1/images/variations` | 图片变体（Agnes 暂未支持） |
| Video Generation | POST | `/v1/videos/generations` | 异步任务：文生视频 / 图生视频 / 首尾帧 |
| Video Status | GET | `/v1/videos/:id` | 轮询视频任务状态 |
| Video Download | GET | `/v1/videos/:id/content` | 下载视频文件（302 重定向到视频 URL） |
| List Models | GET | `/v1/models` | 列出可用模型 |
| Get Model | GET | `/v1/models/:model` | 获取模型信息 |

## 支持的 Anthropic 接口

| 接口 | 方法 | 路径 | 说明 |
|------|------|------|------|
| Messages | POST | `/v1/messages` | 兼容 Anthropic Messages API，支持流式和非流式 |

支持的 Anthropic 特性：
- `system` 系统提示（string 或 blocks 数组）
- `max_tokens`、`temperature`、`top_p`、`top_k`
- `stop_sequences` 停止序列
- `tools` / `tool_choice` 工具调用
- `thinking` 思考模式（`{"type":"enabled","budget_tokens":2048}`）
- `stream` 流式 SSE（`message_start` / `content_block_delta` / `message_stop` 等完整事件类型）
- 图片输入（base64 / URL）
- `x-api-key` 和 `Authorization: Bearer` 双认证方式

## 已接入的 Provider

### Agnes AI (`agnes`)

- **Base URL**: `https://apihub.agnes-ai.com/v1`
- **模型**:
  - `agnes-1.5-flash` — 文本生成
  - `agnes-2.0-flash` — 文本生成
  - `agnes-image-2.0-flash` — 文生图
  - `agnes-image-2.1-flash` — 文生图 / **图生图**
  - `agnes-video-v2.0` — 文生视频 / 图生视频 / 首尾帧
- **文档**: https://agnes-ai.com/doc

### Provider 能力矩阵

| 能力 | Agnes |
|------|-------|
| Chat | ✅ |
| Image Generation | ✅ |
| Image Edit (图生图) | ✅（调用 `/images/generations` 带 `image` 数组） |
| Image Variation | ❌（Agnes 无此端点） |
| Video Generation | ✅（异步任务） |
| Mask Inpainting | ⚠️ Agnes 不支持 mask，已忽略 |

## 快速开始

### 安装依赖

```bash
go mod tidy
```

### 配置

`config.yaml` 示例：

```yaml
server:
  port: 1241
  host: "0.0.0.0"
  mode: "debug"

gateway:
  # 外网可访问的 URL，用于 base64 图片转临时 URL
  public_url: "http://your-public-url"
  temp_image_ttl: 10
  temp_image_cleanup_interval: 5

providers:
  agnes:
    base_url: "https://apihub.agnes-ai.com/v1"
    enabled: true
    models:
      - external_model: "agnes-1.5-flash"
        provider_model: "agnes-1.5-flash"
      - external_model: "agnes-2.0-flash"
        provider_model: "agnes-2.0-flash"
      - external_model: "agnes-image-2.0-flash"
        provider_model: "agnes-image-2.0-flash"
      - external_model: "agnes-image-2.1-flash"
        provider_model: "agnes-image-2.1-flash"
      - external_model: "agnes-video-v2.0"
        provider_model: "agnes-video-v2.0"
```

> **重要**：客户端的 API Key 通过 `Authorization: Bearer <key>` 头透传给 Provider，
> **不要**在 `config.yaml` 中配置 Provider 的 API Key。

### 启动

```bash
go run cmd/server/main.go
```

或编译后运行：

```bash
GOOS=$(uname -s | tr A-Z a-z) GOARCH=arm64 go build -o o4openai ./cmd/server/
./o4openai
```

## 测试用例

### 1. Chat Completions（非流式）

```bash
curl http://localhost:1241/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_AGNES_API_KEY" \
  -d '{
    "model": "agnes-1.5-flash",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": false
  }'
```

### 2. Chat Completions（流式 SSE）

```bash
curl -N http://localhost:1241/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_AGNES_API_KEY" \
  -d '{
    "model": "agnes-1.5-flash",
    "messages": [{"role": "user", "content": "数到3"}],
    "stream": true
  }'
```

### 3. 文生图

```bash
curl http://localhost:1241/v1/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_AGNES_API_KEY" \
  -d '{
    "model": "agnes-image-2.1-flash",
    "prompt": "A cute cat",
    "n": 1,
    "size": "1024x1024"
  }'
```

### 4. 图生图

```bash
# image 可以是 HTTP(S) URL、Data URI (data:image/png;base64,...) 或纯 base64
curl http://localhost:1241/v1/images/edits \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_AGNES_API_KEY" \
  -d '{
    "model": "agnes-image-2.1-flash",
    "image": "https://example.com/photo.png",
    "prompt": "Change the sky to a starry night, keep the rest",
    "n": 1,
    "size": "1024x1024"
  }'
```

### 5. 文生视频（异步）

```bash
RESP=$(curl -s http://localhost:1241/v1/videos/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_AGNES_API_KEY" \
  -d '{
    "model": "agnes-video-v2.0",
    "input": [{"type": "text", "text": "A butterfly in a sunny garden"}]
  }')

TASK_ID=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
echo "Task ID: $TASK_ID"
```

### 6. 图生视频

```bash
curl http://localhost:1241/v1/videos/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_AGNES_API_KEY" \
  -d '{
    "model": "agnes-video-v2.0",
    "input": [
      {"type": "text", "text": "the flower gently sways in the breeze"},
      {"type": "image", "image": "https://example.com/flower.png"}
    ]
  }'
```

### 7. 首尾帧生视频

```bash
curl http://localhost:1241/v1/videos/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_AGNES_API_KEY" \
  -d '{
    "model": "agnes-video-v2.0",
    "input": [
      {"type": "text", "text": "smooth cinematic transition between keyframes"},
      {"type": "image", "image": "https://example.com/frame1.png"},
      {"type": "image", "image": "https://example.com/frame2.png"}
    ]
  }'
```

### 8. 轮询视频状态

```bash
curl http://localhost:1241/v1/videos/$TASK_ID \
  -H "Authorization: Bearer YOUR_AGNES_API_KEY"
```

状态流转：`queued` → `in_progress` → `completed` / `failed` / `expired`。

`completed` 时返回：

```json
{
  "id": "task_xxx",
  "status": "completed",
  "output": [{
    "type": "url",
    "url": "https://storage.googleapis.com/.../video.mp4",
    "duration": 5.0,
    "mime_type": "video/mp4"
  }]
}
```

### 9. 下载视频内容

```bash
# 视频未完成时返回 video_not_ready 错误；完成后 302 重定向到实际视频 URL
curl -L http://localhost:1241/v1/videos/$TASK_ID/content \
  -H "Authorization: Bearer YOUR_AGNES_API_KEY" \
  -o video.mp4
```

### 10. 列出模型

```bash
curl http://localhost:1241/v1/models
```

## Anthropic 兼容接口测试

### 11. Anthropic Messages（非流式）

```bash
curl http://localhost:1241/v1/messages \
  -H "x-api-key: YOUR_AGNES_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "agnes-2.0-flash",
    "max_tokens": 256,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

响应：

```json
{
  "id": "msg_chatcmpl-xxx",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "你好！"}],
  "model": "agnes-2.0-flash",
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 10, "output_tokens": 3}
}
```

### 12. Anthropic Messages（流式 SSE）

```bash
curl -N http://localhost:1241/v1/messages \
  -H "x-api-key: YOUR_AGNES_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "agnes-2.0-flash",
    "max_tokens": 128,
    "stream": true,
    "messages": [{"role": "user", "content": "说你好"}]
  }'
```

### 13. Anthropic Messages（带 system prompt）

```bash
curl http://localhost:1241/v1/messages \
  -H "x-api-key: YOUR_AGNES_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "agnes-2.0-flash",
    "max_tokens": 64,
    "system": "你只能用英文回答",
    "messages": [{"role": "user", "content": "说你好"}]
  }'
```

### 14. Anthropic Messages（带 Thinking 模式）

```bash
curl http://localhost:1241/v1/messages \
  -H "x-api-key: YOUR_AGNES_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "agnes-2.0-flash",
    "max_tokens": 2048,
    "thinking": {"type": "enabled", "budget_tokens": 1024},
    "messages": [{"role": "user", "content": "写一段 Python 排序代码"}]
  }'
```

### 15. 使用 Anthropic Python SDK

```python
import anthropic

client = anthropic.Anthropic(
    base_url="http://localhost:1241",
    api_key="YOUR_AGNES_API_KEY"
)

# 非流式
message = client.messages.create(
    model="agnes-2.0-flash",
    max_tokens=256,
    messages=[{"role": "user", "content": "Hello!"}]
)
print(message.content[0].text)

# 流式
with client.messages.stream(
    model="agnes-2.0-flash",
    max_tokens=256,
    messages=[{"role": "user", "content": "Hello!"}]
) as stream:
    for text in stream.text_stream:
        print(text, end="", flush=True)
```

## 错误响应

### OpenAI 接口错误

| 场景 | HTTP 状态码 |
|------|------------|
| 客户端未传 `Authorization` 头 | **401** |
| 客户端传了错误的 API Key | **401**（透传 Agnes 的 401） |
| 请求参数错误（缺字段、格式错） | **400** |
| Agnes 上游 4xx 错误（如 422） | **透传** 4xx |
| 网关内部错误 | **500** |

错误响应体遵循 OpenAI 格式：

```json
{
  "error": {
    "message": "Chat completion failed: no API key provided: client must send Authorization: Bearer <key>",
    "type": "invalid_request_error",
    "code": "provider_error"
  }
}
```

### Anthropic 接口错误

Anthropic 接口（`/v1/messages`）返回 Anthropic 格式的错误：

```json
{
  "type": "error",
  "error": {
    "type": "authentication_error",
    "message": "Missing or invalid API key. Please provide x-api-key header."
  }
}
```

| HTTP 状态码 | Anthropic 错误类型 |
|-------------|-------------------|
| 400 | `invalid_request_error` |
| 401 | `authentication_error` |
| 403 | `permission_error` |
| 404 | `not_found_error` |
| 429 | `rate_limit_error` |
| 500 | `api_error` |
| 503 | `overloaded_error` |

## 架构

```
┌──────────────────────────────────────────────────────────────┐
│                        Client                                │
│       (OpenAI SDK / Anthropic SDK / curl / 任何客户端)       │
└──────────┬───────────────────────────────┬───────────────────┘
           │ OpenAI API                    │ Anthropic Messages API
           │ (Authorization: Bearer)       │ (x-api-key / Authorization)
           ▼                               ▼
┌──────────────────────────────────────────────────────────────┐
│                    O4OpenAI Gateway                           │
│                                                              │
│  ┌───────────────┐  ┌──────────────┐  ┌──────────────────┐  │
│  │ OpenAI Handler│  │   Anthropic  │  │    Middleware     │  │
│  │ (chat/image/  │  │   Handler    │  │ (CORS/Log/Auth)  │  │
│  │  video/models)│  │ (/v1/messages)│  │                  │  │
│  └───────┬───────┘  └──────┬───────┘  └──────────────────┘  │
│          │                 │                                  │
│          │   格式转换层    │                                  │
│          │ ◄──────────────┘ (Anthropic → OpenAI → Agnes)    │
│          ▼                                                    │
│  ┌───────────────┐                                           │
│  │   Registry    │  (Provider 注册表/路由)                    │
│  └───────┬───────┘                                           │
│          │                                                    │
│  ┌───────┴──────────────────────────────────────────┐        │
│  │         Provider Interface (接口层)               │        │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────────┐     │        │
│  │  │  Agnes   │ │  OpenAI  │ │   Future     │     │        │
│  │  │ Provider │ │ Provider │ │  Providers   │     │        │
│  │  └────┬─────┘ └────┬─────┘ └──────────────┘     │        │
│  └───────┼────────────┼────────────────────────────┘        │
│          │ (error/status passthrough)                        │
│          ▼                                                   │
│    Agnes AI API                                              │
│    https://apihub.agnes-ai.com/v1                            │
└──────────────────────────────────────────────────────────────┘
```

### 添加新的 Provider

1. 在 `internal/provider/` 下创建新目录（如 `anthropic/`）
2. 实现 `model.Provider` 接口
3. 在 `config.yaml` 中添加 Provider 配置
4. 在 `cmd/server/main.go` 中注册 Provider

```go
// model.Provider 接口
type Provider interface {
    Name() string
    SupportedModels() []ModelInfo
    ChatCompletion(ctx, req) (*Response, error)
    ChatCompletionStream(ctx, req) (io.ReadCloser, error)
    ImageGeneration(ctx, req) (*Response, error)
    ImageEdit(ctx, req) (*Response, error)
    ImageVariation(ctx, req) (*Response, error)
    VideoGeneration(ctx, req) (*Response, error)
    VideoRetrieve(ctx, videoID) (*Response, error)
    SupportsChat() bool
    SupportsImageGeneration() bool
    SupportsImageEdit() bool
    SupportsImageVariation() bool
    SupportsVideoGeneration() bool
}
```

## Agnes AI 接口适配差异

| 差异点 | 处理方式 |
|--------|----------|
| **图生图端点** | Agnes 用 `/images/generations` + `image` 数组，而非 OpenAI 的 `/images/edits` |
| **视频端点** | Agnes 用 `POST /videos` + 异步轮询 `GET /videos/{task_id}` |
| **视频 URL 字段** | Agnes 用 `remixed_from_video_id`（名字反直觉），我们把它映射到 `output[].url` |
| **视频状态枚举** | Agnes: `queued`/`in_progress`/`processing`/`running`/`completed`/`failed`/`expired` → 归一化为 OpenAI 标准: `queued`/`in_progress`/`completed`/`failed`/`expired` |
| **response_format 位置** | 图生图的 `response_format` 必须放在 `extra_body` 里 |
| **图片输入** | `image` 数组支持公网 URL、Data URI、纯 base64 |
| **`num_frames` 约束** | 必须是 `8n+1`，≤ 441；网关自动四舍五入 |
| **多图视频** | 多张图放在 `extra_body.image` 数组里；含"keyframe"/"transition"时自动设 `mode=keyframes` |
| **流式输出** | Agnes 兼容 OpenAI SSE 格式，直接透传并重写模型名 |
| **HTTP 状态码** | 全部透传上游状态码（401/422 等） |
| **Thinking 模式** | Anthropic `thinking` 字段和 OpenAI `chat_template_kwargs` 均透传给 Agnes |
| **工具调用** | Anthropic `tools`/`tool_choice` 转换为 OpenAI 格式后透传给 Agnes |
| **`top_k` 参数** | Anthropic `top_k` 通过 Extra map 透传给 Agnes |

## 环境变量

| 变量 | 说明 |
|------|------|
| `O4OPENAI_SERVER_PORT` | 服务端口（覆盖 config） |
| `O4OPENAI_GATEWAY_PUBLIC_URL` | 网关外网 URL |
| `O4OPENAI_PROVIDERS_AGNES_APIKEY` | Agnes 兜底 Key（可选，客户端 Key 优先） |

## License

MIT
