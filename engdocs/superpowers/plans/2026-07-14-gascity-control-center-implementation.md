# GasCity Control Center Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a standalone loopback-only GasCity Control Center that projects
one configured city's live convoys, orders, Mayor session, and mail, shows local
worktree state, and provides explicit terminal, assistant, runtime, and action
surfaces through one adaptive React cockpit.

**Architecture:** `cmd/gc-control` embeds a React/Vite application and delegates
to fork-owned Go packages under `internal/controlcenter`. The Go server owns the
typed Huma API, generated Supervisor client adapter, bounded jobs, Git reads,
tmux/PTY lifecycle, native terminal launch, assistant calls, and pack commands.
GasCity Supervisor, beads, events, Git, tmux, the OS process table, and
schema-versioned pack output remain authoritative; Control Center keeps no
operational database or status files.

**Tech Stack:** Go, Huma/OpenAPI, the repository's generated Go Supervisor
client, React 19, TypeScript, Vite, Vitest, Testing Library, ESLint, Stylelint,
Playwright, `@xterm/xterm`, `@xterm/addon-fit`, Gorilla WebSocket,
`github.com/creack/pty`, tmux, and argument-safe Git/`gc`/`glab` subprocesses.

## Global Constraints

- Execute in `/Volumes/DATA/repos/personal/gascity-control-center` on branch
  `codex/gc-control-center`; do not modify the user's detached dirty checkout at
  `/Volumes/DATA/repos/personal/gascity`.
- Track implementation in the TaxDome HQ Beads epic `ga-8mr`. Claim one child
  before editing, close it only after its focused gates pass, and locally commit
  the Dolt state after each Beads update.
- Read the repository `AGENTS.md`, `TESTING.md`,
  `engdocs/architecture/api-control-plane.md`, and
  `engdocs/contributors/huma-usage.md` before API implementation.
- Production code follows a strict red/green/refactor cycle. Every production
  behavior below starts with the listed failing test and its observed failure.
- Do not add hardcoded agent roles, copied domain state, PID/status files, raw
  shell fragments from HTTP input, bare `tmux kill-server`, a city switcher,
  Storybook, or a development component-gallery route.
- Only explicit **Open terminal** may create a tmux session. Runtime start/stop
  and tmux open/close remain independent in code and tests.
- Mayor must resolve and reuse the existing configured named session. Control
  Center never creates a second Mayor or falls back to another agent.
- Mail is read-only in this release. Opening a message must not mark it read,
  and no send, reply, archive, delete, or read-state mutation is exposed.
- All feature UI imports shared controls through `@/ui`. Feature code may not
  declare raw `button`, `input`, `select`, or `textarea` elements or use raw
  palette values.
- Regenerate and commit every affected OpenAPI/client artifact. Never hand-edit
  generated files.
- Run the focused checks after each green step, the slice checks before each
  slice commit, and the complete quality gate in Task 12 before completion.
- Commit messages below are the required slice boundaries. Do not combine
  unrelated slices into one commit.

## Dependency and Delivery Map

| Order | Bead | Deliverable | Hard dependency |
| --- | --- | --- | --- |
| 0 | `ga-8mr.12` | Establish a green baseline | none |
| 1 | `ga-8mr.1` | Explicit session `work_dir` | baseline |
| 2 | `ga-8mr.2` | Go/React application foundation | Task 1 |
| 3 | `ga-8mr.13` | Internal design system | Task 2 |
| 4 | `ga-8mr.3` | Live convoys, beads, orders, progress, events | Task 3 |
| 4A | `ga-8mr.15` | Existing Mayor session workspace | Tasks 3-4 |
| 4B | `ga-8mr.16` | Read-only mail and notifications | Tasks 3-4 |
| 5 | `ga-8mr.4` | Bounded cancellable job engine | Task 4 |
| 6 | `ga-8mr.5` | Canonical worktree and local diff | Tasks 4-5 |
| 7 | `ga-8mr.6` | Explicit embedded/native tmux terminal | Tasks 5-6 |
| 8 | `ga-8mr.7` | Persistent convoy assistant integration | Tasks 1, 4, 6 |
| 9 | `ga-8mr.8` | Runtime `env-*` integration | Tasks 5-6; TaxDome runtime contract |
| 10 | `ga-8mr.9` | Action stubs, order detail, read-only MR | Tasks 4-6 |
| 11 | `ga-8mr.10` | Adaptive integrated cockpit | Tasks 3-10, 4A-4B |
| 12 | `ga-8mr.11` | Packaging, docs, acceptance | Tasks 1-11, 4A-4B |

The runtime contract is implemented on TaxDome branch
`codex/resumable-feature-environments` at bead `ga-xep`. Before Task 9, verify
that the configured city exposes those commands. The convoy assistant template
is intentionally external; Task 8 may merge with a truthful unavailable state,
but its live acceptance remains blocked until the configured template exists.

---

## Task 0: Restore a Trustworthy Test Baseline (`ga-8mr.12`)

**Purpose:** Feature work must not hide or normalize the current compact-Dolt
test failures and broad-suite timeouts.

**Files:**

- Modify only files proven to cause the baseline failure.
- Update Bead `ga-8mr.12` with the failing command, root cause, and verified
  resolution.

- [ ] **Step 1: Reproduce the baseline failure from the clean feature branch.**

Run:

```bash
make test-fast-parallel
```

Record the failing shard names and first causal error. The known observation is
`examples/bd/dolt` reporting `commit count probe failed`, followed by timeout;
do not assume it is still the only cause.

- [ ] **Step 2: Isolate the smallest deterministic failing command.**

Use `superpowers:systematic-debugging`, then run the exact package or script
with `-count=1`. Preserve the full failing output in `ga-8mr.12` rather than in a
new repository status file.

- [ ] **Step 3: Add a regression test before changing the cause.**

The regression belongs beside the failing implementation and must fail for the
same reason as Step 1. If the failure is environmental rather than a product
defect, add a deterministic preflight assertion that fails quickly with the
missing prerequisite instead of timing out.

- [ ] **Step 4: Implement the smallest baseline correction and rerun the
regression.**

Run the new focused test twice with `-count=1`, then run:

```bash
make test-fast-parallel
```

Expected: the causal failure and any runaway descendants are eliminated. If the
full suite then exposes independent pre-existing defects, record their exact
tests in a separate Bead that blocks Task 12. Task 1 may start only after the
operator approves a bounded focused-test exception and `ga-8mr.12` records it.

- [ ] **Step 5: Commit and push the isolated baseline correction.**

```bash
git add -A
git commit -m "test: restore deterministic fast-suite baseline"
git pull --rebase
git push
```

Close `ga-8mr.12` after the full fast suite exits successfully or after the
approved focused-test exception is recorded and every residual broad-suite
failure is tracked as a dependency of final acceptance.

---

## Task 1: Add Explicit Worktree Binding to Manual Sessions (`ga-8mr.1`)

**Files:**

- Modify: `internal/api/huma_types_sessions.go`
- Modify: `internal/api/session_create_agent.go`
- Modify: `internal/api/huma_handlers_sessions_command.go`
- Modify: `internal/api/handler_sessions.go`
- Test: `internal/api/session_create_agent_test.go`
- Test: `internal/api/handler_sessions_test.go`
- Generate: `internal/api/openapi.json`
- Generate: `docs/reference/schema/openapi.json`
- Generate: `docs/reference/schema/openapi.txt`
- Generate: `internal/api/genclient/client_gen.go`

- [ ] **Step 1: Write resolver tests for the optional override.**

