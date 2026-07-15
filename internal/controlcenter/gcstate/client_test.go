package gcstate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

func TestClientGeneratedCallFailureMatrix(t *testing.T) {
	tests := []struct {
		name            string
		call            func(*Client) callOutcome
		supportsPartial bool
	}{
		{name: "convoys", call: func(client *Client) callOutcome { return pageOutcome(client.ListConvoys(context.Background())) }, supportsPartial: true},
		{name: "beads", call: func(client *Client) callOutcome {
			return pageOutcome(client.ListRecentClosedConvoys(context.Background(), 5))
		}, supportsPartial: true},
		{name: "convoy", call: func(client *Client) callOutcome {
			value, err := client.GetConvoy(context.Background(), "convoy-1")
			return valueOutcome(value.Convoy != nil, value.Partial, err)
		}},
		{name: "workflow", call: func(client *Client) callOutcome {
			value, err := client.GetWorkflow(context.Background(), "wf-1", "city", "taxdome")
			return valueOutcome(value.WorkflowID != "", value.Partial, err)
		}, supportsPartial: true},
		{name: "sessions", call: func(client *Client) callOutcome { return pageOutcome(client.ListSessions(context.Background())) }, supportsPartial: true},
		{name: "pending", call: func(client *Client) callOutcome { return pageOutcome(client.ListPending(context.Background())) }, supportsPartial: true},
		{name: "orders", call: func(client *Client) callOutcome {
			value, err := client.ListOrders(context.Background())
			return valueOutcome(len(value) > 0, false, err)
		}},
		{name: "checks", call: func(client *Client) callOutcome {
			value, err := client.CheckOrders(context.Background())
			return valueOutcome(len(value) > 0, false, err)
		}},
		{name: "feed", call: func(client *Client) callOutcome { return pageOutcome(client.ListOrderFeed(context.Background())) }, supportsPartial: true},
		{name: "history", call: func(client *Client) callOutcome {
			value, err := client.ListOrderHistory(context.Background(), "city/review", "", 20)
			return valueOutcome(len(value) > 0, false, err)
		}},
		{name: "output", call: func(client *Client) callOutcome {
			value, err := client.GetOrderRunOutput(context.Background(), "run-1", "rig")
			return valueOutcome(value.BeadID != "", false, err)
		}},
	}

	for _, test := range tests {
		for _, mode := range []string{"transport", "nil_response", "non_2xx", "nil_body", "valid"} {
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				fake := &fakeSupervisor{modes: map[string]string{test.name: mode}}
				client, err := NewClient("taxdome", fake, fake)
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				got := test.call(client)
				if mode == "valid" {
					if got.code != "" || !got.valid {
						t.Fatalf("valid outcome = %#v", got)
					}
					return
				}
				wantCode := "upstream_protocol"
				switch mode {
				case "transport":
					wantCode = "upstream_unavailable"
				case "non_2xx":
					wantCode = "upstream_http"
					if got.partial {
						wantCode = "upstream_unavailable"
					}
				}
				if got.code != wantCode {
					t.Fatalf("failure code = %q, want %q (outcome=%#v)", got.code, wantCode, got)
				}
			})
		}
		if test.supportsPartial {
			t.Run(test.name+"/partial", func(t *testing.T) {
				fake := &fakeSupervisor{modes: map[string]string{test.name: "partial"}}
				client, err := NewClient("taxdome", fake, fake)
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				got := test.call(client)
				if !got.valid || !got.partial {
					t.Fatalf("partial outcome = %#v", got)
				}
			})
		}
	}
}

func TestClientPreservesUnavailableStatusInPageProblems(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fake := &fakeSupervisor{}
			fake.convoys = func(context.Context, string, *genclient.GetV0CityByCityNameConvoysParams) (*genclient.GetV0CityByCityNameConvoysResponse, error) {
				return errorResponse[genclient.GetV0CityByCityNameConvoysResponse](status, "temporarily unavailable"), nil
			}
			page := mustClient(t, fake).ListConvoys(context.Background())
			if len(page.Problems) != 1 || page.Problems[0].Code != "upstream_unavailable" {
				t.Fatalf("problems = %#v, want one upstream_unavailable problem", page.Problems)
			}
		})
	}
}

