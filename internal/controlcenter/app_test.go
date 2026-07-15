package controlcenter

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func staticTestFS() fs.FS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>Control Center</title><main id=\"root\"></main>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('control center')")},
	}
}

func newTestApp(t *testing.T, ping func(context.Context) error) *App {
	t.Helper()
	app, err := NewApp(validTestConfig(), Dependencies{StaticFS: staticTestFS(), SupervisorPing: ping})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	return app
}

func requestApp(t *testing.T, app *App, path, host string) *httptest.ResponseRecorder {
	return requestAppMethod(t, app, http.MethodGet, path, host)
}

func requestAppMethod(t *testing.T, app *App, method, path, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	return rec
}

func TestStaticRejectsUnsupportedMethodsBeforeServingIndexOrAssets(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })
	for _, path := range []string{"/", "/index.html", "/assets/app.js", "/convoys/ga-123"} {
		t.Run(path, func(t *testing.T) {
			rec := requestAppMethod(t, app, http.MethodPost, path, "127.0.0.1:8080")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("POST %s = %d, want 404; body=%q", path, rec.Code, rec.Body.String())
			}
		})
	}
	for _, path := range []string{"/", "/assets/app.js"} {
		t.Run("HEAD "+path, func(t *testing.T) {
			rec := requestAppMethod(t, app, http.MethodHead, path, "127.0.0.1:8080")
			if rec.Code != http.StatusOK {
				t.Fatalf("HEAD %s = %d, want 200", path, rec.Code)
			}
		})
	}
}

func TestStaticReservedPathsCannotBeShadowedByPackagedFiles(t *testing.T) {
	shadowed := fstest.MapFS{
		"index.html":          &fstest.MapFile{Data: []byte("<!doctype html><title>Control Center</title>")},
		"api/shadow.json":     &fstest.MapFile{Data: []byte(`{"secret":true}`)},
		"ws/shadow.txt":       &fstest.MapFile{Data: []byte("shadow")},
		"openapi-shadow.json": &fstest.MapFile{Data: []byte(`{"openapi":"shadow"}`)},
	}
	app, err := NewApp(validTestConfig(), Dependencies{
		StaticFS:       shadowed,
		SupervisorPing: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	for _, path := range []string{"/api/shadow.json", "/ws/shadow.txt", "/openapi-shadow.json"} {
		rec := requestApp(t, app, path, "127.0.0.1:8080")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404; body=%q", path, rec.Code, rec.Body.String())
		}
	}
}

func TestStaticServesAssetAndBrowserDeepLink(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })

	asset := requestApp(t, app, "/assets/app.js", "127.0.0.1:8080")
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "control center") {
		t.Fatalf("asset = %d %q", asset.Code, asset.Body.String())
	}
	deepLink := requestApp(t, app, "/convoys/ga-123/diff", "127.0.0.1")
	if deepLink.Code != http.StatusOK || !strings.Contains(deepLink.Body.String(), "Control Center") {
		t.Fatalf("deep link = %d %q", deepLink.Code, deepLink.Body.String())
	}
}

func TestStaticKeepsReservedAndMissingAssetPathsAt404(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })
	for _, path := range []string{
		"/api",
		"/api/v1/missing",
		"/ws",
		"/ws/v1/missing",
		"/assets/missing.js",
		"/openapi.yaml/child",
		"/openapi.json/child",
	} {
		t.Run(path, func(t *testing.T) {
			rec := requestApp(t, app, path, "127.0.0.1:8080")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s = %d, want 404; body=%q", path, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHostGuardAllowsOnlyLiteralLoopback(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })
	for _, host := range []string{"127.0.0.1", "127.0.0.1:8080", "127.0.0.1:0"} {
		if got := requestApp(t, app, "/", host).Code; got != http.StatusOK {
			t.Errorf("Host %q = %d, want 200", host, got)
		}
	}
	for _, host := range []string{"evil.example", "localhost:8080", "[::1]:8080", "127.0.0.1:http", "127.0.0.2:8080", ""} {
		t.Run(host, func(t *testing.T) {
			if got := requestApp(t, app, "/", host).Code; got != http.StatusForbidden {
				t.Fatalf("Host %q = %d, want 403", host, got)
			}
		})
	}
}

func TestHostGuardReturnsTypedProblemDetails(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })
	rec := requestApp(t, app, "/api/v1/health", "attacker.example")
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var got problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode problem details: %v", err)
	}
	want := problemBody{
		Type:   "about:blank",
		Title:  "Forbidden",
		Status: http.StatusForbidden,
		Detail: "request Host is not allowed",
	}
	if got != want {
		t.Fatalf("problem details = %#v, want %#v", got, want)
	}
}

func TestAppRunStopsGracefullyWhenContextIsCancelled(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

func TestNewAppRejectsMissingStaticBundle(t *testing.T) {
	_, err := NewApp(validTestConfig(), Dependencies{
		StaticFS: fstest.MapFS{},
		SupervisorPing: func(context.Context) error {
			return errors.New("not relevant")
		},
	})
	if err == nil {
		t.Fatal("NewApp accepted a static bundle without index.html")
	}
}
