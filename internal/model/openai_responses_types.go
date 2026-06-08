package model

import "encoding/json"

// Minimal OpenAI Responses API compatible request/response types.
// These are additive and do not modify existing Chat/Anthropic types.

type ResponsesRequest struct {
	Model        string          `json:"model"`
	Input        json.RawMessage `json:"input"`
	Instructions string          `json:"instructions,omitempty"`
	Stream       bool            `json:"stream,omitempty"`
}

type ResponsesOutputTextContent struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

type ResponsesOutputMessageItem struct {
	ID      string                       `json:"id"`
	Type    string                       `json:"type"` // "message"
	Role    string                       `json:"role"`
	Status  string                       `json:"status"`
	Content []ResponsesOutputTextContent `json:"content"`
}

type ResponsesOutputFunctionCallItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // "function_call"
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ResponsesOutputItem struct {
	// Exactly one of the following should be populated.
	Message      *ResponsesOutputMessageItem      `json:"-"`
	FunctionCall *ResponsesOutputFunctionCallItem `json:"-"`
}

// MarshalJSON allows ResponsesOutputItem to serialize as either a message or function_call object.
func (o ResponsesOutputItem) MarshalJSON() ([]byte, error) {
	if o.FunctionCall != nil {
		return json.Marshal(o.FunctionCall)
	}
	if o.Message != nil {
		return json.Marshal(o.Message)
	}
	return []byte("null"), nil
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type ResponsesResult struct {
	ID        string                `json:"id"`
	Object    string                `json:"object"` // "response"
	CreatedAt int64                 `json:"created_at"`
	Status    string                `json:"status"` // "completed", "failed"
	Model     string                `json:"model"`
	Output    []ResponsesOutputItem `json:"output"`
	Usage     *ResponsesUsage       `json:"usage,omitempty"`
}
