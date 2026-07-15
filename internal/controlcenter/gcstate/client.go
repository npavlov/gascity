package gcstate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/beadmeta"
)

const (
	readTimeout              = 3 * time.Second
	upstreamPageLimit  int64 = 1000
	orderFeedLimit     int64 = 500
	orderHistoryLimit  int64 = 20
	historyConcurrency       = 4
)

// SupervisorResponses is the generated read-only response surface consumed by
// the adapter. It deliberately excludes every mutation method.
type SupervisorResponses interface {
	GetV0CityByCityNameConvoysWithResponse(context.Context, string, *genclient.GetV0CityByCityNameConvoysParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameConvoysResponse, error)
	GetV0CityByCityNameBeadsWithResponse(context.Context, string, *genclient.GetV0CityByCityNameBeadsParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameBeadsResponse, error)
	GetV0CityByCityNameConvoyByIdWithResponse(context.Context, string, string, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameConvoyByIdResponse, error)
	GetV0CityByCityNameWorkflowByWorkflowIdWithResponse(context.Context, string, string, *genclient.GetV0CityByCityNameWorkflowByWorkflowIdParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse, error)
	GetV0CityByCityNameSessionsWithResponse(context.Context, string, *genclient.GetV0CityByCityNameSessionsParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameSessionsResponse, error)
	GetV0CityByCityNamePendingWithResponse(context.Context, string, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNamePendingResponse, error)
	GetV0CityByCityNameOrdersWithResponse(context.Context, string, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrdersResponse, error)
	GetV0CityByCityNameOrdersCheckWithResponse(context.Context, string, *genclient.GetV0CityByCityNameOrdersCheckParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrdersCheckResponse, error)
	GetV0CityByCityNameOrdersFeedWithResponse(context.Context, string, *genclient.GetV0CityByCityNameOrdersFeedParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrdersFeedResponse, error)
	GetV0CityByCityNameOrdersHistoryWithResponse(context.Context, string, *genclient.GetV0CityByCityNameOrdersHistoryParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrdersHistoryResponse, error)
	GetV0CityByCityNameOrderHistoryByBeadIdWithResponse(context.Context, string, string, *genclient.GetV0CityByCityNameOrderHistoryByBeadIdParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse, error)
}

// RawEventClient is the generated raw stream surface. It exists separately
// because the generated WithResponse parser waits for stream EOF.
type RawEventClient interface {
	StreamEvents(context.Context, string, *genclient.StreamEventsParams, ...genclient.RequestEditorFn) (*http.Response, error)
}

// UpstreamError classifies a failed generated call for local HTTP mapping.
type UpstreamError struct {
	Code       string
	Operation  string
	StatusCode int
	Detail     string
	Err        error
}

func (e *UpstreamError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Operation, e.Detail, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Operation, e.Detail)
}

// Unwrap exposes the transport or context cause.
func (e *UpstreamError) Unwrap() error { return e.Err }

// Client adapts the generated Supervisor client to the projection Reader.
type Client struct {
	city string
	api  SupervisorResponses
	raw  RawEventClient
}

// NewClient constructs a read-only adapter for one configured city.
func NewClient(city string, api SupervisorResponses, raw RawEventClient) (*Client, error) {
	if city == "" {
		return nil, fmt.Errorf("control center gcstate: city is required")
	}
	if api == nil {
		return nil, fmt.Errorf("control center gcstate: generated response client is required")
	}
	return &Client{city: city, api: api, raw: raw}, nil
}

// ListConvoys pages every active convoy and deduplicates by stable ID.
func (c *Client) ListConvoys(ctx context.Context) Page[BeadSource] {
	result := Page[BeadSource]{Items: []BeadSource{}}
	seen := map[string]bool{}
	seenCursors := map[string]bool{}
	var cursor string
	for {
		params := &genclient.GetV0CityByCityNameConvoysParams{Limit: int64Ptr(upstreamPageLimit)}
		if cursor != "" {
			params.Cursor = &cursor
		}
		callCtx, cancel := boundedReadContext(ctx)
		response, err := c.api.GetV0CityByCityNameConvoysWithResponse(callCtx, c.city, params)
		cancel()
		body, callErr := checkedBody("list convoys", response != nil, statusOfConvoys(response), problemOfConvoys(response), bodyOfConvoys(response), err)
		if callErr != nil {
			return mergePageError(result, callErr, "convoys")
		}
		mergeBeadPage(&result, body, seen, "convoys")
		cursor = pointerString(body.NextCursor)
		if cursor == "" {
			return result
		}
		if seenCursors[cursor] {
			return mergePageError(result, repeatedCursorError("list convoys", cursor), "convoys")
		}
		seenCursors[cursor] = true
	}
}

