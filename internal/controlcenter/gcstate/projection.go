package gcstate

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

const recentClosedConvoyLimit = 20

// Service coordinates authoritative reads and retains only last-confirmed list
// DTOs so temporary Supervisor failures can be shown as stale, never as empty.
type Service struct {
	reader Reader

	mu          sync.Mutex
	convoyCache *ResourceList[ConvoySummary]
	orderCache  *ResourceList[OrderView]
}

// NewService constructs the read-through projection service.
func NewService(reader Reader) (*Service, error) {
	if reader == nil {
		return nil, fmt.Errorf("control center gcstate: reader is required")
	}
	return &Service{reader: reader}, nil
}

// ListConvoys projects active and bounded recent closed tracking convoys.
func (s *Service) ListConvoys(ctx context.Context) (ResourceList[ConvoySummary], error) {
	active := s.reader.ListConvoys(ctx)
	recent := s.reader.ListRecentClosedConvoys(ctx, recentClosedConvoyLimit)
	result := ResourceList[ConvoySummary]{Items: []ConvoySummary{}}
	result.Problems = append(result.Problems, active.Problems...)
	result.Problems = append(result.Problems, recent.Problems...)
	result.Degraded = active.Partial || recent.Partial || len(result.Problems) > 0
	if problem, failed := essentialPageFailure(active); failed {
		if cached, ok := s.staleConvoys(result.Problems); ok {
			return cached, nil
		}
		return ResourceList[ConvoySummary]{}, &UpstreamError{Code: problem.Code, Operation: "list convoys", Detail: problem.Detail}
	}

	sources := append([]BeadSource(nil), active.Items...)
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		if source.ID != "" {
			seen[source.ID] = true
		}
	}
	for _, source := range recent.Items {
		if source.ID != "" && seen[source.ID] {
			continue
		}
		sources = append(sources, source)
		if source.ID != "" {
			seen[source.ID] = true
		}
	}

	if len(sources) == 0 && len(result.Problems) > 0 {
		if cached, ok := s.staleConvoys(result.Problems); ok {
			return cached, nil
		}
		return result, nil
	}
	sessions := s.reader.ListSessions(ctx)
	pending := s.reader.ListPending(ctx)
	for _, listed := range sources {
		detail, err := s.projectConvoy(ctx, listed.ID, sessions, pending)
		if err != nil {
			problem := problemFromError(err, "convoy", listed.ID)
			fallback := ProjectConvoy(ConvoySource{Convoy: &listed, Problems: []Problem{problem}, Partial: true}, sessions, pending, nil)
			result.Items = append(result.Items, fallback.Convoy)
			result.Problems = append(result.Problems, problem)
			result.Degraded = true
			continue
		}
		result.Items = append(result.Items, detail.Convoy)
		if len(detail.Convoy.Problems) > 0 {
			result.Degraded = true
		}
	}
	s.cacheConvoys(result)
	return result, nil
}

// GetConvoy projects one authoritative convoy detail.
func (s *Service) GetConvoy(ctx context.Context, id string) (ConvoyDetail, error) {
	return s.projectConvoy(ctx, id, s.reader.ListSessions(ctx), s.reader.ListPending(ctx))
}

// ListBeads returns the tracked-member slice for one convoy.
func (s *Service) ListBeads(ctx context.Context, id string) (ResourceList[BeadView], error) {
	detail, err := s.GetConvoy(ctx, id)
	if err != nil {
		return ResourceList[BeadView]{}, err
	}
	return ResourceList[BeadView]{
		Items: detail.Beads, Degraded: len(detail.Convoy.Problems) > 0,
		Problems: append([]Problem(nil), detail.Convoy.Problems...),
	}, nil
}

// ListOrders projects definitions, checks, exact feed identities and bounded
// histories while retaining definitions whose individual histories fail.
func (s *Service) ListOrders(ctx context.Context) (ResourceList[OrderView], error) {
	definitions, err := s.reader.ListOrders(ctx)
	if err != nil {
		if cached, ok := s.staleOrders([]Problem{problemFromError(err, "orders", "")}); ok {
			return cached, nil
		}
		return ResourceList[OrderView]{}, err
	}
	checks, checkErr := s.reader.CheckOrders(ctx)
	feed := s.reader.ListOrderFeed(ctx)
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if definition.ScopedName != "" {
			names = append(names, definition.ScopedName)
		}
	}
	histories, historyProblems := s.loadOrderHistories(ctx, names, int(orderHistoryLimit))
	result := ProjectOrders(definitions, checks, feed, histories)
	if checkErr != nil {
		result.Degraded = true
		result.Problems = append(result.Problems, problemFromError(checkErr, "orders_check", ""))
	}
	for _, problem := range historyProblems {
		result.Degraded = true
		result.Problems = append(result.Problems, problem)
		for index := range result.Items {
			if result.Items[index].ScopedName == problem.ResourceID {
				result.Items[index].Problems = append(result.Items[index].Problems, problem)
			}
		}
	}
	s.cacheOrders(result)
	return result, nil
}

