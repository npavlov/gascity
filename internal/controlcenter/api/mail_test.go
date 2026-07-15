package controlapi

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/controlcenter/mailbox"
)

func TestMailRoutesProjectCountPageDetailAndOrderedThread(t *testing.T) {
	rig := "taxdome"
	threadID := "thread-1"
	created := time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC)
	first := mailbox.Message{ID: "mail-1", From: "mayor", To: "crew", Subject: "Unread", Body: "plain <body>", CreatedAt: created, Read: false, ThreadID: &threadID, CC: []string{}, Rig: &rig}
	second := mailbox.Message{ID: "mail-2", From: "worker", To: "mayor", Subject: "Read", Body: "second", CreatedAt: created.Add(time.Minute), Read: true, CC: []string{}}
	reader := &fakeMailboxReader{
		count:  mailbox.Count{Total: 12, Unread: 3, Partial: true, PartialErrors: []string{"rig-two unavailable"}},
		page:   mailbox.Page{Items: []mailbox.Message{first, second}, Total: 12, NextCursor: "next", Partial: true, PartialErrors: []string{"rig-two unavailable"}},
		detail: first,
		thread: mailbox.Thread{Items: []mailbox.Message{second, first}, Total: 2, PartialErrors: []string{}},
	}
	mux, _ := registeredMailAPI(t, reader)

	countRecorder := requestAPI(t, mux, "/api/v1/mail/count")
	var count MailCount
	decodeAPI(t, countRecorder, &count)
	if countRecorder.Code != http.StatusOK || count.SchemaVersion != 1 || count.Total != 12 || count.Unread != 3 || !count.Partial || !reflect.DeepEqual(count.PartialErrors, []string{"rig-two unavailable"}) {
		t.Fatalf("count = status %d %#v", countRecorder.Code, count)
	}

	pageRecorder := requestAPI(t, mux, "/api/v1/mail?status=all&cursor="+url.QueryEscape("opaque cursor")+"&limit=75")
	var page MailPage
	decodeAPI(t, pageRecorder, &page)
	if pageRecorder.Code != http.StatusOK || page.SchemaVersion != 1 || len(page.Items) != 2 || page.Total != 12 || page.NextCursor != "next" || !page.Partial {
		t.Fatalf("page = status %d %#v", pageRecorder.Code, page)
	}
	if reader.query != (mailbox.Query{Filter: mailbox.FilterAll, Cursor: "opaque cursor", Limit: 75}) {
		t.Fatalf("query = %#v", reader.query)
	}
	if page.Items[0].SchemaVersion != 1 || page.Items[0].Read || page.Items[0].Body != "plain <body>" {
		t.Fatalf("page message = %#v", page.Items[0])
	}

	detailRecorder := requestAPI(t, mux, "/api/v1/mail/mail-1?rig=taxdome")
	var detail MailMessage
	decodeAPI(t, detailRecorder, &detail)
	if detailRecorder.Code != http.StatusOK || detail.SchemaVersion != 1 || detail.ID != "mail-1" || detail.Read {
		t.Fatalf("detail = status %d %#v", detailRecorder.Code, detail)
	}
	if reader.detailID != "mail-1" || valueString(reader.detailRig) != "taxdome" {
		t.Fatalf("detail lookup = %q rig=%v", reader.detailID, reader.detailRig)
	}

	threadRecorder := requestAPI(t, mux, "/api/v1/mail/thread-1/thread?rig=taxdome")
	var thread MailThread
	decodeAPI(t, threadRecorder, &thread)
	if threadRecorder.Code != http.StatusOK || thread.SchemaVersion != 1 || idsMail(thread.Items) != "mail-2,mail-1" || thread.Total != 2 {
		t.Fatalf("thread = status %d %#v", threadRecorder.Code, thread)
	}
	if reader.threadID != "thread-1" || valueString(reader.threadRig) != "taxdome" {
		t.Fatalf("thread lookup = %q rig=%v", reader.threadID, reader.threadRig)
	}
	if reader.getCalls != 1 || reader.threadCalls != 1 || reader.detail.Read != false {
		t.Fatalf("GET-only selection calls detail=%d thread=%d read=%v", reader.getCalls, reader.threadCalls, reader.detail.Read)
	}
}

