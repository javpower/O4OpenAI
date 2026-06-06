# O4OpenAI 接口协议对照文档

本文档逐一说明**OpenAI 标准接口**与**O4OpenAI 网关实际处理**的请求/响应差异。
对每个端点，展示三组对照：

1. **客户端 → 网关**（用户实际发什么）
2. **网关 → Provider**（实际转发到 Agnes AI 什么）
3. **Provider → 网关**（Agnes 实际返回什么）
4. **网关 → 客户端**（网关最终返回什么）

---

## 全局行为

### 1. 认证

| 阶段 | 行为 |
|------|------|
| 客户端 → 网关 | 必须传 `Authorization: Bearer <key>` 头 |
| 网关 → Provider | **原样透传**客户端的 `Authorization` 头 |
| 网关 | **不验证** API Key 的有效性 |
| 客户端未传 | 网关返回 **401 Unauthorized** |

### 2. 错误状态码

| 客户端行为 | 网关响应 |
|-----------|---------|
| 缺 `Authorization` 头 | **401** `invalid_request_error` |
| 错误 API Key | **401**（透传 Agnes 的 401） |
| 请求体格式错 | **400** `invalid_request_error` |
| Provider 4xx | **透传**对应状态码 |
| Provider 5xx | **透传** |
| 网关内部错误 | **500** `api_error` |

### 3. Base64 / URL 输入兼容

**两种 base64 形式**（`data:image/png;base64,...` 和纯 base64）在内部**完全等价** — 网关会先统一处理，再决定如何转发到上游。

#### 按端点拆分的处理逻辑

| 端点 | 客户端输入 | 网关处理 | 转发给 Provider |
|------|-----------|---------|---------------|
| **图生图** `/v1/images/edits` | 公网 URL | 透传 | URL 原样 |
| | `data:image/png;base64,...` | 转为临时 URL | `http://<gateway>/_temp/images/<hash>` |
| | 纯 base64 | 自动包装为 Data URI，再转为临时 URL | `http://<gateway>/_temp/images/<hash>` |
| **图生视频** `/v1/videos/generations` | 公网 URL | 透传 | URL 原样 |
| | `data:image/png;base64,...` | **存为本地临时文件** | `http://<gateway>/_temp/images/<hash>` |
| | 纯 base64 | **先包装为 Data URI，再存为临时文件** | `http://<gateway>/_temp/images/<hash>` |

#### 为什么图生视频和图生图都要转临时 URL？

| 维度 | 图生图 | 图生视频 |
|------|--------|---------|
| 上游端点 | `POST /v1/images/generations` | `POST /v1/videos` |
| `image` 字段 | 数组，支持 URL / Data URI | 单值，**必须是可 fetch 的 URL** |
| Data URI 处理 | 转为临时 URL | 转为临时 URL |
| 请求体大小 | 几 MB（图片本身） | 几十 MB+（含视频参数） |

网关统一将所有 Data URI / base64 输入转换为临时 URL，确保上游 Provider 可以通过 HTTP GET 拉取。

#### 临时文件生命周期

1. 收到请求 → base64 解码 → 写入 `temp_image_dir`（默认在内存中）
2. 生成 hash URL：`http://<gateway.public_url>/_temp/images/<sha256>`
3. 请求**完成**（成功或失败）→ **立即删除**文件
4. **安全兜底**：超过 `temp_image_ttl`（默认 10 分钟）的文件被清理协程回收

#### 不支持的输入

| 形式 | 行为 |
|------|------|
| 本地文件路径（`/path/to/img.png`） | ❌ 不会处理，透传给上游（通常 422） |
| 需要登录的 URL | ⚠️ 透传，Agnes 拉不到会失败 |
| `file://` 协议 | ❌ 不会处理 |
| base64 长度 < 64 字符 | ⚠️ 不会被识别为 base64，按 URL 透传（通常失败） |

---

## 1. Chat Completions

### POST /v1/chat/completions

