package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/o4openai/internal/model"
	"github.com/o4openai/internal/provider"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// VideoHandler handles video-related requests
type VideoHandler struct {
	registry       *provider.Registry
	logger         *zap.Logger
	base64Handler  *utils.Base64Handler
	forcedProvider string
}

// NewVideoHandler creates a new video handler
func NewVideoHandler(registry *provider.Registry, base64Handler *utils.Base64Handler, logger *zap.Logger, forcedProvider string) *VideoHandler {
	return &VideoHandler{
		registry:       registry,
		logger:         logger,
		base64Handler:  base64Handler,
		forcedProvider: forcedProvider,
	}
}

// HandleGenerate handles POST /v1/videos/generations
func (h *VideoHandler) HandleGenerate(c *gin.Context) {
	contentType := c.GetHeader("Content-Type")

	var req model.VideoGenerationRequest
	var err error

	if strings.HasPrefix(contentType, "multipart/form-data") {
		err = h.parseMultipartVideoGenerate(c, &req)
	} else {
		err = c.ShouldBindJSON(&req)
	}

	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
				Code:    "invalid_json",
			},
		})
		return
	}

	h.logger.Info("Video generation request", zap.String("model", req.Model))

	if req.Model == "" {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: "Model is required for video generation",
				Type:    "invalid_request_error",
				Code:    "missing_model",
				Param:   "model",
			},
		})
		return
	}

	var p model.Provider
	if h.forcedProvider != "" {
		p, err = h.registry.GetProvider(h.forcedProvider)
	} else {
		p, err = h.registry.GetProviderForModel(req.Model)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Model %q not found for video generation", req.Model),
				Type:    "invalid_request_error",
				Code:    "model_not_found",
			},
		})
		return
	}

	if !p.SupportsVideoGeneration() {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Provider %q does not support video generation", p.Name()),
				Type:    "invalid_request_error",
				Code:    "unsupported_capability",
			},
		})
		return
	}

	resp, err := p.VideoGeneration(ctxWithKey(c), &req)
	if err != nil {
		h.logger.Error("Video generation failed", zap.Error(err))
		respondProviderError(c, "Video generation", err)
		return
	}

	if resp.Status == "in_progress" || resp.Status == "queued" {
		c.JSON(http.StatusAccepted, resp)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// HandleRetrieve handles GET /v1/videos/:id
func (h *VideoHandler) HandleRetrieve(c *gin.Context) {
	videoID := c.Param("id")
	if videoID == "" {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: "Video ID is required",
				Type:    "invalid_request_error",
				Code:    "missing_video_id",
			},
		})
		return
	}

	ctx := ctxWithKey(c)

	// Try the forced provider first, then iterate over all providers.
	// A 404 from the upstream means the task ID isn't there — try the next one.
	// Any other error (auth, network, etc.) is reported to the caller.
	providers := h.providersToTry()

	var lastUpstreamErr error
	for _, p := range providers {
		if !p.SupportsVideoGeneration() {
			continue
		}
		resp, err := p.VideoRetrieve(ctx, videoID)
		if err == nil {
			c.JSON(http.StatusOK, resp)
			return
		}
		// If the upstream says "not found", keep trying; otherwise stop.
		if isUpstreamNotFound(err) {
			lastUpstreamErr = err
			continue
		}
		respondProviderError(c, "Video retrieve", err)
		return
	}

	if lastUpstreamErr != nil {
		c.JSON(http.StatusNotFound, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: fmt.Sprintf("Video %q not found", videoID),
				Type:    "invalid_request_error",
				Code:    "video_not_found",
			},
		})
		return
	}
	c.JSON(http.StatusNotFound, model.ErrorResponse{
		Error: model.ErrorDetail{
			Message: fmt.Sprintf("Video %q not found", videoID),
			Type:    "invalid_request_error",
			Code:    "video_not_found",
		},
	})
}