Add table-driven tests proving that an omitted value keeps the existing
template-derived directory, an existing absolute directory is canonicalized,
and relative, missing, and regular-file paths return errors.

```go
func TestResolveRequestedSessionWorkDir(t *testing.T) {
    dir := t.TempDir()
    link := filepath.Join(t.TempDir(), "worktree")
    require.NoError(t, os.Symlink(dir, link))

    got, err := resolveRequestedSessionWorkDir(link)
    require.NoError(t, err)
    require.Equal(t, dir, got)
}
```

Run and observe failure because the helper does not exist:

```bash
go test ./internal/api -run 'TestResolveRequestedSessionWorkDir' -count=1
```

- [ ] **Step 2: Write HTTP contract tests before adding the request field.**

Extend the existing `newSessionFakeState`/`newTestCityHandlerWith` harness. POST
an agent session with `work_dir`, await the async create result, then GET the
session and assert the canonical path. Add 4xx cases for invalid paths.

```go
body := fmt.Sprintf(
    `{"kind":"agent","name":%q,"alias":"cc-workdir","work_dir":%q,"async":true}`,
    template, workDir,
)
req := newPostRequest(t, cityURL(fs, "/sessions"), body)
```

Run and observe that `work_dir` is ignored or absent:

```bash
go test ./internal/api -run 'TestHandleSessionCreate.*WorkDir' -count=1
```

- [ ] **Step 3: Implement generic path validation and request plumbing.**

Add the optional field and change the resolver signature without adding convoy
or TaxDome knowledge:

```go
type sessionCreateBody struct {
    // existing fields remain unchanged
    WorkDir string `json:"work_dir,omitempty" doc:"Existing absolute working directory for the session."`
}

func (s *Server) resolveAgentCreateContext(
    template, alias, requestedWorkDir string,
) (agentCreateContext, error)
```

`resolveRequestedSessionWorkDir` must trim, require `filepath.IsAbs`, call
`filepath.EvalSymlinks`, call `os.Stat`, require `info.IsDir()`, and return the
clean canonical path. In `humaHandleSessionCreate`, pass `body.WorkDir`; map
validation failures to `huma.Error422UnprocessableEntity`. When empty, retain
`s.resolveSessionWorkDir(agentCfg, identity)` exactly.

- [ ] **Step 4: Expose the effective path in session reads.**

Add and populate:

```go
type sessionResponse struct {
    // existing fields remain unchanged
    WorkDir string `json:"work_dir,omitempty"`
}

// inside sessionToResponse
WorkDir: info.WorkDir,
```

Run:

```bash
go test ./internal/api -run 'TestResolveRequestedSessionWorkDir|TestHandleSessionCreate.*WorkDir|TestSessionToResponse' -count=1
```

- [ ] **Step 5: Regenerate and verify both API clients.**

```bash
go run ./cmd/genspec
go generate ./internal/api/genclient
make dashboard-check
go test ./internal/api -count=1
go vet ./internal/api/...
git diff --exit-code --check
```

Confirm `work_dir` appears in the generated create body and session response.

- [ ] **Step 6: Commit and push the isolated API capability.**

```bash
git add internal/api docs/reference/schema cmd/gc/dashboard/web/dist
git commit -m "api: allow explicit session work directories"
git pull --rebase
git push
```

---

## Task 2: Scaffold the Standalone Go and React Application (`ga-8mr.2`)

**Files:**

- Create: `cmd/gc-control/main.go`
- Create: `cmd/gc-control/embed.go`
- Create: `cmd/gc-control/main_test.go`
- Generate: `cmd/gc-control/testenv_import_test.go`
- Create: `internal/controlcenter/config.go`
- Create: `internal/controlcenter/config_test.go`
- Create: `internal/controlcenter/app.go`
- Create: `internal/controlcenter/app_test.go`
- Generate: `internal/controlcenter/testenv_import_test.go`
- Create: `internal/controlcenter/api/server.go`
- Create: `internal/controlcenter/api/health.go`
- Create: `internal/controlcenter/api/health_test.go`
- Create: `internal/controlcenter/api/openapi_sync_test.go`
- Generate: `internal/controlcenter/api/testenv_import_test.go`
- Create: `cmd/gencontrolspec/main.go`
- Generate: `internal/controlcenter/openapi.json`
- Create: `cmd/gc-control/web/{.gitignore,package.json,package-lock.json,tsconfig.json,vite.config.ts,openapi-ts.config.ts,index.html}`
- Create: `cmd/gc-control/web/src/{main.tsx,vite-env.d.ts,app/App.tsx,app/App.test.tsx,lib/api.ts,test/setup.ts,styles/global.css}`
- Generate and commit: `cmd/gc-control/web/dist/*`
- Modify: `Makefile`

- [ ] **Step 1: Add failing configuration tests.**

Use the exact application contract:

```go
type Config struct {
    BindAddress       string
    SupervisorURL     string
    CityName          string
    GCExecutable      string
    PackName          string
    MayorIdentity     string
    AssistantTemplate string
    NativeTerminalApp string
}
```

Tests must accept `127.0.0.1:0`, default `NativeTerminalApp` to `Terminal`,
require city/pack/Mayor-identity/assistant values, validate the Supervisor URL,
resolve a blank `GCExecutable` with `exec.LookPath("gc")`, and reject
`localhost`, IPv6, `0.0.0.0`, LAN IPs, and all other hosts. Normalize only the
literal `127.0.0.1:<numeric-port>` contract, and inject path/address resolvers
in tests so DNS and PATH never depend on the developer machine.

```bash
go test ./internal/controlcenter -run 'TestConfig' -count=1
```

- [ ] **Step 2: Add failing server and static fallback tests.**

Define dependency injection before implementation:

```go
type Dependencies struct {
    StaticFS       fs.FS
    SupervisorPing func(context.Context) error
}

func NewApp(cfg Config, deps Dependencies) (*App, error)
func (a *App) Handler() http.Handler
func (a *App) Run(ctx context.Context) error
```

Assert `GET /api/v1/health` returns:

```json
{"schema_version":1,"status":"ok","city":"taxdome","supervisor_reachable":true}
```

Assert unknown SPA routes return embedded `index.html`, while unknown `/api/`,
`/ws/`, `/assets/`, and `/openapi.json/child` routes return 404. Add a Host
allowlist test: requests whose `Host` is not literal `127.0.0.1` with an
optional numeric port are rejected before reaching the application. Add an
OpenAPI sync test that compares the live registered document with the committed
`internal/controlcenter/openapi.json`.

- [ ] **Step 3: Implement the minimal Huma server and graceful runner.**

Use `humago.New` with `huma.DefaultConfig`; clear `SchemasPath` and `DocsPath`
and set `CreateHooks=nil`. Register versioned operations under `/api/v1` and
expose the same API's OpenAPI document at `/openapi.json`. `App.Run` must use an
`http.Server`, shut down with a bounded fresh background context after run
context cancellation, and return non-`http.ErrServerClosed` failures with
operation context.

- [ ] **Step 4: Add a failing React boot test.**

Install and lock React 19, React DOM 19, Vite, Vitest, jsdom, Testing Library,
`openapi-fetch`, `openapi-typescript`, `@hey-api/openapi-ts`, and
`@hey-api/client-fetch` on the repository's Vite 6/Vitest 4 toolchain. Include
the React Vite plugin, React type packages, jsdom setup, and the repository Node
engine range. Test the loading state, the rendered city/health state, and an
explicit connection-error state using a mocked typed client.

```tsx
render(<App api={fakeAPI({ city: "taxdome", supervisor_reachable: true })} />);
expect(await screen.findByText("taxdome")).toBeVisible();
expect(screen.getByText("Supervisor connected")).toBeVisible();
```

