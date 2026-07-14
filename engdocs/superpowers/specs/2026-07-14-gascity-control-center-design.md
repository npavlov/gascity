# GasCity Control Center Design

**Status:** Approved for implementation planning

**Target repository:** `/Volumes/DATA/repos/personal/gascity`

**Tracking epic:** `ga-8mr`

## Summary

GasCity Control Center is a standalone localhost application in the GasCity
repository. It gives an operator one focused interface for observing and
controlling convoys without routing every action through the Mayor or opening
an unmanaged terminal manually.

The product controls one preconfigured GasCity city. The left side lists
convoys and orders. The right side is the selected convoy cockpit: workflow
progress, simultaneous status signals, actions, local diff, beads, persistent
assistant chat, runtime controls, and an explicitly opened terminal rooted in
the convoy worktree.

The Control Center does not replace GasCity's object model or persistence. It
projects current state from GasCity and invokes typed infrastructure
operations. GasCity Supervisor, beads, events, Git, tmux, the process table,
and pack commands remain the sources of truth.

## Goals

- Let an operator inspect any active convoy and understand what is happening.
- Show exact workflow progress and all relevant current state signals.
- Keep work on different convoys independent and parallel.
- Open an embedded terminal in the correct convoy worktree only after an
  explicit user action.
- Support short follow-up conversations with one persistent assistant session
  per convoy.
- Show the local dirty worktree diff without requiring a commit or MR.
- Start and stop the selected convoy environment through a pack-owned typed
  command contract.
- Keep every Control Center screen visually consistent through one internal
  design system with enforced tokens, components, and composition rules.
- Work well at 1366x768 and 2560x1440 in light and dark themes.

## Non-goals for the first release

- Switching between multiple cities.
- Replacing the existing GasCity dashboard.
- Defining the `taxdome/workers.convoy_assistant` template or its prompt.
- Running a local pre-MR reviewer or rendering invented pre-MR inline review
  comments.
- Editing or enabling orders.
- Implementing MR mutations before their pack commands exist.
- Storing a second copy of convoy, order, workflow, runtime, or session state.
- Starting a tmux session when the user only selects or views a convoy.
- Publishing a reusable npm UI package, migrating the existing dashboard to
  the new components, or maintaining Storybook or a development UI catalog.

## Product boundary

The Control Center is a new binary and web application, separate from
`cmd/gc/dashboard`:

- `cmd/gc-control` starts the localhost server and embeds the compiled SPA.
- `internal/controlcenter` owns configuration and application assembly.
- focused subpackages own Supervisor projection, local Git inspection, jobs,
  terminal management, runtime commands, and assistant integration.
- `cmd/gc-control/web` contains the React and TypeScript application.
- `cmd/gc-control/web/src/ui` is the Control Center-only design system and the
  sole source of shared visual primitives, components, patterns, and tokens.

The existing dashboard remains a reference implementation for Supervisor API
usage. The new application has a backend because browsers cannot safely own
local shell jobs, Git commands, PTY file descriptors, tmux lifecycle, native
application launch, or pack command execution.

## Technology choices

- **Backend:** Go, using the repository's existing Huma and typed OpenAPI
  conventions.
- **Frontend:** React, TypeScript, and Vite.
- **Design system:** an internal typed React library under `src/ui`, semantic
  CSS custom-property tokens, ESLint import and JSX boundaries, and Stylelint
  value rules. It is not a separately published package.
- **Browser terminal:** xterm.js over a WebSocket attached to a server-side PTY.
- **Persistent terminal:** a dedicated tmux socket and one sanitized session
  name per convoy.
- **Live state:** the generated Supervisor client plus SSE invalidation events.
- **Local source inspection:** bounded, argument-safe Git subprocesses.
- **Long-running operations:** an in-memory job manager with cancellable
  processes and bounded logs. Domain state is never persisted there.

The first release binds only to `127.0.0.1`. A non-loopback bind is rejected.
Authentication and multi-user isolation are outside this local-only release.

## Internal design system

The Control Center owns one internal visual library:

```text
cmd/gc-control/web/src/ui/
├── tokens.css
├── themes.css
├── typography.css
├── icons.ts
├── primitives/
├── components/
├── patterns/
├── index.ts
└── README.md
```

`tokens.css` defines semantic color, spacing, typography, size, radius,
shadow, motion, and z-index values. `themes.css` supplies light and dark token
values. Feature code consumes semantic names such as surface, text, border,
accent, success, warning, and danger rather than palette values.

Primitives contain the smallest reusable controls and layout units, including
Button, IconButton, Input, Select, Textarea, Text, Stack, and Grid. Components
compose them into domain-neutral controls such as Badge, StatusSignal,
Progress, Tabs, Tooltip, Dialog, Panel, EmptyState, Skeleton, and Spinner.
Patterns capture repeated Control Center compositions such as ActionBar,
DetailHeader, and split-panel tool frames without embedding convoy-specific
business decisions.

