package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigMapsExplicitFlagsWithoutChangingIdentity(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--bind", "127.0.0.1:9010",
		"--supervisor-url", "http://127.0.0.1:9011",
		"--city", "taxdome",
		"--gc", "/custom/bin/gc",
		"--pack", "gascity",
		"--mayor-identity", "taxdome/lead.operator",
		"--assistant-template", "taxdome/workers.convoy_assistant",
		"--terminal-app", "iTerm",
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.BindAddress != "127.0.0.1:9010" || cfg.SupervisorURL != "http://127.0.0.1:9011" {
		t.Fatalf("network flags not mapped: %#v", cfg)
	}
	if cfg.MayorIdentity != "taxdome/lead.operator" {
		t.Fatalf("MayorIdentity = %q", cfg.MayorIdentity)
	}
	if cfg.GCExecutable != "/custom/bin/gc" || cfg.NativeTerminalApp != "iTerm" {
		t.Fatalf("executable flags not mapped: %#v", cfg)
	}
}

func TestParseConfigProvidesOnlyInfrastructureDefaults(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--city", "taxdome",
		"--pack", "gascity",
		"--mayor-identity", "taxdome/lead.operator",
		"--assistant-template", "taxdome/workers.convoy_assistant",
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.BindAddress != "127.0.0.1:8477" {
		t.Errorf("BindAddress = %q", cfg.BindAddress)
	}
	if cfg.SupervisorURL != "http://127.0.0.1:8372" {
		t.Errorf("SupervisorURL = %q", cfg.SupervisorURL)
	}
	if cfg.GCExecutable != "" {
		t.Errorf("GCExecutable = %q, want deferred PATH resolution", cfg.GCExecutable)
	}
	if cfg.NativeTerminalApp != "Terminal" {
		t.Errorf("NativeTerminalApp = %q", cfg.NativeTerminalApp)
	}
}

func TestSupervisorPingRequiresTypedSuccessResponse(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{
			name:   "typed health",
			status: http.StatusOK,
			body:   `{"cities_running":1,"cities_total":1,"status":"ok","uptime_sec":10,"version":"test"}`,
			ok:     true,
		},
		{name: "non-success", status: http.StatusServiceUnavailable, body: `{"type":"about:blank"}`},
		{name: "malformed success", status: http.StatusOK, body: `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			ping, err := newSupervisorPing(server.URL, server.Client())
			if err != nil {
				t.Fatalf("newSupervisorPing: %v", err)
			}
			err = ping(context.Background())
			if tt.ok && err != nil {
				t.Fatalf("ping: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("ping accepted an untyped or non-success response")
			}
		})
	}
}

func TestEmbeddedWebFSContainsProductionIndex(t *testing.T) {
	web, err := embeddedWebFS()
	if err != nil {
		t.Fatalf("embeddedWebFS: %v", err)
	}
	data, err := fs.ReadFile(web, "index.html")
	if err != nil {
		t.Fatalf("read embedded index: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded index is empty")
	}
}

func TestMakefileKeepsControlCenterBuildOrderAndOutput(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)
	for _, contract := range []string{
		"control-center-web-install:",
		"control-center-gen: control-center-web-install",
		"control-center-build: control-center-gen",
		"control-center-test: control-center-gen",
		"control-center-check: control-center-build control-center-test",
		"go build -o $(BUILD_DIR)/gc-control ./cmd/gc-control",
		"rm -f $(BUILD_DIR)/$(BINARY) $(BUILD_DIR)/gc-control",
	} {
		if !strings.Contains(makefile, contract) {
			t.Errorf("Makefile is missing %q", contract)
		}
	}
}