// ListRecentClosedConvoys pages all convoy beads, retains terminal ones, and
// returns a bounded newest-first slice.
func (c *Client) ListRecentClosedConvoys(ctx context.Context, limit int) Page[BeadSource] {
	if limit < 0 {
		return Page[BeadSource]{Items: []BeadSource{}, Problems: []Problem{{Code: "invalid_limit", Source: "beads", Detail: "recent closed convoy limit must not be negative"}}}
	}
	result := Page[BeadSource]{Items: []BeadSource{}}
	seen := map[string]bool{}
	seenCursors := map[string]bool{}
	var cursor string
	for {
		params := &genclient.GetV0CityByCityNameBeadsParams{
			Type: stringPtr("convoy"), All: boolPtr(true), Limit: int64Ptr(upstreamPageLimit),
		}
		if cursor != "" {
			params.Cursor = &cursor
		}
		callCtx, cancel := boundedReadContext(ctx)
		response, err := c.api.GetV0CityByCityNameBeadsWithResponse(callCtx, c.city, params)
		cancel()
		body, callErr := checkedBody("list recent closed convoys", response != nil, statusOfBeads(response), problemOfBeads(response), bodyOfBeads(response), err)
		if callErr != nil {
			result = mergePageError(result, callErr, "beads")
			break
		}
		mergeBeadPage(&result, body, seen, "beads")
		cursor = pointerString(body.NextCursor)
		if cursor == "" {
			break
		}
		if seenCursors[cursor] {
			result = mergePageError(result, repeatedCursorError("list recent closed convoys", cursor), "beads")
			break
		}
		seenCursors[cursor] = true
	}
	closed := result.Items[:0]
	for _, item := range result.Items {
		if terminalStatus(item.Status) {
			closed = append(closed, item)
		}
	}
	result.Items = closed
	sort.SliceStable(result.Items, func(left, right int) bool {
		return result.Items[left].UpdatedAt.After(result.Items[right].UpdatedAt)
	})
	if limit >= 0 && len(result.Items) > limit {
		result.Items = result.Items[:limit]
	}
	return result
}

// GetConvoy reads canonical direct children and Supervisor progress.
func (c *Client) GetConvoy(ctx context.Context, id string) (ConvoySource, error) {
	callCtx, cancel := boundedReadContext(ctx)
	response, err := c.api.GetV0CityByCityNameConvoyByIdWithResponse(callCtx, c.city, id)
	cancel()
	body, err := checkedBody("get convoy", response != nil, statusOfConvoy(response), problemOfConvoy(response), bodyOfConvoy(response), err)
	if err != nil {
		return ConvoySource{}, err
	}
	result := ConvoySource{}
	if body.Convoy != nil {
		convoy := convertBead(*body.Convoy)
		result.Convoy = &convoy
	}
	if body.Children != nil {
		result.Children = make([]BeadSource, 0, len(*body.Children))
		for _, child := range *body.Children {
			result.Children = append(result.Children, convertBead(child))
		}
	}
	if body.Progress != nil {
		result.Progress = &Progress{Closed: int(body.Progress.Closed), Total: int(body.Progress.Total)}
	}
	return result, nil
}

// GetWorkflow reads the dedicated workflow endpoint for graph context.
func (c *Client) GetWorkflow(ctx context.Context, workflowID, scopeKind, scopeRef string) (WorkflowSource, error) {
	params := &genclient.GetV0CityByCityNameWorkflowByWorkflowIdParams{ScopeKind: optionalString(scopeKind), ScopeRef: optionalString(scopeRef)}
	callCtx, cancel := boundedReadContext(ctx)
	response, err := c.api.GetV0CityByCityNameWorkflowByWorkflowIdWithResponse(callCtx, c.city, workflowID, params)
	cancel()
	body, err := checkedBody("get workflow", response != nil, statusOfWorkflow(response), problemOfWorkflow(response), bodyOfWorkflow(response), err)
	if err != nil {
		return WorkflowSource{}, err
	}
	return WorkflowSource{
		WorkflowID: body.WorkflowId, RootID: body.RootBeadId, RootStoreRef: body.RootStoreRef,
		ScopeKind: body.ScopeKind, ScopeRef: body.ScopeRef, Partial: body.Partial,
	}, nil
}