func TestMailRouteDefaultsToUnreadAndFifty(t *testing.T) {
	reader := &fakeMailboxReader{page: mailbox.Page{Items: []mailbox.Message{}, PartialErrors: []string{}}}
	mux, _ := registeredMailAPI(t, reader)
	if got := requestAPI(t, mux, "/api/v1/mail").Code; got != http.StatusOK {
		t.Fatalf("status = %d", got)
	}
	if reader.query != (mailbox.Query{Filter: mailbox.FilterUnread, Limit: 50}) {
		t.Fatalf("default query = %#v", reader.query)
	}
}

func TestMailRoutesValidateBeforeCallingReader(t *testing.T) {
	invalid := []string{
		"/api/v1/mail?status=",
		"/api/v1/mail?status=read",
		"/api/v1/mail?limit=",
		"/api/v1/mail?limit=0",
		"/api/v1/mail?limit=101",
		"/api/v1/mail?cursor=",
		"/api/v1/mail?cursor=" + strings.Repeat("x", 1025),
		"/api/v1/mail/-bad",
		"/api/v1/mail/bad%2Fid",
		"/api/v1/mail/" + strings.Repeat("x", 129),
		"/api/v1/mail/mail-1?rig=",
		"/api/v1/mail/mail-1?rig=" + strings.Repeat("x", 129),
		"/api/v1/mail/-bad/thread",
		"/api/v1/mail/thread-1/thread?rig=",
	}
	for _, path := range invalid {
		t.Run(path, func(t *testing.T) {
			reader := &fakeMailboxReader{}
			mux, _ := registeredMailAPI(t, reader)
			if got := requestAPI(t, mux, path).Code; got != http.StatusUnprocessableEntity {
				t.Fatalf("GET %s = %d, want 422", path, got)
			}
			if reader.calls() != 0 {
				t.Fatalf("invalid request called reader %d times", reader.calls())
			}
		})
	}
}

func TestMailRoutesMapStableErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		set  func(*fakeMailboxReader, error)
		err  error
		want int
	}{
		{name: "count unavailable", path: "/api/v1/mail/count", set: func(reader *fakeMailboxReader, err error) { reader.countErr = err }, err: context.DeadlineExceeded, want: http.StatusServiceUnavailable},
		{name: "list malformed", path: "/api/v1/mail", set: func(reader *fakeMailboxReader, err error) { reader.listErr = err }, err: &mailbox.UpstreamError{Code: "upstream_protocol", StatusCode: http.StatusOK, Detail: "missing body"}, want: http.StatusBadGateway},
		{name: "detail missing", path: "/api/v1/mail/mail-1", set: func(reader *fakeMailboxReader, err error) { reader.detailErr = err }, err: &mailbox.UpstreamError{Code: "upstream_http", StatusCode: http.StatusNotFound, Detail: "missing"}, want: http.StatusNotFound},
		{name: "thread upstream 503", path: "/api/v1/mail/thread-1/thread", set: func(reader *fakeMailboxReader, err error) { reader.threadErr = err }, err: &mailbox.UpstreamError{Code: "upstream_unavailable", StatusCode: http.StatusServiceUnavailable, Detail: "offline"}, want: http.StatusServiceUnavailable},
		{name: "thread other upstream", path: "/api/v1/mail/thread-1/thread", set: func(reader *fakeMailboxReader, err error) { reader.threadErr = err }, err: &mailbox.UpstreamError{Code: "upstream_http", StatusCode: http.StatusTeapot, Detail: "bad"}, want: http.StatusBadGateway},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &fakeMailboxReader{}
			test.set(reader, test.err)
			mux, _ := registeredMailAPI(t, reader)
			if got := requestAPI(t, mux, test.path).Code; got != test.want {
				t.Fatalf("status = %d, want %d", got, test.want)
			}
		})
	}
}

