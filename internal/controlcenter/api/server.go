// Package controlapi owns the typed HTTP contract for GasCity Control Center.
package controlapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Options supplies the runtime dependencies used by registered handlers.
type Options struct {
	CityName       string
	SupervisorPing func(context.Context) error
}

// Register installs the complete Control Center API on mux and returns its
// Huma API so generators and sync tests inspect the same live contract.
func Register(mux *http.ServeMux, opts Options) huma.API {
	cfg := huma.DefaultConfig("GasCity Control Center API", "0.1.0")
	cfg.SchemasPath = ""
	cfg.DocsPath = ""
	cfg.CreateHooks = nil
	api := humago.New(mux, cfg)
	registerHealth(api, opts)
	return api
}