func TestClientPaginatesDeduplicatesAndPreservesPartialConvoys(t *testing.T) {
	var cursors []string
	fake := &fakeSupervisor{}
	fake.convoys = func(_ context.Context, _ string, params *genclient.GetV0CityByCityNameConvoysParams) (*genclient.GetV0CityByCityNameConvoysResponse, error) {
		cursor := stringValue(params.Cursor)
		cursors = append(cursors, cursor)
		if cursor == "" {
			items := []genclient.Bead{generatedBead("a", "open"), generatedBead("b", "open")}
			next, partial := "next", true
			errors := []string{"rig store unavailable"}
			return convoyListResponse(&genclient.ListBodyBead{Items: &items, NextCursor: &next, Partial: &partial, PartialErrors: &errors}), nil
		}
		items := []genclient.Bead{generatedBead("b", "open"), generatedBead("c", "open")}
		return convoyListResponse(&genclient.ListBodyBead{Items: &items}), nil
	}
	client := mustClient(t, fake)

	got := client.ListConvoys(context.Background())

	if !reflect.DeepEqual(cursors, []string{"", "next"}) {
		t.Fatalf("cursors = %v", cursors)
	}
	if ids := beadSourceIDs(got.Items); !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("ids = %v", ids)
	}
	if !got.Partial || len(got.Problems) != 1 || got.Problems[0].Detail != "rig store unavailable" {
		t.Fatalf("partial page = %#v", got)
	}
}

func TestClientUsesExactGeneratedParameters(t *testing.T) {
	fake := &fakeSupervisor{}
	var beadsParams *genclient.GetV0CityByCityNameBeadsParams
	var workflowParams *genclient.GetV0CityByCityNameWorkflowByWorkflowIdParams
	var sessionsParams *genclient.GetV0CityByCityNameSessionsParams
	var checksParams *genclient.GetV0CityByCityNameOrdersCheckParams
	var feedParams *genclient.GetV0CityByCityNameOrdersFeedParams
	var historyParams *genclient.GetV0CityByCityNameOrdersHistoryParams
	var outputParams *genclient.GetV0CityByCityNameOrderHistoryByBeadIdParams
	fake.beads = func(_ context.Context, _ string, params *genclient.GetV0CityByCityNameBeadsParams) (*genclient.GetV0CityByCityNameBeadsResponse, error) {
		paramsCopy := *params
		beadsParams = &paramsCopy
		items := []genclient.Bead{}
		return beadListResponse(&genclient.ListBodyBead{Items: &items}), nil
	}
	fake.workflow = func(_ context.Context, _ string, _ string, params *genclient.GetV0CityByCityNameWorkflowByWorkflowIdParams) (*genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse, error) {
		paramsCopy := *params
		workflowParams = &paramsCopy
		return workflowResponse(validWorkflowBody(false)), nil
	}
	fake.sessions = func(_ context.Context, _ string, params *genclient.GetV0CityByCityNameSessionsParams) (*genclient.GetV0CityByCityNameSessionsResponse, error) {
		paramsCopy := *params
		sessionsParams = &paramsCopy
		items := []genclient.SessionResponse{}
		return sessionsResponse(&genclient.ListBodySessionResponse{Items: &items}), nil
	}
	fake.checks = func(_ context.Context, _ string, params *genclient.GetV0CityByCityNameOrdersCheckParams) (*genclient.GetV0CityByCityNameOrdersCheckResponse, error) {
		paramsCopy := *params
		checksParams = &paramsCopy
		checks := []genclient.OrderCheckResponse{}
		return checksResponse(&genclient.OrderCheckListBody{Checks: &checks}), nil
	}
	fake.feed = func(_ context.Context, _ string, params *genclient.GetV0CityByCityNameOrdersFeedParams) (*genclient.GetV0CityByCityNameOrdersFeedResponse, error) {
		paramsCopy := *params
		feedParams = &paramsCopy
		items := []genclient.MonitorFeedItemResponse{}
		return feedResponse(&genclient.OrdersFeedBody{Items: &items}), nil
	}
	fake.history = func(_ context.Context, _ string, params *genclient.GetV0CityByCityNameOrdersHistoryParams) (*genclient.GetV0CityByCityNameOrdersHistoryResponse, error) {
		paramsCopy := *params
		historyParams = &paramsCopy
		entries := []genclient.OrderHistoryEntry{}
		return historyResponse(&genclient.OrderHistoryListBody{Entries: &entries}), nil
	}
	fake.output = func(_ context.Context, _ string, _ string, params *genclient.GetV0CityByCityNameOrderHistoryByBeadIdParams) (*genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse, error) {
		paramsCopy := *params
		outputParams = &paramsCopy
		body := validOutputBody()
		body.StoreRef = "rig:taxdome"
		return outputResponse(body), nil
	}
	client := mustClient(t, fake)

	client.ListRecentClosedConvoys(context.Background(), 5)
	_, _ = client.GetWorkflow(context.Background(), "wf", "rig", "taxdome")
	client.ListSessions(context.Background())
	_, _ = client.CheckOrders(context.Background())
	client.ListOrderFeed(context.Background())
	_, _ = client.ListOrderHistory(context.Background(), "taxdome/review", "2026-07-15T10:00:00Z", 0)
	_, _ = client.GetOrderRunOutput(context.Background(), "run-1", "rig:taxdome")

	if beadsParams == nil || stringValue(beadsParams.Type) != "convoy" || beadsParams.All == nil || !*beadsParams.All || int64Value(beadsParams.Limit) != 1000 {
		t.Fatalf("beads params = %#v", beadsParams)
	}
	if workflowParams == nil || stringValue(workflowParams.ScopeKind) != "rig" || stringValue(workflowParams.ScopeRef) != "taxdome" {
		t.Fatalf("workflow params = %#v", workflowParams)
	}
	if sessionsParams == nil || sessionsParams.Peek == nil || *sessionsParams.Peek || int64Value(sessionsParams.Limit) != 1000 {
		t.Fatalf("sessions params = %#v", sessionsParams)
	}
	if checksParams == nil || checksParams.Fresh != nil {
		t.Fatalf("checks params = %#v", checksParams)
	}
	if feedParams == nil || stringValue(feedParams.ScopeKind) != "city" || stringValue(feedParams.ScopeRef) != "taxdome" || int64Value(feedParams.Limit) != 500 {
		t.Fatalf("feed params = %#v", feedParams)
	}
	if historyParams == nil || historyParams.ScopedName != "taxdome/review" || stringValue(historyParams.Before) != "2026-07-15T10:00:00Z" || int64Value(historyParams.Limit) != 20 {
		t.Fatalf("history params = %#v", historyParams)
	}
	if outputParams == nil || stringValue(outputParams.StoreRef) != "rig:taxdome" {
		t.Fatalf("output params = %#v", outputParams)
	}
}

