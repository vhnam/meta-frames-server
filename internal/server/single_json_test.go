package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSingleJSONValueGuardsOneRoute(test *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(singleJSONValue(http.MethodPut, "/guarded"))
	echo := func(ctx *gin.Context) {
		body, _ := io.ReadAll(ctx.Request.Body)
		ctx.String(http.StatusOK, string(body))
	}
	router.PUT("/guarded", echo)
	router.PUT("/other", echo)

	cases := []struct {
		name, path, body string
		want             int
	}{
		{"one object", "/guarded", `{"a":1}`, http.StatusOK},
		{"trailing whitespace", "/guarded", "{\"a\":1} \n\t", http.StatusOK},
		{"object then object", "/guarded", `{"a":1}{}`, http.StatusBadRequest},
		{"object then text", "/guarded", `{"a":1}x`, http.StatusBadRequest},
		{"empty body", "/guarded", ``, http.StatusBadRequest},
		{"malformed", "/guarded", `{"a":`, http.StatusBadRequest},
		{"other route with trailing data", "/other", `{"a":1}{}`, http.StatusOK},
	}
	for _, testCase := range cases {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, testCase.path, strings.NewReader(testCase.body)))
		if recorder.Code != testCase.want {
			test.Errorf("%s: status = %d, want %d", testCase.name, recorder.Code, testCase.want)
		}
		if testCase.want == http.StatusOK && recorder.Body.String() != testCase.body {
			test.Errorf("%s: the handler read %q, want the original body", testCase.name, recorder.Body.String())
		}
	}
}
