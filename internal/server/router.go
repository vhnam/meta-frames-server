// Package server assembles the HTTP handler: middleware, request validation and routes.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"

	apispec "meta-frames-server/api"
	"meta-frames-server/internal/api"
	"meta-frames-server/internal/auth"
	"meta-frames-server/internal/common/requestctx"
)

// scanUploadPath matches the multipart import route, which bypasses body validation
// because the validator would buffer entire uploads in memory.
var scanUploadPath = regexp.MustCompile(`^/processing/[^/]+/scans$`)

// NewHandler wires the controllers and the authboss routes into a Gin engine with CORS, the
// session check and OpenAPI validation. The spec documents the authboss routes too, so their
// bodies are validated like any other.
func NewHandler(appControllers api.StrictServerInterface, accounts *auth.Auth, corsOrigins []string) (http.Handler, error) {
	swagger, err := apispec.Load()
	if err != nil {
		return nil, err
	}
	swagger.Servers = nil // match any host
	sessionCheck, err := requireSession(swagger)
	if err != nil {
		return nil, err
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.ContextWithFallback = true // services read the caller from the request context
	// first, so the headers also reach the 401 from sessionCheck and the 403 from cors
	router.Use(requestid.New(), gin.Recovery(), securityHeaders())
	router.Use(gzip.Gzip(gzip.DefaultCompression)) // JSON compresses well; images are excluded by extension
	// Browsers send Origin on every cross-origin write, and this rejects origins outside the list
	// with a 403. Together with the SameSite session cookie that is the CSRF defence.
	router.Use(cors.New(cors.Config{
		AllowOrigins:     corsOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Idempotency-Key", "If-Match", "If-None-Match"},
		AllowCredentials: true,
		MaxAge:           5 * time.Minute,
	}))
	router.Use(accounts.LoadUser(), sessionCheck, requestContext(), singleJSONValue(http.MethodPut, "/auth/me/currency"), requestValidation(swagger))
	accounts.Mount(router)

	strictHandler := api.NewStrictHandlerWithOptions(appControllers, nil, api.StrictGinServerOptions{
		RequestErrorHandlerFunc:  RequestErrorHandler,
		HandlerErrorFunc:         ResponseErrorHandler,
		ResponseErrorHandlerFunc: ResponseErrorHandler,
	})
	api.RegisterHandlersWithOptions(router, strictHandler, api.GinServerOptions{ErrorHandler: ParameterErrorHandler})
	return router, nil
}

// securityHeaders sets the response headers that every API reply carries, errors included.
func securityHeaders() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		headers := ctx.Writer.Header()
		headers.Set("Strict-Transport-Security", "max-age=31536000")
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		headers.Set("Content-Security-Policy", "frame-ancestors 'none'")
		ctx.Next()
	}
}

// requestContext records who is calling (the signed-in account, whose records services are
// limited to) and which request this is, so the audit trail can attribute every change.
func requestContext() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		meta := requestctx.Meta{RequestID: requestid.Get(ctx)}
		if user, ok := auth.CurrentUser(ctx.Request.Context()); ok {
			meta.Actor, meta.OwnerID = user.Email, user.ID
		}
		ctx.Request = ctx.Request.WithContext(requestctx.With(ctx.Request.Context(), meta))
		ctx.Next()
	}
}

// singleJSONValue refuses a body for method and route that is not exactly one JSON value. The
// validator and the binding each decode one value and ignore what follows, so `{...}{}` would
// pass them. It runs for one route only: the money setting must never write on a doubtful body.
func singleJSONValue(method, route string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.Method != method || ctx.FullPath() != route {
			ctx.Next()
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 1<<20))
		if err == nil {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			var first, second json.RawMessage
			if err = decoder.Decode(&first); err == nil {
				err = decoder.Decode(&second)
				if errors.Is(err, io.EOF) {
					err = nil
				} else if err == nil {
					err = errors.New("data after the JSON value")
				}
			}
		}
		if err != nil {
			WriteProblem(ctx, http.StatusBadRequest, "bad_request", "invalid request")
			return
		}
		ctx.Request.Body = io.NopCloser(bytes.NewReader(raw))
		ctx.Next()
	}
}

func requestValidation(swagger *openapi3.T) gin.HandlerFunc {
	validate := ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
		SilenceServersWarning: true,
		// requireSession has already checked the session cookie with the right status (401).
		Options: openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
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