// ListSessions pages live session evidence without requesting transcript peeks.
func (c *Client) ListSessions(ctx context.Context) Page[SessionSource] {
	result := Page[SessionSource]{Items: []SessionSource{}}
	seen := map[string]bool{}
	seenCursors := map[string]bool{}
	var cursor string
	for {
		params := &genclient.GetV0CityByCityNameSessionsParams{Limit: int64Ptr(upstreamPageLimit), Peek: boolPtr(false)}
		if cursor != "" {
			params.Cursor = &cursor
		}
		callCtx, cancel := boundedReadContext(ctx)
		response, err := c.api.GetV0CityByCityNameSessionsWithResponse(callCtx, c.city, params)
		cancel()
		body, callErr := checkedBody("list sessions", response != nil, statusOfSessions(response), problemOfSessions(response), bodyOfSessions(response), err)
		if callErr != nil {
			return mergePageError(result, callErr, "sessions")
		}
		if body.Items != nil {
			for _, item := range *body.Items {
				if seen[item.Id] {
					continue
				}
				seen[item.Id] = true
				result.Items = append(result.Items, convertSession(item))
			}
		}
		mergePartial(&result, boolValue(body.Partial), stringsValue(body.PartialErrors), "sessions")
		cursor = pointerString(body.NextCursor)
		if cursor == "" {
			return result
		}
		if seenCursors[cursor] {
			return mergePageError(result, repeatedCursorError("list sessions", cursor), "sessions")
		}
		seenCursors[cursor] = true
	}
}

// ListPending reads city pending-interaction evidence.
func (c *Client) ListPending(ctx context.Context) Page[PendingSource] {
	callCtx, cancel := boundedReadContext(ctx)
	response, err := c.api.GetV0CityByCityNamePendingWithResponse(callCtx, c.city)
	cancel()
	body, err := checkedBody("list pending interactions", response != nil, statusOfPending(response), problemOfPending(response), bodyOfPending(response), err)
	if err != nil {
		return mergePageError(Page[PendingSource]{Items: []PendingSource{}}, err, "pending")
	}
	result := Page[PendingSource]{Items: []PendingSource{}}
	if body.Items != nil {
		for _, item := range *body.Items {
			result.Items = append(result.Items, PendingSource{SessionID: item.SessionId, Kind: item.Kind, RequestID: item.RequestId})
		}
	}
	mergePartial(&result, boolValue(body.Partial), stringsValue(body.PartialErrors), "pending")
	return result
}

// ListOrders reads registered order definitions.
func (c *Client) ListOrders(ctx context.Context) ([]OrderSource, error) {
	callCtx, cancel := boundedReadContext(ctx)
	response, err := c.api.GetV0CityByCityNameOrdersWithResponse(callCtx, c.city)
	cancel()
	body, err := checkedBody("list orders", response != nil, statusOfOrders(response), problemOfOrders(response), bodyOfOrders(response), err)
	if err != nil {
		return nil, err
	}
	result := []OrderSource{}
	if body.Orders == nil {
		return result, nil
	}
	for _, order := range *body.Orders {
		result = append(result, convertOrder(order))
	}
	return result, nil
}

// CheckOrders reads cached trigger and last-run facts.
func (c *Client) CheckOrders(ctx context.Context) ([]OrderCheckSource, error) {
	params := &genclient.GetV0CityByCityNameOrdersCheckParams{}
	callCtx, cancel := boundedReadContext(ctx)
	response, err := c.api.GetV0CityByCityNameOrdersCheckWithResponse(callCtx, c.city, params)
	cancel()
	body, err := checkedBody("check orders", response != nil, statusOfChecks(response), problemOfChecks(response), bodyOfChecks(response), err)
	if err != nil {
		return nil, err
	}
	result := []OrderCheckSource{}
	if body.Checks == nil {
		return result, nil
	}
	for _, check := range *body.Checks {
		result = append(result, OrderCheckSource{
			Name: check.Name, ScopedName: check.ScopedName, Due: check.Due, Reason: check.Reason,
			LastRun: pointerString(check.LastRun), LastRunOutcome: pointerString(check.LastRunOutcome),
		})
	}
	return result, nil
}

