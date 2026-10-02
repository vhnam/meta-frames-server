// Package server assembles the HTTP handler: middleware, request validation and routes.
package server

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/requestctx"
)

// scanUploadPath matches the multipart import route, which bypasses body validation
// because the validator would buffer entire uploads in memory.
var scanUploadPath = regexp.MustCompile(`^/processing/[^/]+/scans$`)

// NewHandler wires the controllers into a Gin engine with CORS and OpenAPI validation.
func NewHandler(appControllers api.StrictServerInterface, corsOrigins []string) (http.Handler, error) {
	swagger, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	swagger.Servers = nil // match any host

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.ContextWithFallback = true // services read the caller from the request context
	router.Use(requestid.New(), gin.Recovery(), requestContext())
	router.Use(gzip.Gzip(gzip.DefaultCompression)) // JSON compresses well; images are excluded by extension
	router.Use(cors.New(cors.Config{
		AllowOrigins:     corsOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Authorization", "Content-Type", "Idempotency-Key", actorHeader},
		AllowCredentials: true,
		MaxAge:           5 * time.Minute,
	}))
	router.Use(requestValidation(swagger))

	strictHandler := api.NewStrictHandlerWithOptions(appControllers, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  RequestErrorHandler,
		HandlerErrorFunc:         ResponseErrorHandler,
		ResponseErrorHandlerFunc: ResponseErrorHandler,
	})
	api.RegisterHandlers(router, strictHandler)
	return router, nil
}

const (
	actorHeader   = "X-Actor"
	maxActorBytes = 100
)

// requestContext records who is calling (X-Actor header) and which request this is, so the
// audit trail can attribute every change.
func requestContext() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		actor := strings.TrimSpace(ctx.GetHeader(actorHeader))
		if len(actor) > maxActorBytes {
			actor = actor[:maxActorBytes]
		}
		meta := requestctx.Meta{RequestID: requestid.Get(ctx), Actor: actor}
		ctx.Request = ctx.Request.WithContext(requestctx.With(ctx.Request.Context(), meta))
		ctx.Next()
	}
}

func requestValidation(swagger *openapi3.T) gin.HandlerFunc {
	validate := ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
		SilenceServersWarning: true,
		ErrorHandler: func(ctx *gin.Context, message string, status int) {
			WriteProblem(ctx, status, "bad_request", message)
		},
	})
	return func(ctx *gin.Context) {
		if ctx.Request.Method == http.MethodPost && scanUploadPath.MatchString(ctx.Request.URL.Path) {
			ctx.Next()
			return
		}
		validate(ctx)
	}
}
