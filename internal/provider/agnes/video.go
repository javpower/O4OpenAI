package agnes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/o4openai/internal/model"
	"github.com/o4openai/internal/provider"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// ============================================================
// Video adapter - converts between OpenAI and Agnes video formats
//
// Agnes API spec (https://agnes-ai.com/doc/agnes-video-v20):
//
//   POST /v1/videos   - create task
//   GET  /v1/videos/{task_id}   - retrieve result
//
//   Request body (text-to-video):
//     {
//       "model": "agnes-video-v2.0",
//       "prompt": "...",
//       "height": 768, "width": 1152,
//       "num_frames": 121, "frame_rate": 24
//     }
//
//   Image-to-video: add top-level "image" with a URL
//   Multi-image:    use "extra_body": { "image": ["url1","url2"] }
//   Keyframe mode:  "extra_body": { "image": [...], "mode": "keyframes" }
//
//   Response:
//     { "id", "task_id", "status": "queued|in_progress|completed|failed",
//       "progress": 0-100, "created_at", "completed_at",
//       "video_url" (only when completed), "size", "seconds" }
// ============================================================

// defaultNumFrames is the default frame count (≈5s at 24fps).
// Must satisfy 8n+1, and be <= 441.
const defaultNumFrames = 121

// defaultFrameRate is the default fps.
const defaultFrameRate = 24.0

// VideoGeneration creates a video generation task
func (p *Provider) VideoGeneration(ctx context.Context, req *model.VideoGenerationRequest) (*model.VideoResponse, error) {
	agnesReq, err := p.convertVideoRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to convert video request: %w", err)
	}

	resp, err := p.doRequest(ctx, "POST", "/videos", agnesReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var agnesResp AgnesVideoResponse
	if err := json.NewDecoder(resp.Body).Decode(&agnesResp); err != nil {
		return nil, fmt.Errorf("failed to decode video response: %w", err)
	}

	return p.convertVideoResponse(&agnesResp, req.Model), nil
}

// VideoRetrieve fetches the status/result of a video task using the new recommended endpoint.
//
// Endpoint: GET /agnesapi?video_id=<VIDEO_ID>&model_name=agnes-video-v2.0
//
// Note: We don't fall back to the legacy /v1/videos/{task_id} endpoint because
// the ID we return to clients is a video_id (base64-encoded), not a task_id.
// The legacy endpoint expects task_id format and can't look up our video_id.
func (p *Provider) VideoRetrieve(ctx context.Context, videoID string) (*model.VideoResponse, error) {
	// New recommended endpoint: GET /agnesapi?video_id=...
	// Note: this endpoint is NOT under /v1, so we use the base host directly
	host := p.baseURL
	if idx := strings.LastIndex(host, "/v1"); idx > 0 {
		host = host[:idx]
	}
	// URL-encode the video_id (the LiteLLM-encoded base64 contains = padding that
	// must be percent-encoded for strict URL parsers like macOS curl)
	encodedID := url.QueryEscape(videoID)
	url := fmt.Sprintf("%s/agnesapi?video_id=%s&model_name=%s",
		host, encodedID, p.resolveModel("agnes-video-v2.0"))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	apiKey := utils.APIKeyFromCtx(ctx)
	if apiKey == "" {
		apiKey = p.apiKey
	}
	if apiKey == "" {
		return nil, provider.ErrNoAPIKey
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	p.logger.Info("Agnes video query (new endpoint)", zap.String("video_id", videoID))
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("video query request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, &provider.ProviderError{
			StatusCode: resp.StatusCode,
			Body:       fmt.Sprintf("video query failed: %s", videoID),
			Provider:   "Agnes",
		}
	}

	var agnesResp AgnesVideoResponse
	if err := json.NewDecoder(resp.Body).Decode(&agnesResp); err != nil {
		return nil, fmt.Errorf("failed to decode video status response: %w", err)
	}

	return p.convertVideoResponse(&agnesResp, ""), nil
}

