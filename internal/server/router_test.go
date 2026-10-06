package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"meta-frames-server/internal/common/requestctx"

	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/controllers"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/testutil"
)

func newHandler(test *testing.T) http.Handler {
	test.Helper()
	appServices := services.New(testutil.Store{Querier: testutil.NewMemory()}, testutil.NewMemoryFiles(), clock.Fixed{})
	handler, err := NewHandler(controllers.New(appServices), []string{"http://localhost:5173"})
	if err != nil {
		test.Fatal(err)
	}
	return handler
}

func serve(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestHealthIsServed(test *testing.T) {
	recorder := serve(newHandler(test), httptest.NewRequest("GET", "/health", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "ok") {
		test.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		test.Fatal("every response must carry a request id")
	}
}

func TestInvalidRequestsAreRejectedWithAProblemBody(test *testing.T) {
	handler := newHandler(test)

	invalidBody := httptest.NewRequest("PUT", "/cameras/"+uuid.NewString(), strings.NewReader(`{"brand": 3}`))
	invalidBody.Header.Set("Content-Type", "application/json")
	recorder := serve(handler, invalidBody)
	var problem map[string]string
	_ = json.Unmarshal(recorder.Body.Bytes(), &problem)
	if recorder.Code != http.StatusBadRequest || problem["code"] != "bad_request" {
		test.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}

	badID := serve(handler, httptest.NewRequest("GET", "/cameras/not-a-uuid", nil))
	if badID.Code != http.StatusBadRequest {
		test.Fatalf("a malformed id must be a 400, got %d", badID.Code)
	}
	if unknown := serve(handler, httptest.NewRequest("GET", "/nowhere", nil)); unknown.Code != http.StatusNotFound {
		test.Fatalf("an unknown route must be a 404, got %d", unknown.Code)
	}
}

func TestBusinessErrorsKeepTheirStatus(test *testing.T) {
	recorder := serve(newHandler(test), httptest.NewRequest("GET", "/cameras/"+uuid.NewString(), nil))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "not_found") {
		test.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
}

func TestCORSPreflightAllowsTheConfiguredOrigin(test *testing.T) {
	request := httptest.NewRequest("OPTIONS", "/cameras", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", "PUT")
	recorder := serve(newHandler(test), request)
	if recorder.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		test.Fatalf("headers = %v", recorder.Header())
	}
}

func TestScanUploadsBypassBodyValidation(test *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("scanner", "noritsu")
	_ = writer.Close()
	request := httptest.NewRequest("POST", "/processing/"+uuid.NewString()+"/scans", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	// An unknown job is a 404 from the controller, which proves the validator let the upload through.
	if recorder := serve(newHandler(test), request); recorder.Code != http.StatusNotFound {
		test.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
}

func TestRequestContextCarriesTheActorAndRequestID(test *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.ContextWithFallback = true
	router.Use(requestid.New(), requestContext())
	var seen requestctx.Meta
	router.GET("/x", func(ctx *gin.Context) {
		seen, _ = requestctx.From(ctx) // the same lookup services do, via the gin context
		ctx.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest("GET", "/x", nil)
	request.Header.Set("X-Actor", "  nam  ")
	request.Header.Set("X-Request-ID", "req-42")
	serve(router, request)
	if seen.Actor != "nam" || seen.RequestID != "req-42" {
		test.Fatalf("meta = %+v", seen)
	}

	long := httptest.NewRequest("GET", "/x", nil)
	long.Header.Set("X-Actor", strings.Repeat("a", 500))
	serve(router, long)
	if len(seen.Actor) != maxActorBytes {
		test.Fatalf("actor length = %d, want it capped at %d", len(seen.Actor), maxActorBytes)
	}

	serve(router, httptest.NewRequest("GET", "/x", nil))
	if seen.Actor != "" || seen.RequestID == "" {
		test.Fatalf("without a header the actor is empty but the request id is generated, got %+v", seen)
	}
}
