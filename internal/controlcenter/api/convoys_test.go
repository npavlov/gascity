package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
)

func TestConvoyRoutesProjectListsDetailsAndPartialReads(t *testing.T) {
	reader := populatedReader()
	mux, _ := registeredTestAPI(t, reader, nil)

	list := requestAPI(t, mux, "/api/v1/convoys")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", list.Code, list.Body.String())
	}
	var listed gcstate.ResourceList[gcstate.ConvoySummary]
	decodeAPI(t, list, &listed)
	if len(listed.Items) != 1 || listed.Items[0].Progress == nil || listed.Items[0].Progress.Total != 1 {
		t.Fatalf("convoys = %#v", listed)
	}

	detail := requestAPI(t, mux, "/api/v1/convoys/convoy-1")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d: %s", detail.Code, detail.Body.String())
	}
	var projected gcstate.ConvoyDetail
	decodeAPI(t, detail, &projected)
	if projected.Convoy.ID != "convoy-1" || len(projected.Beads) != 1 || len(projected.Sessions) != 1 {
		t.Fatalf("detail = %#v", projected)
	}

	reader.convoys.Partial = true
	reader.convoys.Problems = []gcstate.Problem{{Code: "upstream_partial", Source: "convoys", Detail: "one store unavailable", Retryable: true}}
	partialMux, _ := registeredTestAPI(t, reader, nil)
	partial := requestAPI(t, partialMux, "/api/v1/convoys")
	decodeAPI(t, partial, &listed)
	if partial.Code != http.StatusOK || !listed.Degraded || len(listed.Items) != 1 || len(listed.Problems) == 0 {
		t.Fatalf("partial list = status %d body %#v", partial.Code, listed)
	}
}

func TestConvoyListRequiresLastConfirmedDataOnEssentialFailure(t *testing.T) {
	failure := gcstate.Page[gcstate.BeadSource]{
		Partial:  true,
		Problems: []gcstate.Problem{{Code: "upstream_unavailable", Source: "convoys", Detail: "Supervisor offline", Retryable: true}},
	}
	firstReader := populatedReader()
	firstReader.convoys = failure
	firstReader.recent = gcstate.Page[gcstate.BeadSource]{Items: []gcstate.BeadSource{}}
	firstMux, _ := registeredTestAPI(t, firstReader, nil)
	if got := requestAPI(t, firstMux, "/api/v1/convoys").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("first-load failure status = %d, want 503", got)
	}

	cachedReader := populatedReader()
	cachedMux, _ := registeredTestAPI(t, cachedReader, nil)
	if got := requestAPI(t, cachedMux, "/api/v1/convoys").Code; got != http.StatusOK {
		t.Fatalf("prime cache status = %d", got)
	}
	cachedReader.convoys = failure
	recorder := requestAPI(t, cachedMux, "/api/v1/convoys")
	var stale gcstate.ResourceList[gcstate.ConvoySummary]
	decodeAPI(t, recorder, &stale)
	if recorder.Code != http.StatusOK || !stale.Stale || !stale.Degraded || len(stale.Items) != 1 {
		t.Fatalf("cached failure = status %d body %#v", recorder.Code, stale)
	}
}

func TestConvoyListUsesRecentDataWhenActiveUnavailable(t *testing.T) {
	reader := populatedReader()
	reader.convoys = gcstate.Page[gcstate.BeadSource]{
		Partial:  true,
		Problems: []gcstate.Problem{{Code: "upstream_unavailable", Source: "convoys", Detail: "active store unavailable", Retryable: true}},
	}
	recent := *reader.convoy.Convoy
	recent.Status = "closed"
	reader.recent = gcstate.Page[gcstate.BeadSource]{Items: []gcstate.BeadSource{recent}}
	reader.convoy.Convoy = &recent
	mux, _ := registeredTestAPI(t, reader, nil)

	recorder := requestAPI(t, mux, "/api/v1/convoys")
	var got gcstate.ResourceList[gcstate.ConvoySummary]
	decodeAPI(t, recorder, &got)

	if recorder.Code != http.StatusOK || len(got.Items) != 1 || got.Items[0].ID != "convoy-1" || !got.Degraded || got.Stale {
		t.Fatalf("response = status %d body %#v, want usable degraded recent data", recorder.Code, got)
	}
}

