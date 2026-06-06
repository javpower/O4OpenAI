package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/o4openai/internal/middleware"
	"github.com/o4openai/internal/provider"
	"github.com/o4openai/pkg/utils"
	"go.uber.org/zap"
)

// SetupRouter configures all routes.
//
// Routing design:
//
//	/v1/chat/completions            → auto-route by model name (default)
//	/v1/images/generations          → auto-route
//	/{provider}/v1/chat/completions → explicitly use named provider (e.g., /agnes/v1/...)
//	/{provider}/v1/images/generations
//
// This way, clients just change the base URL to switch providers:
//
//	base_url = "https://my-gateway.com"            → auto
//	base_url = "https://my-gateway.com/agnes"      → force Agnes
//	base_url = "https://my-gateway.com/openai"     → force OpenAI
func SetupRouter(
	engine *gin.Engine,
	registry *provider.Registry,
	base64Handler *utils.Base64Handler,
	logger *zap.Logger,
) {
	engine.Use(middleware.CORSMiddleware())
	engine.Use(middleware.LoggingMiddleware(logger))

	engine.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Temp image serving (for base64 → URL conversion)
	if base64Handler != nil {
		engine.GET("/_temp/images/:hash", gin.WrapH(http.StripPrefix("/_temp/images", base64Handler)))
	}

	// Default route: /v1/... (auto-route by model name)
	registerAPIRoutes(engine.Group("/v1"), registry, base64Handler, logger, "")

	// Provider-specific routes: /{provider}/v1/...
	// e.g., /agnes/v1/chat/completions
	for _, providerName := range registry.GetAllProviders() {
		pathPrefix := "/" + providerName + "/v1"
		registerAPIRoutes(engine.Group(pathPrefix), registry, base64Handler, logger, providerName)
		logger.Info("Registered provider route", zap.String("prefix", pathPrefix))
	}
}

// registerAPIRoutes registers all OpenAI-compatible API endpoints on a router group.
// If forcedProvider is non-empty, all requests go to that specific provider.
func registerAPIRoutes(rg *gin.RouterGroup, registry *provider.Registry, base64Handler *utils.Base64Handler, logger *zap.Logger, forcedProvider string) {
	chatHandler := NewChatHandler(registry, base64Handler, logger, forcedProvider)
	rg.POST("/chat/completions", chatHandler.HandleCreate)

	imageHandler := NewImageHandler(registry, base64Handler, logger, forcedProvider)
	rg.POST("/images/generations", imageHandler.HandleGenerate)
	rg.POST("/images/edits", imageHandler.HandleEdit)
	rg.POST("/images/variations", imageHandler.HandleVariation)

	videoHandler := NewVideoHandler(registry, base64Handler, logger, forcedProvider)
	rg.POST("/videos/generations", videoHandler.HandleGenerate)
	rg.GET("/videos/:id", videoHandler.HandleRetrieve)
	rg.GET("/videos/:id/content", videoHandler.HandleDownloadContent)

	modelsHandler := NewModelsHandler(registry, logger)
	rg.GET("/models", modelsHandler.HandleList)
	rg.GET("/models/:model", modelsHandler.HandleRetrieve)

	// Anthropic Messages API compatibility
	anthropicHandler := NewAnthropicHandler(registry, base64Handler, logger, forcedProvider)
	rg.POST("/messages", anthropicHandler.HandleMessages)
}