func TestClientRejectsMissingOrMismatchedOrderOutputIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*genclient.OrderHistoryDetailResponse)
	}{
		{name: "missing bead id", mutate: func(body *genclient.OrderHistoryDetailResponse) { body.BeadId = "" }},
		{name: "missing store ref", mutate: func(body *genclient.OrderHistoryDetailResponse) { body.StoreRef = "" }},
		{name: "mismatched bead id", mutate: func(body *genclient.OrderHistoryDetailResponse) { body.BeadId = "other-run" }},
		{name: "mismatched store ref", mutate: func(body *genclient.OrderHistoryDetailResponse) { body.StoreRef = "other-store" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeSupervisor{}
			fake.output = func(context.Context, string, string, *genclient.GetV0CityByCityNameOrderHistoryByBeadIdParams) (*genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse, error) {
				body := validOutputBody()
				test.mutate(body)
				return outputResponse(body), nil
			}

			_, err := mustClient(t, fake).GetOrderRunOutput(context.Background(), "run-1", "rig")

			var upstream *UpstreamError
			if !errors.As(err, &upstream) || upstream.Code != "upstream_protocol" || upstream.StatusCode != http.StatusOK {
				t.Fatalf("error = %#v, want HTTP 200 upstream_protocol", err)
			}
		})
	}
}

func TestClientRecentClosedConvoysAreBoundedAndNewestFirst(t *testing.T) {
	fake := &fakeSupervisor{}
	old := generatedBead("old", "closed")
	newer := generatedBead("new", "tombstone")
	open := generatedBead("open", "open")
	oldTime, newTime := time.Unix(1, 0), time.Unix(2, 0)
	old.UpdatedAt, newer.UpdatedAt = &oldTime, &newTime
	fake.beads = func(context.Context, string, *genclient.GetV0CityByCityNameBeadsParams) (*genclient.GetV0CityByCityNameBeadsResponse, error) {
		items := []genclient.Bead{old, open, newer}
		return beadListResponse(&genclient.ListBodyBead{Items: &items}), nil
	}
	client := mustClient(t, fake)

	got := client.ListRecentClosedConvoys(context.Background(), 1)

	if ids := beadSourceIDs(got.Items); !reflect.DeepEqual(ids, []string{"new"}) {
		t.Fatalf("recent closed IDs = %v", ids)
	}
}