func TestConvoyListRequiresCacheForItemlessPartialPages(t *testing.T) {
	failure := gcstate.Page[gcstate.BeadSource]{
		Partial:  true,
		Problems: []gcstate.Problem{{Code: "upstream_partial", Source: "convoys", Detail: "store returned no usable items", Retryable: true}},
	}
	reader := populatedReader()
	reader.convoys, reader.recent = failure, failure
	mux, _ := registeredTestAPI(t, reader, nil)
	if got := requestAPI(t, mux, "/api/v1/convoys").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("first-load status = %d, want 503", got)
	}

	cachedReader := populatedReader()
	cachedMux, _ := registeredTestAPI(t, cachedReader, nil)
	if got := requestAPI(t, cachedMux, "/api/v1/convoys").Code; got != http.StatusOK {
		t.Fatalf("prime cache status = %d", got)
	}
	cachedReader.convoys, cachedReader.recent = failure, failure
	recorder := requestAPI(t, cachedMux, "/api/v1/convoys")
	var stale gcstate.ResourceList[gcstate.ConvoySummary]
	decodeAPI(t, recorder, &stale)
	if recorder.Code != http.StatusOK || len(stale.Items) != 1 || !stale.Degraded || !stale.Stale {
		t.Fatalf("cached response = status %d body %#v", recorder.Code, stale)
	}
}

func TestConvoyRoutesValidateIDsAndMapUpstreamErrors(t *testing.T) {
	reader := populatedReader()
	mux, _ := registeredTestAPI(t, reader, nil)
	for _, id := range []string{"bad%2Fid", "-starts-dash", strings.Repeat("a", 129)} {
		rec := requestAPI(t, mux, "/api/v1/convoys/"+id)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("id %q status = %d, want 422", id, rec.Code)
		}
	}
	if rec := requestAPI(t, mux, "/api/v1/convoys/"); rec.Code != http.StatusNotFound {
		t.Fatalf("empty path status = %d, want 404", rec.Code)
	}

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: &gcstate.UpstreamError{Code: "upstream_http", StatusCode: http.StatusNotFound, Detail: "missing"}, want: http.StatusNotFound},
		{name: "unavailable", err: &gcstate.UpstreamError{Code: "upstream_unavailable", Detail: "offline"}, want: http.StatusServiceUnavailable},
		{name: "malformed success", err: &gcstate.UpstreamError{Code: "upstream_protocol", StatusCode: http.StatusOK, Detail: "missing body"}, want: http.StatusBadGateway},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			broken := populatedReader()
			broken.convoyErr = test.err
			brokenMux, _ := registeredTestAPI(t, broken, nil)
			if got := requestAPI(t, brokenMux, "/api/v1/convoys/convoy-1").Code; got != test.want {
				t.Fatalf("status = %d, want %d", got, test.want)
			}
		})
	}
}

func TestConvoyOpenAPISchemaUsesTypedResourceDTOs(t *testing.T) {
	_, api := registeredTestAPI(t, populatedReader(), nil)
	for _, schema := range []string{"ConvoySummary", "ConvoyDetail", "Problem", "Progress"} {
		if api.OpenAPI().Components.Schemas.Map()[schema] == nil {
			t.Errorf("OpenAPI missing %s", schema)
		}
	}
}

type fakeReader struct {
	convoys     gcstate.Page[gcstate.BeadSource]
	recent      gcstate.Page[gcstate.BeadSource]
	convoy      gcstate.ConvoySource
	convoyErr   error
	workflow    gcstate.WorkflowSource
	workflowErr error
	sessions    gcstate.Page[gcstate.SessionSource]
	pending     gcstate.Page[gcstate.PendingSource]
	orders      []gcstate.OrderSource
	ordersErr   error
	checks      []gcstate.OrderCheckSource
	checksErr   error
	feed        gcstate.Page[gcstate.OrderFeedSource]
	histories   map[string][]gcstate.OrderRunSource
	historyErr  map[string]error
	output      gcstate.OrderRunOutput
	outputErr   error

	outputBeadID   string
	outputStoreRef string
	outputCalls    int
	historyBefore  string
	historyLimit   int
}