**客户端 → 网关**（OpenAI 标准）

```http
POST /v1/chat/completions HTTP/1.1
Host: gateway.local
Authorization: Bearer sk-xxx
Content-Type: application/json

{
  "model": "agnes-1.5-flash",
  "messages": [
    {"role": "user", "content": "Hello"}
  ],
  "stream": false,
  "temperature": 0.7
}
```

**网关 → Provider**

```http
POST /v1/chat/completions HTTP/1.1
Host: apihub.agnes-ai.com
Authorization: Bearer sk-xxx  ← 原样透传
Content-Type: application/json

{
  "model": "agnes-1.5-flash",  ← 解析为 provider_model（默认同名）
  "messages": [...],
  "stream": false,
  "temperature": 0.7
  // 其他 OpenAI 不支持的字段（logprobs 等）会被忽略
}
```

**Provider → 网关 → 客户端**

非流式（200 OK）：

```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "created": 1780000000,
  "model": "agnes-1.5-flash",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "Hi!"},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
}
```

**网关处理**：

- ✅ 直接透传 Agnes 的 OpenAI 兼容响应
- ✅ 如果 Agnes 返回的 `model` 字段是 provider_model 名称，重写为客户端请求的 external_model
- ✅ 流式（SSE）：逐 chunk 透传，结束时客户端收到 `data: [DONE]\n\n`

---

## 2. Image Generation（文生图）

### POST /v1/images/generations

**客户端 → 网关**

```http
POST /v1/images/generations HTTP/1.1
Content-Type: application/json

{
  "model": "agnes-image-2.1-flash",
  "prompt": "a cute orange cat",
  "n": 1,
  "size": "1024x1024",
  "response_format": "url"
}
```

**网关 → Provider**（`/images/generations`）

```http
POST /v1/images/generations HTTP/1.1
Host: apihub.agnes-ai.com
Authorization: Bearer sk-xxx

{
  "model": "agnes-image-2.1-flash",
  "prompt": "a cute orange cat",
  "n": 1,
  "size": "1024x1024",
  "extra_body": {                   ← response_format 移到 extra_body
    "response_format": "url"        ← Agnes 不允许 response_format 在顶层
  }
}
```

**网关转换**：

| 客户端字段 | 转换 |
|-----------|------|
| `response_format` | 移入 `extra_body.response_format` |
| `size` | 原样转发（Agnes 支持 `1024x1024`、`1024x768` 等） |
| `prompt` | 原样转发 |
| 其他字段 | 原样转发 |

**Provider → 网关 → 客户端**

```json
{
  "created": 1780000000,
  "data": [
    {"url": "https://storage.googleapis.com/.../image.png", "b64_json": null}
  ]
}
```

**网关处理**：

- ✅ 直接透传 Agnes 的 OpenAI 兼容响应
- ⚠️ Agnes 要求 `response_format` 在 `extra_body` 中（不是顶层），网关已自动处理

---

## 3. Image Edit（图生图）

### POST /v1/images/edits

**客户端 → 网关**（OpenAI 标准）

```http
POST /v1/images/edits HTTP/1.1
Content-Type: application/json

{
  "model": "agnes-image-2.1-flash",
  "image": "https://example.com/photo.png",   ← 也支持 data: URL、纯 base64
  "prompt": "Change the sky to sunset",
  "n": 1,
  "size": "1024x1024",
  "response_format": "url",
  "mask": "data:image/png;base64,..."        ← 可选，Agnes 不支持，会忽略
}
```

**关键差异：OpenAI `/v1/images/edits` vs Agnes 实际端点**

| 维度 | OpenAI | Agnes |
|------|--------|-------|
| 端点 | `POST /v1/images/edits` | ❌ 不存在；图生图走 `POST /v1/images/generations` |
| 输入图片 | JSON 中的 `image` 字段 / multipart 文件 | 顶层 `image` 数组（URL / Data URI） |
| `response_format` | 顶层 | 必须在 `extra_body.response_format` |
| Mask | 顶层 `mask` | Agnes 不支持 |