func TestClientOrderHistoryBatchUsesAtMostFourConcurrentReads(t *testing.T) {
	fake := &fakeSupervisor{}
	var active atomic.Int64
	var maximum atomic.Int64
	fake.history = func(_ context.Context, _ string, params *genclient.GetV0CityByCityNameOrdersHistoryParams) (*genclient.GetV0CityByCityNameOrdersHistoryResponse, error) {
		current := active.Add(1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
		entries := []genclient.OrderHistoryEntry{{BeadId: params.ScopedName, ScopedName: params.ScopedName, Name: params.ScopedName, StoreRef: "city"}}
		return historyResponse(&genclient.OrderHistoryListBody{Entries: &entries}), nil
	}
	client := mustClient(t, fake)
	names := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}

	got, problems := client.ListOrderHistories(context.Background(), names, 20)

	if len(got) != len(names) || len(problems) != 0 {
		t.Fatalf("histories=%d problems=%#v", len(got), problems)
	}
	if maximum.Load() > 4 {
		t.Fatalf("maximum concurrency = %d, want <= 4", maximum.Load())
	}
}

func TestClientBoundsReadsAndHonorsParentCancellation(t *testing.T) {
	fake := &fakeSupervisor{}
	var deadline time.Time
	fake.convoys = func(ctx context.Context, _ string, _ *genclient.GetV0CityByCityNameConvoysParams) (*genclient.GetV0CityByCityNameConvoysResponse, error) {
		deadline, _ = ctx.Deadline()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	client := mustClient(t, fake)
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	got := client.ListConvoys(parent)

	if deadline.IsZero() || time.Until(deadline) > readTimeout {
		t.Fatalf("bounded deadline = %v", deadline)
	}
	if len(got.Problems) != 1 || got.Problems[0].Code != "upstream_unavailable" {
		t.Fatalf("canceled page = %#v", got)
	}
}

func TestClientPaginatesAndDeduplicatesBeadsAndSessions(t *testing.T) {
	fake := &fakeSupervisor{}
	fake.beads = func(_ context.Context, _ string, params *genclient.GetV0CityByCityNameBeadsParams) (*genclient.GetV0CityByCityNameBeadsResponse, error) {
		if params.Cursor == nil {
			next := "next"
			items := []genclient.Bead{generatedBead("a", "closed"), generatedBead("b", "closed")}
			return beadListResponse(&genclient.ListBodyBead{Items: &items, NextCursor: &next}), nil
		}
		items := []genclient.Bead{generatedBead("b", "closed"), generatedBead("c", "closed")}
		return beadListResponse(&genclient.ListBodyBead{Items: &items}), nil
	}
	fake.sessions = func(_ context.Context, _ string, params *genclient.GetV0CityByCityNameSessionsParams) (*genclient.GetV0CityByCityNameSessionsResponse, error) {
		makeSession := func(id string) genclient.SessionResponse {
			return genclient.SessionResponse{Id: id, SessionName: id, Template: "worker", Provider: "fake", State: "running", CreatedAt: "now", Title: id}
		}
		if params.Cursor == nil {
			next := "next"
			items := []genclient.SessionResponse{makeSession("a"), makeSession("b")}
			return sessionsResponse(&genclient.ListBodySessionResponse{Items: &items, NextCursor: &next}), nil
		}
		items := []genclient.SessionResponse{makeSession("b"), makeSession("c")}
		return sessionsResponse(&genclient.ListBodySessionResponse{Items: &items}), nil
	}
	client := mustClient(t, fake)

	if ids := beadSourceIDs(client.ListRecentClosedConvoys(context.Background(), 10).Items); !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("bead ids = %v", ids)
	}
	sessions := client.ListSessions(context.Background()).Items
	if got := []string{sessions[0].ID, sessions[1].ID, sessions[2].ID}; !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("session ids = %v", got)
	}
}

func TestClientLaterPageFailuresPreserveUsableItems(t *testing.T) {
	for _, mode := range []string{"transport", "nil_response", "nil_body"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			fake := &fakeSupervisor{}
			fake.convoys = func(context.Context, string, *genclient.GetV0CityByCityNameConvoysParams) (*genclient.GetV0CityByCityNameConvoysResponse, error) {
				calls++
				if calls == 1 {
					next := "next"
					items := []genclient.Bead{generatedBead("usable", "open")}
					return convoyListResponse(&genclient.ListBodyBead{Items: &items, NextCursor: &next}), nil
				}
				switch mode {
				case "transport":
					return nil, errors.New("offline")
				case "nil_response":
					return nil, nil
				default:
					return convoyListResponse(nil), nil
				}
			}
			got := mustClient(t, fake).ListConvoys(context.Background())
			if ids := beadSourceIDs(got.Items); !reflect.DeepEqual(ids, []string{"usable"}) || !got.Partial || len(got.Problems) != 1 {
				t.Fatalf("page = %#v", got)
			}
		})
	}
}