// ListOrderFeed reads the bounded city feed used for exact run-status joins.
func (c *Client) ListOrderFeed(ctx context.Context) Page[OrderFeedSource] {
	params := &genclient.GetV0CityByCityNameOrdersFeedParams{
		ScopeKind: stringPtr("city"), ScopeRef: &c.city, Limit: int64Ptr(orderFeedLimit),
	}
	callCtx, cancel := boundedReadContext(ctx)
	response, err := c.api.GetV0CityByCityNameOrdersFeedWithResponse(callCtx, c.city, params)
	cancel()
	body, err := checkedBody("list order feed", response != nil, statusOfFeed(response), problemOfFeed(response), bodyOfFeed(response), err)
	if err != nil {
		return mergePageError(Page[OrderFeedSource]{Items: []OrderFeedSource{}}, err, "orders_feed")
	}
	result := Page[OrderFeedSource]{Items: []OrderFeedSource{}}
	if body.Items != nil {
		for _, item := range *body.Items {
			result.Items = append(result.Items, OrderFeedSource{
				BeadID: pointerString(item.BeadId), StoreRef: pointerString(item.StoreRef), Status: item.Status,
			})
		}
	}
	mergePartial(&result, body.Partial, stringsValue(body.PartialErrors), "orders_feed")
	return result
}

// ListOrderHistory reads one order's bounded history page.
func (c *Client) ListOrderHistory(ctx context.Context, scopedName, before string, limit int) ([]OrderRunSource, error) {
	if limit <= 0 {
		limit = int(orderHistoryLimit)
	}
	params := &genclient.GetV0CityByCityNameOrdersHistoryParams{ScopedName: scopedName, Limit: int64Ptr(int64(limit)), Before: optionalString(before)}
	callCtx, cancel := boundedReadContext(ctx)
	response, err := c.api.GetV0CityByCityNameOrdersHistoryWithResponse(callCtx, c.city, params)
	cancel()
	body, err := checkedBody("list order history", response != nil, statusOfHistory(response), problemOfHistory(response), bodyOfHistory(response), err)
	if err != nil {
		return nil, err
	}
	result := []OrderRunSource{}
	if body.Entries == nil {
		return result, nil
	}
	for _, entry := range *body.Entries {
		result = append(result, OrderRunSource{
			BeadID: entry.BeadId, StoreRef: entry.StoreRef, CreatedAt: entry.CreatedAt,
			DurationMS: pointerString(entry.DurationMs), ExitCode: pointerString(entry.ExitCode),
			Labels: stringsValue(entry.Labels), HasOutput: entry.HasOutput,
		})
	}
	return result, nil
}

// ListOrderHistories reads independent order histories with a hard concurrency
// ceiling so one slow order does not serialize or fan out without bound.
func (c *Client) ListOrderHistories(ctx context.Context, scopedNames []string, limit int) (map[string][]OrderRunSource, []Problem) {
	result := make(map[string][]OrderRunSource, len(scopedNames))
	problems := []Problem{}
	jobs := make(chan string)
	var mu sync.Mutex
	var workers sync.WaitGroup
	for worker := 0; worker < historyConcurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for scopedName := range jobs {
				runs, err := c.ListOrderHistory(ctx, scopedName, "", limit)
				mu.Lock()
				if err != nil {
					problem := problemFromError(err, "orders_history", scopedName)
					problems = append(problems, problem)
				} else {
					result[scopedName] = runs
				}
				mu.Unlock()
			}
		}()
	}
	for _, name := range scopedNames {
		select {
		case jobs <- name:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return result, append(problems, problemFromError(ctx.Err(), "orders_history", name))
		}
	}
	close(jobs)
	workers.Wait()
	sort.Slice(problems, func(left, right int) bool { return problems[left].ResourceID < problems[right].ResourceID })
	return result, problems
}

// GetOrderRunOutput loads output on demand and forwards both identity fields.
func (c *Client) GetOrderRunOutput(ctx context.Context, beadID, storeRef string) (OrderRunOutput, error) {
	const operation = "get order run output"
	params := &genclient.GetV0CityByCityNameOrderHistoryByBeadIdParams{StoreRef: &storeRef}
	callCtx, cancel := boundedReadContext(ctx)
	response, err := c.api.GetV0CityByCityNameOrderHistoryByBeadIdWithResponse(callCtx, c.city, beadID, params)
	cancel()
	body, err := checkedBody(operation, response != nil, statusOfOutput(response), problemOfOutput(response), bodyOfOutput(response), err)
	if err != nil {
		return OrderRunOutput{}, err
	}
	status := statusOfOutput(response)
	if body.BeadId == "" || body.StoreRef == "" {
		return OrderRunOutput{}, &UpstreamError{
			Code: "upstream_protocol", Operation: operation, StatusCode: status,
			Detail: "successful Supervisor response omitted bead_id or store_ref",
		}
	}
	if body.BeadId != beadID || body.StoreRef != storeRef {
		return OrderRunOutput{}, &UpstreamError{
			Code: "upstream_protocol", Operation: operation, StatusCode: status,
			Detail: fmt.Sprintf("Supervisor output identity %q/%q does not match requested %q/%q", body.StoreRef, body.BeadId, storeRef, beadID),
		}
	}
	return OrderRunOutput{
		BeadID: body.BeadId, StoreRef: body.StoreRef, CreatedAt: body.CreatedAt,
		Labels: stringsValue(body.Labels), Output: body.Output,
	}, nil
}