**网关 → Provider**（实际调用 `/images/generations`）

```http
POST /v1/images/generations HTTP/1.1
Host: apihub.agnes-ai.com
Authorization: Bearer sk-xxx

{
  "model": "agnes-image-2.1-flash",
  "prompt": "Change the sky to sunset",
  "size": "1024x1024",
  "image": [                                     ← 关键：image 是数组
    "https://example.com/photo.png"              ← 透传
  ],
  "extra_body": {
    "response_format": "url"
  }
}
```

**输入归一化**：

| 客户端传入 | 网关处理 | 转发给 Agnes（`image` 数组元素） |
|-----------|---------|--------------------------------|
| `"https://..."` URL | 直接转发 | URL 原样 |
| `"data:image/png;base64,..."` Data URI | 转为临时 URL | `http://<gateway>/_temp/images/<hash>` |
| 纯 base64（≥64 字符、仅 base64 字符） | 自动包装为 Data URI，再转为临时 URL | `http://<gateway>/_temp/images/<hash>` |
| 顶层 `mask` 字段 | 追加到 `image` 数组 | `mask` 原文（Agnes 不识别，行为未定义） |

> 注意：图生图的 Data URI / base64 输入会通过 `base64Handler` 转换为临时 URL（与图生视频行为一致），确保上游 Provider 可通过 HTTP GET 拉取。

**多图上传**（multipart/form-data）：

网关支持通过 `multipart/form-data` 一次上传最多 16 张图片：

| 文件数量 | 处理方式 |
|---------|---------|
| 1 个 `image` 文件 | 兼容单图路径，设置 `req.Image` |
| 2–16 个 `image` 文件 | 填充 `req.Images` 数组，同时设置 `req.Image` 为第一张图（向后兼容） |
| >16 个 | 返回错误："too many images: maximum 16 allowed" |

JSON 模式仅支持单图（`image` 字段）。

**`quality` 参数**：

图生图请求中的 `quality` 字段会透传给 Agnes（用于 ArcReel SDK 等场景），若客户端未传则不设置。

**Provider → 网关 → 客户端**

```json
{
  "created": 1780000000,
  "data": [
    {"url": "https://storage.googleapis.com/.../edited.png", "b64_json": null}
  ]
}
```

---

## 4. Image Variation

### POST /v1/images/variations

**客户端 → 网关**

```http
POST /v1/images/variations HTTP/1.1
Content-Type: application/json

{
  "image": "https://example.com/photo.png",
  "n": 1,
  "size": "1024x1024"
}
```

**网关处理**：

| 状态 | 说明 |
|------|------|
| ❌ **不支持** | `SupportsImageVariation() = false`，直接返回 400 `unsupported_capability` |
| 原因 | Agnes AI 上游没有 `/v1/images/variations` 端点 |

---

## 5. Video Generation（异步）

### POST /v1/videos/generations

OpenAI 的视频 API 是**异步任务**模式：先创建任务拿到 ID，再轮询。

#### 5.1 文生视频（T2V）

**客户端 → 网关**

```http
POST /v1/videos/generations HTTP/1.1
Content-Type: application/json

{
  "model": "agnes-video-v2.0",
  "input": [
    {"type": "text", "text": "A butterfly in a sunny garden"}
  ]
}
```

**关键差异**

| 字段 | OpenAI 标准 | Agnes 实际 |
|------|------------|----------|
| 端点 | `POST /v1/videos/generations` | `POST /v1/videos` |
| 文本输入 | `input: [{type: "text", text: "..."}]` | `prompt: "..."` |
| 图片输入 | `input: [{type: "image", image: "url/data-uri"}]` | 单图：顶层 `image: "..."`；多图：`extra_body.image: ["...", "..."]` |
| 时长控制 | `duration: "5s"`、`resolution: "..."` | `num_frames: 121`, `frame_rate: 24`, `width: 1152`, `height: 768` |
| Keyframe 模式 | 无 | `extra_body.mode: "keyframes"` |
| `instructions` | 顶层字段 | 拼接到 `prompt` 前面 |

