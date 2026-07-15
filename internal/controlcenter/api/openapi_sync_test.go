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

func TestRegisterWithNilRuntimeDependenciesIncludesCompletePhaseFourContract(t *testing.T) {
	mux := http.NewServeMux()
	api := Register(mux, Options{CityName: "spec-city"})
	wantParameters := map[string][]string{
		"/api/v1/convoys":                  nil,
		"/api/v1/convoys/{id}":             {"id"},
		"/api/v1/convoys/{id}/beads":       {"id"},
		"/api/v1/orders":                   nil,
		"/api/v1/orders/history":           {"scoped_name", "before", "limit"},
		"/api/v1/orders/history/{bead_id}": {"bead_id", "store_ref"},
		"/api/v1/events":                   nil,
	}
	for path, parameterNames := range wantParameters {
		item := api.OpenAPI().Paths[path]
		if item == nil || item.Get == nil {
			t.Errorf("OpenAPI missing GET %s", path)
			continue
		}
		operation := item.Get
		if operation.OperationID == "" {
			t.Errorf("GET %s has no operationId", path)
		}
		if operation.Responses["200"] == nil {
			t.Errorf("GET %s has no typed 200 response", path)
		}
		gotParameters := map[string]bool{}
		for _, parameter := range operation.Parameters {
			gotParameters[parameter.Name] = true
		}
		for _, name := range parameterNames {
			if !gotParameters[name] {
				t.Errorf("GET %s missing %s parameter", path, name)
			}
		}
	}
	for _, path := range []string{
		"/api/v1/convoys",
		"/api/v1/convoys/{id}",
		"/api/v1/convoys/{id}/beads",
		"/api/v1/orders",
		"/api/v1/orders/history",
		"/api/v1/orders/history/{bead_id}",
	} {
		response := api.OpenAPI().Paths[path].Get.Responses["200"]
		if response.Content["application/json"] == nil || response.Content["application/json"].Schema == nil {
			t.Errorf("GET %s has no application/json response schema", path)
		}
	}
	events := api.OpenAPI().Paths["/api/v1/events"].Get.Responses["200"]
	if events.Content["text/event-stream"] == nil || events.Content["text/event-stream"].Schema == nil {
		t.Error("GET /api/v1/events has no text/event-stream response schema")
	}
}
