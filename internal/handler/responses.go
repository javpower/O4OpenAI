package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/o4openai/internal/model"
	"github.com/o4openai/internal/provider"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// ResponsesHandler implements OpenAI Responses API compatibility on top of Chat.
type ResponsesHandler struct {
	registry       *provider.Registry
	logger         *zap.Logger
	base64Handler  *utils.Base64Handler
	forcedProvider string
}

func NewResponsesHandler(registry *provider.Registry, base64Handler *utils.Base64Handler, logger *zap.Logger, forcedProvider string) *ResponsesHandler {
	return &ResponsesHandler{
		registry:       registry,
		logger:         logger,
		base64Handler:  base64Handler,
		forcedProvider: forcedProvider,
	}
}

func (h *ResponsesHandler) HandleCreate(c *gin.Context) {
	var req model.ResponsesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
				Code:    "invalid_json",
			},
		})
		return
	}

	if req.Model == "" {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: "model: Field required",
				Type:    "invalid_request_error",
				Code:    "invalid_request",
				Param:   "model",
			},
		})
		return
	}

	var p model.Provider
	var err error
	if h.forcedProvider != "" {
		p, err = h.registry.GetProvider(h.forcedProvider)
	} else {
		p, err = h.registry.GetProviderForModel(req.Model)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Model %q not found. Available models can be listed via GET /v1/models", req.Model),
				Type:    "invalid_request_error",
				Code:    "model_not_found",
				Param:   "model",
			},
		})
		return
	}

	if !p.SupportsChat() {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Provider %q does not support chat completions", p.Name()),
				Type:    "invalid_request_error",
				Code:    "unsupported_capability",
			},
		})
		return
	}

	chatReq, err := responsesRequestToChatRequest(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Invalid input: %v", err),
				Type:    "invalid_request_error",
				Code:    "invalid_request",
				Param:   "input",
			},
		})
		return
	}

	if req.Stream {
		h.handleStream(c, p, &req, chatReq)
	} else {
		h.handleNonStream(c, p, &req, chatReq)
	}
}

func (h *ResponsesHandler) handleNonStream(c *gin.Context, p model.Provider, req *model.ResponsesRequest, chatReq *model.ChatCompletionRequest) {
	reqCtx := utils.NewRequestContext()
	ctx := ctxWithKey(c)
	ctx = utils.WithRequestContext(ctx, reqCtx)

	resp, err := p.ChatCompletion(ctx, chatReq)
	if h.base64Handler != nil {
		h.base64Handler.CleanupRequest(reqCtx)
	}
	if err != nil {
		h.logger.Error("Responses chat completion failed", zap.String("model", req.Model), zap.Error(err))
		respondProviderError(c, "Responses create", err)
		return
	}

	c.JSON(http.StatusOK, chatCompletionToResponsesResult(resp, req.Model))
}

