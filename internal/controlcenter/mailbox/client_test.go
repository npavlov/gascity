package mailbox

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

func TestClientGeneratedReadFailureMatrix(t *testing.T) {
	tests := []struct {
		operation string
		call      func(*Client) error
		modes     []string
	}{
		{operation: "count", call: func(client *Client) error { _, err := client.Count(context.Background()); return err }, modes: []string{"transport", "nil_response", "not_found", "unavailable", "non_2xx", "nil_body", "valid_empty", "partial"}},
		{operation: "list", call: func(client *Client) error {
			_, err := client.List(context.Background(), Query{Filter: FilterUnread})
			return err
		}, modes: []string{"transport", "nil_response", "not_found", "unavailable", "non_2xx", "nil_body", "malformed", "valid_empty", "valid_page", "partial"}},
		{operation: "detail", call: func(client *Client) error { _, err := client.Get(context.Background(), "mail-1", nil); return err }, modes: []string{"transport", "nil_response", "not_found", "unavailable", "non_2xx", "nil_body", "malformed", "mismatched", "valid_page"}},
		{operation: "thread", call: func(client *Client) error { _, err := client.Thread(context.Background(), "thread-1", nil); return err }, modes: []string{"transport", "nil_response", "not_found", "unavailable", "non_2xx", "nil_body", "malformed", "valid_empty", "valid_page", "partial"}},
	}

	for _, test := range tests {
		for _, mode := range test.modes {
			t.Run(test.operation+"/"+mode, func(t *testing.T) {
				client, err := NewClient("taxdome", &fakeSupervisorMail{modes: map[string]string{test.operation: mode}})
				if err != nil {
					t.Fatalf("NewClient: %v", err)
				}
				err = test.call(client)
				valid := mode == "valid_empty" || mode == "valid_page" || mode == "partial"
				if valid {
					if err != nil {
						t.Fatalf("valid read: %v", err)
					}
					return
				}
				var upstream *UpstreamError
				if !errors.As(err, &upstream) {
					t.Fatalf("error = %T %v, want UpstreamError", err, err)
				}
				if upstream.Operation != "mail."+test.operation {
					t.Fatalf("operation = %q", upstream.Operation)
				}
				wantCode, wantStatus := "upstream_protocol", 0
				switch mode {
				case "transport":
					wantCode = "upstream_unavailable"
				case "nil_body", "malformed", "mismatched":
					wantStatus = http.StatusOK
				case "not_found":
					wantCode, wantStatus = "upstream_http", http.StatusNotFound
				case "unavailable":
					wantCode, wantStatus = "upstream_unavailable", http.StatusServiceUnavailable
				case "non_2xx":
					wantCode, wantStatus = "upstream_http", http.StatusTeapot
				}
				if upstream.Code != wantCode || upstream.StatusCode != wantStatus {
					t.Fatalf("upstream = %#v, want code=%s status=%d", upstream, wantCode, wantStatus)
				}
			})
		}
	}
}

