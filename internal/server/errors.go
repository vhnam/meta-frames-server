package server

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
)

// statusByKind maps business error kinds to HTTP statuses.
var statusByKind = map[apperror.Kind]int{
	apperror.KindNotFound:      http.StatusNotFound,
	apperror.KindConflict:      http.StatusConflict,
	apperror.KindUnprocessable: http.StatusUnprocessableEntity,
}

// WriteProblem renders the standard {code, message} error body and stops the handler chain.
func WriteProblem(ctx *gin.Context, status int, code, message string) {
	ctx.AbortWithStatusJSON(status, api.Problem{Code: code, Message: message})
}

// RequestErrorHandler handles malformed requests detected by the generated server.
func RequestErrorHandler(ctx *gin.Context, err error) {
	WriteProblem(ctx, http.StatusBadRequest, "bad_request", err.Error())
}

// ResponseErrorHandler turns errors returned by controllers into HTTP responses.
func ResponseErrorHandler(ctx *gin.Context, err error) {
	if appError, ok := apperror.From(err); ok {
		status, known := statusByKind[appError.Kind]
		if !known {
			status = http.StatusInternalServerError
		}
		WriteProblem(ctx, status, appError.Code, appError.Message)
		return
	}
	slog.Error("request failed", "path", ctx.Request.URL.Path, "err", err)
	WriteProblem(ctx, http.StatusInternalServerError, "internal", "internal error")
}
