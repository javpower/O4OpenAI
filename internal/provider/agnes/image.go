package agnes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/o4openai/internal/model"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// ============================================================
// Image adapter - converts between OpenAI and Agnes image formats
//
// Key differences handled:
//
// 1. Request input format:
//   - OpenAI /images/edits accepts multipart/form-data with file uploads
//   - Our handler layer already converts file uploads → base64 strings
//   - Agnes may need URLs, not base64 → we convert via Base64Handler
//
// 2. Response format:
//   - OpenAI supports response_format: url | b64_json
//   - Agnes likely only returns URLs
//   - If client requests b64_json, we download the image and convert
//
// 3. Image sizes: OpenAI has many sizes, Agnes may have different set
// 4. Quality: OpenAI has standard/hd/low/medium/high/auto
// 5. Background: transparent/opaque/auto (PNG alpha)
// 6. Output format: png/webp/jpeg
// ============================================================

// ImageGeneration generates images from text prompt
func (p *Provider) ImageGeneration(ctx context.Context, req *model.ImageGenerationRequest) (*model.ImageResponse, error) {
	agnesReq := p.convertImageRequest(req)

	resp, err := p.doRequest(ctx, "POST", "/images/generations", agnesReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var agnesResp AgnesImageResponse
	if err := json.NewDecoder(resp.Body).Decode(&agnesResp); err != nil {
		return nil, fmt.Errorf("failed to decode image response: %w", err)
	}

	return p.convertImageResponse(&agnesResp, req), nil
}

// ImageEdit edits an existing image (图生图).
//
// Agnes's image model uses the same /v1/images/generations endpoint for both
// text-to-image and image-to-image. For image-to-image, the input image(s)
// are passed in a top-level "image" array (string[] of URLs or Data URIs).
//
// Spec quirks we handle here:
//   - response_format must be in extra_body.response_format, never at top level
//   - input images can be Data URIs (we pass them through; they don't need
//     to be re-uploaded since Agnes accepts them directly)
//   - URL or Data URI input is supported
//
// Scenarios:
//   - Text + image → edited image (图生图)
//   - Text + image + mask → inpaint (Agnes may ignore the mask)
//
// Base64 handling:
//   - Data URIs are passed through verbatim to Agnes
//   - Plain base64 (no "data:" prefix) is wrapped into a Data URI
//   - HTTP(S) URLs are passed through
func (p *Provider) ImageEdit(ctx context.Context, req *model.ImageEditRequest) (*model.ImageResponse, error) {
	// Build the Agnes request body
	agnesReq := map[string]interface{}{
		"model":  p.resolveModel(req.Model),
		"prompt": req.Prompt,
		"size":   convertSizeForAgnes(req.Size),
	}

	// Normalize the image input(s) — supports multi-image from multipart
	images := p.normalizeImageInputs(ctx, req)
	if len(images) == 0 {
		return nil, fmt.Errorf("image is required for image edit")
	}
	agnesReq["image"] = images

	// response_format goes in extra_body (per Agnes spec) when present
	extra := map[string]interface{}{}
	if req.ResponseFormat == "b64_json" {
		extra["response_format"] = "b64_json"
	} else {
		// Default to url for edits, since we already converted base64
		extra["response_format"] = "url"
	}
	agnesReq["extra_body"] = extra

	if req.N != nil {
		agnesReq["n"] = *req.N
	}

	// Pass quality through to Agnes (used by ArcReel SDK)
	if req.Quality != "" {
		agnesReq["quality"] = req.Quality
	}

	p.logger.Info("Agnes image edit (i2i) request",
		zap.String("model", req.Model),
		zap.Int("image_count", len(images)),
	)

	// Forward to the same /v1/images/generations endpoint that T2I uses
	resp, err := p.doRequest(ctx, "POST", "/images/generations", agnesReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var agnesResp AgnesImageResponse
	if err := json.NewDecoder(resp.Body).Decode(&agnesResp); err != nil {
		return nil, fmt.Errorf("failed to decode image edit response: %w", err)
	}

	return p.convertImageResponse(&agnesResp, &model.ImageGenerationRequest{
		Model:          req.Model,
		ResponseFormat: req.ResponseFormat,
	}), nil
}

// normalizeImageInputs turns the OpenAI-style image request into a
// string array suitable for Agnes's "image" parameter.
// Supports multi-image uploads (req.Images) from multipart, falling back
// to single image (req.Image) for JSON-mode backward compatibility.
// Base64 data URIs are converted to temp URLs when a base64Handler is available.
func (p *Provider) normalizeImageInputs(ctx context.Context, req *model.ImageEditRequest) []string {
	out := []string{}

	// Collect source images: multi-image takes priority, fall back to single
	imageSources := req.Images
	if len(imageSources) == 0 && req.Image != "" {
		imageSources = []string{req.Image}
	}

	reqCtx := utils.RequestContextFromCtx(ctx)

	for _, img := range imageSources {
		normalized := normalizeOneImage(img)
		// Convert data URIs to temp URLs so Agnes can fetch them
		if utils.IsDataURL(normalized) && p.base64Handler != nil {
			if tmpURL, ok, err := p.base64Handler.ConvertDataURL(normalized, reqCtx); err == nil && ok {
				normalized = tmpURL
			}
		}
		out = append(out, normalized)
	}

	// Mask is appended separately (Agnes treats it as an additional input image)
	if req.Mask != "" {
		maskNorm := normalizeOneImage(req.Mask)
		if utils.IsDataURL(maskNorm) && p.base64Handler != nil {
			if tmpURL, ok, err := p.base64Handler.ConvertDataURL(maskNorm, reqCtx); err == nil && ok {
				maskNorm = tmpURL
			}
		}
		out = append(out, maskNorm)
	}

	return out
}

// normalizeOneImage normalizes a single image string into a form Agnes accepts.
func normalizeOneImage(s string) string {
	s = strings.TrimSpace(s)
	if utils.IsDataURL(s) {
		return s // already a data URI
	}
	// Plain base64? Wrap it
	if looksLikeBase64(s) {
		return "data:image/png;base64," + s
	}
	return s // assume URL
}

// looksLikeBase64 is a quick heuristic to detect raw base64 payloads.
func looksLikeBase64(s string) bool {
	if len(s) < 64 {
		return false
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z',
			c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			c == '+', c == '/', c == '=':
			// ok
		default:
			return false
		}
	}
	return true
}

// ImageVariation creates variations of an image.
//
// The input image may be base64 encoded (from multipart upload).
// We convert base64 → temp URL before sending to Agnes.
func (p *Provider) ImageVariation(ctx context.Context, req *model.ImageVariationRequest) (*model.ImageResponse, error) {
	agnesReq := map[string]interface{}{
		"model": p.resolveModel(req.Model),
	}

	// Convert image: base64 → temp URL if needed
	if req.Image != "" {
		convertedImage := req.Image
		if utils.IsDataURL(req.Image) && p.base64Handler != nil {
			reqCtx := utils.RequestContextFromCtx(ctx)
			url, _, err := p.base64Handler.ConvertDataURL(req.Image, reqCtx)
			if err != nil {
				p.logger.Error("Failed to convert variation image base64 to temp URL", zap.Error(err))
			} else {
				convertedImage = url
			}
		}
		agnesReq["image"] = convertedImage
	}

	if req.N != nil {
		agnesReq["n"] = *req.N
	}
	if req.Size != "" {
		agnesReq["size"] = convertSizeForAgnes(req.Size)
	}
	if req.ResponseFormat != "" {
		agnesReq["response_format"] = req.ResponseFormat
	}

	resp, err := p.doRequest(ctx, "POST", "/images/variations", agnesReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var agnesResp AgnesImageResponse
	if err := json.NewDecoder(resp.Body).Decode(&agnesResp); err != nil {
		return nil, fmt.Errorf("failed to decode image variation response: %w", err)
	}

	return p.convertImageResponse(&agnesResp, &model.ImageGenerationRequest{
		Model:          req.Model,
		ResponseFormat: req.ResponseFormat,
	}), nil
}

// convertImageRequest converts OpenAI image request to Agnes format
func (p *Provider) convertImageRequest(req *model.ImageGenerationRequest) *AgnesImageRequest {
	agnesReq := &AgnesImageRequest{
		Model:  p.resolveModel(req.Model),
		Prompt: req.Prompt,
	}

	if req.N != nil {
		agnesReq.N = *req.N
	} else {
		agnesReq.N = 1 // default
	}

	// Convert size format
	if req.Size != "" {
		agnesReq.Size = convertSizeForAgnes(req.Size)
	}

	// Handle response format
	// OpenAI: url | b64_json
	// Agnes: likely only supports url
	if req.ResponseFormat == "b64_json" {
		p.logger.Warn("Agnes AI may not support b64_json response format, using url instead")
		agnesReq.ResponseFormat = "url"
	} else if req.ResponseFormat != "" {
		agnesReq.ResponseFormat = req.ResponseFormat
	}

	return agnesReq
}

// convertImageResponse converts Agnes image response to OpenAI format.
// Handles the reverse conversion: URL → b64_json if client requested it.
func (p *Provider) convertImageResponse(agnesResp *AgnesImageResponse, origReq *model.ImageGenerationRequest) *model.ImageResponse {
	images := make([]model.ImageData, 0, len(agnesResp.Data))
	for _, img := range agnesResp.Data {
		imageData := model.ImageData{
			RevisedPrompt: img.RevisedPrompt,
		}

		// Handle response format conversion
		// If client requested b64_json but Agnes returned a URL,
		// we download the image and convert to base64
		if origReq.ResponseFormat == "b64_json" && img.URL != "" && img.B64JSON == "" {
			b64, err := downloadImageAsBase64(img.URL)
			if err != nil {
				p.logger.Error("Failed to download image for base64 conversion",
					zap.String("url", img.URL),
					zap.Error(err))
				// Fall back to URL
				imageData.URL = img.URL
			} else {
				imageData.B64JSON = b64
			}
		} else {
			imageData.URL = img.URL
			imageData.B64JSON = img.B64JSON
		}

		images = append(images, imageData)
	}

	return &model.ImageResponse{
		Created: agnesResp.Created,
		Data:    images,
	}
}

// convertSizeForAgnes converts OpenAI image sizes to Agnes format
func convertSizeForAgnes(size string) string {
	sizeMap := map[string]string{
		"1024x1024": "1024x1024",
		"1024x1536": "1024x1536",
		"1536x1024": "1536x1024",
		"256x256":   "1024x1024", // Upgrade small sizes
		"512x512":   "1024x1024", // Upgrade small sizes
	}
	if mapped, ok := sizeMap[size]; ok {
		return mapped
	}
	return size
}

// downloadImageAsBase64 downloads an image from a URL and returns it as base64
func downloadImageAsBase64(imageURL string) (string, error) {
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	return base64.StdEncoding.EncodeToString(data), nil
}