func TestClientPropagatesPartialDetailsAcrossListCalls(t *testing.T) {
	fake := &fakeSupervisor{modes: map[string]string{"sessions": "partial", "pending": "partial", "feed": "partial"}}
	client := mustClient(t, fake)
	for name, page := range map[string]Page[Problem]{
		"sessions": problemsPage(client.ListSessions(context.Background())),
		"pending":  problemsPage(client.ListPending(context.Background())),
		"feed":     problemsPage(client.ListOrderFeed(context.Background())),
	} {
		if !page.Partial || len(page.Problems) != 1 || page.Problems[0].Detail != "partial backend" {
			t.Errorf("%s page = %#v", name, page)
		}
	}
}

func TestClientTypedHTTPErrorPreservesStatusAndDetail(t *testing.T) {
	fake := &fakeSupervisor{modes: map[string]string{"workflow": "non_2xx"}}
	_, err := mustClient(t, fake).GetWorkflow(context.Background(), "wf", "city", "taxdome")
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusServiceUnavailable || upstream.Detail != "supervisor unavailable" {
		t.Fatalf("error = %#v", err)
	}
}

func TestClientRejectsNegativeRecentLimitWithoutUpstreamRead(t *testing.T) {
	calls := 0
	fake := &fakeSupervisor{}
	fake.beads = func(context.Context, string, *genclient.GetV0CityByCityNameBeadsParams) (*genclient.GetV0CityByCityNameBeadsResponse, error) {
		calls++
		return beadListResponse(nil), nil
	}
	got := mustClient(t, fake).ListRecentClosedConvoys(context.Background(), -1)
	if calls != 0 || len(got.Problems) != 1 || got.Problems[0].Code != "invalid_limit" {
		t.Fatalf("calls=%d page=%#v", calls, got)
	}
}

func TestClientStopsRepeatedCursorLoops(t *testing.T) {
	for _, resource := range []string{"convoys", "beads", "sessions"} {
		t.Run(resource, func(t *testing.T) {
			calls := 0
			fake := &fakeSupervisor{}
			next := "same"
			fake.convoys = func(context.Context, string, *genclient.GetV0CityByCityNameConvoysParams) (*genclient.GetV0CityByCityNameConvoysResponse, error) {
				calls++
				if calls > 3 {
					return nil, errors.New("cursor loop")
				}
				items := []genclient.Bead{generatedBead("one", "open")}
				return convoyListResponse(&genclient.ListBodyBead{Items: &items, NextCursor: &next}), nil
			}
			fake.beads = func(context.Context, string, *genclient.GetV0CityByCityNameBeadsParams) (*genclient.GetV0CityByCityNameBeadsResponse, error) {
				calls++
				if calls > 3 {
					return nil, errors.New("cursor loop")
				}
				items := []genclient.Bead{generatedBead("one", "closed")}
				return beadListResponse(&genclient.ListBodyBead{Items: &items, NextCursor: &next}), nil
			}
			fake.sessions = func(context.Context, string, *genclient.GetV0CityByCityNameSessionsParams) (*genclient.GetV0CityByCityNameSessionsResponse, error) {
				calls++
				if calls > 3 {
					return nil, errors.New("cursor loop")
				}
				items := []genclient.SessionResponse{{Id: "one", SessionName: "one", Template: "worker", Provider: "fake", State: "running", CreatedAt: "now", Title: "one"}}
				return sessionsResponse(&genclient.ListBodySessionResponse{Items: &items, NextCursor: &next}), nil
			}
			client := mustClient(t, fake)
			var problems []Problem
			switch resource {
			case "convoys":
				problems = client.ListConvoys(context.Background()).Problems
			case "beads":
				problems = client.ListRecentClosedConvoys(context.Background(), 10).Problems
			case "sessions":
				problems = client.ListSessions(context.Background()).Problems
			}
			if calls != 2 || len(problems) != 1 || problems[0].Code != "upstream_protocol" {
				t.Fatalf("calls=%d problems=%#v", calls, problems)
			}
		})
	}
}

func problemsPage[T any](page Page[T]) Page[Problem] {
	return Page[Problem]{Partial: page.Partial, Problems: page.Problems}
}

type callOutcome struct {
	code    string
	valid   bool
	partial bool
}

func pageOutcome[T any](page Page[T]) callOutcome {
	code := ""
	if len(page.Problems) > 0 && len(page.Items) == 0 {
		code = page.Problems[0].Code
	}
	return callOutcome{code: code, valid: len(page.Items) > 0, partial: page.Partial}
}

func valueOutcome(valid, partial bool, err error) callOutcome {
	if err == nil {
		return callOutcome{valid: valid, partial: partial}
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return callOutcome{code: upstream.Code}
	}
	return callOutcome{code: "other"}
}

