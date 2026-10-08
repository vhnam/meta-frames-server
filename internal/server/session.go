package server

import (
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/gin-gonic/gin"

	"meta-frames-server/internal/auth"
)

// requireSession answers 401 for operations that need a session when the request has none.
// The spec decides: the top-level `security` applies to every operation, and the public ones
// (/health and the /auth routes) opt out with `security: []`. Unknown routes pass through so
// validation can answer 404.
func requireSession(swagger *openapi3.T) (gin.HandlerFunc, error) {
	router, err := gorillamux.NewRouter(swagger)
	if err != nil {
		return nil, err
	}
	return func(ctx *gin.Context) {
		route, _, err := router.FindRoute(ctx.Request)
		if err != nil || !needsSession(swagger, route.Operation) {
			ctx.Next()
			return
		}
		if _, ok := auth.CurrentUser(ctx.Request.Context()); !ok {
			WriteProblem(ctx, http.StatusUnauthorized, "unauthorized", "not logged in")
			return
		}
		ctx.Next()
	}, nil
}

func needsSession(swagger *openapi3.T, operation *openapi3.Operation) bool {
	requirements := swagger.Security
	if operation.Security != nil {
		requirements = *operation.Security
	}
	for _, requirement := range requirements {
		if len(requirement) == 0 { // {} in the list makes the session optional
			return false
		}
	}
	return len(requirements) > 0
}
