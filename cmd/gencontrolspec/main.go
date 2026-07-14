// Command gencontrolspec writes the live Control Center OpenAPI document.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	controlapi "github.com/gastownhall/gascity/internal/controlcenter/api"
)

func main() {
	mux := http.NewServeMux()
	controlapi.Register(mux, controlapi.Options{
		CityName:       "spec-city",
		SupervisorPing: func(context.Context) error { return nil },
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if rec.Code != http.StatusOK {
		fatalf("GET /openapi.json returned %d: %s", rec.Code, rec.Body.String())
	}

	var document any
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		fatalf("decode generated OpenAPI: %v", err)
	}
	var formatted bytes.Buffer
	encoder := json.NewEncoder(&formatted)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		fatalf("format generated OpenAPI: %v", err)
	}

	path := filepath.Join("internal", "controlcenter", "openapi.json")
	if err := os.WriteFile(path, formatted.Bytes(), 0o644); err != nil {
		fatalf("write %s: %v", path, err)
	}
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "gencontrolspec: "+format+"\n", args...)
	os.Exit(1)
}
