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
	apperror.KindNotFound:           http.StatusNotFound,
	apperror.KindConflict:           http.StatusConflict,
	apperror.KindUnprocessable:      http.StatusUnprocessableEntity,
	apperror.KindBadRequest:         http.StatusBadRequest,
	apperror.KindPreconditionFailed: http.StatusPreconditionFailed,
}

// WriteProblem renders the standard {code, message} error body and stops the handler chain.
func WriteProblem(ctx *gin.Context, status int, code, message string) {
	ctx.AbortWithStatusJSON(status, api.Problem{Code: code, Message: message})
}

// RequestErrorHandler handles malformed requests detected by the generated server. The message is
// fixed: the decoder's error text can name Go types.
func RequestErrorHandler(ctx *gin.Context, _ error) {
	WriteProblem(ctx, http.StatusBadRequest, "bad_request", "invalid request")
}

// ParameterErrorHandler handles path and query parameter binding errors of the generated wrapper.
// The message is fixed: the binding error names Go types (for example uuid.UUID).
func ParameterErrorHandler(ctx *gin.Context, _ error, status int) {
	WriteProblem(ctx, status, "bad_request", "invalid path or query parameter")
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
