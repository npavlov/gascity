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
	"github.com/gastownhall/gascity/internal/controlcenter/mayor"
)

// Options supplies the runtime dependencies used by registered handlers.
type Options struct {
	CityName       string
	SupervisorPing func(context.Context) error
	State          *gcstate.Service
	Events         *gcstate.Hub
	Mayor          *mayor.Service
	MayorEvents    *mayor.Hub
	eventKeepalive time.Duration
	mayorKeepalive time.Duration
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
	registerEvents(api, opts)
	registerMayor(api, opts)
	return api
}

func stateUnavailable() error {
	return huma.Error503ServiceUnavailable("Control Center Supervisor state is unavailable")
}

const mayorProblemTypePrefix = "urn:gascity:control-center:mayor:"

func mayorProblem(status int, code, detail string) error {
	if code == "" {
		code = "mayor_request_failed"
	}
	return &huma.ErrorModel{
		Type:   mayorProblemTypePrefix + code,
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}
}

func mapMayorError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return mayorProblem(http.StatusServiceUnavailable, "mayor_unavailable", err.Error())
	}
	var domain *mayor.Error
	if errors.As(err, &domain) {
		switch domain.StatusCode {
		case http.StatusUnprocessableEntity:
			return mayorProblem(http.StatusUnprocessableEntity, domain.Code, domain.Detail)
		case http.StatusConflict:
			return mayorProblem(http.StatusConflict, domain.Code, domain.Detail)
		case http.StatusNotFound:
			return mayorProblem(http.StatusNotFound, domain.Code, domain.Detail)
		case http.StatusBadGateway:
			return mayorProblem(http.StatusBadGateway, domain.Code, domain.Detail)
		case http.StatusGatewayTimeout:
			if domain.Code == "submit_result_timeout" {
				return mayorProblem(http.StatusGatewayTimeout, domain.Code, domain.Detail)
			}
			return mayorProblem(http.StatusServiceUnavailable, domain.Code, domain.Detail)
		default:
			return mayorProblem(http.StatusServiceUnavailable, domain.Code, domain.Detail)
		}
	}
	var upstream *mayor.UpstreamError
	if errors.As(err, &upstream) {
		if upstream.StatusCode == http.StatusNotFound {
			return mayorProblem(http.StatusNotFound, upstream.Code, upstream.Detail)
		}
		if upstream.Code == "upstream_unavailable" || upstream.StatusCode == http.StatusServiceUnavailable || upstream.StatusCode == http.StatusGatewayTimeout {
			return mayorProblem(http.StatusServiceUnavailable, upstream.Code, upstream.Detail)
		}
		if upstream.Code == "upstream_protocol" {
			return mayorProblem(http.StatusBadGateway, upstream.Code, upstream.Detail)
		}
		if upstream.StatusCode >= http.StatusInternalServerError {
			return mayorProblem(http.StatusServiceUnavailable, upstream.Code, upstream.Detail)
		}
		return mayorProblem(http.StatusBadGateway, upstream.Code, upstream.Detail)
	}
	return mayorProblem(http.StatusServiceUnavailable, "mayor_unavailable", err.Error())
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