func TestClientMapsTimeoutTransportToUnavailable(t *testing.T) {
	fake := &fakeSupervisorMail{count: func(context.Context, string, *genclient.GetV0CityByCityNameMailCountParams) (*genclient.GetV0CityByCityNameMailCountResponse, error) {
		return nil, context.DeadlineExceeded
	}}
	client := mustMailClient(t, fake)
	_, err := client.Count(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Code != "upstream_unavailable" || !errors.Is(upstream, context.DeadlineExceeded) {
		t.Fatalf("error = %#v", err)
	}
}

func TestClientUsesExactGeneratedParameters(t *testing.T) {
	fake := &fakeSupervisorMail{}
	var countParams *genclient.GetV0CityByCityNameMailCountParams
	var listParams []*genclient.GetV0CityByCityNameMailParams
	var detailIDs []string
	var detailRigs []*string
	var threadIDs []string
	var threadRigs []*string
	fake.count = func(_ context.Context, city string, params *genclient.GetV0CityByCityNameMailCountParams) (*genclient.GetV0CityByCityNameMailCountResponse, error) {
		if city != "taxdome" {
			t.Fatalf("city = %q", city)
		}
		copy := *params
		countParams = &copy
		return mailCountResponse(&genclient.MailCountOutputBody{}), nil
	}
	fake.list = func(_ context.Context, city string, params *genclient.GetV0CityByCityNameMailParams) (*genclient.GetV0CityByCityNameMailResponse, error) {
		if city != "taxdome" {
			t.Fatalf("city = %q", city)
		}
		copy := *params
		listParams = append(listParams, &copy)
		items := []genclient.Message{}
		return mailListResponse(&genclient.MailListBody{Items: &items}), nil
	}
	fake.detail = func(_ context.Context, city, id string, params *genclient.GetV0CityByCityNameMailByIdParams) (*genclient.GetV0CityByCityNameMailByIdResponse, error) {
		if city != "taxdome" {
			t.Fatalf("city = %q", city)
		}
		detailIDs = append(detailIDs, id)
		detailRigs = append(detailRigs, params.Rig)
		return mailDetailResponseFor("mail-1", nil), nil
	}
	fake.thread = func(_ context.Context, city, id string, params *genclient.GetV0CityByCityNameMailThreadByIdParams) (*genclient.GetV0CityByCityNameMailThreadByIdResponse, error) {
		if city != "taxdome" {
			t.Fatalf("city = %q", city)
		}
		threadIDs = append(threadIDs, id)
		threadRigs = append(threadRigs, params.Rig)
		items := []genclient.Message{}
		return mailThreadResponse(&genclient.MailListBody{Items: &items}), nil
	}
	client := mustMailClient(t, fake)

	_, _ = client.Count(context.Background())
	_, _ = client.List(context.Background(), Query{})
	_, _ = client.List(context.Background(), Query{Filter: FilterAll, Cursor: "opaque", Limit: 150})
	rig := "taxdome"
	_, _ = client.Get(context.Background(), "mail-1", &rig)
	_, _ = client.Get(context.Background(), "mail-1", nil)
	_, _ = client.Thread(context.Background(), "thread-1", &rig)
	_, _ = client.Thread(context.Background(), "thread-1", nil)

	if countParams == nil || countParams.Agent != nil || countParams.Rig != nil {
		t.Fatalf("count params = %#v", countParams)
	}
	if len(listParams) != 2 {
		t.Fatalf("list calls = %d", len(listParams))
	}
	first, second := listParams[0], listParams[1]
	if first.Agent != nil || first.Rig != nil || first.Index != nil || first.Wait != nil || testStringValue(first.Status) != "unread" || testStringValue(first.Cursor) != "" || testInt64Value(first.Limit) != 50 {
		t.Fatalf("default list params = %#v", first)
	}
	if second.Agent != nil || second.Rig != nil || second.Index != nil || second.Wait != nil || testStringValue(second.Status) != "all" || testStringValue(second.Cursor) != "opaque" || testInt64Value(second.Limit) != 100 {
		t.Fatalf("bounded list params = %#v", second)
	}
	if !reflect.DeepEqual(detailIDs, []string{"mail-1", "mail-1"}) || testStringValue(detailRigs[0]) != "taxdome" || detailRigs[1] != nil {
		t.Fatalf("detail forwarding ids=%v rigs=%v", detailIDs, detailRigs)
	}
	if !reflect.DeepEqual(threadIDs, []string{"thread-1", "thread-1"}) || testStringValue(threadRigs[0]) != "taxdome" || threadRigs[1] != nil {
		t.Fatalf("thread forwarding ids=%v rigs=%v", threadIDs, threadRigs)
	}
}

func TestClientPreservesMailFieldsPagesPartialsAndThreadTruncation(t *testing.T) {
	rig := "rig-one"
	fake := &fakeSupervisorMail{
		count: func(context.Context, string, *genclient.GetV0CityByCityNameMailCountParams) (*genclient.GetV0CityByCityNameMailCountResponse, error) {
			partial := true
			errors := []string{"rig-two unavailable"}
			return mailCountResponse(&genclient.MailCountOutputBody{Total: 8, Unread: 3, Partial: &partial, PartialErrors: &errors}), nil
		},
		list: func(context.Context, string, *genclient.GetV0CityByCityNameMailParams) (*genclient.GetV0CityByCityNameMailResponse, error) {
			items := []genclient.Message{generatedMail("mail-1", &rig), generatedMail("mail-2", nil)}
			next, partial := "next-page", true
			errors := []string{"rig-two unavailable"}
			return mailListResponse(&genclient.MailListBody{Items: &items, Total: 20, NextCursor: &next, Partial: &partial, PartialErrors: &errors}), nil
		},
		detail: func(context.Context, string, string, *genclient.GetV0CityByCityNameMailByIdParams) (*genclient.GetV0CityByCityNameMailByIdResponse, error) {
			return mailDetailResponseFor("mail-1", &rig), nil
		},
		thread: func(context.Context, string, string, *genclient.GetV0CityByCityNameMailThreadByIdParams) (*genclient.GetV0CityByCityNameMailThreadByIdResponse, error) {
			items := []genclient.Message{generatedMail("mail-2", nil), generatedMail("mail-1", &rig)}
			next := "cannot-follow"
			return mailThreadResponse(&genclient.MailListBody{Items: &items, Total: 9, NextCursor: &next}), nil
		},
	}
	client := mustMailClient(t, fake)

	count, err := client.Count(context.Background())
	if err != nil || count.Total != 8 || count.Unread != 3 || !count.Partial || !reflect.DeepEqual(count.PartialErrors, []string{"rig-two unavailable"}) {
		t.Fatalf("count = %#v, %v", count, err)
	}
	page, err := client.List(context.Background(), Query{Filter: FilterUnread})
	if err != nil || len(page.Items) != 2 || page.Total != 20 || page.NextCursor != "next-page" || !page.Partial || page.Items[0].CC == nil {
		t.Fatalf("page = %#v, %v", page, err)
	}
	detail, err := client.Get(context.Background(), "mail-1", &rig)
	if err != nil || detail.ID != "mail-1" || detail.Read || detail.Body != "plain <body>" || detail.ThreadID == nil || detail.Priority == nil || detail.CC == nil {
		t.Fatalf("detail = %#v, %v", detail, err)
	}
	thread, err := client.Thread(context.Background(), "thread-1", &rig)
	if err != nil || !reflect.DeepEqual(messageIDs(thread.Items), []string{"mail-2", "mail-1"}) || !thread.Truncated || !thread.Partial || thread.Total != 9 {
		t.Fatalf("thread = %#v, %v", thread, err)
	}
}

type fakeSupervisorMail struct {
	modes  map[string]string
	count  func(context.Context, string, *genclient.GetV0CityByCityNameMailCountParams) (*genclient.GetV0CityByCityNameMailCountResponse, error)
	list   func(context.Context, string, *genclient.GetV0CityByCityNameMailParams) (*genclient.GetV0CityByCityNameMailResponse, error)
	detail func(context.Context, string, string, *genclient.GetV0CityByCityNameMailByIdParams) (*genclient.GetV0CityByCityNameMailByIdResponse, error)
	thread func(context.Context, string, string, *genclient.GetV0CityByCityNameMailThreadByIdParams) (*genclient.GetV0CityByCityNameMailThreadByIdResponse, error)
}

//nolint:revive // Method spelling is fixed by the generated client interface.
func (f *fakeSupervisorMail) GetV0CityByCityNameMailCountWithResponse(ctx context.Context, city string, params *genclient.GetV0CityByCityNameMailCountParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameMailCountResponse, error) {
	if f.count != nil {
		return f.count(ctx, city, params)
	}
	mode := f.mode("count")
	if mode == "transport" {
		return nil, errors.New("transport failed")
	}
	if mode == "nil_response" {
		return nil, nil
	}
	if status := failureStatus(mode); status != 0 {
		return mailCountErrorResponse(status), nil
	}
	if mode == "nil_body" {
		return mailCountResponse(nil), nil
	}
	partial := mode == "partial"
	errors := []string(nil)
	if partial {
		errors = []string{"rig partial"}
	}
	return mailCountResponse(&genclient.MailCountOutputBody{Total: 1, Unread: 1, Partial: &partial, PartialErrors: &errors}), nil
}

//nolint:revive // Method spelling is fixed by the generated client interface.
func (f *fakeSupervisorMail) GetV0CityByCityNameMailWithResponse(ctx context.Context, city string, params *genclient.GetV0CityByCityNameMailParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameMailResponse, error) {
	if f.list != nil {
		return f.list(ctx, city, params)
	}
	mode := f.mode("list")
	if mode == "transport" {
		return nil, errors.New("transport failed")
	}
	if mode == "nil_response" {
		return nil, nil
	}
	if status := failureStatus(mode); status != 0 {
		return mailListErrorResponse(status), nil
	}
	if mode == "nil_body" {
		return mailListResponse(nil), nil
	}
	items := []genclient.Message{}
	if mode == "malformed" {
		items = append(items, generatedMail("", nil))
	}
	if mode == "valid_page" || mode == "partial" {
		items = append(items, generatedMail("mail-1", nil))
	}
	partial := mode == "partial"
	errors := []string(nil)
	if partial {
		errors = []string{"rig partial"}
	}
	return mailListResponse(&genclient.MailListBody{Items: &items, Total: int64(len(items)), Partial: &partial, PartialErrors: &errors}), nil
}

//nolint:revive // Method spelling is fixed by the generated client interface.
func (f *fakeSupervisorMail) GetV0CityByCityNameMailByIdWithResponse(ctx context.Context, city, id string, params *genclient.GetV0CityByCityNameMailByIdParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameMailByIdResponse, error) {
	if f.detail != nil {
		return f.detail(ctx, city, id, params)
	}
	mode := f.mode("detail")
	if mode == "transport" {
		return nil, errors.New("transport failed")
	}
	if mode == "nil_response" {
		return nil, nil
	}
	if status := failureStatus(mode); status != 0 {
		return mailDetailErrorResponse(status), nil
	}
	if mode == "nil_body" {
		return mailDetailResponse(nil), nil
	}
	messageID := "mail-1"
	if mode == "malformed" {
		messageID = ""
	} else if mode == "mismatched" {
		messageID = "mail-other"
	}
	return mailDetailResponseFor(messageID, nil), nil
}

//nolint:revive // Method spelling is fixed by the generated client interface.
func (f *fakeSupervisorMail) GetV0CityByCityNameMailThreadByIdWithResponse(ctx context.Context, city, id string, params *genclient.GetV0CityByCityNameMailThreadByIdParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameMailThreadByIdResponse, error) {
	if f.thread != nil {
		return f.thread(ctx, city, id, params)
	}
	mode := f.mode("thread")
	if mode == "transport" {
		return nil, errors.New("transport failed")
	}
	if mode == "nil_response" {
		return nil, nil
	}
	if status := failureStatus(mode); status != 0 {
		return mailThreadErrorResponse(status), nil
	}
	if mode == "nil_body" {
		return mailThreadResponse(nil), nil
	}
	items := []genclient.Message{}
	if mode == "malformed" {
		items = append(items, generatedMail("", nil))
	}
	if mode == "valid_page" || mode == "partial" {
		items = append(items, generatedMail("mail-1", nil))
	}
	partial := mode == "partial"
	errors := []string(nil)
	if partial {
		errors = []string{"rig partial"}
	}
	return mailThreadResponse(&genclient.MailListBody{Items: &items, Total: int64(len(items)), Partial: &partial, PartialErrors: &errors}), nil
}

func (f *fakeSupervisorMail) mode(operation string) string {
	if f.modes == nil || f.modes[operation] == "" {
		return "valid_empty"
	}
	return f.modes[operation]
}

func failureStatus(mode string) int {
	switch mode {
	case "not_found":
		return http.StatusNotFound
	case "unavailable":
		return http.StatusServiceUnavailable
	case "non_2xx":
		return http.StatusTeapot
	default:
		return 0
	}
}

func generatedMail(id string, rig *string) genclient.Message {
	threadID, replyTo := "thread-1", "mail-0"
	priority := int64(4)
	cc := []string{"qa"}
	return genclient.Message{
		Id: id, From: "mayor", To: "crew", Subject: "Status", Body: "plain <body>",
		CreatedAt: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), Read: false,
		ThreadId: &threadID, ReplyTo: &replyTo, Priority: &priority, Cc: &cc, Rig: rig,
	}
}