func checkedBody[T any](operation string, responsePresent bool, status int, problem *genclient.ErrorModel, body *T, err error) (*T, error) {
	if err != nil {
		return nil, &UpstreamError{Code: "upstream_unavailable", Operation: operation, Detail: "Supervisor request failed", Err: err}
	}
	if !responsePresent {
		return nil, &UpstreamError{Code: "upstream_protocol", Operation: operation, Detail: "generated client returned a nil response"}
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		detail := fmt.Sprintf("Supervisor returned HTTP %d", status)
		if problem != nil && problem.Detail != nil && *problem.Detail != "" {
			detail = *problem.Detail
		}
		return nil, &UpstreamError{Code: "upstream_http", Operation: operation, StatusCode: status, Detail: detail}
	}
	if body == nil {
		return nil, &UpstreamError{Code: "upstream_protocol", Operation: operation, StatusCode: status, Detail: "successful Supervisor response omitted its typed body"}
	}
	return body, nil
}

func mergeBeadPage(result *Page[BeadSource], body *genclient.ListBodyBead, seen map[string]bool, source string) {
	if body.Items != nil {
		for _, item := range *body.Items {
			if seen[item.Id] {
				continue
			}
			seen[item.Id] = true
			result.Items = append(result.Items, convertBead(item))
		}
	}
	mergePartial(result, boolValue(body.Partial), stringsValue(body.PartialErrors), source)
}

func mergePartial[T any](result *Page[T], partial bool, details []string, source string) {
	if !partial && len(details) == 0 {
		return
	}
	result.Partial = true
	if len(details) == 0 {
		details = []string{"Supervisor reported a partial response"}
	}
	for _, detail := range details {
		result.Problems = append(result.Problems, Problem{Code: "upstream_partial", Source: source, Detail: detail, Retryable: true})
	}
}

func mergePageError[T any](result Page[T], err error, source string) Page[T] {
	result.Partial = true
	result.Problems = append(result.Problems, problemFromError(err, source, ""))
	return result
}

func problemFromError(err error, source, resourceID string) Problem {
	problem := Problem{Code: "upstream_unavailable", Source: source, Detail: err.Error(), ResourceID: resourceID, Retryable: true}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		problem.Code = upstream.Code
		// A projection Page cannot carry the upstream HTTP status separately.
		// Preserve Supervisor availability failures in the stable problem code so
		// callers do not accidentally turn a 503/504 into a local 502.
		if upstream.StatusCode == http.StatusServiceUnavailable || upstream.StatusCode == http.StatusGatewayTimeout {
			problem.Code = "upstream_unavailable"
		}
		problem.Detail = upstream.Detail
		problem.Retryable = problem.Code != "upstream_protocol" || upstream.StatusCode >= http.StatusInternalServerError
	}
	return problem
}

func repeatedCursorError(operation, cursor string) error {
	return &UpstreamError{Code: "upstream_protocol", Operation: operation, Detail: fmt.Sprintf("Supervisor repeated pagination cursor %q", cursor)}
}

func convertBead(source genclient.Bead) BeadSource {
	metadata := cloneStringPointerMap(source.Metadata)
	updatedAt := time.Time{}
	if source.UpdatedAt != nil {
		updatedAt = *source.UpdatedAt
	}
	return BeadSource{
		ID: source.Id, Title: source.Title, Type: source.IssueType, Status: source.Status,
		Assignee: pointerString(source.Assignee), LogicalID: metadata[beadmeta.LogicalBeadIDMetadataKey],
		StepRef: metadata[beadmeta.StepRefMetadataKey], Metadata: metadata, Needs: stringsValue(source.Needs),
		Blocked: boolValue(source.IsBlocked), UpdatedAt: updatedAt,
	}
}