func TestMailOpenAPIHasFourGETOnlyRoutesAndPrefixedSchemas(t *testing.T) {
	_, api := registeredMailAPI(t, &fakeMailboxReader{})
	wantPaths := map[string]bool{
		"/api/v1/mail/count":       true,
		"/api/v1/mail":             true,
		"/api/v1/mail/{id}":        true,
		"/api/v1/mail/{id}/thread": true,
	}
	found := map[string]bool{}
	for path, item := range api.OpenAPI().Paths {
		if !strings.HasPrefix(path, "/api/v1/mail") {
			continue
		}
		found[path] = true
		if item.Get == nil || item.Post != nil || item.Put != nil || item.Patch != nil || item.Delete != nil {
			t.Errorf("mail path %s is not GET-only: %#v", path, item)
		}
	}
	if !reflect.DeepEqual(found, wantPaths) {
		t.Fatalf("mail paths = %v, want %v", found, wantPaths)
	}
	for _, name := range []string{"MailCount", "MailPage", "MailMessage", "MailThread"} {
		schema := api.OpenAPI().Components.Schemas.Map()[name]
		if schema == nil || schema.Properties["schema_version"] == nil {
			t.Errorf("OpenAPI missing versioned %s schema", name)
		}
	}
}

func TestGeneratedTypeScriptMailSurfaceIsGETOnly(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "cmd", "gc-control", "web", "src", "generated", "schema.d.ts"))
	if err != nil {
		t.Fatalf("read generated TypeScript schema: %v", err)
	}
	text := string(data)
	for _, path := range []string{"/api/v1/mail/count", "/api/v1/mail", "/api/v1/mail/{id}", "/api/v1/mail/{id}/thread"} {
		if !strings.Contains(text, path) {
			t.Errorf("generated TypeScript missing %s", path)
		}
	}
	for _, mutation := range []string{"postApiV1Mail", "deleteApiV1Mail", "patchApiV1Mail", "putApiV1Mail"} {
		if strings.Contains(text, mutation) {
			t.Errorf("generated TypeScript exposes mail mutation %s", mutation)
		}
	}
}

func registeredMailAPI(t *testing.T, reader mailbox.Reader) (*http.ServeMux, huma.API) {
	t.Helper()
	mux := http.NewServeMux()
	api := Register(mux, Options{CityName: "taxdome", SupervisorPing: func(context.Context) error { return nil }, Mail: reader})
	return mux, api
}

type fakeMailboxReader struct {
	count  mailbox.Count
	page   mailbox.Page
	detail mailbox.Message
	thread mailbox.Thread

	countErr  error
	listErr   error
	detailErr error
	threadErr error

	countCalls  int
	listCalls   int
	getCalls    int
	threadCalls int
	query       mailbox.Query
	detailID    string
	detailRig   *string
	threadID    string
	threadRig   *string
}

func (f *fakeMailboxReader) Count(context.Context) (mailbox.Count, error) {
	f.countCalls++
	return f.count, f.countErr
}

func (f *fakeMailboxReader) List(_ context.Context, query mailbox.Query) (mailbox.Page, error) {
	f.listCalls++
	f.query = query
	if f.page.Items == nil {
		f.page.Items = []mailbox.Message{}
	}
	if f.page.PartialErrors == nil {
		f.page.PartialErrors = []string{}
	}
	return f.page, f.listErr
}

func (f *fakeMailboxReader) Get(_ context.Context, id string, rig *string) (mailbox.Message, error) {
	f.getCalls++
	f.detailID, f.detailRig = id, rig
	return f.detail, f.detailErr
}

func (f *fakeMailboxReader) Thread(_ context.Context, id string, rig *string) (mailbox.Thread, error) {
	f.threadCalls++
	f.threadID, f.threadRig = id, rig
	if f.thread.Items == nil {
		f.thread.Items = []mailbox.Message{}
	}
	if f.thread.PartialErrors == nil {
		f.thread.PartialErrors = []string{}
	}
	return f.thread, f.threadErr
}

func (f *fakeMailboxReader) calls() int {
	return f.countCalls + f.listCalls + f.getCalls + f.threadCalls
}

func valueString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func idsMail(items []MailMessage) string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return strings.Join(ids, ",")
}

var _ mailbox.Reader = (*fakeMailboxReader)(nil)
