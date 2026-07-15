package controlapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
)

func TestOrderRoutesProjectDefinitionsHistoryAndOnDemandOutput(t *testing.T) {
	reader := populatedReader()
	mux, _ := registeredTestAPI(t, reader, nil)

	orders := requestAPI(t, mux, "/api/v1/orders")
	if orders.Code != http.StatusOK {
		t.Fatalf("orders status = %d: %s", orders.Code, orders.Body.String())
	}
	var projected gcstate.ResourceList[gcstate.OrderView]
	decodeAPI(t, orders, &projected)
	if len(projected.Items) != 1 || projected.Items[0].LastRun == nil || projected.Items[0].LastRun.Status != "active" {
		t.Fatalf("orders = %#v", projected)
	}
	if reader.outputCalls != 0 {
		t.Fatalf("orders fetched output eagerly: calls=%d", reader.outputCalls)
	}

	before := "2026-07-15T11:00:00Z"
	history := requestAPI(t, mux, "/api/v1/orders/history?scoped_name="+url.QueryEscape("city/review")+"&before="+url.QueryEscape(before))
	if history.Code != http.StatusOK {
		t.Fatalf("history status = %d: %s", history.Code, history.Body.String())
	}
	var runs gcstate.ResourceList[gcstate.OrderRunView]
	decodeAPI(t, history, &runs)
	if len(runs.Items) != 1 || runs.Items[0].Status != "active" {
		t.Fatalf("history = %#v", runs)
	}
	if reader.historyBefore != before || reader.historyLimit != 20 {
		t.Fatalf("history forwarding before=%q limit=%d", reader.historyBefore, reader.historyLimit)
	}

	output := requestAPI(t, mux, "/api/v1/orders/history/run-1?store_ref="+url.QueryEscape("rig:taxdome"))
	if output.Code != http.StatusOK {
		t.Fatalf("output status = %d: %s", output.Code, output.Body.String())
	}
	if reader.outputBeadID != "run-1" || reader.outputStoreRef != "rig:taxdome" {
		t.Fatalf("forwarded identity = %q %q", reader.outputBeadID, reader.outputStoreRef)
	}
	var gotOutput gcstate.OrderRunOutput
	decodeAPI(t, output, &gotOutput)
	if reader.outputCalls != 1 || gotOutput.Output != "hello" || gotOutput.StoreRef != "city" {
		t.Fatalf("output calls=%d body=%#v", reader.outputCalls, gotOutput)
	}
}

func TestOrdersKeepDefinitionsWhenOneHistoryReadFails(t *testing.T) {
	reader := populatedReader()
	reader.historyErr["city/review"] = errors.New("history unavailable")
	mux, _ := registeredTestAPI(t, reader, nil)
	recorder := requestAPI(t, mux, "/api/v1/orders")
	var got gcstate.ResourceList[gcstate.OrderView]
	decodeAPI(t, recorder, &got)
	if recorder.Code != http.StatusOK || !got.Degraded || len(got.Items) != 1 || len(got.Items[0].Problems) == 0 {
		t.Fatalf("orders = status %d %#v", recorder.Code, got)
	}
}

func TestOrderRoutesValidateRequiredBoundedQueries(t *testing.T) {
	mux, _ := registeredTestAPI(t, populatedReader(), nil)
	paths := []string{
		"/api/v1/orders/history",
		"/api/v1/orders/history?scoped_name=%20%20",
		"/api/v1/orders/history?scoped_name=" + strings.Repeat("a", 257),
		"/api/v1/orders/history?scoped_name=city%2Freview&limit=0",
		"/api/v1/orders/history?scoped_name=city%2Freview&limit=21",
		"/api/v1/orders/history?scoped_name=city%2Freview&before=not-a-time",
		"/api/v1/orders/history/run-1",
		"/api/v1/orders/history/bad%2Fid?store_ref=city",
		"/api/v1/orders/history/-bad?store_ref=city",
		"/api/v1/orders/history/run-1?store_ref=%20%20",
		"/api/v1/orders/history/run-1?store_ref=" + strings.Repeat("a", 257),
	}
	for _, path := range paths {
		if got := requestAPI(t, mux, path).Code; got != http.StatusUnprocessableEntity {
			t.Errorf("GET %s = %d, want 422", path, got)
		}
	}
}

