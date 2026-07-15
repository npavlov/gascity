package controlapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
)

func TestBeadRouteReturnsTrackedMembers(t *testing.T) {
	mux, _ := registeredTestAPI(t, populatedReader(), nil)
	recorder := requestAPI(t, mux, "/api/v1/convoys/convoy-1/beads")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var got gcstate.ResourceList[gcstate.BeadView]
	decodeAPI(t, recorder, &got)
	if len(got.Items) != 1 || got.Items[0].ID != "step-1" {
		t.Fatalf("beads = %#v", got)
	}
}

func TestBeadRouteValidatesConvoyID(t *testing.T) {
	mux, _ := registeredTestAPI(t, populatedReader(), nil)
	if got := requestAPI(t, mux, "/api/v1/convoys/bad%2Fid/beads").Code; got != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", got)
	}
}

func TestBeadRouteMapsUpstreamFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: &gcstate.UpstreamError{Code: "upstream_http", StatusCode: http.StatusNotFound, Detail: "missing"}, want: http.StatusNotFound},
		{name: "malformed", err: &gcstate.UpstreamError{Code: "upstream_protocol", StatusCode: http.StatusOK, Detail: "missing body"}, want: http.StatusBadGateway},
		{name: "deadline", err: context.DeadlineExceeded, want: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := populatedReader()
			reader.convoyErr = test.err
			mux, _ := registeredTestAPI(t, reader, nil)
			if got := requestAPI(t, mux, "/api/v1/convoys/convoy-1/beads").Code; got != test.want {
				t.Fatalf("status = %d, want %d", got, test.want)
			}
		})
	}
}