Run and observe the missing app failure:

```bash
cd cmd/gc-control/web && npm test -- --run src/app/App.test.tsx
```

- [ ] **Step 5: Implement the shell and typed client generation.**

`cmd/gencontrolspec` registers the API on a fresh mux with a no-op Supervisor
ping and writes a stable, formatted `internal/controlcenter/openapi.json`; it
must not require valid runtime config, static assets, `gc`, or a live
Supervisor. The frontend `gen` script reads
`../../../internal/controlcenter/openapi.json` and generates typed REST plus SSE
clients; Vite development proxies `/api`, `/ws`, and `/openapi.json` to the Go
address. Do not expose filesystem paths, Mayor identity, or command
configuration in an HTML bootstrap object. Run
`go run scripts/add-testenv-import.go` for every new tested Go package.

- [ ] **Step 6: Add build targets and verify a single embedded binary.**

Add the targets to `.PHONY`, extend `clean` for `bin/gc-control`, keep `dist/`
tracked so a clean Go checkout compiles, and add:

```make
control-center-web-install:
	cd cmd/gc-control/web && npm ci --silent

control-center-gen: control-center-web-install
	go run ./cmd/gencontrolspec
	cd cmd/gc-control/web && npm run gen

control-center-build: control-center-gen
	cd cmd/gc-control/web && npm run build
	go build -o $(BUILD_DIR)/gc-control ./cmd/gc-control

control-center-test: control-center-gen
	$(TEST_ENV) go test ./internal/controlcenter/... ./cmd/gc-control/...
	cd cmd/gc-control/web && npm test

control-center-check: control-center-build control-center-test
	cd cmd/gc-control/web && npm run typecheck
```

Run:

```bash
make control-center-check
go test ./internal/testenv -run TestRequiresDedicatedTestenvImportFile -count=1
git diff --exit-code --check
```

- [ ] **Step 7: Commit and push the runnable foundation.**

```bash
git add cmd/gc-control cmd/gencontrolspec internal/controlcenter Makefile
git commit -m "feat: scaffold GasCity Control Center"
git pull --rebase
git push
```

---

## Task 3: Establish the Internal Design System (`ga-8mr.13`)

**Files:**

- Create: `cmd/gc-control/web/src/ui/{tokens.css,themes.css,typography.css,icons.ts,index.ts,README.md}`
- Create: `cmd/gc-control/web/src/ui/primitives/{Button,IconButton,Input,Select,Textarea,Text,Stack,Grid}.tsx`
- Create: matching `*.test.tsx` and `*.css` files
- Create: `cmd/gc-control/web/src/ui/components/{Badge,StatusSignal,Progress,Tabs,Tooltip,Dialog,Panel,EmptyState,Skeleton,Spinner}.tsx`
- Create: matching `*.test.tsx` and `*.css` files
- Create: `cmd/gc-control/web/src/ui/patterns/{ActionBar,DetailHeader,ToolFrame}.tsx`
- Create: matching `*.test.tsx` and `*.css` files
- Create: `cmd/gc-control/web/scripts/check-ui-boundaries.mjs`
- Create: `cmd/gc-control/web/eslint.config.js`
- Create: `cmd/gc-control/web/stylelint.config.mjs`
- Modify: `cmd/gc-control/web/package.json`

- [ ] **Step 1: Write failing token and theme contract tests.**

Parse the CSS files and assert that both themes assign every semantic token.
The required namespaces are `--cc-color-*`, `--cc-space-*`, `--cc-font-*`,
`--cc-size-*`, `--cc-radius-*`, `--cc-shadow-*`, `--cc-motion-*`, and
`--cc-z-*`. Theme tests must assert the same key set for light and dark modes.

- [ ] **Step 2: Write failing architecture-boundary fixtures.**

Add fixtures proving that feature code fails lint for `@/ui/components/Badge`,
raw `button`/`input`/`select`/`textarea`, raw hex/rgb/hsl values, and non-token
spacing. The same elements are allowed inside `src/ui` implementations.

The enforcement scripts must exit non-zero for each bad fixture and zero for:

```tsx
import { Button, StatusSignal } from "@/ui";

export function Example() {
  return <Button variant="primary">Run</Button>;
}
```

- [ ] **Step 3: Implement tokens, public imports, and lint boundaries.**

Configure the `@` alias in Vite and TypeScript. ESLint's feature-code override
uses `no-restricted-imports` for `@/ui/*` and `no-restricted-syntax` JSX
selectors for the raw interactive elements. Stylelint rejects raw color
functions/literals and requires variables for spacing, radii, shadows, type
sizes, and motion outside `tokens.css` and `themes.css`.

- [ ] **Step 4: Implement primitives test-first.**

Every primitive exposes semantic union variants and forwards native accessible
props without a color or arbitrary style variant:

```ts
export type ButtonVariant = "primary" | "secondary" | "danger" | "quiet";
export type ButtonSize = "compact" | "regular";
```

For each primitive, first add tests for accessible name, keyboard behavior,
disabled state, visible focus class, ref forwarding, and light/dark token usage;
then add the minimum component implementation.

- [ ] **Step 5: Implement components and patterns test-first.**

`StatusSignal` always renders an icon marked decorative plus visible label text;
`Progress` exposes `aria-valuenow/min/max`; `Tabs` follows roving keyboard
navigation; `Dialog` restores focus; Tooltip is not the only accessible name.

```ts
export type StatusTone =
  | "neutral"
  | "info"
  | "success"
  | "warning"
  | "danger";
```

`ActionBar`, `DetailHeader`, and `ToolFrame` remain domain-neutral and accept
composed children rather than convoy objects. Their layout CSS uses fluid
`minmax()`/`clamp()` bounds, wrapping, and content-derived reflow thresholds;
it never switches on a named device class or display diagonal.

- [ ] **Step 6: Document every public export.**

`src/ui/README.md` must define the `@/ui` boundary, semantic-token extension
process, variant naming rule, accessibility contract, and one compiling usage
example for every exported primitive, component, and pattern. Add a test that
compares the barrel exports with documented export names.

- [ ] **Step 7: Make the complete UI contract one command.**

Add:

```json
{
  "scripts": {
    "lint": "eslint . && node scripts/check-ui-boundaries.mjs",
    "stylelint": "stylelint 'src/**/*.css'",
    "check": "npm run typecheck && npm test && npm run lint && npm run stylelint && npm run build"
  }
}
```

Run:

```bash
cd cmd/gc-control/web
npm run check
```

- [ ] **Step 8: Commit and push the design-system boundary.**

```bash
git add cmd/gc-control/web
git commit -m "feat: add Control Center design system"
git pull --rebase
git push
```

Responsive screenshots use real cockpit compositions and are completed in
Task 11; no separate component gallery is created here.

---

## Task 4: Project Live Convoys, Beads, Orders, and Progress (`ga-8mr.3`)

**Files:**

- Create: `internal/controlcenter/gcstate/{client.go,projection.go,signals.go,events.go,types.go}`
- Create: matching `*_test.go` and `testdata/*.json`
- Create: `internal/controlcenter/api/{convoys.go,beads.go,orders.go,events.go}`
- Create: matching `*_test.go`
- Create: `cmd/gc-control/web/src/features/convoys/*`
- Create: `cmd/gc-control/web/src/features/beads/*`
- Create: `cmd/gc-control/web/src/features/orders/*`
- Create: `cmd/gc-control/web/src/lib/events.ts` and test

- [ ] **Step 1: Define wire models and write projection fixtures.**

Use closed unions for wire-visible values:

```go
type Progress struct { Closed, Total int }

type StatusSignal struct {
    Key    string `json:"key"`
    Icon   string `json:"icon"`
    Label  string `json:"label"`
    Tone   string `json:"tone" enum:"neutral,info,success,warning,danger"`
    Detail string `json:"detail,omitempty"`
}

type BeadView struct {
    ID, Title, Type, Status, Assignee, LogicalID string
    Metadata map[string]string
    UpdatedAt time.Time
}
```

Fixture tests must cover 3/3 complete, 2/3 running, fail-gate plus running,
waiting, Needs input, a missing required field, and a partial Supervisor
response. A failed closed gate counts as closed and still emits `fail_gate`.
MR lifecycle is asserted separately and never changes the percentage.

- [ ] **Step 2: Define an adapter around the generated Supervisor client.**

The production adapter uses these generated methods rather than issuing
untyped JSON requests:

```go
GetV0CityByCityNameConvoysWithResponse
GetV0CityByCityNameConvoyByIdWithResponse
GetV0CityByCityNameOrdersWithResponse
GetV0CityByCityNameOrdersHistoryWithResponse
GetV0CityByCityNameOrderHistoryByBeadIdWithResponse
StreamEvents
```

The small internal interface accepts typed parameters and returns typed domain
inputs so tests can use a fake without importing HTTP concerns.

- [ ] **Step 3: Implement projections with explicit degraded state.**

Build `ConvoySummary`, `ConvoyDetail`, `WorkflowStage`, `SessionSummary`,
`OrderView`, `OrderRunView`, and `MergeRequestSummary`. Required missing values
produce a `Problem` entry; they are not replaced with fabricated empty values.
Status composition returns a slice in deterministic display order and never
collapses simultaneous facts into one priority status.

- [ ] **Step 4: Add typed Huma endpoints and tests.**

Register:

```text
GET /api/v1/convoys
GET /api/v1/convoys/{id}
GET /api/v1/convoys/{id}/beads
GET /api/v1/orders
GET /api/v1/orders/history
GET /api/v1/orders/history/{bead_id}
GET /api/v1/events
```

Validate IDs at the path boundary. Assert Huma returns typed 404/422/503
problems and that list responses preserve partial items plus a degraded marker.

- [ ] **Step 5: Implement SSE invalidation and reconnect.**

Supervisor event payloads are invalidation hints only. Emit Control Center
events shaped as:

```json
{"event":"invalidate","resources":["convoys","orders"],"cursor":"42"}
```

Keep only bounded subscriber channels. On disconnect/reconnect the frontend
retains last confirmed state marked stale, reconnects with exponential backoff
capped at 10 seconds, and performs a full refresh before clearing stale state.

- [ ] **Step 6: Build the first real two-pane read-only UI.**

Use only `@/ui`. The left rail toggles Convoys and Orders. The right side shows
the selected item, progress, status signals, bead list, session decision
summaries, and order history/output. Add unit tests for selection preservation,
multiple simultaneous signals, partial state, empty state, and reconnect.

- [ ] **Step 7: Regenerate, verify, commit, and push.**

```bash
make control-center-gen
go test ./internal/controlcenter/gcstate ./internal/controlcenter/api -count=1
cd cmd/gc-control/web && npm run check
cd ../../.. && make control-center-check
git add internal/controlcenter cmd/gc-control internal/controlcenter/openapi.json
git commit -m "feat: project live Control Center state"
git pull --rebase
git push
```

---

## Task 4A: Add the Existing Mayor Session Workspace (`ga-8mr.15`)

**Files:**

- Create: `internal/controlcenter/mayor/{client.go,service.go,types.go}`
- Create: matching `*_test.go`
- Create: `internal/controlcenter/api/mayor.go` and tests
- Create: `cmd/gc-control/web/src/features/mayor/*`

- [ ] **Step 1: Write named-session discovery tests.**

Resolve the configured Mayor identity from
`StatusBody.NamedSessionDetails`. Cover reserved-unmaterialized, materialized,
missing, duplicate, partial, and disconnected states. The resolver receives an
explicit configured identity; it must not match a process title, guess another
session, or call session creation.

Use the typed Supervisor reads:

```go
GetV0CityByCityNameStatusWithResponse
GetV0CityByCityNameSessionByIdWithResponse
GetV0CityByCityNameSessionByIdTranscriptWithResponse
GetV0CityByCityNameSessionByIdPendingWithResponse
```

A reserved-unmaterialized Mayor is available but dormant. A direct session 404
in that state is not presented as an infrastructure failure.

- [ ] **Step 2: Write transcript and live-stream tests.**

Test conversation-format pagination, older-page cursors, live turn/activity/
pending events, bounded subscribers, disconnect state, and full snapshot
refresh after reconnect. The Go adapter must use raw `StreamSession` with a
bounded SSE decoder; `StreamSessionWithResponse` buffers the long-lived body
and is forbidden in the live path.

- [ ] **Step 3: Write submit and pending-response tests.**

First explicit send to a reserved identity uses `default` and may materialize
that configured named session. Later active sends use `follow_up` only when
`SubmissionCapabilities.SupportsFollowUp` allows it. Never expose
`interrupt_now`. Require a unique `X-GC-Request`, correlate the 202 request ID
through city events, and bind `RespondSessionWithResponse` to the currently
displayed pending request ID. Prove no duplicate Mayor or fallback session is
created.

- [ ] **Step 4: Add the typed API and top-level UI.**

Register:

```text
GET  /api/v1/mayor
GET  /api/v1/mayor/transcript
GET  /api/v1/mayor/events
POST /api/v1/mayor/messages
POST /api/v1/mayor/interactions/{request_id}
```

Build the Mayor top-level view with state, transcript, live activity, pending
interaction, composer, loading/empty/degraded states, and navigation
persistence. Use only `@/ui`. The view is available without selecting a convoy
and shares no state with the convoy assistant.

- [ ] **Step 5: Regenerate, verify, commit, and push.**

```bash
make control-center-gen
go test ./internal/controlcenter/mayor ./internal/controlcenter/api -count=1
cd cmd/gc-control/web && npm run check
cd ../../.. && make control-center-check
git add internal/controlcenter cmd/gc-control
git commit -m "feat: add Control Center Mayor workspace"
git pull --rebase
git push
```

---

## Task 4B: Add Read-Only Mail Notifications (`ga-8mr.16`)

**Files:**

- Create: `internal/controlcenter/mailbox/{client.go,projection.go,types.go}`
- Create: matching `*_test.go`
- Create: `internal/controlcenter/api/mail.go` and tests
- Create: `cmd/gc-control/web/src/features/mail/*`

- [ ] **Step 1: Write mailbox projection and pagination tests.**

Use only the non-mutating typed Supervisor reads:

```go
GetV0CityByCityNameMailCountWithResponse
GetV0CityByCityNameMailWithResponse
GetV0CityByCityNameMailByIdWithResponse
GetV0CityByCityNameMailThreadByIdWithResponse
```

Cover unread and all filters, total versus page length, cursor pagination,
message detail, ordered thread context, multiple rigs, partial items plus
`PartialErrors`, and all-provider failure. Opening detail must leave read state
unchanged.

- [ ] **Step 2: Prove the mutation boundary.**

The mailbox interface intentionally contains no send, reply, mark-read,
mark-unread, archive, or delete method. Add API tests that no mutation route is
registered and UI tests that selecting a message performs GETs only. Do not
call `PostV0CityByCityNameMailByIdReadWithResponse` or any other mail mutation.

- [ ] **Step 3: Add event-driven invalidation.**