// ListOrderHistory projects one order's history with exact feed status joins.
func (s *Service) ListOrderHistory(ctx context.Context, scopedName, before string, limit int) (ResourceList[OrderRunView], error) {
	runs, err := s.reader.ListOrderHistory(ctx, scopedName, before, limit)
	if err != nil {
		return ResourceList[OrderRunView]{}, err
	}
	feed := s.reader.ListOrderFeed(ctx)
	result := ResourceList[OrderRunView]{Items: []OrderRunView{}, Problems: append([]Problem(nil), feed.Problems...)}
	result.Degraded = feed.Partial || len(feed.Problems) > 0
	feedByIdentity := map[string]OrderFeedSource{}
	for _, item := range feed.Items {
		if item.StoreRef == "" || item.BeadID == "" {
			result.Degraded = true
			result.Problems = append(result.Problems, Problem{Code: "missing_order_feed_identity", Source: "orders_feed", Detail: "order feed rows require store_ref and bead_id", Retryable: true})
			continue
		}
		feedByIdentity[orderRunKey(item.StoreRef, item.BeadID)] = item
	}
	for _, source := range runs {
		run := projectOrderRun(source)
		if run.StoreRef == "" || run.BeadID == "" {
			run.Problems = append(run.Problems, missingProblem("missing_order_run_identity", "orders_history", "order history rows require store_ref and bead_id", run.BeadID))
		}
		if exact, ok := feedByIdentity[orderRunKey(run.StoreRef, run.BeadID)]; ok && validOrderStatus(exact.Status) && run.StoreRef != "" && run.BeadID != "" {
			run.Status = exact.Status
		} else {
			run.Status = "unknown"
			run.Problems = append(run.Problems, Problem{Code: "unknown_order_status", Source: "orders_feed", Detail: "no exact feed row identifies this run status", ResourceID: run.BeadID, Retryable: true})
		}
		result.Items = append(result.Items, run)
	}
	if limit > 0 && len(runs) == limit {
		result.Problems = append(result.Problems, Problem{Code: "history_pagination_limit", Source: "orders_history", Detail: "equal timestamps at the upstream history boundary may be skipped"})
	}
	return result, nil
}

// GetOrderRunOutput loads one run's output only on explicit request.
func (s *Service) GetOrderRunOutput(ctx context.Context, beadID, storeRef string) (OrderRunOutput, error) {
	return s.reader.GetOrderRunOutput(ctx, beadID, storeRef)
}

func (s *Service) projectConvoy(ctx context.Context, id string, sessions Page[SessionSource], pending Page[PendingSource]) (ConvoyDetail, error) {
	source, err := s.reader.GetConvoy(ctx, id)
	if err != nil {
		return ConvoyDetail{}, err
	}
	var workflow *WorkflowSource
	if source.Convoy != nil {
		workflowID := source.Convoy.Metadata["workflow_root"]
		if workflowID != "" {
			scopeKind := source.Convoy.Metadata[beadmeta.ScopeKindMetadataKey]
			scopeRef := source.Convoy.Metadata[beadmeta.ScopeRefMetadataKey]
			loaded, workflowErr := s.reader.GetWorkflow(ctx, workflowID, scopeKind, scopeRef)
			if workflowErr != nil {
				source.Partial = true
				source.Problems = append(source.Problems, problemFromError(workflowErr, "workflow", workflowID))
			} else {
				workflow = &loaded
			}
		}
	}
	return ProjectConvoy(source, sessions, pending, workflow), nil
}