func (h *ResponsesHandler) handleStream(c *gin.Context, p model.Provider, req *model.ResponsesRequest, chatReq *model.ChatCompletionRequest) {
	reqCtx := utils.NewRequestContext()
	ctx := ctxWithKey(c)
	ctx = utils.WithRequestContext(ctx, reqCtx)

	chatReq.Stream = true
	body, err := p.ChatCompletionStream(ctx, chatReq)
	if err != nil {
		if h.base64Handler != nil {
			h.base64Handler.CleanupRequest(reqCtx)
		}
		h.logger.Error("Responses stream failed", zap.String("model", req.Model), zap.Error(err))
		respondProviderError(c, "Responses stream", err)
		return
	}
	defer body.Close()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	responseID := generateResponseID()
	now := time.Now().Unix()
	textIndex := 0

	writeEvent := func(eventType string, payload interface{}) {
		data, err := json.Marshal(payload)
		if err != nil {
			h.logger.Error("Failed to marshal Responses SSE event", zap.Error(err))
			return
		}
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, string(data))
		c.Writer.Flush()
	}

	writeEvent("response.created", map[string]interface{}{
		"type": "response.created",
		"response": map[string]interface{}{
			"id":         responseID,
			"object":     "response",
			"created_at": now,
			"status":     "in_progress",
			"model":      req.Model,
			"output":     []interface{}{},
		},
	})

	writeEvent("response.in_progress", map[string]interface{}{
		"type": "response.in_progress",
		"response": map[string]interface{}{
			"id":         responseID,
			"object":     "response",
			"created_at": now,
			"status":     "in_progress",
			"model":      req.Model,
		},
	})

	itemID := responseID + "-message-0"
	writeEvent("response.output_item.added", map[string]interface{}{
		"type":  "response.output_item.added",
		"index": 0,
		"output_item": map[string]interface{}{
			"id":      itemID,
			"type":    "message",
			"role":    "assistant",
			"status":  "in_progress",
			"content": []interface{}{},
		},
	})

	writeEvent("response.content_part.added", map[string]interface{}{
		"type":          "response.content_part.added",
		"index":         0,
		"output_index":  0,
		"content_index": 0,
		"part": map[string]interface{}{
			"type": "output_text",
			"text": "",
		},
	})

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		finalUsage *model.ResponsesUsage
	)

	for scanner.Scan() {
		select {
		case <-c.Request.Context().Done():
			break
		default:
		}

		line := scanner.Text()
		if len(line) <= 6 || line[:6] != "data: " {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			break
		}

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if u, ok := chunk["usage"].(map[string]interface{}); ok {
			finalUsage = &model.ResponsesUsage{}
			if v, ok := u["prompt_tokens"].(float64); ok {
				finalUsage.InputTokens = int(v)
			}
			if v, ok := u["completion_tokens"].(float64); ok {
				finalUsage.OutputTokens = int(v)
			}
			if v, ok := u["total_tokens"].(float64); ok {
				finalUsage.TotalTokens = int(v)
			}
		}

		if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if delta, ok := choice["delta"].(map[string]interface{}); ok {
					if content, ok := delta["content"].(string); ok && content != "" {
						writeEvent("response.output_text.delta", map[string]interface{}{
							"type":          "response.output_text.delta",
							"index":         textIndex,
							"output_index":  0,
							"content_index": 0,
							"delta":         content,
						})
						textIndex++
					}
				}
				if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
					_ = fr
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		h.logger.Error("Responses stream scanner error", zap.Error(err))
	}

	writeEvent("response.output_text.done", map[string]interface{}{
		"type":          "response.output_text.done",
		"index":         textIndex,
		"output_index":  0,
		"content_index": 0,
		"text":          "",
	})

	writeEvent("response.content_part.done", map[string]interface{}{
		"type":          "response.content_part.done",
		"index":         0,
		"output_index":  0,
		"content_index": 0,
		"part": map[string]interface{}{
			"type": "output_text",
			"text": "",
		},
	})

	completed := map[string]interface{}{
		"id":     itemID,
		"type":   "message",
		"role":   "assistant",
		"status": "completed",
		"content": []interface{}{
			map[string]interface{}{
				"type": "output_text",
				"text": "",
			},
		},
	}
	writeEvent("response.output_item.done", map[string]interface{}{
		"type":        "response.output_item.done",
		"index":       0,
		"output_item": completed,
	})

	responseObj := map[string]interface{}{
		"id":         responseID,
		"object":     "response",
		"created_at": now,
		"status":     "completed",
		"model":      req.Model,
		"output":     []interface{}{completed},
	}
	if finalUsage != nil {
		responseObj["usage"] = finalUsage
	}
	writeEvent("response.completed", map[string]interface{}{
		"type":     "response.completed",
		"response": responseObj,
	})
}

