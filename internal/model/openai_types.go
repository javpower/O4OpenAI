package model

import "encoding/json"

// ============================================================
// OpenAI-compatible request/response types
// Complete types covering Chat, Images, Videos, Models
// ============================================================

// ------------------- Chat Completions -------------------

// ChatCompletionRequest represents an OpenAI chat completion request
type ChatCompletionRequest struct {
	Model               string                           `json:"model"`
	Messages            []ChatCompletionMessageParam     `json:"messages"`
	Temperature         *float64                         `json:"temperature,omitempty"`
	TopP                *float64                         `json:"top_p,omitempty"`
	N                   *int                             `json:"n,omitempty"`
	MaxTokens           *int                             `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                             `json:"max_completion_tokens,omitempty"`
	Stream              bool                             `json:"stream,omitempty"`
	StreamOptions       *ChatCompletionStreamOptions     `json:"stream_options,omitempty"`
	Stop                json.RawMessage                  `json:"stop,omitempty"` // string or []string
	FrequencyPenalty    *float64                         `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64                         `json:"presence_penalty,omitempty"`
	Seed                *int64                           `json:"seed,omitempty"`
	Logprobs            *bool                            `json:"logprobs,omitempty"`
	TopLogprobs         *int                             `json:"top_logprobs,omitempty"`
	ResponseFormat      json.RawMessage                  `json:"response_format,omitempty"`
	Tools               []Tool                           `json:"tools,omitempty"`
	ToolChoice          json.RawMessage                  `json:"tool_choice,omitempty"`
	Modalities          []string                         `json:"modalities,omitempty"`
	Audio               *ChatCompletionAudioParam        `json:"audio,omitempty"`
	Prediction          *ChatCompletionPredictionContent `json:"prediction,omitempty"`
	ServiceTier         string                           `json:"service_tier,omitempty"`
	User                string                           `json:"user,omitempty"`

	// Agnes-specific extensions (transparent passthrough)
	ChatTemplateKwargs json.RawMessage `json:"chat_template_kwargs,omitempty"` // {"enable_thinking": true}
	Thinking           json.RawMessage `json:"thinking,omitempty"`             // {"type":"enabled","budget_tokens":2048}

	// Provider-specific extra params passed through
	Extra map[string]interface{} `json:"-"`
}

// ChatCompletionMessageParam represents a message in the chat completion request
type ChatCompletionMessageParam struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"` // string or []ContentPart
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Refusal    string          `json:"refusal,omitempty"`

	// For assistant messages with audio
	Audio *ChatCompletionAudio `json:"audio,omitempty"`
}

// ChatCompletionContentPart represents a content part in a message
type ChatCompletionContentPart struct {
	Type       string       `json:"type"`
	Text       string       `json:"text,omitempty"`
	ImageURL   *ImageURL    `json:"image_url,omitempty"`
	InputAudio *InputAudio  `json:"input_audio,omitempty"`
	File       *FileContent `json:"file,omitempty"`
	Refusal    string       `json:"refusal,omitempty"`
}

// ImageURL represents an image URL content part
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // auto, low, high
}

// InputAudio represents an input audio content part
type InputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"` // wav, mp3
}

// FileContent represents a file content part
type FileContent struct {
	FileData string `json:"file_data,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// ChatCompletionAudioParam represents audio output parameters
type ChatCompletionAudioParam struct {
	Format string      `json:"format"` // wav, mp3, flac, opus, pcm16, aac
	Voice  interface{} `json:"voice"`  // string or object {id: "..."}
}

// ChatCompletionAudio represents audio response data
type ChatCompletionAudio struct {
	ID         string `json:"id"`
	Data       string `json:"data,omitempty"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

// ChatCompletionPredictionContent represents predicted output content
type ChatCompletionPredictionContent struct {
	Type    string          `json:"type"` // always "content"
	Content json.RawMessage `json:"content"`
}

// ChatCompletionStreamOptions represents streaming options
type ChatCompletionStreamOptions struct {
	IncludeUsage       *bool `json:"include_usage,omitempty"`
	IncludeObfuscation *bool `json:"include_obfuscation,omitempty"`
}

// Tool represents a tool definition
type Tool struct {
	Type     string       `json:"type"` // function
	Function ToolFunction `json:"function"`
}

// ToolFunction represents a function definition
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // tool definition (request)
	Arguments   string          `json:"arguments,omitempty"`  // tool call result (response) — JSON string
}

// ToolCall represents a tool call in a message
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // function
	Function ToolFunction `json:"function"`
}

