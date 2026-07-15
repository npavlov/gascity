// Package controlapi owns the typed HTTP contract for GasCity Control Center.
package controlapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
	"github.com/gastownhall/gascity/internal/controlcenter/mailbox"
)

// Options supplies the runtime dependencies used by registered handlers.
type Options struct {
	CityName       string
	SupervisorPing func(context.Context) error
	State          *gcstate.Service
	Events         *gcstate.Hub
	Mail           mailbox.Reader
	eventKeepalive time.Duration
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
	registerConvoys(api, opts)
	registerBeads(api, opts)
	registerOrders(api, opts)
	registerMail(api, opts)
	registerEvents(api, opts)
	return api
}

func stateUnavailable() error {
	return huma.Error503ServiceUnavailable("Control Center Supervisor state is unavailable")
}

func mapStateError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return huma.Error503ServiceUnavailable(err.Error())
	}
	var upstream *gcstate.UpstreamError
	if errors.As(err, &upstream) {
		if upstream.StatusCode == http.StatusNotFound {
			return huma.Error404NotFound(upstream.Detail)
		}
		if upstream.StatusCode == http.StatusServiceUnavailable || upstream.StatusCode == http.StatusGatewayTimeout {
			return huma.Error503ServiceUnavailable(upstream.Detail)
		}
		switch upstream.Code {
		case "upstream_unavailable":
			return huma.Error503ServiceUnavailable(upstream.Detail)
		case "upstream_protocol", "upstream_http":
			return huma.Error502BadGateway(upstream.Detail)
		}
	}
	return huma.Error503ServiceUnavailable(err.Error())
}