Use raw `StreamEvents`, not the buffering `StreamEventsWithResponse`. Treat all
`mail.*` event variants as invalidation hints, refresh count and visible list,
and refresh or clear selected detail as needed. Test event cursor resume,
bounded reconnect backoff, last-confirmed stale state, and full refresh after
reconnect.

- [ ] **Step 4: Add the typed API and top-level UI.**

Register:

```text
GET /api/v1/mail/count
GET /api/v1/mail
GET /api/v1/mail/{id}
GET /api/v1/mail/{id}/thread
```

Build a top-level Mail view with authoritative unread badge, `Unread | All`
filter, paginated list, message detail, thread, loading/empty/partial/stale
states, and selection preservation across refresh. It is city-global and uses
only `@/ui`.

- [ ] **Step 5: Regenerate, verify, commit, and push.**

```bash
make control-center-gen
go test ./internal/controlcenter/mailbox ./internal/controlcenter/api -count=1
cd cmd/gc-control/web && npm run check
cd ../../.. && make control-center-check
git add internal/controlcenter cmd/gc-control
git commit -m "feat: add Control Center mail notifications"
git pull --rebase
git push
```

---

## Task 5: Implement Cancellable Per-Convoy Jobs (`ga-8mr.4`)

**Files:**

- Create: `internal/controlcenter/jobs/{spec.go,runner.go,registry.go,stream.go}`
- Create: matching `*_test.go`
- Create: `internal/controlcenter/api/jobs.go` and test
- Create: `cmd/gc-control/web/src/features/jobs/*`

- [ ] **Step 1: Write state-machine and bounded-buffer tests.**

Define:

```go
type State string
const (
    StateQueued State = "queued"
    StateRunning State = "running"
    StateSucceeded State = "succeeded"
    StateFailed State = "failed"
    StateCancelled State = "cancelled"
    StateTimedOut State = "timed_out"
)

type Spec struct {
    ID, ConvoyID, Kind, Cwd string
    Argv []string
    Env map[string]string
    Timeout time.Duration
    ConflictKey string
}
```

Tests must cover ordered stdout/stderr frames, a 256 KiB combined tail with a
truncation flag, exit code/signal/timestamps, timeout, cancel, duplicate IDs,
and rejection of empty argv or non-canonical cwd.

- [ ] **Step 2: Prove concurrency and serialization before implementation.**

Use a controllable fake executor. Two different convoy IDs must enter running
state concurrently. Two jobs with the same non-empty `ConflictKey` must remain
ordered. A read-only job with no conflict key must not wait behind a mutation.

- [ ] **Step 3: Prove process-group cleanup.**

Use `internal/processgroup.StartCommandInNewGroup` and
`internal/processgroup.TerminateCommand`; do not duplicate signal logic. Add an
opt-in real-process test that starts a child heartbeat process and proves both
parent and descendant stop after cancellation.

- [ ] **Step 4: Implement the in-memory manager.**

Expose:

```go
Start(context.Context, Spec) (Snapshot, error)
Get(id string) (Snapshot, bool)
Cancel(id string) error
Subscribe(id string) (<-chan Event, func(), error)
```

Keep active jobs and the 100 most recently completed snapshots. The registry is
not a source of liveness after a process exits. Execute `Argv[0]` plus
`Argv[1:]` directly; shell semantics require an explicit shell executable in
the trusted adapter's argv.

- [ ] **Step 5: Add typed endpoints and UI.**

Register GET/DELETE `/api/v1/jobs/{id}` and GET
`/api/v1/jobs/{id}/events`. Do not expose a generic HTTP endpoint accepting
arbitrary argv; domain adapters create jobs internally. The React surface shows
icon+text state, bounded logs, truncation, cancel, timestamps, and final errors.

- [ ] **Step 6: Verify race safety, commit, and push.**

```bash
go test -race ./internal/controlcenter/jobs ./internal/controlcenter/api -count=1
cd cmd/gc-control/web && npm run check
cd ../../.. && make control-center-check
git add internal/controlcenter cmd/gc-control
git commit -m "feat: add Control Center job engine"
git pull --rebase
git push
```

---

## Task 6: Add Canonical Worktree Status and Local Diff (`ga-8mr.5`)

**Files:**

- Create: `internal/controlcenter/worktree/{resolver.go,status.go,diff.go,git.go,types.go}`
- Create: matching `*_test.go`
- Create: `internal/controlcenter/api/worktree.go` and test
- Create: `cmd/gc-control/web/src/features/diff/*`

- [ ] **Step 1: Write resolver tests with real temporary Git repositories.**

Cover canonical `work_dir` on the convoy, fallback to the linked workflow root,
missing metadata, symlink canonicalization, a missing directory, a path outside
the reported repository, and an invalid `parent_commit`.

```go
type Descriptor struct {
    Path         string `json:"path"`
    Repository   string `json:"repository"`
    ParentCommit string `json:"parent_commit"`
}
```

The resolver must call `git -C "$WORKTREE" rev-parse --show-toplevel` through an
injected argv runner and verify `filepath.Rel(repository, path)` does not begin
with `..`.

- [ ] **Step 2: Write diff fixtures before implementing Git commands.**

Create real repos containing staged edits, unstaged edits, added/deleted/renamed
files, untracked UTF-8 text, untracked binary data, filenames with spaces, and a
large file. Assert:

```go
type FileDiff struct {
    Path, OldPath, Status string
    Binary bool
    Additions, Deletions int
    Patch string
    Truncated bool
}
```

- [ ] **Step 3: Implement bounded argument-safe Git inspection.**

Use only fixed argv templates:

```text
git -C "$WORKTREE" status --porcelain=v2 -z --untracked-files=all
git -C "$WORKTREE" diff --no-ext-diff --find-renames --no-color HEAD --
git -C "$WORKTREE" diff --no-index --no-color -- /dev/null "$UNTRACKED_FILE"
```

Accept exit 1 only for `diff --no-index`. Never interpolate a path into a shell
string. Cap the response at 500 files, 1 MiB per file, and 5 MiB total. Detect
binary input from Git's patch markers and bounded content probes; report a
summary without returning binary bytes.

- [ ] **Step 4: Add API and status-signal integration.**

Register:

```text
GET /api/v1/convoys/{id}/worktree
GET /api/v1/convoys/{id}/diff
```

Include a worktree status summary in convoy detail and emit `dirty_worktree`
only from the current Git result. A Git failure becomes a subsystem-specific
degraded problem, not a clean-worktree result.

- [ ] **Step 5: Implement accessible file and hunk navigation.**

The React diff surface has a keyboard-navigable file list, unified hunks,
addition/deletion line labels, binary summaries, and truncation notices. It
shows only the local dirty worktree; no MR diff or local reviewer comment model
is added.

- [ ] **Step 6: Verify, commit, and push.**

```bash
go test ./internal/controlcenter/worktree ./internal/controlcenter/api -count=1
cd cmd/gc-control/web && npm run check
cd ../../.. && make control-center-check
git add internal/controlcenter cmd/gc-control
git commit -m "feat: show local convoy worktree diffs"
git pull --rebase
git push
```

---

## Task 7: Add Explicit Embedded tmux and Native Handoff (`ga-8mr.6`)

**Files:**

- Create: `internal/controlcenter/terminal/{manager.go,tmux.go,buffer.go,pty_unix.go,native_darwin.go,native_stub.go,types.go}`
- Create: matching `*_test.go`
- Create: `internal/controlcenter/api/terminal.go` and WebSocket tests
- Create: `cmd/gc-control/web/src/features/terminal/*`
- Modify: `go.mod`, `go.sum`, and frontend lockfiles