func (s *Service) loadOrderHistories(ctx context.Context, names []string, limit int) (map[string][]OrderRunSource, []Problem) {
	if batch, ok := s.reader.(interface {
		ListOrderHistories(context.Context, []string, int) (map[string][]OrderRunSource, []Problem)
	}); ok {
		return batch.ListOrderHistories(ctx, names, limit)
	}
	result := make(map[string][]OrderRunSource, len(names))
	problems := []Problem{}
	jobs := make(chan string)
	var mu sync.Mutex
	var workers sync.WaitGroup
	for index := 0; index < historyConcurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for name := range jobs {
				runs, err := s.reader.ListOrderHistory(ctx, name, "", limit)
				mu.Lock()
				if err != nil {
					problems = append(problems, problemFromError(err, "orders_history", name))
				} else {
					result[name] = runs
				}
				mu.Unlock()
			}
		}()
	}
	for _, name := range names {
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

func (s *Service) staleConvoys(problems []Problem) (ResourceList[ConvoySummary], bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.convoyCache == nil {
		return ResourceList[ConvoySummary]{}, false
	}
	result := cloneResourceList(*s.convoyCache)
	result.Degraded, result.Stale = true, true
	result.Problems = append(result.Problems, problems...)
	return result, true
}

func essentialPageFailure(page Page[BeadSource]) (Problem, bool) {
	if len(page.Items) > 0 {
		return Problem{}, false
	}
	for _, problem := range page.Problems {
		switch problem.Code {
		case "upstream_unavailable", "upstream_protocol", "upstream_http":
			return problem, true
		}
	}
	return Problem{}, false
}

func (s *Service) cacheConvoys(result ResourceList[ConvoySummary]) {
	s.mu.Lock()
	cached := cloneResourceList(result)
	s.convoyCache = &cached
	s.mu.Unlock()
}

func (s *Service) staleOrders(problems []Problem) (ResourceList[OrderView], bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.orderCache == nil {
		return ResourceList[OrderView]{}, false
	}
	result := cloneResourceList(*s.orderCache)
	result.Degraded, result.Stale = true, true
	result.Problems = append(result.Problems, problems...)
	return result, true
}

func (s *Service) cacheOrders(result ResourceList[OrderView]) {
	s.mu.Lock()
	cached := cloneResourceList(result)
	s.orderCache = &cached
	s.mu.Unlock()
}

func cloneResourceList[T any](source ResourceList[T]) ResourceList[T] {
	return ResourceList[T]{
		Items: append([]T(nil), source.Items...), Degraded: source.Degraded, Stale: source.Stale,
		Problems: append([]Problem(nil), source.Problems...),
	}
}

// ProjectConvoy derives a detail projection only from confirmed Supervisor
// facts and explicit partial-read evidence.
func ProjectConvoy(source ConvoySource, sessions Page[SessionSource], pending Page[PendingSource], workflow *WorkflowSource) ConvoyDetail {
	problems := appendProblems(nil, source.Problems, source.Partial, "convoy", resourceID(source.Convoy))
	problems = appendProblems(problems, sessions.Problems, sessions.Partial, "sessions", resourceID(source.Convoy))
	problems = appendProblems(problems, pending.Problems, pending.Partial, "pending", resourceID(source.Convoy))

	detail := ConvoyDetail{Beads: []BeadView{}, Sessions: []SessionSummary{}}
	if source.Convoy == nil {
		problems = append(problems, missingProblem("missing_convoy", "convoy", "Supervisor response omitted the convoy", ""))
		detail.Convoy.Problems = problems
		return detail
	}

	convoy := source.Convoy
	detail.Convoy.ID = convoy.ID
	detail.Convoy.Title = convoy.Title
	detail.Convoy.Status = convoy.Status
	detail.Convoy.UpdatedAt = convoy.UpdatedAt
	for field, value := range map[string]string{"id": convoy.ID, "title": convoy.Title, "status": convoy.Status} {
		if value == "" {
			problems = append(problems, missingProblem("missing_required_field", "convoy", fmt.Sprintf("convoy %s is required", field), convoy.ID))
		}
	}

	switch {
	case source.Progress != nil:
		progress := *source.Progress
		detail.Convoy.Progress = &progress
	case !source.Partial && source.Children != nil:
		progress := Progress{Total: len(source.Children)}
		for _, child := range source.Children {
			if terminalStatus(child.Status) {
				progress.Closed++
			}
		}
		detail.Convoy.Progress = &progress
	default:
		problems = append(problems, missingProblem("missing_progress", "convoy", "Supervisor response omitted progress and tracked children", convoy.ID))
	}

	metadata := convoy.Metadata
	path, parent, branch := metadata["work_dir"], metadata["parent_commit"], metadata["feature_branch"]
	if path == "" || parent == "" || branch == "" {
		problems = append(problems, missingProblem("missing_worktree", "convoy", "tracking convoy work_dir, parent_commit, and feature_branch are required", convoy.ID))
	} else {
		detail.Convoy.Worktree = &WorktreeRef{Path: path, ParentCommit: parent, FeatureBranch: branch}
	}

	workflowID := metadata["workflow_root"]
	if workflowID == "" || workflow == nil || workflow.WorkflowID != workflowID || workflow.RootID == "" {
		problems = append(problems, missingProblem("missing_workflow", "workflow", "tracking convoy workflow_root must resolve to a workflow snapshot", convoy.ID))
	} else {
		detail.Workflow = &WorkflowRef{
			WorkflowID: workflow.WorkflowID, RootID: workflow.RootID, RootStoreRef: workflow.RootStoreRef,
			ScopeKind: workflow.ScopeKind, ScopeRef: workflow.ScopeRef, Partial: workflow.Partial,
		}
		if workflow.Partial {
			problems = append(problems, Problem{Code: "upstream_partial", Source: "workflow", Detail: "workflow snapshot is partial", ResourceID: convoy.ID, Retryable: true})
		}
	}

	for _, child := range source.Children {
		view := projectBead(child)
		detail.Beads = append(detail.Beads, view)
		for field, value := range map[string]string{"id": view.ID, "title": view.Title, "status": view.Status} {
			if value == "" {
				problems = append(problems, missingProblem("missing_required_field", "bead", fmt.Sprintf("tracked bead %s is required", field), view.ID))
			}
		}
	}

	pendingSessions := make(map[string]bool, len(pending.Items))
	for _, item := range pending.Items {
		if item.SessionID != "" {
			pendingSessions[item.SessionID] = true
		}
	}
	projectedSessions := make([]SessionSummary, 0, len(sessions.Items))
	for _, session := range sessions.Items {
		summary := projectSession(session)
		summary.NeedsInput = pendingSessions[summary.ID]
		projectedSessions = append(projectedSessions, summary)
	}
	matched := matchedSessions(detail.Beads, projectedSessions)
	detail.Sessions = matched
	detail.Convoy.Stage = projectStage(detail.Beads, detail.Convoy.Progress)
	detail.Convoy.Signals = composeSignals(detail.Beads, projectedSessions, !sessions.Partial)
	detail.Convoy.Problems = problems
	return detail
}

// ProjectOrders joins order definitions, checks, feed rows and history without
// inferring a run status when the exact feed identity is absent.
func ProjectOrders(definitions []OrderSource, checks []OrderCheckSource, feed Page[OrderFeedSource], histories map[string][]OrderRunSource) ResourceList[OrderView] {
	result := ResourceList[OrderView]{Items: []OrderView{}, Problems: append([]Problem(nil), feed.Problems...)}
	result.Degraded = feed.Partial || len(feed.Problems) > 0
	if feed.Partial && len(feed.Problems) == 0 {
		result.Problems = append(result.Problems, Problem{Code: "upstream_partial", Source: "orders_feed", Detail: "order feed is partial", Retryable: true})
	}
	checkByName := make(map[string]OrderCheckSource, len(checks))
	for _, check := range checks {
		checkByName[check.ScopedName] = check
	}
	feedByIdentity := make(map[string]OrderFeedSource, len(feed.Items))
	for _, item := range feed.Items {
		if item.StoreRef == "" || item.BeadID == "" {
			result.Degraded = true
			result.Problems = append(result.Problems, Problem{Code: "missing_order_feed_identity", Source: "orders_feed", Detail: "order feed rows require store_ref and bead_id", Retryable: true})
			continue
		}
		feedByIdentity[orderRunKey(item.StoreRef, item.BeadID)] = item
	}
	for _, definition := range definitions {
		view := OrderView{
			Name: definition.Name, ScopedName: definition.ScopedName, Type: definition.Type,
			Enabled: definition.Enabled, Problems: []Problem{},
		}
		if definition.Name == "" || definition.ScopedName == "" || definition.Type == "" {
			view.Problems = append(view.Problems, missingProblem("missing_order_identity", "orders", "order name, scoped_name, and type are required", definition.ScopedName))
		}
		runs := histories[definition.ScopedName]
		if len(runs) == 0 {
			if check, ok := checkByName[definition.ScopedName]; ok && check.LastRun != "" {
				runs = []OrderRunSource{{CreatedAt: check.LastRun}}
			}
		}
		if len(runs) > 0 {
			run := projectOrderRun(runs[0])
			if run.StoreRef == "" || run.BeadID == "" {
				run.Problems = append(run.Problems, missingProblem("missing_order_run_identity", "orders_history", "order history rows require store_ref and bead_id", run.BeadID))
			}
			if run.StoreRef != "" && run.BeadID != "" {
				if exact, ok := feedByIdentity[orderRunKey(run.StoreRef, run.BeadID)]; ok && validOrderStatus(exact.Status) {
					run.Status = exact.Status
				}
			}
			if run.Status == "" {
				run.Status = "unknown"
				run.Problems = append(run.Problems, Problem{Code: "unknown_order_status", Source: "orders_feed", Detail: "no exact feed row identifies this run status", ResourceID: run.BeadID, Retryable: true})
			}
			view.LastRun = &run
		}
		result.Items = append(result.Items, view)
	}
	return result
}

func projectBead(source BeadSource) BeadView {
	metadata := cloneStrings(source.Metadata)
	stepRef := metadata[beadmeta.StepRefMetadataKey]
	return BeadView{
		ID: source.ID, Title: source.Title, Type: source.Type, Status: source.Status, Assignee: source.Assignee,
		LogicalID: source.LogicalID, StepRef: stepRef, Metadata: metadata, Needs: append([]string(nil), source.Needs...),
		Blocked: source.Blocked, UpdatedAt: source.UpdatedAt,
	}
}

func projectSession(source SessionSource) SessionSummary {
	return SessionSummary{
		ID: source.ID, SessionName: source.SessionName, Template: source.Template, State: source.State,
		Activity: source.Activity, ActiveBead: source.ActiveBead, Running: source.Running, Attached: source.Attached,
		Alias: source.Alias,
	}
}

func projectStage(beads []BeadView, progress *Progress) WorkflowStage {
	stage := WorkflowStage{Active: []string{}, Waiting: []string{}}
	for _, bead := range beads {
		label := bead.StepRef
		if label == "" {
			label = bead.Title
		}
		if bead.Status == "in_progress" {
			stage.Active = append(stage.Active, label)
		}
		if !terminalStatus(bead.Status) && (bead.Blocked || len(bead.Needs) > 0) {
			stage.Waiting = append(stage.Waiting, label)
		}
	}
	stage.Complete = progress != nil && progress.Total > 0 && progress.Closed == progress.Total
	return stage
}

func matchedSessions(beads []BeadView, sessions []SessionSummary) []SessionSummary {
	matched := []SessionSummary{}
	seen := map[string]bool{}
	for _, bead := range beads {
		if session := matchSession(bead, sessions); session != nil && !seen[session.ID] {
			matched = append(matched, *session)
			seen[session.ID] = true
		}
	}
	return matched
}

func projectOrderRun(source OrderRunSource) OrderRunView {
	return OrderRunView{
		BeadID: source.BeadID, StoreRef: source.StoreRef, CreatedAt: source.CreatedAt,
		DurationMS: source.DurationMS, ExitCode: source.ExitCode, HasOutput: source.HasOutput, Problems: []Problem{},
	}
}

func appendProblems(dst, source []Problem, partial bool, sourceName, resourceID string) []Problem {
	dst = append(dst, source...)
	if partial && len(source) == 0 {
		dst = append(dst, Problem{Code: "upstream_partial", Source: sourceName, Detail: sourceName + " response is partial", ResourceID: resourceID, Retryable: true})
	}
	return dst
}

func missingProblem(code, source, detail, resourceID string) Problem {
	return Problem{Code: code, Source: source, Detail: detail, ResourceID: resourceID}
}

func resourceID(source *BeadSource) string {
	if source == nil {
		return ""
	}
	return source.ID
}

func terminalStatus(status string) bool { return status == "closed" || status == "tombstone" }

func validOrderStatus(status string) bool {
	return status == "active" || status == "completed" || status == "failed"
}

func orderRunKey(storeRef, beadID string) string { return storeRef + "\x00" + beadID }

func cloneStrings(source map[string]string) map[string]string {
	if source == nil {
		return map[string]string{}
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
