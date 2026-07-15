package controlcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	controlapi "github.com/gastownhall/gascity/internal/controlcenter/api"
	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
	"github.com/gastownhall/gascity/internal/controlcenter/mailbox"
)

const shutdownTimeout = 5 * time.Second

// Dependencies contains the application edges supplied by the executable.
type Dependencies struct {
	StaticFS          fs.FS
	SupervisorFactory SupervisorFactory
}

// SupervisorBundle is the one city-scoped Supervisor dependency graph shared
// by health, authoritative reads, and the event hub.
type SupervisorBundle struct {
	Ping   func(context.Context) error
	State  *gcstate.Service
	Events *gcstate.Hub
	Mail   mailbox.Reader
}

// SupervisorFactory constructs all Supervisor edges from one normalized
// endpoint and city identity.
type SupervisorFactory func(supervisorURL, cityName string) (SupervisorBundle, error)

// App is one configured Control Center HTTP application.
type App struct {
	cfg     Config
	handler http.Handler
	events  *gcstate.Hub
	serve   func(*http.Server, net.Listener) error
}

type problemBody struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

// NewApp validates configuration and assembles the typed API and embedded SPA.
func NewApp(cfg Config, deps Dependencies) (*App, error) {
	normalized, err := normalizeConfig(cfg, configDependencies{})
	if err != nil {
		return nil, err
	}
	if deps.SupervisorFactory == nil {
		return nil, fmt.Errorf("control center: Supervisor factory is required")
	}
	bundle, err := deps.SupervisorFactory(normalized.SupervisorURL, normalized.CityName)
	if err != nil {
		return nil, fmt.Errorf("control center: create Supervisor dependencies: %w", err)
	}
	if bundle.Ping == nil || bundle.State == nil || bundle.Events == nil || bundle.Mail == nil {
		return nil, fmt.Errorf("control center: incomplete Supervisor dependency bundle")
	}
	staticHandler, err := newStaticHandler(deps.StaticFS)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	controlapi.Register(mux, controlapi.Options{
		CityName:       normalized.CityName,
		SupervisorPing: bundle.Ping,
		State:          bundle.State,
		Events:         bundle.Events,
		Mail:           bundle.Mail,
	})
	mux.Handle("/", staticHandler)

	return &App{
		cfg:     normalized,
		handler: hostGuard(mux),
		events:  bundle.Events,
		serve:   func(server *http.Server, listener net.Listener) error { return server.Serve(listener) },
	}, nil
}

// Handler returns the fully guarded local HTTP handler.
func (a *App) Handler() http.Handler {
	return a.handler
}

// Run listens on the validated loopback address until ctx is canceled.
func (a *App) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", a.cfg.BindAddress)
	if err != nil {
		return fmt.Errorf("control center: listen on %s: %w", a.cfg.BindAddress, err)
	}
	defer func() { _ = listener.Close() }()

	server := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	if err := a.events.Start(runCtx); err != nil {
		cancelRun()
		return fmt.Errorf("control center: start event hub: %w", err)
	}
	var stopOnce sync.Once
	stopHub := func() {
		stopOnce.Do(func() {
			cancelRun()
			a.events.Stop()
		})
	}
	defer stopHub()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- a.serve(server, listener)
	}()

	var primaryErr error
	serveExited := false
	select {
	case err := <-serveErr:
		serveExited = true
		if !errors.Is(err, http.ErrServerClosed) || ctx.Err() == nil {
			primaryErr = fmt.Errorf("control center: serve HTTP: %w", err)
		}
	case <-ctx.Done():
	}

	// Stop the hub first so every local SSE handler is released before HTTP
	// shutdown waits for active connections.
	stopHub()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	shutdownErr := server.Shutdown(shutdownCtx)
	cancelShutdown()
	if !serveExited {
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) && primaryErr == nil {
			primaryErr = fmt.Errorf("control center: serve HTTP during shutdown: %w", err)
		}
	}
	if shutdownErr != nil && primaryErr == nil {
		primaryErr = fmt.Errorf("control center: shut down HTTP server: %w", shutdownErr)
	}
	return primaryErr
}

func newStaticHandler(staticFS fs.FS) (http.Handler, error) {
	if staticFS == nil {
		return nil, fmt.Errorf("control center: static bundle is required")
	}
	index, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		return nil, fmt.Errorf("control center: read static index: %w", err)
	}
	files := http.FileServer(http.FS(staticFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		if isControlReservedPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			serveIndex(w, index)
			return
		}
		if info, err := fs.Stat(staticFS, path); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		if isAssetPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, index)
	}), nil
}

func serveIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(index)
}

func isControlReservedPath(path string) bool {
	for _, prefix := range []string{"/api", "/ws"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return strings.HasPrefix(path, "/openapi")
}

func isAssetPath(path string) bool {
	return path == "/assets" || strings.HasPrefix(path, "/assets/")
}

func hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedHost(r.Host) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(problemBody{
				Type:   "about:blank",
				Title:  "Forbidden",
				Status: http.StatusForbidden,
				Detail: "request Host is not allowed",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowedHost(raw string) bool {
	if raw == "127.0.0.1" {
		return true
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host != "127.0.0.1" || port == "" {
		return false
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	_, err = strconv.ParseUint(port, 10, 16)
	return err == nil
}