func mailCountResponse(body *genclient.MailCountOutputBody) *genclient.GetV0CityByCityNameMailCountResponse {
	return &genclient.GetV0CityByCityNameMailCountResponse{HTTPResponse: &http.Response{StatusCode: http.StatusOK}, JSON200: body}
}

func mailCountErrorResponse(status int) *genclient.GetV0CityByCityNameMailCountResponse {
	return &genclient.GetV0CityByCityNameMailCountResponse{HTTPResponse: &http.Response{StatusCode: status}, ApplicationproblemJSONDefault: mailProblem(status)}
}

func mailListResponse(body *genclient.MailListBody) *genclient.GetV0CityByCityNameMailResponse {
	return &genclient.GetV0CityByCityNameMailResponse{HTTPResponse: &http.Response{StatusCode: http.StatusOK}, JSON200: body}
}

func mailListErrorResponse(status int) *genclient.GetV0CityByCityNameMailResponse {
	return &genclient.GetV0CityByCityNameMailResponse{HTTPResponse: &http.Response{StatusCode: status}, ApplicationproblemJSONDefault: mailProblem(status)}
}

func mailDetailResponse(body *genclient.Message) *genclient.GetV0CityByCityNameMailByIdResponse {
	return &genclient.GetV0CityByCityNameMailByIdResponse{HTTPResponse: &http.Response{StatusCode: http.StatusOK}, JSON200: body}
}