`src/ui/index.ts` is the only supported feature-code import boundary. Public
component variants use TypeScript union types and stable semantic names. A
component does not accept arbitrary colors or unchecked style variants merely
to bypass the design system.

The visual language is a dense professional operations interface: compact
panels, clear hierarchy, restrained surfaces, and strong color reserved for
current state and primary actions. Status always combines icon and text.
Light and dark modes use the same semantic component contracts.

The following rules are mandatory and automated:

- Feature code imports shared UI only from `@/ui`, never from internal
  component subpaths.
- Feature code does not create raw button, input, select, or textarea controls;
  interactive elements come from the design system.
- Component and feature styles use semantic tokens instead of raw hex, rgb,
  hsl, or ad hoc spacing values.
- Accessibility names, focus states, disabled states, keyboard behavior, and
  light/dark behavior are part of each component contract.
- Every exported component has focused unit tests and a usage example in
  `src/ui/README.md`.
- `npm run check` runs TypeScript, Vitest, ESLint, and Stylelint enforcement.

There is no Storybook and no `/dev/ui-kit` route. Visual regressions are caught
through component tests and browser screenshots of representative real
Control Center compositions at the required themes and viewport sizes.

## Configuration

The application receives explicit configuration at startup:

```go
type Config struct {
    BindAddress      string
    SupervisorURL    string
    CityName         string
    GCExecutable     string
    PackName         string
    AssistantTemplate string
    NativeTerminalApp string
}
```

`CityName` selects the only city visible to that Control Center instance.
There is no city switcher. `AssistantTemplate` refers to a template delivered
by another task; the Control Center validates and invokes it but never embeds a
role name in Go behavior. `NativeTerminalApp` defaults to macOS Terminal and
can be set to another application such as iTerm or Warp.

## Sources of truth

| Concern | Authoritative source | Control Center behavior |
| --- | --- | --- |
| Convoys and workflow members | Supervisor and beads | Project typed read models |
| Orders and execution history | Supervisor order endpoints | Read-only list and detail |
| Session activity and transcript | Supervisor session endpoints and SSE | Stream and paginate |
| Local changes | Git in the resolved worktree | Compute status and diff on demand |
| Terminal existence | Dedicated tmux socket | Query tmux; never write a status file |
| Runtime services | `gc <pack> env-* --json` | Decode schema version 1 output |
| Process execution | OS process table and owned child handles | Expose current job state and logs |
| MR status | `glab mr view` in the worktree | Read-only projection with degraded state |

The application may keep an in-memory render cache and bounded job or terminal
buffers. It must invalidate cached reads from SSE and perform a full refresh
after reconnect. It does not create a database for operational state.

## Main interface

### Left navigation

The left column has two top-level views:

- **Convoys:** active and recent convoys, each with visible icon plus text
  signals and compact closed/total progress.
- **Orders:** enabled state, last execution status, and execution history.

Selection is preserved while switching tools. The application does not invent
a single priority status. A convoy may simultaneously show `Running`,
`Fail gate`, `Needs input`, or `Environment stopped` where those facts are all
true.

### Convoy header

The selected convoy header shows:

- title and ID;
- canonical worktree path;
- workflow progress as `closed / total` and percentage;
- current workflow stage;
- MR lifecycle separately from workflow progress;
- simultaneous current status signals, each with icon and text;
- runtime and action controls with truthful availability.

Workflow progress counts only tracked workflow-member beads. A closed failed
gate contributes to closed/total progress while still producing a visible
`Fail gate` signal. MR state never changes the workflow percentage.

### Tool surfaces

The right side contains Terminal, Diff, Chat, and Beads surfaces.

At focused widths such as 1366x768, one tool surface is active at a time. At
wide widths such as 2560x1440, the user can keep terminal, diff, and chat
visible together. Wide mode increases information density without stretching
one panel across the full display.

## Live state and progress

The backend consumes the existing generated Supervisor client rather than
reimplementing domain queries. It projects typed view models for the frontend:

```go
type StatusSignal struct {
    Key    string
    Icon   string
    Label  string
    Tone   string
    Detail string
}

type Progress struct {
    Closed int
    Total  int
}
```

Initial signals include running, stopped, fail gate, waiting, needs input,
dirty worktree, runtime running, and runtime stopped. Every signal is
understandable without color.

SSE events are invalidation notifications. On reconnect, the client refreshes
the selected convoy and visible lists. If the Supervisor is unavailable or a
response is partial, the UI shows stale or degraded state instead of replacing
required data with fabricated defaults.