// ChatCompletionResponse represents an OpenAI chat completion response
type ChatCompletionResponse struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"` // "chat.completion"
	Created           int64                  `json:"created"`
	Model             string                 `json:"model"`
	Choices           []ChatCompletionChoice `json:"choices"`
	Usage             *CompletionUsage       `json:"usage,omitempty"`
	ServiceTier       string                 `json:"service_tier,omitempty"`
	SystemFingerprint string                 `json:"system_fingerprint,omitempty"`
}

// ChatCompletionChoice represents a choice in the response
type ChatCompletionChoice struct {
	Index        int                     `json:"index"`
	Message      ChatCompletionMessage   `json:"message"`
	FinishReason string                  `json:"finish_reason"`
	Logprobs     *ChatCompletionLogprobs `json:"logprobs,omitempty"`
}

// ChatCompletionMessage represents the assistant's message in the response
type ChatCompletionMessage struct {
	Role         string               `json:"role"`
	Content      json.RawMessage      `json:"content,omitempty"`
	Refusal      string               `json:"refusal,omitempty"`
	ToolCalls    []ToolCall           `json:"tool_calls,omitempty"`
	Audio        *ChatCompletionAudio `json:"audio,omitempty"`
	Annotations  []Annotation         `json:"annotations,omitempty"`
	FunctionCall *FunctionCall        `json:"function_call,omitempty"`
}

// Annotation represents a URL citation annotation
type Annotation struct {
	Type        string       `json:"type"`
	URLCitation *URLCitation `json:"url_citation,omitempty"`
}

// URLCitation represents a URL citation
type URLCitation struct {
	EndIndex   int    `json:"end_index"`
	StartIndex int    `json:"start_index"`
	Title      string `json:"title"`
	URL        string `json:"url"`
}

// FunctionCall represents a deprecated function call
type FunctionCall struct {
	Arguments string `json:"arguments"`
	Name      string `json:"name"`
}

// ChatCompletionLogprobs represents log probability information
type ChatCompletionLogprobs struct {
	Content []TokenLogprob `json:"content,omitempty"`
	Refusal []TokenLogprob `json:"refusal,omitempty"`
}

// TokenLogprob represents a token's log probability
type TokenLogprob struct {
	Token       string       `json:"token"`
	Logprob     float64      `json:"logprob"`
	Bytes       []int        `json:"bytes,omitempty"`
	TopLogprobs []TopLogprob `json:"top_logprobs"`
}

// TopLogprob represents a top log probability entry
type TopLogprob struct {
	Token   string  `json:"token"`
	Logprob float64 `json:"logprob"`
	Bytes   []int   `json:"bytes,omitempty"`
}

// CompletionUsage represents token usage statistics
type CompletionUsage struct {
	PromptTokens            int           `json:"prompt_tokens"`
	CompletionTokens        int           `json:"completion_tokens"`
	TotalTokens             int           `json:"total_tokens"`
	PromptTokensDetails     *TokenDetails `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *TokenDetails `json:"completion_tokens_details,omitempty"`
}

// TokenDetails represents detailed token counts
type TokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
	AudioTokens  int `json:"audio_tokens,omitempty"`
	ImageTokens  int `json:"image_tokens,omitempty"`
	TextTokens   int `json:"text_tokens,omitempty"`
}

// ------------------- Chat Completion Streaming -------------------

// ChatCompletionChunk represents a streamed chunk of a chat completion response
type ChatCompletionChunk struct {
	ID                string                      `json:"id"`
	Object            string                      `json:"object"` // "chat.completion.chunk"
	Created           int64                       `json:"created"`
	Model             string                      `json:"model"`
	Choices           []ChatCompletionChunkChoice `json:"choices"`
	Usage             *CompletionUsage            `json:"usage,omitempty"`
	ServiceTier       string                      `json:"service_tier,omitempty"`
	SystemFingerprint string                      `json:"system_fingerprint,omitempty"`
}

// ChatCompletionChunkChoice represents a choice in a streamed chunk
type ChatCompletionChunkChoice struct {
	Index        int                      `json:"index"`
	Delta        ChatCompletionChunkDelta `json:"delta"`
	FinishReason *string                  `json:"finish_reason"`
	Logprobs     *ChatCompletionLogprobs  `json:"logprobs,omitempty"`
}

// ChatCompletionChunkDelta represents the delta in a streamed chunk
type ChatCompletionChunkDelta struct {
	Role         string        `json:"role,omitempty"`
	Content      *string       `json:"content,omitempty"`
	Refusal      *string       `json:"refusal,omitempty"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
	FunctionCall *FunctionCall `json:"function_call,omitempty"`
}

// ------------------- Image Generation -------------------

