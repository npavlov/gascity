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
	"time"

	controlapi "github.com/gastownhall/gascity/internal/controlcenter/api"
)

const shutdownTimeout = 5 * time.Second

// Dependencies contains the application edges supplied by the executable.
type Dependencies struct {
	StaticFS              fs.FS
	SupervisorPing        func(context.Context) error
	SupervisorPingFactory func(string) (func(context.Context) error, error)
}

// App is one configured Control Center HTTP application.
type App struct {
	cfg     Config
	handler http.Handler
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
	if deps.SupervisorPing != nil && deps.SupervisorPingFactory != nil {
		return nil, fmt.Errorf("control center: provide either SupervisorPing or SupervisorPingFactory, not both")
	}
	supervisorPing := deps.SupervisorPing
	if deps.SupervisorPingFactory != nil {
		supervisorPing, err = deps.SupervisorPingFactory(normalized.SupervisorURL)
		if err != nil {
			return nil, fmt.Errorf("control center: create Supervisor ping: %w", err)
		}
		if supervisorPing == nil {
			return nil, fmt.Errorf("control center: Supervisor ping factory returned nil")
		}
	}
	staticHandler, err := newStaticHandler(deps.StaticFS)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	controlapi.Register(mux, controlapi.Options{
		CityName:       normalized.CityName,
		SupervisorPing: supervisorPing,
	})
	mux.Handle("/", staticHandler)

	return &App{
		cfg:     normalized,
		handler: hostGuard(mux),
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

	server := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("control center: serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("control center: shut down HTTP server: %w", err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("control center: serve HTTP during shutdown: %w", err)
		}
		return nil
	}
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
