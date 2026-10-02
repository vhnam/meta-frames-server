// Package integration_test exercises the whole HTTP stack against a real Postgres container.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"meta-frames-server/internal/common/clock"
	"meta-frames-server/internal/controllers"
	"meta-frames-server/internal/db"
	"meta-frames-server/internal/server"
	"meta-frames-server/internal/services"
	"meta-frames-server/internal/storage"
)

type harness struct {
	test    *testing.T
	pool    *pgxpool.Pool
	handler http.Handler
}

type response struct {
	Status int
	Body   map[string]any
	Raw    []byte
}

func newHarness(test *testing.T) *harness {
	test.Helper()
	if testing.Short() {
		test.Skip("integration tests need Docker; skipped with -short")
	}
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)))
	if err != nil {
		test.Skipf("docker unavailable: %v", err)
	}
	test.Cleanup(func() { _ = container.Terminate(ctx) })

	connectionString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		test.Fatal(err)
	}
	poolConfig, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		test.Fatal(err)
	}
	if err := db.Migrate(poolConfig.ConnConfig); err != nil {
		test.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(pool.Close)

	files, err := storage.NewLocal(test.TempDir())
	if err != nil {
		test.Fatal(err)
	}
	appServices := services.New(db.NewPostgresStore(pool), files, clock.System{})
	handler, err := server.NewHandler(controllers.New(appServices), []string{"*"})
	if err != nil {
		test.Fatal(err)
	}
	return &harness{test: test, pool: pool, handler: handler}
}

func newID() string { return uuid.NewString() }

func (harness *harness) serve(request *http.Request) response {
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	result := response{Status: recorder.Code, Raw: recorder.Body.Bytes()}
	_ = json.Unmarshal(result.Raw, &result.Body)
	return result
}

// call sends a JSON request; a nil body sends no payload.
func (harness *harness) call(method, path string, body any) response {
	harness.test.Helper()
	payload := []byte(nil)
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	return harness.serve(request)
}

func (harness *harness) expect(result response, wantStatus int) response {
	harness.test.Helper()
	if result.Status != wantStatus {
		harness.test.Fatalf("status = %d, want %d; body: %s", result.Status, wantStatus, result.Raw)
	}
	return result
}

func (harness *harness) execSQL(statement string, arguments ...any) {
	harness.test.Helper()
	if _, err := harness.pool.Exec(context.Background(), statement, arguments...); err != nil {
		harness.test.Fatal(err)
	}
}

// listOf GETs a path that returns a JSON array.
func (harness *harness) listOf(path string) []map[string]any {
	harness.test.Helper()
	result := harness.expect(harness.call("GET", path, nil), http.StatusOK)
	var items []map[string]any
	_ = json.Unmarshal(result.Raw, &items)
	return items
}

func (harness *harness) rollStatus(rollID string) string {
	harness.test.Helper()
	result := harness.expect(harness.call("GET", "/rolls/"+rollID, nil), http.StatusOK)
	return result.Body["roll"].(map[string]any)["status"].(string)
}

type upload struct {
	fileName    string
	frameNumber *int
	content     []byte
}

func (harness *harness) uploadScans(jobID, scanner, onConflict string, uploads []upload) response {
	harness.test.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("scanner", scanner)
	if onConflict != "" {
		_ = writer.WriteField("onConflict", onConflict)
	}
	for _, item := range uploads {
		if item.frameNumber != nil {
			_ = writer.WriteField("frameNumber", fmt.Sprint(*item.frameNumber))
		}
		part, _ := writer.CreateFormFile("file", item.fileName)
		_, _ = part.Write(item.content)
	}
	_ = writer.Close()
	request := httptest.NewRequest("POST", "/processing/"+jobID+"/scans", &buffer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return harness.serve(request)
}

func frameNumber(value int) *int { return &value }

func asList(value any) []any { return value.([]any) }

func asObject(value any) map[string]any { return value.(map[string]any) }