func convertSession(source genclient.SessionResponse) SessionSource {
	return SessionSource{
		ID: source.Id, SessionName: source.SessionName, Alias: pointerString(source.Alias), Template: source.Template,
		State: source.State, Activity: pointerString(source.Activity), ActiveBead: pointerString(source.ActiveBead),
		Running: source.Running, Attached: source.Attached,
	}
}

func convertOrder(source genclient.OrderResponse) OrderSource {
	return OrderSource{
		Name: source.Name, ScopedName: source.ScopedName, Type: source.Type, Enabled: source.Enabled,
		CaptureOutput: source.CaptureOutput, Description: pointerString(source.Description), Check: pointerString(source.Check),
		Exec: pointerString(source.Exec), Formula: pointerString(source.Formula),
		Interval: pointerString(source.Interval), On: pointerString(source.On), Pool: pointerString(source.Pool),
		Rig: pointerString(source.Rig), Schedule: pointerString(source.Schedule), Timeout: pointerString(source.Timeout),
		Trigger: pointerString(source.Trigger),
	}
}

func boundedReadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, readTimeout)
}

func cloneStringPointerMap(source *map[string]string) map[string]string {
	if source == nil {
		return map[string]string{}
	}
	return cloneStrings(*source)
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringsValue(value *[]string) []string {
	if value == nil {
		return []string{}
	}
	return append([]string(nil), (*value)...)
}

func boolValue(value *bool) bool { return value != nil && *value }

func stringPtr(value string) *string { return &value }
func int64Ptr(value int64) *int64    { return &value }
func boolPtr(value bool) *bool       { return &value }

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func statusOfConvoys(response *genclient.GetV0CityByCityNameConvoysResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfConvoys(response *genclient.GetV0CityByCityNameConvoysResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfConvoys(response *genclient.GetV0CityByCityNameConvoysResponse) *genclient.ListBodyBead {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfBeads(response *genclient.GetV0CityByCityNameBeadsResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfBeads(response *genclient.GetV0CityByCityNameBeadsResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfBeads(response *genclient.GetV0CityByCityNameBeadsResponse) *genclient.ListBodyBead {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfConvoy(response *genclient.GetV0CityByCityNameConvoyByIdResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfConvoy(response *genclient.GetV0CityByCityNameConvoyByIdResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfConvoy(response *genclient.GetV0CityByCityNameConvoyByIdResponse) *genclient.ConvoyGetResponse {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfWorkflow(response *genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfWorkflow(response *genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfWorkflow(response *genclient.GetV0CityByCityNameWorkflowByWorkflowIdResponse) *genclient.WorkflowSnapshotResponse {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfSessions(response *genclient.GetV0CityByCityNameSessionsResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfSessions(response *genclient.GetV0CityByCityNameSessionsResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfSessions(response *genclient.GetV0CityByCityNameSessionsResponse) *genclient.ListBodySessionResponse {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfPending(response *genclient.GetV0CityByCityNamePendingResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfPending(response *genclient.GetV0CityByCityNamePendingResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfPending(response *genclient.GetV0CityByCityNamePendingResponse) *genclient.ListBodyCityPendingEntry {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfOrders(response *genclient.GetV0CityByCityNameOrdersResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfOrders(response *genclient.GetV0CityByCityNameOrdersResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfOrders(response *genclient.GetV0CityByCityNameOrdersResponse) *genclient.OrderListBody {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfChecks(response *genclient.GetV0CityByCityNameOrdersCheckResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfChecks(response *genclient.GetV0CityByCityNameOrdersCheckResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfChecks(response *genclient.GetV0CityByCityNameOrdersCheckResponse) *genclient.OrderCheckListBody {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfFeed(response *genclient.GetV0CityByCityNameOrdersFeedResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfFeed(response *genclient.GetV0CityByCityNameOrdersFeedResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfFeed(response *genclient.GetV0CityByCityNameOrdersFeedResponse) *genclient.OrdersFeedBody {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfHistory(response *genclient.GetV0CityByCityNameOrdersHistoryResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfHistory(response *genclient.GetV0CityByCityNameOrdersHistoryResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfHistory(response *genclient.GetV0CityByCityNameOrdersHistoryResponse) *genclient.OrderHistoryListBody {
	if response == nil {
		return nil
	}
	return response.JSON200
}

func statusOfOutput(response *genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemOfOutput(response *genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func bodyOfOutput(response *genclient.GetV0CityByCityNameOrderHistoryByBeadIdResponse) *genclient.OrderHistoryDetailResponse {
	if response == nil {
		return nil
	}
	return response.JSON200
}