func mustClient(t *testing.T, fake *fakeSupervisor) *Client {
	t.Helper()
	client, err := NewClient("taxdome", fake, fake)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

type fakeSupervisor struct {
	modes map[string]string

	convoys  func(context.Context, string, *genclient.GetV0CityByCityNameConvoysParams) (*genclient.GetV0CityByCityNameConvoysResponse, error)
	beads    func(context.Context, string, *genclient.GetV0CityByCityNameBeadsParams) (*genclient.GetV0CityByCityNameBeadsResponse, error)
	convoy   func(context.Context, string, string) (*genclient.GetV0CityByCityNameConvoyByIdResponse, error)
	workflow func(context.Context, string, string, *genclient.GetV0CityByCityNameWorkflowByWorkflowIdParams) (*genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse, error)
	sessions func(context.Context, string, *genclient.GetV0CityByCityNameSessionsParams) (*genclient.GetV0CityByCityNameSessionsResponse, error)
	pending  func(context.Context, string) (*genclient.GetV0CityByCityNamePendingResponse, error)
	orders   func(context.Context, string) (*genclient.GetV0CityByCityNameOrdersResponse, error)
	checks   func(context.Context, string, *genclient.GetV0CityByCityNameOrdersCheckParams) (*genclient.GetV0CityByCityNameOrdersCheckResponse, error)
	feed     func(context.Context, string, *genclient.GetV0CityByCityNameOrdersFeedParams) (*genclient.GetV0CityByCityNameOrdersFeedResponse, error)
	history  func(context.Context, string, *genclient.GetV0CityByCityNameOrdersHistoryParams) (*genclient.GetV0CityByCityNameOrdersHistoryResponse, error)
	output   func(context.Context, string, string, *genclient.GetV0CityByCityNameOrderHistoryByBeadIdParams) (*genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse, error)
	stream   func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error)
}

func (f *fakeSupervisor) mode(name string) string {
	if f.modes == nil {
		return ""
	}
	return f.modes[name]
}

func (f *fakeSupervisor) GetV0CityByCityNameConvoysWithResponse(ctx context.Context, city string, params *genclient.GetV0CityByCityNameConvoysParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameConvoysResponse, error) {
	if f.convoys != nil {
		return f.convoys(ctx, city, params)
	}
	items := []genclient.Bead{generatedBead("convoy-1", "open")}
	body := &genclient.ListBodyBead{Items: &items}
	return generatedMode(f.mode("convoys"), body, convoyListResponse)
}

func (f *fakeSupervisor) GetV0CityByCityNameBeadsWithResponse(ctx context.Context, city string, params *genclient.GetV0CityByCityNameBeadsParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameBeadsResponse, error) {
	if f.beads != nil {
		return f.beads(ctx, city, params)
	}
	items := []genclient.Bead{generatedBead("closed-1", "closed")}
	body := &genclient.ListBodyBead{Items: &items}
	return generatedMode(f.mode("beads"), body, beadListResponse)
}

//nolint:revive // Method spelling is fixed by the generated client interface.
func (f *fakeSupervisor) GetV0CityByCityNameConvoyByIdWithResponse(ctx context.Context, city, id string, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameConvoyByIdResponse, error) {
	if f.convoy != nil {
		return f.convoy(ctx, city, id)
	}
	bead := generatedBead(id, "open")
	children := []genclient.Bead{}
	body := &genclient.ConvoyGetResponse{Convoy: &bead, Children: &children, Progress: &genclient.ConvoyProgress{}}
	return generatedMode(f.mode("convoy"), body, convoyResponse)
}

//nolint:revive // Method spelling is fixed by the generated client interface.
func (f *fakeSupervisor) GetV0CityByCityNameWorkflowByWorkflowIdWithResponse(ctx context.Context, city, id string, params *genclient.GetV0CityByCityNameWorkflowByWorkflowIdParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse, error) {
	if f.workflow != nil {
		return f.workflow(ctx, city, id, params)
	}
	body := validWorkflowBody(f.mode("workflow") == "partial")
	return generatedMode(f.mode("workflow"), body, workflowResponse)
}

func (f *fakeSupervisor) GetV0CityByCityNameSessionsWithResponse(ctx context.Context, city string, params *genclient.GetV0CityByCityNameSessionsParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameSessionsResponse, error) {
	if f.sessions != nil {
		return f.sessions(ctx, city, params)
	}
	items := []genclient.SessionResponse{{Id: "session-1", SessionName: "session", Template: "worker", Provider: "fake", State: "running", Running: true, Attached: true, CreatedAt: "now", Title: "worker"}}
	body := &genclient.ListBodySessionResponse{Items: &items}
	return generatedMode(f.mode("sessions"), body, sessionsResponse)
}