func populatedReader() *fakeReader {
	metadata := map[string]string{
		"work_dir": "/tmp/feature", "parent_commit": "abc", "feature_branch": "feature/test", "workflow_root": "wf-1",
	}
	convoy := gcstate.BeadSource{ID: "convoy-1", Title: "Feature", Type: "convoy", Status: "open", Metadata: metadata}
	child := gcstate.BeadSource{ID: "step-1", Title: "Dev", Type: "task", Status: "in_progress", Assignee: "session-1", Metadata: map[string]string{"gc.step_ref": "dev"}}
	return &fakeReader{
		convoys:    gcstate.Page[gcstate.BeadSource]{Items: []gcstate.BeadSource{convoy}},
		recent:     gcstate.Page[gcstate.BeadSource]{Items: []gcstate.BeadSource{}},
		convoy:     gcstate.ConvoySource{Convoy: &convoy, Children: []gcstate.BeadSource{child}, Progress: &gcstate.Progress{Total: 1}},
		workflow:   gcstate.WorkflowSource{WorkflowID: "wf-1", RootID: "root-1", ScopeKind: "city", ScopeRef: "taxdome"},
		sessions:   gcstate.Page[gcstate.SessionSource]{Items: []gcstate.SessionSource{{ID: "session-1", SessionName: "worker", Template: "dynamic/worker", State: "running", ActiveBead: "step-1", Running: true}}},
		pending:    gcstate.Page[gcstate.PendingSource]{Items: []gcstate.PendingSource{}},
		orders:     []gcstate.OrderSource{{Name: "review", ScopedName: "city/review", Type: "cooldown", Enabled: true, CaptureOutput: true}},
		checks:     []gcstate.OrderCheckSource{{Name: "review", ScopedName: "city/review", Reason: "cooldown", LastRun: "2026-07-15T10:00:00Z", LastRunOutcome: "success"}},
		feed:       gcstate.Page[gcstate.OrderFeedSource]{Items: []gcstate.OrderFeedSource{{BeadID: "run-1", StoreRef: "city", Status: "active"}}},
		histories:  map[string][]gcstate.OrderRunSource{"city/review": {{BeadID: "run-1", StoreRef: "city", CreatedAt: "2026-07-15T10:00:00Z", HasOutput: true}}},
		historyErr: map[string]error{},
		output:     gcstate.OrderRunOutput{BeadID: "run-1", StoreRef: "city", CreatedAt: "2026-07-15T10:00:00Z", Output: "hello"},
	}
}

func (f *fakeReader) ListConvoys(context.Context) gcstate.Page[gcstate.BeadSource] { return f.convoys }
func (f *fakeReader) ListRecentClosedConvoys(context.Context, int) gcstate.Page[gcstate.BeadSource] {
	return f.recent
}

func (f *fakeReader) GetConvoy(context.Context, string) (gcstate.ConvoySource, error) {
	return f.convoy, f.convoyErr
}

func (f *fakeReader) GetWorkflow(context.Context, string, string, string) (gcstate.WorkflowSource, error) {
	return f.workflow, f.workflowErr
}

func (f *fakeReader) ListSessions(context.Context) gcstate.Page[gcstate.SessionSource] {
	return f.sessions
}

func (f *fakeReader) ListPending(context.Context) gcstate.Page[gcstate.PendingSource] {
	return f.pending
}

func (f *fakeReader) ListOrders(context.Context) ([]gcstate.OrderSource, error) {
	return f.orders, f.ordersErr
}

func (f *fakeReader) CheckOrders(context.Context) ([]gcstate.OrderCheckSource, error) {
	return f.checks, f.checksErr
}

func (f *fakeReader) ListOrderFeed(context.Context) gcstate.Page[gcstate.OrderFeedSource] {
	return f.feed
}

func (f *fakeReader) ListOrderHistory(_ context.Context, scopedName, before string, limit int) ([]gcstate.OrderRunSource, error) {
	f.historyBefore, f.historyLimit = before, limit
	return f.histories[scopedName], f.historyErr[scopedName]
}

func (f *fakeReader) GetOrderRunOutput(_ context.Context, beadID, storeRef string) (gcstate.OrderRunOutput, error) {
	f.outputBeadID, f.outputStoreRef = beadID, storeRef
	f.outputCalls++
	return f.output, f.outputErr
}

func registeredTestAPI(t *testing.T, reader gcstate.Reader, hub *gcstate.Hub) (*http.ServeMux, huma.API) {
	t.Helper()
	state, err := gcstate.NewService(reader)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	mux := http.NewServeMux()
	api := Register(mux, Options{CityName: "taxdome", SupervisorPing: func(context.Context) error { return nil }, State: state, Events: hub})
	return mux, api
}

func requestAPI(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func decodeAPI(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
}

var _ gcstate.Reader = (*fakeReader)(nil)
