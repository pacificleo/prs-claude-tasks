# Multi-Agent Support (claude / gemini / codex) with Per-Agent Models

**Status:** Approved
**Date:** 2026-04-18

## Summary

Add support for running scheduled tasks against three CLI agents — `claude`, `gemini`, and `codex` — with a per-agent allow-list of selectable models. Today the executor hardcodes `claude`; the `Task.Agent` field exists in the model and DB but is not honored at execution time.

User-facing display format is `agent@model` (e.g. `claude@claude-sonnet-4-6`, `gemini@flash`). Storage and API keep agent and model as separate fields; the `@` form is display-only.

Model selection is constrained to a fixed allow-list per agent (no free text) so users cannot mistype. Both the TUI and the mobile app present pickers; the REST API validates input and rejects unknown combinations with `400`.

## Goals

- Run `gemini` and `codex` CLIs as alternatives to `claude` for any scheduled or one-off task.
- Let the user pick agent and model from constrained lists in TUI and mobile UI.
- Keep DB, API, and UI in sync via a single in-process registry of agent metadata.
- Backward compatible: existing tasks (all `agent='claude'`, no model) continue to run unchanged.

## Non-Goals

- Adding any agent beyond claude/gemini/codex in this change.
- Per-agent usage thresholds. Existing usage threshold remains Anthropic-only and gates only Claude tasks.
- Auto-detecting installed CLI binaries or installing them. Pre-flight `LookPath` only reports a clear failure.
- Configuration files for agent definitions. The registry lives in Go code.
- Cross-agent model mapping (switching agent resets the model to the new agent's default).

## Allowed Models and CLI Invocations

| Agent | Binary | Prompt flag | Model flag | Skip-perms flag | Allowed models (default first) |
|-------|--------|-------------|------------|-----------------|--------------------------------|
| `claude` | `claude` | `-p <prompt>` | `--model <m>` | `--dangerously-skip-permissions` | `claude-sonnet-4-6`, `claude-opus-4-7`, `claude-haiku-4-5` |
| `gemini` | `gemini` | `-p <prompt>` | `-m <m>` | `--approval-mode=yolo` | `flash`, `auto`, `pro`, `flash-lite` |
| `codex` | `codex` | `exec <prompt>` (subcommand) | `-m <m>` | `--dangerously-bypass-approvals-and-sandbox` | `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.2` |

The first model in each list is the default used when the user has not chosen a specific model.

## Architecture

### New package: `internal/agent`

Single source of truth for all agent metadata. Used by DB validation, executor, TUI pickers, and the REST API.

```go
package agent

type Name string

const (
    Claude Name = "claude"
    Gemini Name = "gemini"
    Codex  Name = "codex"
)

type Spec struct {
    Name          Name
    Binary        string
    AllowedModels []string                              // first entry = default
    BuildArgs     func(model, prompt string) []string   // argv after Binary
}

func Get(n Name) (Spec, bool)
func All() []Spec
func DefaultModel(n Name) string
func Validate(n Name, model string) error              // empty model = OK (resolved later)
func Display(n Name, model string) string              // "claude@claude-sonnet-4-6"
func ShortDisplay(n Name, model string) string         // strips "claude-" prefix; used in TUI list
```

`BuildArgs` per agent:

- claude → `["-p", "--dangerously-skip-permissions", "--model", model, prompt]`
- gemini → `["-p", "--approval-mode=yolo", "-m", model, prompt]`
- codex → `["exec", "--dangerously-bypass-approvals-and-sandbox", "-m", model, prompt]`

The existing `db.Agent` enum moves into `internal/agent`. `db/models.go` imports it as `agent.Name`.

### DB layer (`internal/db`)

**Schema migration** in `db.go` (added to the existing idempotent `ALTER TABLE` block):

```sql
ALTER TABLE tasks ADD COLUMN model TEXT NOT NULL DEFAULT '';
```

**Task struct**:

```go
type Task struct {
    // ... existing fields ...
    Agent agent.Name `json:"agent"`
    Model string     `json:"model"`
}

func (t *Task) ResolvedModel() string  // returns t.Model or agent.DefaultModel(t.Agent)
func (t *Task) Display() string        // agent.Display(t.Agent, t.ResolvedModel())
```

**CRUD validation** — `CreateTask` and `UpdateTask`:
1. If `task.Model == ""`, set `task.Model = agent.DefaultModel(task.Agent)`.
2. Call `agent.Validate(task.Agent, task.Model)`. Return error if invalid.
3. Existing INSERT/UPDATE statements add the `model` column.

**Lazy backfill rule** — existing rows have `agent='claude'` and `model=''`. They are not touched by migration. On any read path that needs a model (executor, API serialization), `ResolvedModel()` returns the default. On the next edit through TUI/API, the model column gets persisted.

### Executor (`internal/executor/executor.go`)

Replace the hardcoded `exec.CommandContext(ctx, "claude", ...)` block (currently around line 92):

```go
spec, ok := agent.Get(task.Agent)
if !ok {
    // mark run failed with "unknown agent" and return
}
if _, err := exec.LookPath(spec.Binary); err != nil {
    // mark run failed with "binary 'gemini' not found in PATH"
    return
}
model := task.ResolvedModel()
args := spec.BuildArgs(model, task.Prompt)
cmd := exec.CommandContext(ctx, spec.Binary, args...)
cmd.Dir = task.WorkingDir
```

**Usage threshold** — wrap the existing Anthropic usage check (`e.usageClient.CheckThreshold`) in `if task.Agent == agent.Claude { ... }`. Non-Claude tasks skip the check entirely.

**Webhook payloads** — `webhook/discord.go` and `webhook/slack.go` add `agent@model` to the title or footer of the rendered notification (small string change; payload schema unchanged).

### TUI (`internal/tui/app.go`)

Add two fields to the add/edit form between `Prompt` and `WorkingDir`:

- **Agent** — picker over `agent.All()`. Defaults to `claude` for new tasks. Style matches existing fields.
- **Model** — picker whose items rebuild from `agent.Get(currentAgent).AllowedModels` when the agent field changes. Always pre-selects the agent's default. Switching agent resets model to the new default (no cross-agent mapping).

**List view** — add an `Agent` column rendering `agent.ShortDisplay(t.Agent, t.ResolvedModel())`:
- claude → `claude@sonnet-4-6` (strips `claude-` prefix)
- gemini → `gemini@flash`
- codex → `codex@gpt-5.4`

**Validation feedback** — when `db.UpdateTask` returns a validation error, surface via the existing status-line error path (same channel cron parse errors use today). Should be unreachable through pickers but the error path covers API/mobile-driven races.

**Settings screen** — unchanged.

### REST API (`internal/api`)

**`types.go` additions**:

```go
type TaskRequest struct {
    // ... existing ...
    Agent string `json:"agent"`             // optional; defaults to "claude"
    Model string `json:"model,omitempty"`   // optional; defaults to agent's default
}

type TaskResponse struct {
    // ... existing ...
    Agent   string `json:"agent"`
    Model   string `json:"model"`           // always populated (resolved)
    Display string `json:"display"`         // "claude@claude-sonnet-4-6"
}
```

**New endpoint** `GET /api/v1/agents` — exposes the registry:

```json
{
  "agents": [
    {"name": "claude", "default_model": "claude-sonnet-4-6",
     "models": ["claude-sonnet-4-6", "claude-opus-4-7", "claude-haiku-4-5"]},
    {"name": "gemini", "default_model": "flash",
     "models": ["flash", "auto", "pro", "flash-lite"]},
    {"name": "codex",  "default_model": "gpt-5.4",
     "models": ["gpt-5.4", "gpt-5.4-mini", "gpt-5.2"]}
  ]
}
```

Default model is always listed first in `models` so clients render it as the pre-selected option without separate logic.

**Validation in handlers** — `CreateTask` and `UpdateTask` handlers call `agent.Validate()` before passing to the DB layer. On invalid input return `400` with `ErrorResponse{Error: "invalid model 'foo' for agent 'gemini'", Code: "invalid_agent_model"}`.

**Backward compatibility** — requests omitting `agent`/`model` are treated as `claude` with its default model. Existing mobile clients keep working without changes.

### Mobile app (`mobile/`)

Add Agent and Model dropdowns to the add and edit task screens. On mount, fetch `GET /api/v1/agents` and populate. The Model dropdown re-renders when the Agent dropdown changes. Show `agent@model` chip in the task list row.

`mobile/lib/api.ts` (or equivalent type module) adds matching TS types for the new request/response fields and the new endpoint.

## Data Flow

1. **Create/edit task**: TUI/mobile picker → `agent` + `model` strings → handler/`db.CreateTask` → `agent.Validate` → INSERT.
2. **Schedule fires**: scheduler calls `executor.Execute(task)` → `agent.Get(task.Agent)` → `LookPath(spec.Binary)` → `spec.BuildArgs(task.ResolvedModel(), task.Prompt)` → `exec.CommandContext`.
3. **Run completes**: existing TaskRun update + webhook flow, with `agent@model` injected into the webhook title/footer.

## Error Handling

| Failure | Surface | Behavior |
|---------|---------|----------|
| Unknown agent in DB row | Executor | Mark run `failed` with `"unknown agent: X"`. No subprocess. |
| Binary not in PATH | Executor (LookPath) | Mark run `failed` with `"binary 'gemini' not found in PATH"`. No subprocess. |
| Invalid model on save | DB + API | Reject with typed error; API returns 400 with `invalid_agent_model` code. |
| Subprocess non-zero exit | Executor (existing path) | Existing behavior — captured stderr stored in TaskRun.Error. |

## Testing

- `internal/agent` — unit tests for `Validate`, `DefaultModel`, `Display`, `ShortDisplay`, and each `BuildArgs` to lock down the exact argv per agent.
- `internal/db` — extend `db_test.go` to cover the new `model` column round-trip and validation rejection.
- `internal/executor` — table-driven test asserting the constructed `*exec.Cmd` (binary + args) for each agent. Use a stubbed binary on `PATH` so the LookPath check passes without invoking real CLIs. Add a test for the missing-binary failure path.
- `internal/api` — handler tests for `GET /api/v1/agents`, `POST /tasks` with valid and invalid `(agent, model)` combinations, and back-compat (request without `agent`/`model` → defaults).
- Mobile — type-level changes only; manual verification via the dev build.

## Migration / Rollout

- Auto-applied schema change on next startup; existing rows untouched.
- No config file changes. No env vars.
- Old binaries reading new DB rows: they don't query the `model` column today, so no runtime conflict. New binaries reading old rows resolve `model=''` → default lazily.
- Mobile app: forward-compatible — the new fields are optional in `TaskRequest`, and the new endpoint is additive. Old app version keeps working against the new server.

## Open Questions

None.
