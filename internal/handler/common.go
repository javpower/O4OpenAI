package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/o4openai/internal/model"
	"github.com/o4openai/internal/provider"
	"github.com/o4openai/pkg/utils"
)

// ctxWithKey extracts the Bearer token from the client's Authorization header
// and injects it into the context. The provider will use this key to call
// the third-party API — the gateway never stores any keys.
//
// Flow: Client sends Authorization: Bearer <key> → Gateway extracts → Provider uses it
func ctxWithKey(c *gin.Context) context.Context {
	authHeader := c.GetHeader("Authorization")
	if len(authHeader) > 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
		return utils.WithAPIKey(c.Request.Context(), authHeader[7:])
	}
	return c.Request.Context()
}

// ctxWithKeyAnthropic extracts API key from either the x-api-key header (Anthropic convention)
// or the Authorization: Bearer header (fallback). This allows Anthropic SDK clients
// to authenticate against the gateway without changes.
func ctxWithKeyAnthropic(c *gin.Context) context.Context {
	// Anthropic primary: x-api-key header
	if apiKey := c.GetHeader("x-api-key"); apiKey != "" {
		return utils.WithAPIKey(c.Request.Context(), apiKey)
	}
	// Fallback: Authorization: Bearer (also accepted by Anthropic SDK)
	return ctxWithKey(c)
}

// respondProviderError writes an OpenAI-style error response with the
// most appropriate HTTP status code:
//
//   - 401 if the client did not provide an API key
//   - the upstream status code when the provider returns one
//   - 500 for everything else
//
// The client-facing message is intentionally short and generic — it does
// NOT echo the raw upstream body (which may contain internal IDs, stack
// traces, or other sensitive info). Detailed error context is logged
// server-side only.
func respondProviderError(c *gin.Context, op string, err error) {
	status := provider.StatusCodeForError(err)
	msg := clientSafeMessage(op, err)
	c.JSON(status, model.ErrorResponse{
		Error: model.ErrorDetail{
			Message: msg,
			Type:    errorTypeForStatus(status),
			Code:    errorCodeForStatus(status),
		},
	})
}

// clientSafeMessage builds a short, generic message safe to return to clients.
func clientSafeMessage(op string, err error) string {
	status := provider.StatusCodeForError(err)
	var pe *provider.ProviderError
	if errors.As(err, &pe) {
		return fmt.Sprintf("%s failed: %s", op, pe.ClientMessage())
	}
	if errors.Is(err, provider.ErrNoAPIKey) {
		return "Missing or invalid API key. Please provide Authorization: Bearer <key>."
	}
	return fmt.Sprintf("%s failed (status %d)", op, status)
}

// errorTypeForStatus returns the OpenAI-style "type" field for a status code
func errorTypeForStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "invalid_request_error"
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusNotFound:
		return "invalid_request_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

// errorCodeForStatus returns a short machine-readable code.
func errorCodeForStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "invalid_api_key"
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case http.StatusInternalServerError:
		return "internal_error"
	default:
		return "provider_error"
	}
}

// ---------- Anthropic-format error helpers ----------

// respondAnthropicError writes an Anthropic-style error response.
func respondAnthropicError(c *gin.Context, status int, errType string, message string) {
	c.JSON(status, model.AnthropicErrorResponse{
		Type: "error",
		Error: model.AnthropicErrorDetail{
			Type:    errType,
			Message: message,
		},
	})
}

// respondAnthropicProviderError wraps a provider error into an Anthropic-format response.
func respondAnthropicProviderError(c *gin.Context, op string, err error) {
	status := provider.StatusCodeForError(err)
	msg := clientSafeMessage(op, err)
	respondAnthropicError(c, status, anthropicErrorTypeForStatus(status), msg)
}

// anthropicErrorTypeForStatus maps an HTTP status code to an Anthropic error type string.
func anthropicErrorTypeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusInternalServerError:
		return "api_error"
	case http.StatusServiceUnavailable:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

// encodeToBase64String encodes bytes to base64 string.
// Shared across image and video handlers for multipart file processing.
func encodeToBase64String(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