**网关 → Provider**

```http
POST /v1/videos HTTP/1.1
Host: apihub.agnes-ai.com
Authorization: Bearer sk-xxx

{
  "model": "agnes-video-v2.0",
  "prompt": "A butterfly in a sunny garden",   ← 从 input[].text 拼接
  "height": 768,
  "width": 1152,
  "num_frames": 121,
  "frame_rate": 24
}
```

**默认参数**（客户端未指定时）：

| 参数 | 默认值 |
|------|--------|
| `height` | 768 |
| `width` | 1152 |
| `num_frames` | 121（约 5 秒 @ 24fps） |
| `frame_rate` | 24.0 |
| `mode` | （自动检测） |

**参数推导**：

| 客户端 | 转换 |
|--------|------|
| `"size": "1920x1080"` | `width: 1920, height: 1080` |
| `"size": "1024x576"` | `width: 1024, height: 576` |
| `"aspect_ratio": "16:9"` | `width: 1152, height: 768` |
| `"aspect_ratio": "1:1"` | `width: 1024, height: 1024` |
| `"duration": "10"` (秒) | `num_frames: 240`（10 × 24fps），四舍五入到 `8n+1` |
| `"duration": "10"` (秒) | 上限 441 帧（约 18 秒 @ 24fps） |

**`num_frames` 约束处理**：

- 必须满足 `8n + 1`（如 9, 17, 25, ..., 121, 241, 441）
- 上限 441
- 网关自动四舍五入到最近的合法值

**Provider → 网关 → 客户端**

```json
{
  "id": "task_xxx",
  "object": "video",
  "created_at": 1780000000,
  "status": "queued",   // queued → in_progress → completed / failed / expired
  "model": "agnes-video-v2.0"
}
```

#### 5.2 图生视频（I2V）

**客户端 → 网关**

```json
{
  "model": "agnes-video-v2.0",
  "input": [
    {"type": "text", "text": "wind blowing through the flower"},
    {"type": "image", "image": "https://example.com/flower.png"}
  ]
}
```

**输入图片处理**：

| 输入形式 | 网关处理 |
|---------|---------|
| 公网 URL | 透传给 Agnes |
| `data:image/png;base64,...` | 解码后保存到本地，生成临时 URL，Agnes 从该 URL 拉取 |
| 纯 base64 | 先包装为 Data URI，再保存为临时 URL（同上） |

> 详细说明见文档顶部"Base64 / URL 输入兼容"章节。

**网关 → Provider**

```json
{
  "model": "agnes-video-v2.0",
  "prompt": "wind blowing through the flower",
  "image": "https://example.com/flower.png",   ← 顶层 image 字段
  "mode": "ti2vid",                             ← 自动标记
  "height": 768, "width": 1152,
  "num_frames": 121, "frame_rate": 24
}
```

#### 5.3 首尾帧生视频（Keyframe）

**客户端 → 网关**

```json
{
  "model": "agnes-video-v2.0",
  "input": [
    {"type": "text", "text": "smooth transition between keyframes"},
    {"type": "image", "image": "https://example.com/frame1.png"},
    {"type": "image", "image": "https://example.com/frame2.png"}
  ]
}
```

**网关 → Provider**

```json
{
  "model": "agnes-video-v2.0",
  "prompt": "smooth transition between keyframes",
  "extra_body": {
    "image": [
      "https://example.com/frame1.png",
      "https://example.com/frame2.png"
    ],
    "mode": "keyframes"                          ← 自动检测 prompt 含 "keyframe"
  },
  "height": 768, "width": 1152,
  "num_frames": 121, "frame_rate": 24
}
```

**`mode` 自动检测**：