// convertVideoRequest converts the OpenAI-style request into the Agnes format
func (p *Provider) convertVideoRequest(ctx context.Context, req *model.VideoGenerationRequest) (*AgnesVideoRequest, error) {
	agnesReq := &AgnesVideoRequest{
		Model:     p.resolveModel(req.Model),
		Height:    768,
		Width:     1152,
		NumFrames: defaultNumFrames,
		FrameRate: defaultFrameRate,
	}

	// Collect text and image inputs
	var textParts []string
	var imageURLs []string

	for i, item := range req.Input {
		switch item.Type {
		case "text":
			textParts = append(textParts, item.Text)

		case "image":
			// base64 (Data URI or pure) → temp URL conversion
			// For both formats the data is the same — wrap pure base64 as a
			// Data URI first so downstream code can handle it uniformly.
			imgURL := item.Image
			if utils.IsDataURL(imgURL) || looksLikeBase64(imgURL) {
				if p.base64Handler == nil {
					return nil, fmt.Errorf("base64 image provided but no Base64Handler configured")
				}
				// Wrap pure base64 as a Data URI
				dataURL := imgURL
				if !utils.IsDataURL(dataURL) {
					dataURL = "data:image/png;base64," + dataURL
				}
				reqCtx := utils.RequestContextFromCtx(ctx)
				convertedURL, _, err := p.base64Handler.ConvertDataURL(dataURL, reqCtx)
				if err != nil {
					p.logger.Error("Failed to convert video input image base64 to temp URL",
						zap.Int("index", i),
						zap.Error(err))
					return nil, fmt.Errorf("failed to process image at index %d: %w", i, err)
				}
				p.logger.Debug("Converted video input image to temp URL",
					zap.Int("index", i),
					zap.String("temp_url", convertedURL))
				imgURL = convertedURL
			}
			imageURLs = append(imageURLs, imgURL)
		}
	}

	// Build prompt from text inputs (preserve order, separate with newlines)
	if len(textParts) > 0 {
		prompt := strings.Join(textParts, "\n")
		if req.Instructions != "" {
			prompt = req.Instructions + "\n" + prompt
		}
		agnesReq.Prompt = prompt
	} else if req.Instructions != "" {
		agnesReq.Prompt = req.Instructions
	}

	// Map images to the right field based on count
	switch len(imageURLs) {
	case 0:
		// text-to-video: nothing else to do
	case 1:
		// image-to-video: top-level "image" field
		agnesReq.Image = imageURLs[0]
		agnesReq.Mode = "ti2vid"
	default:
		// multi-image / keyframe: use extra_body.image
		extra := &AgnesVideoExtra{Image: imageURLs}
		// Heuristic: if the prompt mentions "keyframe" or "transition", set mode=keyframes
		promptLower := strings.ToLower(agnesReq.Prompt)
		if strings.Contains(promptLower, "keyframe") || strings.Contains(promptLower, "transition between") {
			extra.Mode = "keyframes"
		}
		agnesReq.ExtraBody = extra
		p.logger.Info("Multi-image video generation",
			zap.Int("count", len(imageURLs)),
			zap.String("mode", extra.Mode))
	}

	// Width/Height/NumFrames/FrameRate from request fields
	if req.Size != "" {
		w, h := parseSize(req.Size)
		if w > 0 {
			agnesReq.Width = w
		}
		if h > 0 {
			agnesReq.Height = h
		}
	}
	if req.Resolution != "" {
		// accept "WxH" form
		w, h := parseSize(req.Resolution)
		if w > 0 {
			agnesReq.Width = w
		}
		if h > 0 {
			agnesReq.Height = h
		}
	}
	if req.Duration != "" {
		// duration in seconds (string per OpenAI spec) → derive num_frames at default fps
		if secs, err := strconv.ParseFloat(req.Duration, 64); err == nil && secs > 0 {
			frames := int(secs * defaultFrameRate)
			frames = roundTo8nPlus1(frames)
			if frames > 441 {
				frames = 441
			}
			if frames < 9 {
				frames = 9
			}
			agnesReq.NumFrames = frames
		}
	}

	// AspectRatio as a hint when size is missing
	if req.AspectRatio != "" && req.Size == "" && req.Resolution == "" {
		w, h := aspectRatioToSize(req.AspectRatio)
		agnesReq.Width = w
		agnesReq.Height = h
	}

	// Enforce spec constraints
	agnesReq.NumFrames = clampNumFrames(agnesReq.NumFrames)
	if agnesReq.FrameRate < 1 {
		agnesReq.FrameRate = 1
	}
	if agnesReq.FrameRate > 60 {
		agnesReq.FrameRate = 60
	}

	return agnesReq, nil
}