## Local worktree and diff

The worktree resolver reads canonical `work_dir` and `parent_commit` metadata
from the convoy or linked workflow. It canonicalizes symlinks, requires an
existing directory, and rejects paths outside the resolved repository.

The diff includes:

- staged and unstaged tracked changes;
- added, deleted, and renamed files;
- untracked text files represented as additions from `/dev/null`;
- a summary for binary files;
- file navigation and unified hunks.

The diff always reflects the local worktree and never requires a commit. It
does not fetch an MR diff and does not render pre-MR inline reviewer findings.
Post-MR comments belong to the read-only MR detail surface.

## Terminal lifecycle

The terminal is created only by an explicit **Open terminal** action.
Selecting a convoy, opening its detail, or starting the environment has no
terminal side effect.

On first open, the backend creates or attaches a tmux session using a dedicated
Control Center socket derived from the city identity. The session starts in
the selected convoy's canonical worktree. A PTY attaches to that tmux session
and xterm.js exchanges input, output, and resize frames over WebSocket.

The server-side manager follows the useful parts of Clay's terminal design:

- bounded scrollback;
- explicit WebSocket subscribers;
- replay on reconnect;
- resize ownership;
- detach without killing the underlying terminal.

Unlike Clay's disposable shell PTY, the underlying process is tmux. Browser
reload or disconnect does not end it. **Close tmux** kills only the known
session on the dedicated socket and never runs bare `tmux kill-server`.

**Open in Terminal** launches the configured macOS terminal application and
executes an attach command for the same tmux socket and session. It does not
create a different shell. **Stop Environment** never closes tmux, and **Close
tmux** never stops the environment.

## Persistent convoy assistant

The chat surface is for a persistent worktree-bound assistant, not for the
fire-and-forget workers that execute workflow beads.

The separate TaxDome task supplies the assistant template. The Control Center:

1. validates that the configured template is available;
2. derives a stable session alias from the convoy ID;
3. creates the session in the exact convoy worktree on the first message;
4. resumes a sleeping session when needed;
5. submits later messages with the Supervisor `follow_up` intent;
6. streams transcript and pending interactions;
7. scopes `Needs input` to that convoy only.

Messages may describe new fixes, new review comments, or feature polishing.
Assistant changes remain in the dirty worktree. The Control Center does not
commit them until a future explicit **Update MR** action is implemented.

If the external assistant template is missing, chat shows a configuration
error. It never falls back to the Mayor or another worker implicitly.

## Runtime environment

Runtime state and mutations come only from the TaxDome pack's schema-versioned
commands:

```text
gc <pack> env-list --json
gc <pack> env-status <convoy> --json
gc <pack> env-start <convoy> --json
gc <pack> env-stop <convoy> --json
```

The initial implementation expects `schema_version: 1`. Exactly one JSON
object is accepted on stdout; stderr remains job log output. Unknown schema
versions and malformed output are visible errors.

**Start Environment** is also the initial **Start App** behavior. It may start
the configured frontend, backend, Sidekiq, Docker, or OrbStack-backed services
as defined by the pack. The Control Center does not inspect Docker directly.
The response may expose service statuses and an application URL. **Open App**
uses that returned URL.

Different convoys can own independent runtime jobs. Mutations for the same
convoy are serialized and idempotent; unrelated convoys are not globally
serialized.

## Jobs and actions

Long-running shell operations return a job snapshot immediately. The backend
owns cancellation, exit status, signal, timestamps, and bounded stdout/stderr.
The UI shows queued, running, succeeded, failed, cancelled, and timed-out
states with icon and text.

The initial action registry contains Fix, Update MR, Create MR, Rerun review,
Retry gate, Skip gate, and Abort. Until the corresponding pack command exists,
an action is a visible `stub` with a reason and cannot execute a process or
return fake success.

Orders are observation-only. The UI shows configuration, last run, history,
output, exit code, signal, and duration but does not enable, disable, or edit an
order.

## API and transport

The Control Center exposes its own versioned API under `/api/v1` and a typed
OpenAPI document. Representative routes are:

```text
GET    /api/v1/health
GET    /api/v1/convoys
GET    /api/v1/convoys/{id}
GET    /api/v1/convoys/{id}/beads
GET    /api/v1/convoys/{id}/worktree
GET    /api/v1/convoys/{id}/diff
GET    /api/v1/orders
GET    /api/v1/orders/history
GET    /api/v1/events
POST   /api/v1/convoys/{id}/terminal
DELETE /api/v1/convoys/{id}/terminal
POST   /api/v1/convoys/{id}/terminal/native
WS     /ws/v1/convoys/{id}/terminal
GET    /api/v1/convoys/{id}/chat
POST   /api/v1/convoys/{id}/chat/messages
GET    /api/v1/convoys/{id}/chat/events
GET    /api/v1/convoys/{id}/runtime
POST   /api/v1/convoys/{id}/runtime/start
POST   /api/v1/convoys/{id}/runtime/stop
GET    /api/v1/convoys/{id}/actions
POST   /api/v1/convoys/{id}/actions/{key}
```