如果 prompt 中包含 `"keyframe"` 或 `"transition between"`，自动设置 `extra_body.mode = "keyframes"`。
否则使用默认多图模式。

### GET /v1/videos/:id（轮询）

**客户端 → 网关**

```http
GET /v1/videos/video_xxx HTTP/1.1
Authorization: Bearer sk-xxx
```

**网关 → Provider**

```http
GET /agnesapi?video_id=video_xxx&model_name=agnes-video-v2.0 HTTP/1.1
Host: apihub.agnes-ai.com
Authorization: Bearer sk-xxx
```

注：Agnes 文档推荐使用新接口 `/agnesapi?video_id=...` 查询视频。旧接口 `/v1/videos/{task_id}` 也可以用 task_id 查询，但网关返回给客户端的是 `video_id`（base64 编码格式），不是 `task_id`，所以统一走新接口。

**Provider → 网关**

```json
{
  "id": "task_xxx",
  "video_id": "video_xxx",
  "object": "video",
  "model": "agnes-video-v2.0",
  "status": "completed",
  "progress": 100,
  "created_at": 1780000000,
  "started_at": 1780000000,
  "completed_at": 1780000120,
  "seconds": "5.0",
  "size": "1152x768",
  "video_url": "https://storage.googleapis.com/.../video.mp4"
}
```

**网关 → 客户端**

```json
{
  "id": "video_xxx",
  "object": "video",
  "created_at": 1780000000,
  "status": "completed",
  "model": "agnes-video-2.0-flash",
  "output": [
    {
      "type": "url",
      "url": "https://storage.googleapis.com/.../video.mp4",
      "duration": 5.0,
      "mime_type": "video/mp4"
    }
  ]
}
```

**字段映射**：

| Provider 字段 | 客户端字段 | 转换 |
|--------------|-----------|------|
| `video_id` 或 `task_id` 或 `id` | `id` | 优先 `video_id`（新接口 ID） |
| `model` | `model` | 透传（OpenAI 模型名） |
| `status` | `status` | 归一化为 OpenAI 标准值：`in_progress`/`processing`/`running` → `in_progress`，`succeeded`/`success` → `completed`，`expired` → `expired` |
| `video_url` | `output[0].url` | **新字段**，优先使用 |
| `remixed_from_video_id` | `output[0].url` | 兼容旧字段，仅当 URL 以 `http(s)://` 开头时填充 |
| `seconds` | `output[0].duration` | 字符串转浮点 |

**关键 trick**：

⚠️ Agnes 创建响应里 `remixed_from_video_id` 是 **LiteLLM 编码的占位符**（形如 `video_<base64>`），**不是真实 URL**。网关通过检查 URL 前缀来过滤掉这种占位符，避免在 `output.url` 中返回假 URL。新接口用 `video_url` 字段直接返回真实 URL。

### GET /v1/videos/:id/content（下载视频）

兼容 OpenAI Python SDK 的 `client.videos.download_content()` 方法。

**客户端 → 网关**

```http
GET /v1/videos/video_xxx/content HTTP/1.1
Authorization: Bearer sk-xxx
```

**网关处理**：

1. 调用 `VideoRetrieve` 获取视频状态和输出 URL
2. 视频未完成或无输出 URL → 返回 **400** `video_not_ready`
3. 视频已完成 → 返回 **302** 重定向到实际视频 URL（不做代理，避免网关传输大文件）
4. 视频 ID 不存在 → 返回 **404**

**网关 → 客户端**（视频已完成）

```http
HTTP/1.1 302 Found
Location: https://storage.googleapis.com/.../video.mp4
```

**网关 → 客户端**（视频未完成）

```json
{
  "error": {
    "message": "video is not ready yet, current status: in_progress",
    "type": "invalid_request_error",
    "code": "video_not_ready"
  }
}
```

#### Multipart 表单支持（视频生成）