- [ ] **Step 1: Write tests proving selection has no terminal side effect.**

No read endpoint or convoy selection path may call terminal `Open`. Add a fake
manager assertion at the convoy API and React levels. Only POST
`/api/v1/convoys/{id}/terminal` may create or attach.

- [ ] **Step 2: Write exact tmux command tests.**

Derive a dedicated socket from a hash of the canonical city path under the
user cache directory. Derive the session as `cc-` plus 16 lowercase hex
characters from city identity plus convoy ID. Assert exact argv:

```text
tmux -S "$SOCKET" has-session -t "$SESSION"
tmux -S "$SOCKET" new-session -d -s "$SESSION" -c "$WORKTREE"
tmux -S "$SOCKET" attach-session -t "$SESSION"
tmux -S "$SOCKET" kill-session -t "$SESSION"
```

No command may contain `kill-server` or omit `-S`.

- [ ] **Step 3: Write PTY, replay, and subscriber tests.**

Use `github.com/creack/pty` behind a small interface. Test resize, one resize
owner, multiple subscribers, 512 KiB bounded scrollback, replay truncation,
slow-subscriber eviction, browser detach without process termination, and
reattach to the same tmux session.

- [ ] **Step 4: Implement manager lifecycle.**

Expose:

```go
Open(ctx context.Context, convoyID, workDir string) (Session, error)
Status(ctx context.Context, convoyID string) (Session, error)
Close(ctx context.Context, convoyID string) error
OpenNative(ctx context.Context, convoyID string) error
Attach(ctx context.Context, convoyID string) (*Attachment, error)
```

The manager queries tmux for live state and writes no status file. `Close`
targets only the sanitized session. It must not call runtime services.

Register the resource routes exactly once:

```text
GET    /api/v1/convoys/{id}/terminal
POST   /api/v1/convoys/{id}/terminal
DELETE /api/v1/convoys/{id}/terminal
POST   /api/v1/convoys/{id}/terminal/native
WS     /ws/v1/convoys/{id}/terminal
```

- [ ] **Step 5: Implement the WebSocket protocol and xterm client.**

Server binary frames contain raw PTY output. Client binary frames contain raw
input. Text control frames are JSON:

```json
{"type":"resize","cols":120,"rows":40}
{"type":"replay","truncated":false}
{"type":"error","message":"terminal detached"}
```

Reject dimensions outside 2-500 columns and 2-200 rows and input frames over
64 KiB. The xterm surface connects only after the Open response, fits on panel
resize, reconnects with replay, and displays explicit attached/detached/closed
state.

- [ ] **Step 6: Implement configurable macOS native handoff.**

For `Terminal`, call `/usr/bin/osascript` with a static AppleScript program and
pass one shell-quoted tmux attach command as argv data. Provide explicit tested
adapters for `iTerm` and `Warp`; reject other application names until an adapter
exists. Every adapter attaches the same socket/session and never creates a
second shell identity.

- [ ] **Step 7: Add isolated real-tmux integration coverage.**

Under the integration build tag, use a temporary socket path, open in a temp
Git worktree, run `pwd`, disconnect/reconnect, verify replay, and close only the
test session. Test cleanup calls `kill-session`, never the default tmux server.

```bash
go test ./internal/controlcenter/terminal ./internal/controlcenter/api -count=1
GC_CONTROL_TMUX_INTEGRATION=1 go test -tags integration ./internal/controlcenter/terminal -run TestRealTmux -count=1
cd cmd/gc-control/web && npm run check
```

- [ ] **Step 8: Commit and push.**

```bash
git add internal/controlcenter cmd/gc-control go.mod go.sum
git commit -m "feat: add explicit convoy tmux terminals"
git pull --rebase
git push
```

---

## Task 8: Integrate the Persistent Convoy Assistant (`ga-8mr.7`)

**Files:**

- Create: `internal/controlcenter/assistant/{identity.go,client.go,service.go,types.go}`
- Create: matching `*_test.go`
- Create: `internal/controlcenter/api/chat.go` and test
- Create: `cmd/gc-control/web/src/features/chat/*`

- [ ] **Step 1: Write stable identity and missing-template tests.**

`AssistantIdentity(convoyID)` returns `cc-assistant-` plus 16 hex characters
and a human title containing the convoy ID. Different convoys must differ; the
same convoy must be stable across process restarts. The configured template is
data, not a Go constant. Missing or uncallable template returns a configuration
problem and never falls back to Mayor or a workflow worker.

- [ ] **Step 2: Write first-send and follow-up client tests.**

Define a narrow adapter around the generated Supervisor session methods. Assert
that first send creates:

```json
{
  "kind":"agent",
  "name":"taxdome/workers.convoy_assistant",
  "alias":"cc-assistant-7e19a638df2b3120",
  "work_dir":"/Volumes/DATA/repos/taxdome/taxdome-ta-example",
  "message":"Fix the spacing regression in the invoice header",
  "async":true
}
```

Subsequent sends call session submit with `intent:"follow_up"`. Sleeping
sessions are resumed through the existing Supervisor behavior. Active work is
not interrupted. Correlate 202 results by `request_id` and the returned event
cursor; duplicate browser retries with the same client message ID produce one
submission.

- [ ] **Step 3: Write transcript and interaction isolation tests.**

Test pagination, stream reconnect, pending questions, and answering a question.
The pending request key includes convoy ID, session ID, and request ID, so an
answer clears `Needs input` for only that convoy.

- [ ] **Step 4: Implement typed service and endpoints.**

Register:

```text
GET  /api/v1/convoys/{id}/chat
POST /api/v1/convoys/{id}/chat/messages
GET  /api/v1/convoys/{id}/chat/events
POST /api/v1/convoys/{id}/chat/interactions/{request_id}
```

Resolve the worktree through Task 6. Expose workflow-agent transcript summaries
read-only, but route free-form chat only to the deterministic assistant alias.

- [ ] **Step 5: Build the chat UI and dirty-worktree proof.**

Render persistent history, queued follow-ups, in-turn/sleeping state, reconnect,
pending questions, and the external-template configuration error. Add a test
that a completed assistant turn refreshes local diff and invokes no commit,
push, MR, or action endpoint.

- [ ] **Step 6: Verify available and unavailable modes, commit, and push.**

```bash
go test ./internal/controlcenter/assistant ./internal/controlcenter/api -count=1
cd cmd/gc-control/web && npm run check
cd ../../.. && make control-center-check
git add internal/controlcenter cmd/gc-control
git commit -m "feat: integrate convoy assistant chat"
git pull --rebase
git push
```

Live template acceptance is recorded on `ga-8mr.7` when the separate template
task lands; deterministic missing-template coverage is required regardless.

---

## Task 9: Integrate Schema-Versioned Runtime Commands (`ga-8mr.8`)

**Files:**

- Create: `internal/controlcenter/runtimeenv/{types.go,decoder.go,service.go}`
- Create: matching `*_test.go` and `testdata/*.json`
- Create: `internal/controlcenter/api/runtime.go` and test
- Create: `cmd/gc-control/web/src/features/runtime/*`

- [ ] **Step 1: Verify the external command surface before coding.**

Against the configured TaxDome city, run non-mutating commands:

```bash
gc gascity env-list --json
gc gascity env-status "$CONVOY_ID" --json
```

Assert stdout contains one JSON object with `schema_version:1` and stderr is
separate. If command discovery fails, keep `ga-8mr.8` blocked on the external
pack integration instead of inventing a second runtime implementation.

- [ ] **Step 2: Add fixtures for the complete schema-v1 contract.**

Model:

```go
type Result struct {
    SchemaVersion int `json:"schema_version"`
    OK bool `json:"ok"`
    Action string `json:"action"`
    Convoy *Convoy `json:"convoy"`
    Runtime *Runtime `json:"runtime"`
    Features []Feature `json:"features,omitempty"`
    Error *CommandError `json:"error"`
}
```

Cover `running`, `stopped`, `partial`, `missing`, and `docker_unavailable`;
service `{name,state,health}`; ports/URLs; volumes; allowed actions; and every
documented stable error code including `runtime_busy`, `prepare_failed`,
`start_failed`, `stop_failed`, and `readiness_timeout`. Unknown schema versions,
two stdout objects, trailing non-whitespace, and missing required fields fail.

- [ ] **Step 3: Write command and independence tests.**

Assert exact argv:

```text
"$GC_EXECUTABLE" "$PACK_NAME" env-list --json
"$GC_EXECUTABLE" "$PACK_NAME" env-status "$CONVOY_ID" --json
"$GC_EXECUTABLE" "$PACK_NAME" env-start "$CONVOY_ID" --json
"$GC_EXECUTABLE" "$PACK_NAME" env-stop "$CONVOY_ID" --json
```

Start/stop use the Task 5 job engine with conflict key
`runtime:ta-example`. Production derives the suffix from the validated canonical
convoy ID. Different convoys run concurrently. Runtime methods never
call terminal methods, and terminal methods never call runtime methods.

- [ ] **Step 4: Implement the adapter and API.**

`List` and `Status` are bounded direct reads. `Start` and `Stop` return a job
snapshot immediately; stdout becomes the final typed result and stderr remains
ordered job log frames. Register GET `/runtime` plus POST `/runtime/start` and
`/runtime/stop`. The backend does not query Docker, Compose, ports, or runtime
metadata directly.

- [ ] **Step 5: Implement runtime controls.**

Render Start Environment/Start App, Stop Environment, per-service state,
preparation logs, stable errors, URLs, and Open App. The initial Start App action
is exactly `env-start`. Only enable Open App for a validated returned `http` or
`https` URL. Show simultaneous `runtime_running` or `runtime_stopped` signals
without replacing workflow signals.

- [ ] **Step 6: Verify, commit, and push.**

```bash
go test ./internal/controlcenter/runtimeenv ./internal/controlcenter/api -count=1
cd cmd/gc-control/web && npm run check
cd ../../.. && make control-center-check
git add internal/controlcenter cmd/gc-control
git commit -m "feat: control per-convoy runtime environments"
git pull --rebase
git push
```

---

## Task 10: Add Truthful Action Stubs, Order Detail, and MR Read State (`ga-8mr.9`)

**Files:**

- Create: `internal/controlcenter/actions/{registry.go,types.go}` and tests
- Create: `internal/controlcenter/merge_request/{status.go,types.go}` and tests
- Create: `internal/controlcenter/api/actions.go` and test
- Extend: `internal/controlcenter/api/orders.go` and test
- Create: `cmd/gc-control/web/src/features/actions/*`
- Extend: `cmd/gc-control/web/src/features/orders/*`

- [ ] **Step 1: Write deterministic registry tests.**

Use:

```go
type Availability string
const (
    AvailabilityEnabled Availability = "enabled"
    AvailabilityDisabled Availability = "disabled"
    AvailabilityStub Availability = "stub"
)

type Descriptor struct {
    Key, Label, Icon, Reason, CommandPreview string
    Availability Availability
}
```

Return keys in this exact order: `fix`, `update_mr`, `create_mr`,
`rerun_review`, `retry_gate`, `skip_gate`, `abort`. For the first release, every
descriptor is `stub` with a non-empty reason and no executable command.

- [ ] **Step 2: Prove stubs cannot reach the job engine.**

POST each key against a fake runner that fails the test if called. The API must
return a typed 409 problem explaining that the action is not implemented. An
unknown key returns 404. Never return synthetic success.

- [ ] **Step 3: Add read-only MR fixture tests.**

Run `glab mr view "$MR_IID" -F json -c -R "$REPOSITORY"` from the canonical worktree through
an injected argv runner. Decode:

```go
type View struct {
    URL string; IID int; State string; Draft bool
    Approved *bool; PipelineStatus string
    Comments []Comment; UpdatedAt time.Time
    Degraded *Problem
}
```

Test successful JSON, no MR metadata, missing `glab`, authentication failure,
malformed JSON, and timeout. These are read failures only; no `glab` mutation
command exists in this task.

- [ ] **Step 4: Complete order execution detail.**

Expose configuration, enabled state, last run, history entry, stdout/stderr,
exit code, signal, and duration from the Supervisor order endpoints. Add no
enable/disable/edit route or control.

- [ ] **Step 5: Render actions, MR state, and order detail.**

Every action shows icon, label, `Stub`, and its reason; clicking opens an
explanation and performs no mutation request. Render MR lifecycle separately
from DAG progress. Missing `glab` and unavailable MR status are explicit
degraded states.

- [ ] **Step 6: Verify, commit, and push.**

```bash
go test ./internal/controlcenter/actions ./internal/controlcenter/merge_request ./internal/controlcenter/api -count=1
cd cmd/gc-control/web && npm run check
cd ../../.. && make control-center-check
git add internal/controlcenter cmd/gc-control
git commit -m "feat: add truthful Control Center action surfaces"
git pull --rebase
git push
```

---

## Task 11: Assemble the Adaptive Cockpit (`ga-8mr.10`)

**Files:**

- Create or refine: `cmd/gc-control/web/src/app/{App.tsx,routes.tsx,state.ts}`
- Create: `cmd/gc-control/web/src/features/cockpit/{Cockpit.tsx,Cockpit.css,Cockpit.test.tsx}`
- Create: `cmd/gc-control/web/src/features/cockpit/fixtures.ts`
- Create: `cmd/gc-control/web/e2e/cockpit.spec.ts`
- Create: `cmd/gc-control/web/playwright.config.ts`
- Modify: `cmd/gc-control/web/src/ui/tokens.css`
- Modify: `cmd/gc-control/web/src/ui/token-contract.test.ts`

- [ ] **Step 1: Write integration-level component tests first.**

Test top-level Convoys/Orders/Mayor/Mail selection persistence, Mayor and Mail
availability without a selected convoy, selected-convoy context across
Terminal/Diff/Chat/Beads, simultaneous status text, separate MR lifecycle,
Needs input, job state, unread badge, stub explanations,
loading/empty/partial/stale/disconnected states, and the explicit terminal
creation guard.

- [ ] **Step 2: Write failing continuous viewport and overflow tests.**

Use Playwright with real fixture-backed Control Center routes. Sweep the
desktop range from `1024` through `2560` CSS pixels in `64` pixel increments at
an `800` pixel height, then run the exact checkpoints `1024x720`, `1280x800`,
`1366x768`, `1440x900`, `1680x1050`, `1920x1080`, and `2560x1440` in both
themes. At every width assert:

```ts
expect(await page.evaluate(() => document.documentElement.scrollWidth <=
  document.documentElement.clientWidth)).toBe(true);
expect(await page.getByRole("navigation", { name: "Convoys and orders" })
  .isVisible()).toBe(true);
expect(await page.getByRole("main").isVisible()).toBe(true);
```

Also assert that every implemented tool remains reachable, selection survives
each resize, action bars wrap instead of clipping, and scroll context is kept
inside the owning list/diff/terminal rather than moving to the document. CSS
viewport width is the contract because it already reflects display scaling,
browser zoom, and the current window size; screen inches are not inspected.