func (f *fakeSupervisor) GetV0CityByCityNamePendingWithResponse(ctx context.Context, city string, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNamePendingResponse, error) {
	if f.pending != nil {
		return f.pending(ctx, city)
	}
	items := []genclient.CityPendingEntry{{SessionId: "session-1", Kind: "prompt", RequestId: "request-1"}}
	body := &genclient.ListBodyCityPendingEntry{Items: &items}
	return generatedMode(f.mode("pending"), body, pendingResponse)
}

func (f *fakeSupervisor) GetV0CityByCityNameOrdersWithResponse(ctx context.Context, city string, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrdersResponse, error) {
	if f.orders != nil {
		return f.orders(ctx, city)
	}
	items := []genclient.OrderResponse{{Name: "review", ScopedName: "city/review", Type: "cooldown", Enabled: true}}
	body := &genclient.OrderListBody{Orders: &items}
	return generatedMode(f.mode("orders"), body, ordersResponse)
}

func (f *fakeSupervisor) GetV0CityByCityNameOrdersCheckWithResponse(ctx context.Context, city string, params *genclient.GetV0CityByCityNameOrdersCheckParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrdersCheckResponse, error) {
	if f.checks != nil {
		return f.checks(ctx, city, params)
	}
	items := []genclient.OrderCheckResponse{{Name: "review", ScopedName: "city/review", Reason: "cooldown", Due: false}}
	body := &genclient.OrderCheckListBody{Checks: &items}
	return generatedMode(f.mode("checks"), body, checksResponse)
}

func (f *fakeSupervisor) GetV0CityByCityNameOrdersFeedWithResponse(ctx context.Context, city string, params *genclient.GetV0CityByCityNameOrdersFeedParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrdersFeedResponse, error) {
	if f.feed != nil {
		return f.feed(ctx, city, params)
	}
	bead, store := "run-1", "city"
	items := []genclient.MonitorFeedItemResponse{{Id: "feed-1", BeadId: &bead, StoreRef: &store, Status: "active", ScopeKind: "city", ScopeRef: city, StartedAt: "now", UpdatedAt: "now", Target: "order", Title: "review", Type: "order"}}
	body := &genclient.OrdersFeedBody{Items: &items}
	return generatedMode(f.mode("feed"), body, feedResponse)
}

func (f *fakeSupervisor) GetV0CityByCityNameOrdersHistoryWithResponse(ctx context.Context, city string, params *genclient.GetV0CityByCityNameOrdersHistoryParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrdersHistoryResponse, error) {
	if f.history != nil {
		return f.history(ctx, city, params)
	}
	items := []genclient.OrderHistoryEntry{{BeadId: "run-1", StoreRef: "city", Name: "review", ScopedName: "city/review", CreatedAt: "now"}}
	body := &genclient.OrderHistoryListBody{Entries: &items}
	return generatedMode(f.mode("history"), body, historyResponse)
}

//nolint:revive // Method spelling is fixed by the generated client interface.
func (f *fakeSupervisor) GetV0CityByCityNameOrderHistoryByBeadIdWithResponse(ctx context.Context, city, id string, params *genclient.GetV0CityByCityNameOrderHistoryByBeadIdParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse, error) {
	if f.output != nil {
		return f.output(ctx, city, id, params)
	}
	body := validOutputBody()
	return generatedMode(f.mode("output"), body, outputResponse)
}

func (f *fakeSupervisor) StreamEvents(ctx context.Context, city string, params *genclient.StreamEventsParams, _ ...genclient.RequestEditorFn) (*http.Response, error) {
	if f.stream != nil {
		return f.stream(ctx, city, params)
	}
	return nil, errors.New("not configured")
}

func generatedMode[T any, R any](mode string, body *T, success func(*T) *R) (*R, error) {
	switch mode {
	case "transport":
		return nil, errors.New("connection refused")
	case "nil_response":
		return nil, nil
	case "non_2xx":
		return errorResponse[R](http.StatusServiceUnavailable, "supervisor unavailable"), nil
	case "nil_body":
		return success(nil), nil
	case "partial":
		markPartial(body)
	}
	return success(body), nil
}

func errorResponse[R any](status int, detail string) *R {
	value := new(R)
	response := &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status))}
	problem := &genclient.ErrorModel{Detail: &detail}
	switch typed := any(value).(type) {
	case *genclient.GetV0CityByCityNameConvoysResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNameBeadsResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNameConvoyByIdResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNameSessionsResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNamePendingResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNameOrdersResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNameOrdersCheckResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNameOrdersFeedResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNameOrdersHistoryResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	case *genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse:
		typed.HTTPResponse, typed.ApplicationproblemJSONDefault = response, problem
	default:
		panic(fmt.Sprintf("unsupported response type %T", value))
	}
	return value
}

