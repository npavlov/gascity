package gcstate

import (
	"context"
	"errors"
	"testing"
)

func TestServiceListConvoysUsesRecentItemsWhenActiveUnavailable(t *testing.T) {
	convoy := BeadSource{
		ID: "recent-1", Title: "Recent convoy", Type: "convoy", Status: "closed",
		Metadata: map[string]string{
			"work_dir": "/tmp/recent", "parent_commit": "abc", "feature_branch": "feature/recent", "workflow_root": "wf-1",
		},
	}
	reader := &serviceReader{
		active: Page[BeadSource]{
			Partial:  true,
			Problems: []Problem{{Code: "upstream_unavailable", Source: "convoys", Detail: "active store unavailable", Retryable: true}},
		},
		recent:   Page[BeadSource]{Items: []BeadSource{convoy}},
		convoy:   ConvoySource{Convoy: &convoy, Children: []BeadSource{}, Progress: &Progress{}},
		workflow: WorkflowSource{WorkflowID: "wf-1", RootID: "root-1"},
	}
	service, err := NewService(reader)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := service.ListConvoys(context.Background())
	if err != nil {
		t.Fatalf("ListConvoys: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "recent-1" || !got.Degraded || got.Stale {
		t.Fatalf("convoys = %#v, want usable degraded recent data", got)
	}
	if len(got.Problems) != 1 || got.Problems[0].Code != "upstream_unavailable" {
		t.Fatalf("problems = %#v", got.Problems)
	}
}

func TestServiceListConvoysUsesActiveItemsWhenRecentUnavailable(t *testing.T) {
	convoy := BeadSource{
		ID: "active-1", Title: "Active convoy", Type: "convoy", Status: "open",
		Metadata: map[string]string{
			"work_dir": "/tmp/active", "parent_commit": "abc", "feature_branch": "feature/active", "workflow_root": "wf-1",
		},
	}
	reader := &serviceReader{
		active: Page[BeadSource]{Items: []BeadSource{convoy}},
		recent: Page[BeadSource]{
			Partial:  true,
			Problems: []Problem{{Code: "upstream_unavailable", Source: "beads", Detail: "recent store unavailable", Retryable: true}},
		},
		convoy:   ConvoySource{Convoy: &convoy, Children: []BeadSource{}, Progress: &Progress{}},
		workflow: WorkflowSource{WorkflowID: "wf-1", RootID: "root-1"},
	}
	service, err := NewService(reader)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	got, err := service.ListConvoys(context.Background())
	if err != nil {
		t.Fatalf("ListConvoys: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "active-1" || !got.Degraded || got.Stale {
		t.Fatalf("convoys = %#v, want usable degraded active data", got)
	}
}

func TestServiceListConvoysRequiresCacheWhenFailedPagesHaveNoItems(t *testing.T) {
	failure := Page[BeadSource]{
		Partial:  true,
		Problems: []Problem{{Code: "upstream_partial", Source: "convoys", Detail: "store returned no usable items", Retryable: true}},
	}
	reader := &serviceReader{active: failure, recent: failure}
	service, err := NewService(reader)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = service.ListConvoys(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Code != "upstream_unavailable" {
		t.Fatalf("first-load error = %#v, want upstream_unavailable", err)
	}

	convoy := BeadSource{
		ID: "cached-1", Title: "Cached convoy", Type: "convoy", Status: "open",
		Metadata: map[string]string{
			"work_dir": "/tmp/cached", "parent_commit": "abc", "feature_branch": "feature/cached", "workflow_root": "wf-1",
		},
	}
	reader.active = Page[BeadSource]{Items: []BeadSource{convoy}}
	reader.recent = Page[BeadSource]{Items: []BeadSource{}}
	reader.convoy = ConvoySource{Convoy: &convoy, Children: []BeadSource{}, Progress: &Progress{}}
	reader.workflow = WorkflowSource{WorkflowID: "wf-1", RootID: "root-1"}
	if _, err := service.ListConvoys(context.Background()); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	reader.active, reader.recent = failure, failure

	got, err := service.ListConvoys(context.Background())
	if err != nil {
		t.Fatalf("cached ListConvoys: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "cached-1" || !got.Degraded || !got.Stale {
		t.Fatalf("cached convoys = %#v, want stale last-confirmed data", got)
	}
}

func TestServiceListConvoysTreatsItemlessPartialPageWithoutProblemsAsFailed(t *testing.T) {
	reader := &serviceReader{
		active: Page[BeadSource]{Partial: true},
		recent: Page[BeadSource]{Items: []BeadSource{}},
	}
	service, err := NewService(reader)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = service.ListConvoys(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Code != "upstream_unavailable" || upstream.Detail == "" {
		t.Fatalf("error = %#v, want detailed upstream_unavailable", err)
	}
}

type serviceReader struct {
	active   Page[BeadSource]
	recent   Page[BeadSource]
	convoy   ConvoySource
	workflow WorkflowSource
}

func (r *serviceReader) ListConvoys(context.Context) Page[BeadSource] { return r.active }

func (r *serviceReader) ListRecentClosedConvoys(context.Context, int) Page[BeadSource] {
	return r.recent
}

func (r *serviceReader) GetConvoy(context.Context, string) (ConvoySource, error) {
	return r.convoy, nil
}

func (r *serviceReader) GetWorkflow(context.Context, string, string, string) (WorkflowSource, error) {
	return r.workflow, nil
}

func (*serviceReader) ListSessions(context.Context) Page[SessionSource] {
	return Page[SessionSource]{Items: []SessionSource{}}
}

func (*serviceReader) ListPending(context.Context) Page[PendingSource] {
	return Page[PendingSource]{Items: []PendingSource{}}
}

func (*serviceReader) ListOrders(context.Context) ([]OrderSource, error) { return nil, nil }

func (*serviceReader) CheckOrders(context.Context) ([]OrderCheckSource, error) { return nil, nil }

func (*serviceReader) ListOrderFeed(context.Context) Page[OrderFeedSource] {
	return Page[OrderFeedSource]{Items: []OrderFeedSource{}}
}

func (*serviceReader) ListOrderHistory(context.Context, string, string, int) ([]OrderRunSource, error) {
	return nil, nil
}

func (*serviceReader) GetOrderRunOutput(context.Context, string, string) (OrderRunOutput, error) {
	return OrderRunOutput{}, nil
}

var _ Reader = (*serviceReader)(nil)