// convertVideoResponse converts an Agnes video response to OpenAI format
func (p *Provider) convertVideoResponse(agnesResp *AgnesVideoResponse, originalModel string) *model.VideoResponse {
	resp := &model.VideoResponse{
		ID:        pickID(agnesResp),
		Object:    "video",
		CreatedAt: agnesResp.CreatedAt,
		Status:    normalizeStatus(agnesResp.Status),
		Model:     pickModel(agnesResp, originalModel),
	}

	// Map progress to percentage if present
	if agnesResp.Progress > 0 {
		// expose as part of status string for clients that don't expect a "progress" field
		// (we keep it simple: leave Status as-is, progress is implicit through "in_progress")
	}

	// Build output when the video is actually ready.
	// Priority: video_url (new field) > remixed_from_video_id (legacy field)
	videoURL := agnesResp.VideoURL
	if videoURL == "" {
		videoURL = agnesResp.RemixedFromVideoID
	}
	// Only treat http(s) URLs as ready video URLs (filter out LiteLLM placeholders)
	if !strings.HasPrefix(videoURL, "http://") && !strings.HasPrefix(videoURL, "https://") {
		videoURL = ""
	}
	if videoURL != "" {
		duration := 0.0
		if agnesResp.Seconds != "" {
			if s, err := strconv.ParseFloat(agnesResp.Seconds, 64); err == nil {
				duration = s
			}
		}
		mimeType := "video/mp4"
		if strings.HasSuffix(strings.ToLower(videoURL), ".webm") {
			mimeType = "video/webm"
		}
		resp.Output = []model.VideoOutput{{
			Type:     "url",
			URL:      videoURL,
			Duration: duration,
			MimeType: mimeType,
		}}
	}

	if agnesResp.Error != nil {
		resp.Error = &model.VideoError{
			Code:    agnesResp.Error.Code,
			Message: agnesResp.Error.Message,
		}
	}

	return resp
}

// pickID returns the video_id (preferred for the new /agnesapi endpoint).
//
// Note: Agnes's "video_id" field is a LiteLLM-encoded base64 string, but
// it IS accepted by the new /agnesapi?video_id=... endpoint.
// The legacy endpoint /v1/videos/{id} can also accept this video_id.
func pickID(r *AgnesVideoResponse) string {
	if r.VideoID != "" {
		return r.VideoID
	}
	if r.TaskID != "" {
		return r.TaskID
	}
	return r.ID
}

// pickModel returns the model from the response or falls back to the requested one
func pickModel(r *AgnesVideoResponse, fallback string) string {
	if r.Model != "" {
		return r.Model
	}
	return fallback
}

// normalizeStatus maps Agnes statuses to OpenAI Video API status values.
// OpenAI Video API: queued | in_progress | completed | failed | expired
// Agnes: queued | in_progress | completed | failed
func normalizeStatus(s string) string {
	switch strings.ToLower(s) {
	case "queued":
		return "queued"
	case "in_progress", "processing", "running":
		return "in_progress"
	case "completed", "succeeded", "success":
		return "completed"
	case "failed", "error", "cancelled":
		return "failed"
	case "expired":
		return "expired"
	default:
		return s
	}
}

// parseSize parses "WxH" or "W*H" into ints
func parseSize(s string) (int, int) {
	for _, sep := range []string{"x", "X", "*"} {
		if i := strings.Index(s, sep); i > 0 {
			w, _ := strconv.Atoi(strings.TrimSpace(s[:i]))
			h, _ := strconv.Atoi(strings.TrimSpace(s[i+1:]))
			return w, h
		}
	}
	return 0, 0
}

// aspectRatioToSize returns a default size for the given aspect ratio
func aspectRatioToSize(ratio string) (int, int) {
	switch ratio {
	case "16:9":
		return 1152, 768
	case "9:16":
		return 768, 1152
	case "1:1":
		return 1024, 1024
	case "4:3":
		return 1024, 768
	case "3:4":
		return 768, 1024
	default:
		return 1152, 768
	}
}

// roundTo8nPlus1 rounds n to the nearest value satisfying 8n+1 (e.g. 9, 17, 25, ...)
func roundTo8nPlus1(n int) int {
	if n <= 1 {
		return 9
	}
	// n = 8q + r where r in 0..7
	q := (n - 1) / 8
	return 8*q + 1
}

// clampNumFrames enforces 9 <= frames <= 441 and 8n+1
func clampNumFrames(n int) int {
	if n < 9 {
		return 9
	}
	if n > 441 {
		return 441
	}
	return roundTo8nPlus1(n)
}

// Suppress unused import warnings
var _ = zap.Error(nil)
var _ = json.Marshal