Wire-visible domain shapes are typed. Shell command construction never accepts
raw user fragments. Convoy IDs, worktree paths, command names, tmux sockets,
and terminal application names are validated before process execution.

## Error handling and observability

- No required field is silently replaced with an empty value.
- Supervisor, Git, tmux, assistant, runtime, and MR failures preserve their
  subsystem and operation context.
- The backend writes structured logs with convoy and job correlation fields.
- The frontend has explicit loading, empty, partial, stale, disconnected, and
  failed states.
- Bounded job and terminal buffers report truncation.
- Reconnects are visible while the last confirmed state remains labelled stale.
- Stub actions are distinguishable from disabled and enabled actions.

## Accessibility and responsive behavior

- Status never relies on color alone; every state has an icon and text.
- All controls have accessible names and visible keyboard focus.
- Keyboard navigation covers the left list, tool tabs, action bar, and diff
  files.
- Theme uses semantic tokens shared by light and dark modes.
- UI preference is the only browser-persisted state.
- 1366x768 and 2560x1440 are required viewport test cases with no horizontal
  page overflow.

## Testing strategy

All implementation uses test-first red/green/refactor cycles.

- **Go unit tests:** configuration, loopback validation, Supervisor projection,
  progress calculation, signal composition, job serialization, worktree
  validation, Git diff cases, runtime decoding, and command construction.
- **Frontend unit tests:** selection, status rendering, action availability,
  terminal creation guard, diff navigation, chat state, themes, and degraded
  states.
- **Design-system tests:** typed variants, accessibility behavior, token-only
  styling, light/dark contracts, and enforcement that feature code uses the
  public `@/ui` boundary rather than raw interactive controls.
- **Static checks:** TypeScript, ESLint, and Stylelint are mandatory parts of
  `npm run check`; raw colors, private UI imports, and disallowed JSX controls
  fail locally and in CI.
- **Contract tests:** Huma OpenAPI generation and generated TypeScript client
  sync.
- **Integration tests:** opt-in real tmux on a dedicated socket, real Git temp
  repositories, and a fake Supervisor/pack command boundary.
- **Browser smoke:** focused and wide viewports, both themes, reconnect, and the
  first vertical operator flow.

The first end-to-end acceptance flow is:

1. start Control Center for one configured city;
2. select a convoy;
3. see closed/total progress and simultaneous status signals;
4. inspect its beads and local dirty diff;
5. click Open terminal and verify the exact worktree;
6. reload and reattach to the same tmux session;
7. close only that tmux session.

Runtime and chat acceptance are added when their external pack contracts are
available.

## Delivery order

1. `ga-8mr.1`: allow Supervisor session creation with an explicit validated
   `work_dir`.
2. `ga-8mr.2`: create the localhost Go binary, typed API, embedded React shell,
   and configuration validation.
3. `ga-8mr.13`: establish the internal design system, tokens, themes,
   components, documentation, and automated style boundaries.
4. `ga-8mr.3`: project convoys, workflow progress, sessions, beads, orders, and
   simultaneous signals.
5. `ga-8mr.4`: add the cancellable local job engine.
6. `ga-8mr.5`: add local worktree status and unified diff.
7. `ga-8mr.6`: add explicit xterm plus tmux and native terminal handoff.
8. `ga-8mr.7`: integrate the externally supplied persistent convoy assistant.
9. `ga-8mr.8`: integrate the external runtime command contract.
10. `ga-8mr.9`: add truthful action stubs, order detail, and read-only MR state.
11. `ga-8mr.10`: assemble the adaptive, themed, accessible cockpit.
12. `ga-8mr.11`: package, document, and run final acceptance checks.

## Acceptance criteria

The first release is accepted when a local operator can use one browser window
to select any active convoy in the configured city, understand its exact
workflow progress and simultaneous states, inspect orders, beads, and the
local dirty diff, explicitly open or attach an embedded tmux terminal in the
correct worktree, open that same session in a configured macOS terminal, use
the externally supplied persistent convoy assistant, and start or stop the
convoy environment through the schema-versioned pack command.

Different convoys remain independent. Terminal and environment lifecycles
remain independent. The app works at both required viewport sizes, in light
and dark themes. All feature screens use the internal design system, and the
automated UI-boundary rules reject private imports, raw interactive controls,
and non-token styling. All applicable Go, frontend, contract, integration,
vet, build, and browser smoke checks pass.
