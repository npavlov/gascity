package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAPISpecInSync(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{
		CityName:       "spec-city",
		SupervisorPing: func(context.Context) error { return nil },
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json = %d: %s", rec.Code, rec.Body.String())
	}

	var document any
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode live OpenAPI: %v", err)
	}
	var live bytes.Buffer
	encoder := json.NewEncoder(&live)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		t.Fatalf("format live OpenAPI: %v", err)
	}

	path := filepath.Join("..", "openapi.json")
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `go run ./cmd/gencontrolspec`)", path, err)
	}
	if !bytes.Equal(committed, live.Bytes()) {
		t.Fatalf("%s is out of sync with the registered API; run `go run ./cmd/gencontrolspec`", path)
	}
}