// ImageGenerationRequest represents an OpenAI image generation request
type ImageGenerationRequest struct {
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt"`
	N              *int   `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`            // 1024x1024, 1024x1536, 1536x1024
	Quality        string `json:"quality,omitempty"`         // low, medium, high, auto, standard, hd
	ResponseFormat string `json:"response_format,omitempty"` // url, b64_json
	Style          string `json:"style,omitempty"`           // vivid, natural
	User           string `json:"user,omitempty"`
	Background     string `json:"background,omitempty"`    // transparent, opaque, auto
	OutputFormat   string `json:"output_format,omitempty"` // png, webp, jpeg
	Moderation     string `json:"moderation,omitempty"`    // auto, low
	// dall-e-2 specific
	Mask string `json:"mask,omitempty"` // base64 or file
}

// ImageEditRequest represents an OpenAI image edit request
type ImageEditRequest struct {
	Model          string   `json:"model,omitempty"`
	Image          string   `json:"image,omitempty"` // single image: base64, URL, or Data URI (backward compat for JSON mode)
	Images         []string `json:"-"`               // multiple images from multipart uploads (not JSON-serialized)
	Prompt         string   `json:"prompt"`
	Mask           string   `json:"mask,omitempty"` // base64 or file content
	N              *int     `json:"n,omitempty"`
	Size           string   `json:"size,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
	User           string   `json:"user,omitempty"`
	Background     string   `json:"background,omitempty"`
	OutputFormat   string   `json:"output_format,omitempty"`
	Quality        string   `json:"quality,omitempty"`
}

// ImageVariationRequest represents an OpenAI image variation request
type ImageVariationRequest struct {
	Model          string `json:"model,omitempty"`
	Image          string `json:"image"` // base64 or file content
	N              *int   `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	User           string `json:"user,omitempty"`
}

// ImageResponse represents an OpenAI image generation response
type ImageResponse struct {
	Created      int64       `json:"created"`
	Data         []ImageData `json:"data"`
	Background   string      `json:"background,omitempty"`
	OutputFormat string      `json:"output_format,omitempty"`
	Quality      string      `json:"quality,omitempty"`
	Size         string      `json:"size,omitempty"`
	Usage        *ImageUsage `json:"usage,omitempty"`
}

// ImageData represents a single image in the response
type ImageData struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ImageUsage represents token usage for image generation
type ImageUsage struct {
	InputTokens         int                `json:"input_tokens"`
	InputTokensDetails  *ImageTokenDetails `json:"input_tokens_details,omitempty"`
	OutputTokens        int                `json:"output_tokens"`
	OutputTokensDetails *ImageTokenDetails `json:"output_tokens_details,omitempty"`
	TotalTokens         int                `json:"total_tokens"`
}

// ImageTokenDetails represents token details for image generation
type ImageTokenDetails struct {
	ImageTokens int `json:"image_tokens"`
	TextTokens  int `json:"text_tokens"`
}

// ------------------- Video Generation -------------------

// VideoGenerationRequest represents an OpenAI video generation request
type VideoGenerationRequest struct {
	Model        string           `json:"model"`
	Input        []VideoInputItem `json:"input"`
	Instructions string           `json:"instructions,omitempty"`
	N            *int             `json:"n,omitempty"`
	Size         string           `json:"size,omitempty"`
	Duration     string           `json:"duration,omitempty"`
	Resolution   string           `json:"resolution,omitempty"`
	AspectRatio  string           `json:"aspect_ratio,omitempty"`
	User         string           `json:"user,omitempty"`
}

// VideoInputItem represents an input item for video generation
type VideoInputItem struct {
	Type  string `json:"type"` // text, image, video
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
	Video string `json:"video,omitempty"`
}

// VideoResponse represents an OpenAI video generation response
type VideoResponse struct {
	ID        string        `json:"id"`
	Object    string        `json:"object"`
	CreatedAt int64         `json:"created_at"`
	Status    string        `json:"status"` // processing, completed, failed
	Model     string        `json:"model"`
	Output    []VideoOutput `json:"output,omitempty"`
	Error     *VideoError   `json:"error,omitempty"`
	Usage     *VideoUsage   `json:"usage,omitempty"`
}

// VideoOutput represents video output data
type VideoOutput struct {
	Type     string  `json:"type"` // url, base64
	URL      string  `json:"url,omitempty"`
	Content  string  `json:"content,omitempty"` // base64 content
	Duration float64 `json:"duration,omitempty"`
	MimeType string  `json:"mime_type,omitempty"`
}

// VideoError represents a video generation error
type VideoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// VideoUsage represents video generation usage
type VideoUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ------------------- Models -------------------

// ModelListResponse represents the response for listing models
type ModelListResponse struct {
	Object string      `json:"object"` // "list"
	Data   []ModelInfo `json:"data"`
}

// ModelInfo represents a model in the models list
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"` // "model"
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ------------------- Error -------------------

// ErrorResponse represents an OpenAI error response
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail represents error details
type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
}
