package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthReportsConnectedSupervisor(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{
		CityName: "taxdome",
		SupervisorPing: func(context.Context) error {
			return nil
		},
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/health = %d: %s", rec.Code, rec.Body.String())
	}

	var got HealthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	want := HealthBody{
		SchemaVersion:       1,
		Status:              "ok",
		City:                "taxdome",
		SupervisorReachable: true,
	}
	if got != want {
		t.Fatalf("health = %#v, want %#v", got, want)
	}
}

func TestHealthReportsSupervisorDegradedWithoutFailingLocalApp(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{
		CityName: "taxdome",
		SupervisorPing: func(context.Context) error {
			return errors.New("connection refused")
		},
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/health = %d: %s", rec.Code, rec.Body.String())
	}

	var got HealthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if got.Status != "degraded" || got.SupervisorReachable {
		t.Fatalf("degraded health = %#v", got)
	}
}

func TestHealthUsesTypedOpenAPISchema(t *testing.T) {
	mux := http.NewServeMux()
	api := Register(mux, Options{CityName: "taxdome", SupervisorPing: func(context.Context) error { return nil }})
	schema := api.OpenAPI().Components.Schemas.Map()["HealthBody"]
	if schema == nil {
		t.Fatal("OpenAPI is missing named HealthBody schema")
	}
	for _, property := range []string{"schema_version", "status", "city", "supervisor_reachable"} {
		if schema.Properties[property] == nil {
			t.Errorf("HealthBody schema is missing %q", property)
		}
	}
}