- [ ] **Step 3: Implement one fluid, container-responsive layout.**

Use container queries, fluid column bounds, and auto-fitting tool panels. Query
thresholds come from the minimum readable widths of their content, not from
13-inch/30-inch device labels or a binary focused/wide mode:

```css
:root {
  --cc-size-cockpit-nav-min: 14rem;
  --cc-size-cockpit-nav-max: 20rem;
  --cc-size-cockpit-tool-min: 30rem;
}
.cockpit-shell { container: cockpit / inline-size; }
.cockpit {
  grid-template-columns:
    clamp(
      var(--cc-size-cockpit-nav-min),
      20cqi,
      var(--cc-size-cockpit-nav-max)
    )
    minmax(0, 1fr);
}
.cockpit-tools {
  grid-template-columns:
    repeat(
      auto-fit,
      minmax(min(100%, var(--cc-size-cockpit-tool-min)), 1fr)
    );
}
@container cockpit (width < 64rem) {
  .cockpit { grid-template-columns: minmax(0, 1fr); }
}
```

Reusable dimensions and all visual values come from semantic tokens; add their
presence to the token contract test. The `64rem` query threshold is a
documented content minimum because custom properties cannot participate in a
container-query condition. Between query thresholds, columns grow
continuously. Additional space increases useful simultaneous information
density while each reading/tool region remains bounded; shrinking space
reflows secondary panes without hiding their tabs or losing state.

- [ ] **Step 4: Implement theme and preference behavior.**

Support `system`, `light`, and `dark`. Persist only theme and UI layout
preference in `localStorage`; convoy, job, terminal, runtime, and chat state are
always re-read. Test system-theme changes and complete semantic token sets.

- [ ] **Step 5: Complete keyboard and accessibility behavior.**

The left list, tabs, diff files, action bar, and dialogs are keyboard reachable
with visible focus. Status is understandable with icon and text when all color
styles are disabled. Add automated axe checks for both layouts and themes.

- [ ] **Step 6: Add representative screenshot samples.**

Capture real cockpit compositions, not a component gallery:

```text
1024x768 light
1024x768 dark
1366x768 light
1366x768 dark
1680x1050 light
1680x1050 dark
2560x1440 light
2560x1440 dark
```

Each fixture includes running plus fail-gate signals, dirty diff, a terminal,
chat history, runtime state, Mayor activity, and the Mail unread badge. Keep
screenshot paths under
`cmd/gc-control/web/e2e/__screenshots__/`. These images sample the visual
continuum only; the Step 2 sweep is the acceptance proof for intermediate
widths.

- [ ] **Step 7: Verify, commit, and push.**

```bash
cd cmd/gc-control/web
npm run check
npx playwright test
cd ../../..
make control-center-check
git add cmd/gc-control/web internal/controlcenter/openapi.json
git commit -m "feat: assemble adaptive Control Center cockpit"
git pull --rebase
git push
```

---

## Task 12: Package, Document, and Acceptance-Test (`ga-8mr.11`)

**Files:**

- Create: `test/integration/control_center_test.go`
- Create: `cmd/gc-control/README.md`
- Modify: `Makefile`
- Modify: relevant release/build configuration discovered by repository search

- [ ] **Step 1: Write the hermetic acceptance harness.**

Use the integration build tag and isolated `GC_HOME`, runtime directory, Git
repositories, ports, fake Supervisor, fake pack executable, and dedicated tmux
socket. Seed two convoys, workflow members, sessions, the configured Mayor
identity and transcript, Mayor pending interaction, city mail and thread,
convoy pending interaction, and order history. Cleanup must prove no process,
listener, tmux session, or temp worktree remains.

- [ ] **Step 2: Encode the first complete operator flow.**

The test must:

1. start Control Center on `127.0.0.1:0`;
2. select a convoy and assert exact closed/total plus simultaneous states;
3. inspect beads and a local dirty diff;
4. prove no tmux exists before Open terminal;
5. open terminal, assert `pwd`, reload, and reattach with replay;
6. start and stop the fake schema-v1 environment without closing tmux;
7. close tmux without stopping the environment;
8. send first chat plus follow-up and isolate Needs input;
9. open Mayor, submit a follow-up, answer its pending interaction, and assert
   that no second named session was created;
10. open Mail, verify unread count, detail, and thread while asserting that no
    mail mutation request was made;
11. verify every action mutation remains a non-executing stub;
12. exercise a second convoy concurrently.

- [ ] **Step 3: Document exact operation and boundaries.**

`cmd/gc-control/README.md` includes build, configuration flags, launch, Vite
development, one-city scope, Supervisor prerequisites, configured Mayor
identity, read-only Mail boundary, assistant-template requirement, TaxDome
runtime commands, terminal lifecycle, native terminal adapters,
troubleshooting, and the distinction from `cmd/gc/dashboard`.

- [ ] **Step 4: Finish Makefile and generation drift checks.**

`control-center-check` must run generation, Go tests, race-sensitive packages,
frontend `npm run check`, production build, static embed tests, and OpenAPI/dist
drift detection. Add a separate opt-in `control-center-integration` target for
real tmux and browser smoke.

- [ ] **Step 5: Run the complete verification matrix.**

```bash
make control-center-check
make dashboard-check
make test-fast-parallel
go vet ./...
GC_CONTROL_TMUX_INTEGRATION=1 make control-center-integration
git diff --check
git status --short
```

Expected: all commands exit 0; only intentionally generated tracked artifacts
are changed before the final commit.

- [ ] **Step 6: Review against the approved spec.**

Use `superpowers:requesting-code-review`. The reviewer must check every goal,
non-goal, endpoint, source-of-truth rule, terminal/runtime independence rule,
design-system boundary, viewport, theme, and degraded state. Resolve every
high-confidence finding with another red/green cycle.

- [ ] **Step 7: Commit, close Beads, rebase, and push.**

```bash
git add cmd/gc-control internal/controlcenter test/integration Makefile go.mod go.sum
git commit -m "feat: ship GasCity Control Center"
git pull --rebase
git push
git status --short --branch
```

Close `ga-8mr.11`, then close epic `ga-8mr` only when all child beads are closed
or explicitly blocked by the external assistant-template delivery. Locally
commit the HQ Dolt updates. If the known Beads remote divergence remains, keep
`ga-1iq` open and do not force-push Dolt data.

---

## Plan Self-Review Checklist

- [ ] Every approved requirement maps to a task and verification command.
- [ ] Every production change starts with a named failing test.
- [ ] Session creation remains generic and role-agnostic.
- [ ] Runtime uses only schema-v1 pack commands; it never queries Docker.
- [ ] Terminal creation is explicit and its lifecycle is independent of runtime.
- [ ] Assistant chat uses only the configured external template and leaves edits
  dirty.
- [ ] Mayor reuses exactly one configured named session, exposes no interrupt or
  lifecycle mutation, and never falls back to another agent.
- [ ] Mail count, list, detail, and thread are read-only; opening a message does
  not mark it read and no mailbox mutation route exists.
- [ ] Local diff contains no invented reviewer comments.
- [ ] All initial MR/action mutations are truthful non-executing stubs.
- [ ] Orders remain observation-only.
- [ ] Feature UI imports only `@/ui`; no Storybook or component-gallery route
  exists.
- [ ] Focused and overview layouts pass in light and dark themes.
- [ ] No raw shell input, status database/file, default tmux server mutation, or
  hidden city switcher was introduced.
- [ ] Generated API/client/dist artifacts are in sync.
- [ ] Final commits and code branch are pushed; Beads divergence is handled only
  through `ga-1iq`, never by force.