func mailDetailResponseFor(id string, rig *string) *genclient.GetV0CityByCityNameMailByIdResponse {
	body := generatedMail(id, rig)
	return mailDetailResponse(&body)
}

func mailDetailErrorResponse(status int) *genclient.GetV0CityByCityNameMailByIdResponse {
	return &genclient.GetV0CityByCityNameMailByIdResponse{HTTPResponse: &http.Response{StatusCode: status}, ApplicationproblemJSONDefault: mailProblem(status)}
}

func mailThreadResponse(body *genclient.MailListBody) *genclient.GetV0CityByCityNameMailThreadByIdResponse {
	return &genclient.GetV0CityByCityNameMailThreadByIdResponse{HTTPResponse: &http.Response{StatusCode: http.StatusOK}, JSON200: body}
}

func mailThreadErrorResponse(status int) *genclient.GetV0CityByCityNameMailThreadByIdResponse {
	return &genclient.GetV0CityByCityNameMailThreadByIdResponse{HTTPResponse: &http.Response{StatusCode: status}, ApplicationproblemJSONDefault: mailProblem(status)}
}

func mailProblem(status int) *genclient.ErrorModel {
	detail := http.StatusText(status)
	code := int64(status)
	return &genclient.ErrorModel{Status: &code, Detail: &detail}
}

func mustMailClient(t *testing.T, reader supervisorMailReader) *Client {
	t.Helper()
	client, err := NewClient("taxdome", reader)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func testStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func testInt64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