func markPartial[T any](body *T) {
	if body == nil {
		return
	}
	detail, partial := []string{"partial backend"}, true
	switch typed := any(body).(type) {
	case *genclient.ListBodyBead:
		typed.Partial, typed.PartialErrors = &partial, &detail
	case *genclient.ListBodySessionResponse:
		typed.Partial, typed.PartialErrors = &partial, &detail
	case *genclient.ListBodyCityPendingEntry:
		typed.Partial, typed.PartialErrors = &partial, &detail
	case *genclient.OrdersFeedBody:
		typed.Partial, typed.PartialErrors = true, &detail
	case *genclient.WorkflowSnapshotResponse:
		typed.Partial = true
	}
}

func generatedBead(id, status string) genclient.Bead {
	metadata := map[string]string{"work_dir": "/tmp/work", "parent_commit": "abc", "feature_branch": "feature/test", "workflow_root": "wf-1"}
	return genclient.Bead{Id: id, Title: id, IssueType: "convoy", Status: status, CreatedAt: time.Unix(1, 0), Metadata: &metadata}
}

func validWorkflowBody(partial bool) *genclient.WorkflowSnapshotResponse {
	beads, deps := []genclient.WorkflowBeadResponse{}, []genclient.WorkflowDepResponse{}
	return &genclient.WorkflowSnapshotResponse{WorkflowId: "wf-1", RootBeadId: "root-1", RootStoreRef: "city", ScopeKind: "city", ScopeRef: "taxdome", Partial: partial, Beads: &beads, Deps: &deps}
}

func validOutputBody() *genclient.OrderHistoryDetailResponse {
	labels := []string{"order:review"}
	return &genclient.OrderHistoryDetailResponse{BeadId: "run-1", StoreRef: "rig", CreatedAt: "now", Labels: &labels, Output: "output"}
}

func okResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}
}

func convoyListResponse(body *genclient.ListBodyBead) *genclient.GetV0CityByCityNameConvoysResponse {
	return &genclient.GetV0CityByCityNameConvoysResponse{HTTPResponse: okResponse(), JSON200: body}
}

func beadListResponse(body *genclient.ListBodyBead) *genclient.GetV0CityByCityNameBeadsResponse {
	return &genclient.GetV0CityByCityNameBeadsResponse{HTTPResponse: okResponse(), JSON200: body}
}

func convoyResponse(body *genclient.ConvoyGetResponse) *genclient.GetV0CityByCityNameConvoyByIdResponse {
	return &genclient.GetV0CityByCityNameConvoyByIdResponse{HTTPResponse: okResponse(), JSON200: body}
}

func workflowResponse(body *genclient.WorkflowSnapshotResponse) *genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse {
	return &genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse{HTTPResponse: okResponse(), JSON200: body}
}

func sessionsResponse(body *genclient.ListBodySessionResponse) *genclient.GetV0CityByCityNameSessionsResponse {
	return &genclient.GetV0CityByCityNameSessionsResponse{HTTPResponse: okResponse(), JSON200: body}
}

func pendingResponse(body *genclient.ListBodyCityPendingEntry) *genclient.GetV0CityByCityNamePendingResponse {
	return &genclient.GetV0CityByCityNamePendingResponse{HTTPResponse: okResponse(), JSON200: body}
}

func ordersResponse(body *genclient.OrderListBody) *genclient.GetV0CityByCityNameOrdersResponse {
	return &genclient.GetV0CityByCityNameOrdersResponse{HTTPResponse: okResponse(), JSON200: body}
}

func checksResponse(body *genclient.OrderCheckListBody) *genclient.GetV0CityByCityNameOrdersCheckResponse {
	return &genclient.GetV0CityByCityNameOrdersCheckResponse{HTTPResponse: okResponse(), JSON200: body}
}

func feedResponse(body *genclient.OrdersFeedBody) *genclient.GetV0CityByCityNameOrdersFeedResponse {
	return &genclient.GetV0CityByCityNameOrdersFeedResponse{HTTPResponse: okResponse(), JSON200: body}
}

func historyResponse(body *genclient.OrderHistoryListBody) *genclient.GetV0CityByCityNameOrdersHistoryResponse {
	return &genclient.GetV0CityByCityNameOrdersHistoryResponse{HTTPResponse: okResponse(), JSON200: body}
}

func outputResponse(body *genclient.OrderHistoryDetailResponse) *genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse {
	return &genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse{HTTPResponse: okResponse(), JSON200: body}
}

func beadSourceIDs(items []BeadSource) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