func responsesRequestToChatRequest(req *model.ResponsesRequest) (*model.ChatCompletionRequest, error) {
	messages := make([]model.ChatCompletionMessageParam, 0)

	if req.Instructions != "" {
		systemJSON, _ := json.Marshal(req.Instructions)
		messages = append(messages, model.ChatCompletionMessageParam{
			Role:    "system",
			Content: systemJSON,
		})
	}

	if len(req.Input) > 0 {
		switch req.Input[0] {
		case '"':
			var userInput string
			if err := json.Unmarshal(req.Input, &userInput); err != nil {
				return nil, fmt.Errorf("input must be a string or array of input items")
			}
			contentJSON, _ := json.Marshal(userInput)
			messages = append(messages, model.ChatCompletionMessageParam{
				Role:    "user",
				Content: contentJSON,
			})
		case '[':
			var items []responsesInputItem
			if err := json.Unmarshal(req.Input, &items); err != nil {
				return nil, fmt.Errorf("invalid input array")
			}
			for _, item := range items {
				if item.Type == "" {
					item.Type = "message"
				}
				switch item.Type {
				case "message":
					role := item.Role
					if role == "" {
						role = "user"
					}
					content, err := responsesItemContentToText(item.Content)
					if err != nil {
						return nil, err
					}
					contentJSON, _ := json.Marshal(content)
					messages = append(messages, model.ChatCompletionMessageParam{
						Role:    role,
						Content: contentJSON,
					})
				default:
					// Other input item types are intentionally ignored for now.
				}
			}
		default:
			contentJSON, _ := json.Marshal(string(req.Input))
			messages = append(messages, model.ChatCompletionMessageParam{
				Role:    "user",
				Content: contentJSON,
			})
		}
	}

	if len(messages) == 0 {
		contentJSON, _ := json.Marshal("")
		messages = append(messages, model.ChatCompletionMessageParam{
			Role:    "user",
			Content: contentJSON,
		})
	}

	return &model.ChatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
	}, nil
}

type responsesInputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func responsesItemContentToText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	case '[':
		var parts []responsesInputContentPart
		if err := json.Unmarshal(raw, &parts); err != nil {
			return "", err
		}
		out := ""
		for _, p := range parts {
			if p.Type == "input_text" || p.Type == "text" {
				out += p.Text
			}
		}
		return out, nil
	default:
		return string(raw), nil
	}
}

type responsesInputContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func chatCompletionToResponsesResult(resp *model.ChatCompletionResponse, requestedModel string) *model.ResponsesResult {
	out := make([]model.ResponsesOutputItem, 0, len(resp.Choices))

	for _, choice := range resp.Choices {
		text := ""
		if choice.Message.Content != nil {
			switch choice.Message.Content[0] {
			case '"':
				var s string
				_ = json.Unmarshal(choice.Message.Content, &s)
				text = s
			case '[':
				var parts []model.ChatCompletionContentPart
				if err := json.Unmarshal(choice.Message.Content, &parts); err == nil {
					for _, part := range parts {
						if part.Type == "text" {
							text += part.Text
						}
					}
				} else {
					text = string(choice.Message.Content)
				}
			default:
				text = string(choice.Message.Content)
			}
		}

		out = append(out, model.ResponsesOutputItem{
			Message: &model.ResponsesOutputMessageItem{
				ID:     resp.ID + "-message-" + fmt.Sprintf("%d", choice.Index),
				Type:   "message",
				Role:   "assistant",
				Status: "completed",
				Content: []model.ResponsesOutputTextContent{
					{Type: "output_text", Text: text},
				},
			},
		})
	}

	result := &model.ResponsesResult{
		ID:        resp.ID,
		Object:    "response",
		CreatedAt: resp.Created,
		Status:    "completed",
		Model:     requestedModel,
		Output:    out,
	}
	if resp.Usage != nil {
		result.Usage = &model.ResponsesUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		}
	}
	return result
}

func generateResponseID() string {
	return generateMessageID()
}
