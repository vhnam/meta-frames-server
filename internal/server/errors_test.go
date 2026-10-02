package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"meta-frames-server/internal/api"
	"meta-frames-server/internal/common/apperror"
)

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/x", nil)
	return ctx, recorder
}

func decodeProblem(test *testing.T, recorder *httptest.ResponseRecorder) api.Problem {
	test.Helper()
	var problem api.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		test.Fatalf("body %q is not a problem: %v", recorder.Body.String(), err)
	}
	return problem
}

func TestResponseErrorHandlerMapsBusinessErrorsToStatuses(test *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", apperror.NotFound("camera"), http.StatusNotFound, "not_found"},
		{"conflict", apperror.Conflict("camera_loaded", "busy"), http.StatusConflict, "camera_loaded"},
		{"unprocessable", apperror.Unprocessable("bad", "nope"), http.StatusUnprocessableEntity, "bad"},
		{"unknown kind", &apperror.Error{Kind: 99, Code: "weird", Message: "?"}, http.StatusInternalServerError, "weird"},
	}
	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			ctx, recorder := newTestContext()
			ResponseErrorHandler(ctx, testCase.err)
			if recorder.Code != testCase.wantStatus {
				test.Fatalf("status = %d, want %d", recorder.Code, testCase.wantStatus)
			}
			if got := decodeProblem(test, recorder); got.Code != testCase.wantCode {
				test.Fatalf("code = %q, want %q", got.Code, testCase.wantCode)
			}
			if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
				test.Fatalf("Content-Type = %q", contentType)
			}
		})
	}
}

func TestResponseErrorHandlerHidesUnexpectedErrorDetails(test *testing.T) {
	ctx, recorder := newTestContext()
	ResponseErrorHandler(ctx, errors.New("connection refused: db password=hunter2"))

	if recorder.Code != http.StatusInternalServerError {
		test.Fatalf("status = %d", recorder.Code)
	}
	problem := decodeProblem(test, recorder)
	if problem.Code != "internal" || problem.Message != "internal error" {
		test.Fatalf("problem = %+v", problem)
	}
}

func TestRequestErrorHandlerReportsBadRequest(test *testing.T) {
	ctx, recorder := newTestContext()
	RequestErrorHandler(ctx, errors.New("invalid UUID"))

	if recorder.Code != http.StatusBadRequest {
		test.Fatalf("status = %d", recorder.Code)
	}
	if problem := decodeProblem(test, recorder); problem.Code != "bad_request" || problem.Message != "invalid UUID" {
		test.Fatalf("problem = %+v", problem)
	}
}