// HandleDownloadContent handles GET /v1/videos/:id/content
// Downloads video content by retrieving the video URL and redirecting.
// This is compatible with the OpenAI SDK's client.videos.download_content() method.
func (h *VideoHandler) HandleDownloadContent(c *gin.Context) {
	videoID := c.Param("id")
	if videoID == "" {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.ErrorDetail{
				Message: "Video ID is required",
				Type:    "invalid_request_error",
				Code:    "missing_video_id",
			},
		})
		return
	}

	ctx := ctxWithKey(c)
	providers := h.providersToTry()

	for _, p := range providers {
		if !p.SupportsVideoGeneration() {
			continue
		}
		resp, err := p.VideoRetrieve(ctx, videoID)
		if err != nil {
			if isUpstreamNotFound(err) {
				continue
			}
			respondProviderError(c, "Video download", err)
			return
		}
		if resp.Status != "completed" || len(resp.Output) == 0 || resp.Output[0].URL == "" {
			c.JSON(http.StatusBadRequest, model.ErrorResponse{
				Error: model.ErrorDetail{
					Message: fmt.Sprintf("Video %q is not ready for download (status: %s)", videoID, resp.Status),
					Type:    "invalid_request_error",
					Code:    "video_not_ready",
				},
			})
			return
		}
		// Redirect to the video URL — avoids proxying large files through the gateway
		videoURL := resp.Output[0].URL
		h.logger.Info("Video download redirect", zap.String("video_id", videoID), zap.String("url", videoURL))
		c.Redirect(http.StatusFound, videoURL)
		return
	}

	c.JSON(http.StatusNotFound, model.ErrorResponse{
		Error: model.ErrorDetail{
			Message: fmt.Sprintf("Video %q not found", videoID),
			Type:    "invalid_request_error",
			Code:    "video_not_found",
		},
	})
}

// parseMultipartVideoGenerate parses multipart/form-data for video generation.
// This is needed for compatibility with the OpenAI Python SDK's videos.create() method,
// which sends multipart requests with fields: model, prompt, seconds, size, input_reference.
func (h *VideoHandler) parseMultipartVideoGenerate(c *gin.Context, req *model.VideoGenerationRequest) error {
	form, err := c.MultipartForm()
	if err != nil {
		return err
	}

	if values := form.Value["model"]; len(values) > 0 {
		req.Model = values[0]
	}
	// OpenAI Videos API uses "prompt" as the top-level text instruction
	if values := form.Value["prompt"]; len(values) > 0 {
		req.Instructions = values[0]
	}
	if values := form.Value["seconds"]; len(values) > 0 {
		req.Duration = values[0]
	}
	if values := form.Value["size"]; len(values) > 0 {
		req.Size = values[0]
	}

	// Parse input_reference file uploads (image references for image-to-video)
	if files := form.File["input_reference"]; len(files) > 0 {
		for _, fh := range files {
			file, err := fh.Open()
			if err != nil {
				return fmt.Errorf("failed to open input_reference file %s: %w", fh.Filename, err)
			}
			data, err := io.ReadAll(file)
			file.Close()
			if err != nil {
				return fmt.Errorf("failed to read input_reference file %s: %w", fh.Filename, err)
			}
			req.Input = append(req.Input, model.VideoInputItem{
				Type:  "image",
				Image: encodeToBase64String(data),
			})
		}
	}

	return nil
}

// providersToTry returns the provider list to attempt, starting with
// the forced provider if one is configured.
func (h *VideoHandler) providersToTry() []model.Provider {
	if h.forcedProvider != "" {
		if p, err := h.registry.GetProvider(h.forcedProvider); err == nil {
			return []model.Provider{p}
		}
		return nil
	}
	all := h.registry.GetAllProviders()
	out := make([]model.Provider, 0, len(all))
	for _, name := range all {
		if p, err := h.registry.GetProvider(name); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// isUpstreamNotFound reports whether the upstream returned 404 for the request.
func isUpstreamNotFound(err error) bool {
	var pe *provider.ProviderError
	if errors.As(err, &pe) {
		return pe.StatusCode == http.StatusNotFound
	}
	return false
}