`POST /v1/videos/generations` 同时支持 `application/json` 和 `multipart/form-data` 两种 Content-Type。multipart 模式兼容 OpenAI Python SDK 的 `client.videos.create()` 方法。

**Multipart 字段映射**：

| 表单字段 | 类型 | 映射到 |
|---------|------|--------|
| `model` | string | `req.Model` |
| `prompt` | string | `req.Instructions`（指令） |
| `seconds` | string | `req.Duration`（时长） |
| `size` | string | `req.Size`（分辨率） |
| `input_reference` | file(s) | 每个 file 读取后 base64 编码，追加到 `req.Input` 作为 `{type: "image"}` |

> JSON 模式使用 `input` 数组（含 `type: "text"` 和 `type: "image"` 项），multipart 模式使用独立的表单字段。

---

## 6. Models

### GET /v1/models

**客户端 → 网关**

```http
GET /v1/models
```

**网关响应**（直接由配置生成，未调用 Provider）

```json
{
  "object": "list",
  "data": [
    {"id": "agnes-1.5-flash", "object": "model", "created": 1700000000, "owned_by": "agnes"},
    {"id": "agnes-2.0-flash", "object": "model", "created": 1710000000, "owned_by": "agnes"},
    ...
  ]
}
```

**无需鉴权**（任何客户端都可调用）。

---

## 转换总览

### 请求转换矩阵

| OpenAI 字段 | 实际处理 |
|------------|---------|
| `model` | 重写为 `provider_model`（按 `config.yaml` 映射） |
| `response_format`（图生图） | 移入 `extra_body.response_format` |
| `quality`（图生图） | 透传给 Provider（用于 ArcReel SDK） |
| `size`（图片） | 原样转发 |
| `size`（视频） | 拆分为 `width`、`height` |
| `duration`（视频） | 换算为 `num_frames`（×24fps），四舍五入到 `8n+1` |
| `input[].text` | 拼接到 `prompt` |
| `input[].image` | 单图：`image`；多图：`extra_body.image`；`extra_body.mode=keyframes`（自动） |
| `image`（图生图 JSON） | 放入 `image` 数组；Data URI / base64 转为临时 URL |
| `mask`（图生图） | 追加到 `image` 数组（Agnes 不识别） |
| `stream` | 透传（影响 SSE 行为） |
| `n`、`temperature` 等 | 透传 |

### 响应字段重命名

| 上游字段 | 客户端字段 |
|---------|----------|
| `remixed_from_video_id` | `output[0].url`（仅当是真实 URL） |
| `task_id` / `id` | `id` |
| `in_progress` / `processing` / `running` | `in_progress`（归一化为 OpenAI 标准值） |
| `succeeded` / `success` | `completed` |
| `expired` | `expired` |
| `seconds`（字符串） | `output[0].duration`（浮点） |

### 客户端无法感知的网关行为

1. **临时图片保存**（图生图和图生视频均触发）：任何 base64 输入（Data URI 或纯 base64）都被解码后存到本地 `/_temp/images/<hash>`，生成 5 分钟过期的临时 URL，Agnes worker 通过 HTTP 拉取后再删除
2. **请求上下文跟踪**：每个请求生成唯一 `RequestContext`，请求结束立即清理临时图片
3. **错误聚合**：所有 Provider 错误统一封装为 `provider.ProviderError`，携带原始 HTTP 状态码
4. **错误脱敏**：返回给客户端的错误消息**不包含**上游原始 body（避免泄漏 request ID 等内部信息），完整 body 仅截断到 200 字节后写入服务端日志
5. **日志脱敏**：
   - 关闭 stacktrace（不暴露源码路径）
   - 关闭 caller info（不暴露本地绝对路径）
   - 上游错误 body 截断到 200 字节
6. **CORS**：允许所有来源
7. **结构化日志**：每个请求记录 method、path、latency、body_size、client IP

---

## Provider 抽象接口

