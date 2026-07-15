package mailbox

import (
	"reflect"
	"testing"
	"time"
)

func TestProjectCountPreservesAuthoritativeAndPartialFacts(t *testing.T) {
	tests := []struct {
		name    string
		partial bool
		errors  []string
	}{
		{name: "complete"},
		{name: "partial", partial: true, errors: []string{"rig one unavailable", "rig two stale"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ProjectCount(17, 4, test.partial, test.errors)
			if got.Total != 17 || got.Unread != 4 || got.Partial != test.partial || !reflect.DeepEqual(got.PartialErrors, nonNilStrings(test.errors)) {
				t.Fatalf("ProjectCount = %#v", got)
			}
		})
	}
}

func TestProjectPagePreservesUnreadAllAndTotalBeyondPageLength(t *testing.T) {
	created := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
	rigOne, rigTwo := "rig-one", "rig-two"
	threadID, replyTo := "thread-7", "mail-0"
	priority := int64(2)
	items := []Message{
		{ID: "mail-1", From: "mayor", To: "crew", Subject: "Unread", Body: "first", CreatedAt: created, Read: false, ThreadID: &threadID, ReplyTo: &replyTo, Priority: &priority, CC: []string{"reviewer"}, Rig: &rigOne},
		{ID: "mail-2", From: "worker", To: "mayor", Subject: "Read", Body: "second", CreatedAt: created.Add(time.Minute), Read: true, Rig: &rigTwo},
	}

	for _, test := range []struct {
		name  string
		query Query
		rows  []Message
	}{
		{name: "unread", query: Query{Filter: FilterUnread, Limit: 50}, rows: items[:1]},
		{name: "all", query: Query{Filter: FilterAll, Limit: 50}, rows: items},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ProjectPage(test.rows, 19, "opaque-next", true, []string{"one provider unavailable"})
			if err != nil {
				t.Fatalf("ProjectPage: %v", err)
			}
			if got.Total != 19 || got.Total == int64(len(got.Items)) || got.NextCursor != "opaque-next" || !got.Partial {
				t.Fatalf("page facts = %#v", got)
			}
			if !reflect.DeepEqual(got.Items, normalizedMessages(test.rows)) || !reflect.DeepEqual(got.PartialErrors, []string{"one provider unavailable"}) {
				t.Fatalf("page content = %#v", got)
			}
			if test.query.Filter != FilterUnread && test.query.Filter != FilterAll {
				t.Fatalf("unexpected filter %q", test.query.Filter)
			}
		})
	}
}

func TestProjectPageNormalizesNilSlicesAndPreservesDuplicateIdentityRows(t *testing.T) {
	rig := "rig-one"
	rows := []Message{
		{ID: "same", Rig: &rig},
		{ID: "same", Rig: &rig},
	}
	got, err := ProjectPage(rows, 2, "", false, nil)
	if err != nil {
		t.Fatalf("ProjectPage: %v", err)
	}
	if got.Items == nil || got.PartialErrors == nil || len(got.Items) != 2 {
		t.Fatalf("page = %#v, want non-nil slices and preserved duplicates", got)
	}
	for index := range got.Items {
		if got.Items[index].CC == nil {
			t.Fatalf("item %d CC is nil", index)
		}
	}

	empty, err := ProjectPage(nil, 0, "", false, nil)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.PartialErrors == nil {
		t.Fatalf("empty page = %#v, %v", empty, err)
	}
}

func TestProjectMessagePreservesEveryFieldAndRejectsMissingIdentity(t *testing.T) {
	created := time.Date(2026, 7, 15, 11, 0, 0, 123, time.FixedZone("offset", 90*60))
	threadID, replyTo, rig := "thread-1", "mail-previous", "taxdome"
	priority := int64(9)
	want := Message{
		ID: "mail:1", From: "mayor", To: "crew", Subject: "Status", Body: "plain <body>",
		CreatedAt: created, Read: false, ThreadID: &threadID, ReplyTo: &replyTo,
		Priority: &priority, CC: []string{"reviewer", "qa"}, Rig: &rig,
	}
	got, err := ProjectMessage(want)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ProjectMessage = %#v, %v", got, err)
	}

	if _, err := ProjectMessage(Message{Subject: "missing id"}); err == nil {
		t.Fatal("ProjectMessage accepted an empty required ID")
	}
}

func TestProjectThreadPreservesOrderAndReportsUnrequestableContinuation(t *testing.T) {
	rows := []Message{
		{ID: "mail-3", From: "a", Body: "first", Read: false},
		{ID: "mail-1", From: "b", Body: "second", Read: true},
		{ID: "mail-2", From: "c", Body: "third", Read: false},
	}
	got, err := ProjectThread(rows, 7, "unrequestable-next", true, []string{"rig partial"})
	if err != nil {
		t.Fatalf("ProjectThread: %v", err)
	}
	if ids := messageIDs(got.Items); !reflect.DeepEqual(ids, []string{"mail-3", "mail-1", "mail-2"}) {
		t.Fatalf("thread order = %v", ids)
	}
	if got.Total != 7 || !got.Partial || !got.Truncated || len(got.PartialErrors) != 2 || got.PartialErrors[0] != "rig partial" {
		t.Fatalf("thread = %#v", got)
	}
	if got.PartialErrors[1] != ThreadContinuationProblem {
		t.Fatalf("truncation problem = %q", got.PartialErrors[1])
	}
}

func TestProjectPageAndThreadRejectMalformedRows(t *testing.T) {
	for name, project := range map[string]func() error{
		"page": func() error {
			_, err := ProjectPage([]Message{{ID: "ok"}, {Subject: "missing id"}}, 2, "", false, nil)
			return err
		},
		"thread": func() error {
			_, err := ProjectThread([]Message{{Body: "missing id"}}, 1, "", false, nil)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := project(); err == nil {
				t.Fatal("projection accepted a row without a required ID")
			}
		})
	}
}

func nonNilStrings(values []string) []string {
	return append([]string{}, values...)
}

func normalizedMessages(values []Message) []Message {
	result := append([]Message{}, values...)
	for index := range result {
		result[index].CC = append([]string{}, result[index].CC...)
	}
	return result
}

func messageIDs(values []Message) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.ID)
	}
	return result
}