func TestOrderRoutesMapUpstreamFailures(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		err   error
		apply func(*fakeReader, error)
		want  int
	}{
		{name: "orders unavailable", path: "/api/v1/orders", err: context.DeadlineExceeded, apply: func(reader *fakeReader, err error) { reader.ordersErr = err }, want: http.StatusServiceUnavailable},
		{name: "orders malformed", path: "/api/v1/orders", err: &gcstate.UpstreamError{Code: "upstream_protocol", Detail: "missing body"}, apply: func(reader *fakeReader, err error) { reader.ordersErr = err }, want: http.StatusBadGateway},
		{name: "orders upstream 503", path: "/api/v1/orders", err: &gcstate.UpstreamError{Code: "upstream_http", StatusCode: http.StatusServiceUnavailable, Detail: "offline"}, apply: func(reader *fakeReader, err error) { reader.ordersErr = err }, want: http.StatusServiceUnavailable},
		{name: "history missing", path: "/api/v1/orders/history?scoped_name=city%2Freview", err: &gcstate.UpstreamError{Code: "upstream_http", StatusCode: http.StatusNotFound, Detail: "missing"}, apply: func(reader *fakeReader, err error) { reader.historyErr["city/review"] = err }, want: http.StatusNotFound},
		{name: "history deadline", path: "/api/v1/orders/history?scoped_name=city%2Freview", err: context.DeadlineExceeded, apply: func(reader *fakeReader, err error) { reader.historyErr["city/review"] = err }, want: http.StatusServiceUnavailable},
		{name: "output missing", path: "/api/v1/orders/history/run-1?store_ref=city", err: &gcstate.UpstreamError{Code: "upstream_http", StatusCode: http.StatusNotFound, Detail: "missing"}, apply: func(reader *fakeReader, err error) { reader.outputErr = err }, want: http.StatusNotFound},
		{name: "output malformed", path: "/api/v1/orders/history/run-1?store_ref=city", err: &gcstate.UpstreamError{Code: "upstream_protocol", StatusCode: http.StatusOK, Detail: "missing body"}, apply: func(reader *fakeReader, err error) { reader.outputErr = err }, want: http.StatusBadGateway},
		{name: "output unavailable", path: "/api/v1/orders/history/run-1?store_ref=city", err: &gcstate.UpstreamError{Code: "upstream_unavailable", Detail: "offline"}, apply: func(reader *fakeReader, err error) { reader.outputErr = err }, want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := populatedReader()
			test.apply(reader, test.err)
			mux, _ := registeredTestAPI(t, reader, nil)
			if got := requestAPI(t, mux, test.path).Code; got != test.want {
				t.Fatalf("status = %d, want %d", got, test.want)
			}
		})
	}
}

func TestOrderPartialFeedReturnsUsableDegradedResponse(t *testing.T) {
	reader := populatedReader()
	reader.feed.Partial = true
	reader.feed.Problems = []gcstate.Problem{{Code: "upstream_partial", Source: "orders_feed", Detail: "feed capped", Retryable: true}}
	mux, _ := registeredTestAPI(t, reader, nil)
	recorder := requestAPI(t, mux, "/api/v1/orders")
	var got gcstate.ResourceList[gcstate.OrderView]
	decodeAPI(t, recorder, &got)
	if recorder.Code != http.StatusOK || !got.Degraded || len(got.Items) != 1 || len(got.Problems) == 0 {
		t.Fatalf("orders = status %d %#v", recorder.Code, got)
	}
}