```go
// model.Provider 是所有 Provider 必须实现的接口
type Provider interface {
    Name() string
    SupportedModels() []ModelInfo

    ChatCompletion(ctx, req) (*Response, error)
    ChatCompletionStream(ctx, req) (io.ReadCloser, error)
    ImageGeneration(ctx, req) (*Response, error)
    ImageEdit(ctx, req) (*Response, error)        // 图生图（图生图时实现此方法）
    ImageVariation(ctx, req) (*Response, error)   // 暂未实现
    VideoGeneration(ctx, req) (*Response, error)  // 异步任务
    VideoRetrieve(ctx, videoID) (*Response, error) // 状态轮询

    SupportsChat() bool
    SupportsImageGeneration() bool
    SupportsImageEdit() bool
    SupportsImageVariation() bool
    SupportsVideoGeneration() bool
}
```

**当前实现**：`internal/provider/agnes/` — 仅 Agnes AI Provider。

---

## 7. Anthropic Messages API 兼容

### POST /v1/messages

网关在 OpenAI 兼容接口之外，额外提供 Anthropic Messages API 兼容接口。客户端使用 Anthropic SDK 时只需将 `base_url` 指向网关即可。

#### 认证

| 阶段 | 行为 |
|------|------|
| 客户端 → 网关 | `x-api-key: <key>` 或 `Authorization: Bearer <key>` |
| 网关 → Provider | **原样透传** API Key |
| 客户端未传 | 网关返回 **401** Anthropic 格式错误 |

#### 非流式请求

**客户端 → 网关**（Anthropic 格式）

```http
POST /v1/messages HTTP/1.1
Host: gateway.local
x-api-key: sk-xxx
Content-Type: application/json
anthropic-version: 2023-06-01

{
  "model": "agnes-2.0-flash",
  "max_tokens": 1024,
  "system": "You are a helpful assistant",
  "messages": [
    {"role": "user", "content": "Hello"}
  ],
  "temperature": 0.7
}
```

**网关内部处理**

1. 将 Anthropic 请求转换为 OpenAI `ChatCompletionRequest`（见下方转换矩阵）
2. 通过 Registry 路由到对应 Provider（与 OpenAI 接口共用）
3. 调用 `Provider.ChatCompletion()`
4. 将 OpenAI 响应转换回 Anthropic 格式

**网关 → 客户端**

```json
{
  "id": "msg_chatcmpl-xxx",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "text", "text": "Hello! How can I help you?"}],
  "model": "agnes-2.0-flash",
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {"input_tokens": 25, "output_tokens": 8}
}
```

#### 流式请求（SSE）

**客户端 → 网关**

```json
{
  "model": "agnes-2.0-flash",
  "max_tokens": 256,
  "stream": true,
  "messages": [{"role": "user", "content": "Hello"}]
}
```

**网关内部处理**

1. 转换为 OpenAI 格式，设置 `stream=true`
2. 调用 `Provider.ChatCompletionStream()` 获取 OpenAI SSE 流
3. 通过状态机将 OpenAI SSE 事件转换为 Anthropic SSE 事件

**网关 → 客户端**（Anthropic SSE 格式）

```
event: message_start
data: {"type":"message_start","message":{"id":"msg_xxx","type":"message","role":"assistant","content":[],"model":"agnes-2.0-flash","stop_reason":"","usage":{"input_tokens":25,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":8}}

event: message_stop
data: {"type":"message_stop"}
```

#### 请求字段转换矩阵

| Anthropic 字段 | OpenAI 字段 | 转换逻辑 |
|---------------|-------------|----------|
| `model` | `model` | 直接映射，通过 Registry 路由 |
| `system`（string） | 在 `messages` 前插入 `role:"system"` 消息 | 直接映射 |
| `system`（[]block） | 在 `messages` 前插入 `role:"system"` 消息 | 拼接所有 text block |
| `messages[].content`（string） | `content` 字符串 | 直接映射 |
| `messages[].content`（[]text block） | `content` 数组 | 直接映射 |
| `messages[].content`（[]image base64） | `[{type:"image_url", image_url:{url:"data:...;base64,..."}}]` | 组装 data URI |
| `messages[].content`（[]image url） | `[{type:"image_url", image_url:{url:"..."}}]` | 直接映射 |
| `messages[].content`（[]tool_use） | `tool_calls` 数组 | 转为 OpenAI ToolCall |
| `messages[].content`（[]tool_result） | 独立的 `role:"tool"` 消息 | 拆分为单独消息 |
| `max_tokens` | `max_tokens` | 直接映射 |
| `temperature` | `temperature` | 直接映射 |
| `top_p` | `top_p` | 直接映射 |
| `top_k` | `Extra["top_k"]` | 透传给 Agnes |
| `stop_sequences` | `stop` | `json.Marshal([]string)` |
| `tools` | `tools` | `input_schema` → `parameters` |
| `tool_choice` "auto" | `tool_choice` | 直接映射 |
| `tool_choice` "any" | `tool_choice` `{"type":"required"}` | Anthropic "any" ≈ OpenAI "required" |
| `tool_choice` "none" | `tool_choice` `{"type":"none"}` | 直接映射 |
| `tool_choice` `{"type":"tool","name":"X"}` | `{"type":"function","function":{"name":"X"}}` | 类型映射 |
| `thinking` | `Extra["thinking"]` | 透传给 Agnes（原生支持） |
| `metadata.user_id` | `user` | 直接映射 |

#### 响应字段转换矩阵

| OpenAI 字段 | Anthropic 字段 | 转换逻辑 |
|-------------|---------------|----------|
| `id` | `id` | 不以 `msg_` 开头时加前缀 |
| `choices[0].message.content` | `content: [{type:"text", text:...}]` | 包装为数组 |
| `choices[0].message.tool_calls` | 额外 `content: [{type:"tool_use", id, name, input}]` | 转换 |
| `choices[0].finish_reason` "stop" | `stop_reason` "end_turn" | 映射表 |
| `choices[0].finish_reason` "length" | `stop_reason` "max_tokens" | 映射表 |
| `choices[0].finish_reason` "tool_calls" | `stop_reason` "tool_use" | 映射表 |
| `usage.prompt_tokens` | `usage.input_tokens` | 直接映射 |
| `usage.completion_tokens` | `usage.output_tokens` | 直接映射 |

#### 流式 SSE 事件转换

| OpenAI SSE 事件 | Anthropic SSE 事件 |
|-----------------|-------------------|
| 第一个 chunk（含 role） | `event: message_start` + `event: content_block_start` |
| `delta.content` 非空 | `event: content_block_delta`（`type: "text_delta"`） |
| `delta.tool_calls` | `event: content_block_start`（`type: "tool_use"`）+ `event: content_block_delta`（`type: "input_json_delta"`） |
| `finish_reason` 非空 | `event: content_block_stop` + `event: message_delta` + `event: message_stop` |
| `data: [DONE]` | 兜底结束（正常由 `finish_reason` 触发） |

#### 错误响应

Anthropic 接口返回 Anthropic 格式的错误：

```json
{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "message": "model: Field required"
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

#### Thinking 模式

Agnes 原生支持 Thinking 模式。两种方式：

**Anthropic 格式**（通过 `/v1/messages`）：

```json
{
  "model": "agnes-2.0-flash",
  "max_tokens": 4096,
  "thinking": {"type": "enabled", "budget_tokens": 2048},
  "messages": [{"role": "user", "content": "写一段排序代码"}]
}
```

**OpenAI 格式**（通过 `/v1/chat/completions`）：

```json
{
  "model": "agnes-2.0-flash",
  "max_tokens": 4096,
  "chat_template_kwargs": {"enable_thinking": true},
  "messages": [{"role": "user", "content": "写一段排序代码"}]
}
```

两种格式均透传给 Agnes API。
