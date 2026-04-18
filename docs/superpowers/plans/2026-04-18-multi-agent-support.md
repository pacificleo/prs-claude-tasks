# Multi-Agent Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add support for `gemini` and `codex` CLIs alongside `claude`, with a per-agent allow-list of selectable models. User picks both via TUI/mobile pickers; the executor builds the right subprocess invocation for each.

**Architecture:** A new `internal/agent` package owns all per-agent metadata (binary, flags, allowed models, default model, argv builder) and is the single source of truth used by DB validation, executor, REST API, and UI pickers. DB stores `agent` and `model` as separate columns; UI shows `agent@model` for display only. Existing `claude`-only tasks keep working through lazy-default model resolution.

**Tech Stack:** Go 1.24, SQLite (`mattn/go-sqlite3`), `bubbles`/`bubbletea` TUI, `chi` HTTP router, Expo/React Native mobile.

**Spec:** [`docs/superpowers/specs/2026-04-18-multi-agent-support-design.md`](../specs/2026-04-18-multi-agent-support-design.md)

---

## File Structure

**New files:**
- `internal/agent/registry.go` — agent registry: types, lookup, validation, argv builders
- `internal/agent/registry_test.go` — table-driven tests for the registry
- `internal/executor/executor_test.go` — tests for command construction and missing-binary handling
- `internal/api/agents.go` — handler for `GET /api/v1/agents`
- `internal/api/agents_test.go` — handler test

**Modified files:**
- `internal/db/models.go` — `Task.Model` field, helpers, `Agent` becomes `agent.Name` alias
- `internal/db/db.go` — schema migration, model column in CRUD, validation hook
- `internal/db/db_test.go` — extend tests for `model` column round-trip and validation
- `internal/executor/executor.go` — registry-driven command construction, LookPath preflight, claude-only usage gate
- `internal/webhook/discord.go` — append `agent@model` to embed footer
- `internal/webhook/slack.go` — append `agent@model` to header field
- `internal/api/types.go` — `Agent`, `Model`, `Display` fields on request/response
- `internal/api/api.go` — register `/agents` route
- `internal/api/handlers.go` — agent/model wiring + validation in `CreateTask`/`UpdateTask`, populate response
- `internal/tui/app.go` — agent + model picker fields, list-view column
- `mobile/lib/types.ts` — TS request/response types + agent registry types
- `mobile/lib/api.ts` — `getAgents` client method
- `mobile/app/task/...` — agent/model dropdowns in add/edit screens (paths discovered in Task 13)
- `mobile/components/TaskCard.tsx` — show `agent@model` chip

---

## Task 1: Agent registry foundation

**Files:**
- Create: `internal/agent/registry.go`
- Test: `internal/agent/registry_test.go`

**Why:** A single source of truth for everything per-agent. Pure Go, no other internal package dependencies — every later task imports from here.

- [ ] **Step 1: Write the failing test**

Create `internal/agent/registry_test.go`:

```go
package agent_test

import (
	"strings"
	"testing"

	"github.com/kylemclaren/claude-tasks/internal/agent"
)

func TestAllAgentsRegistered(t *testing.T) {
	all := agent.All()
	if len(all) != 3 {
		t.Fatalf("All() returned %d agents, want 3", len(all))
	}
	want := map[agent.Name]bool{agent.Claude: true, agent.Gemini: true, agent.Codex: true}
	for _, s := range all {
		if !want[s.Name] {
			t.Errorf("unexpected agent: %s", s.Name)
		}
	}
}

func TestDefaultModels(t *testing.T) {
	cases := map[agent.Name]string{
		agent.Claude: "claude-sonnet-4-6",
		agent.Gemini: "flash",
		agent.Codex:  "gpt-5.4",
	}
	for n, want := range cases {
		if got := agent.DefaultModel(n); got != want {
			t.Errorf("DefaultModel(%s) = %q, want %q", n, got, want)
		}
	}
}

func TestDefaultModelIsFirstInAllowedList(t *testing.T) {
	for _, s := range agent.All() {
		if s.AllowedModels[0] != agent.DefaultModel(s.Name) {
			t.Errorf("agent %s: AllowedModels[0]=%q != DefaultModel=%q",
				s.Name, s.AllowedModels[0], agent.DefaultModel(s.Name))
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		agent   agent.Name
		model   string
		wantErr bool
	}{
		{"claude valid", agent.Claude, "claude-opus-4-7", false},
		{"claude empty model ok", agent.Claude, "", false},
		{"claude bad model", agent.Claude, "gpt-5.4", true},
		{"gemini valid", agent.Gemini, "flash", false},
		{"gemini bad", agent.Gemini, "ultra", true},
		{"codex valid", agent.Codex, "gpt-5.4-mini", false},
		{"unknown agent", agent.Name("openai"), "gpt-5.4", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := agent.Validate(tc.agent, tc.model)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate(%s, %q) err=%v, wantErr=%v", tc.agent, tc.model, err, tc.wantErr)
			}
		})
	}
}

func TestBuildArgsClaude(t *testing.T) {
	spec, _ := agent.Get(agent.Claude)
	args := spec.BuildArgs("claude-sonnet-4-6", "hello world")
	want := []string{"-p", "--dangerously-skip-permissions", "--model", "claude-sonnet-4-6", "hello world"}
	if !equalSlices(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestBuildArgsGemini(t *testing.T) {
	spec, _ := agent.Get(agent.Gemini)
	args := spec.BuildArgs("flash", "hello world")
	want := []string{"-p", "--approval-mode=yolo", "-m", "flash", "hello world"}
	if !equalSlices(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestBuildArgsCodex(t *testing.T) {
	spec, _ := agent.Get(agent.Codex)
	args := spec.BuildArgs("gpt-5.4", "hello world")
	want := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "-m", "gpt-5.4", "hello world"}
	if !equalSlices(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestDisplay(t *testing.T) {
	got := agent.Display(agent.Claude, "claude-sonnet-4-6")
	if got != "claude@claude-sonnet-4-6" {
		t.Errorf("Display = %q, want %q", got, "claude@claude-sonnet-4-6")
	}
}

func TestShortDisplayStripsClaudePrefix(t *testing.T) {
	cases := map[string]struct{ agent agent.Name; model, want string }{
		"claude opus":  {agent.Claude, "claude-opus-4-7", "claude@opus-4-7"},
		"claude sonnet":{agent.Claude, "claude-sonnet-4-6", "claude@sonnet-4-6"},
		"gemini flash": {agent.Gemini, "flash", "gemini@flash"},
		"codex gpt":    {agent.Codex, "gpt-5.4-mini", "codex@gpt-5.4-mini"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := agent.ShortDisplay(tc.agent, tc.model)
			if got != tc.want {
				t.Errorf("ShortDisplay = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShortDisplayResolvesEmptyModelToDefault(t *testing.T) {
	got := agent.ShortDisplay(agent.Claude, "")
	if !strings.HasPrefix(got, "claude@") {
		t.Errorf("ShortDisplay with empty model = %q, want claude@<default>", got)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agent/...`
Expected: FAIL with `package github.com/kylemclaren/claude-tasks/internal/agent is not in std`.

- [ ] **Step 3: Implement the registry**

Create `internal/agent/registry.go`:

```go
// Package agent owns all per-agent metadata: binary names, CLI flags,
// allowed models, defaults, and argv construction. It is the single source
// of truth used by db validation, the executor, the REST API, and TUI/mobile pickers.
package agent

import "fmt"

// Name identifies a CLI agent.
type Name string

const (
	Claude Name = "claude"
	Gemini Name = "gemini"
	Codex  Name = "codex"
)

// Spec describes one agent: how to invoke it and which models are allowed.
type Spec struct {
	Name          Name
	Binary        string
	AllowedModels []string                              // first entry is the default
	BuildArgs     func(model, prompt string) []string   // argv after Binary
}

var registry = map[Name]Spec{
	Claude: {
		Name:   Claude,
		Binary: "claude",
		AllowedModels: []string{
			"claude-sonnet-4-6",
			"claude-opus-4-7",
			"claude-haiku-4-5",
		},
		BuildArgs: func(model, prompt string) []string {
			return []string{"-p", "--dangerously-skip-permissions", "--model", model, prompt}
		},
	},
	Gemini: {
		Name:   Gemini,
		Binary: "gemini",
		AllowedModels: []string{"flash", "auto", "pro", "flash-lite"},
		BuildArgs: func(model, prompt string) []string {
			return []string{"-p", "--approval-mode=yolo", "-m", model, prompt}
		},
	},
	Codex: {
		Name:   Codex,
		Binary: "codex",
		AllowedModels: []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.2"},
		BuildArgs: func(model, prompt string) []string {
			return []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "-m", model, prompt}
		},
	},
}

// Order in which agents are returned by All() and rendered in pickers.
var order = []Name{Claude, Gemini, Codex}

// Get returns the spec for an agent.
func Get(n Name) (Spec, bool) {
	s, ok := registry[n]
	return s, ok
}

// All returns all registered agents in display order.
func All() []Spec {
	out := make([]Spec, 0, len(order))
	for _, n := range order {
		out = append(out, registry[n])
	}
	return out
}

// DefaultModel returns the default model for an agent, or "" if the agent is unknown.
func DefaultModel(n Name) string {
	s, ok := registry[n]
	if !ok || len(s.AllowedModels) == 0 {
		return ""
	}
	return s.AllowedModels[0]
}

// Validate returns nil if (n, model) is a legal combination. An empty model
// is treated as "use the default" and accepted.
func Validate(n Name, model string) error {
	s, ok := registry[n]
	if !ok {
		return fmt.Errorf("unknown agent %q", n)
	}
	if model == "" {
		return nil
	}
	for _, m := range s.AllowedModels {
		if m == model {
			return nil
		}
	}
	return fmt.Errorf("invalid model %q for agent %q", model, n)
}

// resolveModel returns model if non-empty, else the agent's default.
func resolveModel(n Name, model string) string {
	if model != "" {
		return model
	}
	return DefaultModel(n)
}

// Display returns "agent@model" using the resolved model.
func Display(n Name, model string) string {
	return string(n) + "@" + resolveModel(n, model)
}

// ShortDisplay returns "agent@<short-model>". For claude models it strips the
// "claude-" prefix to keep TUI columns narrow. Other agents pass through.
func ShortDisplay(n Name, model string) string {
	m := resolveModel(n, model)
	if n == Claude && len(m) > 7 && m[:7] == "claude-" {
		m = m[7:]
	}
	return string(n) + "@" + m
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agent/...`
Expected: PASS — all tests green.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/
git commit -m "feat(agent): add registry package with per-agent CLI specs and validation"
```

---

## Task 2: Move Agent enum into the registry; update db tests

**Files:**
- Modify: `internal/db/models.go:1-12` (replace Agent enum with type alias)
- Modify: `internal/db/db_test.go:1-138` (use `agent.Name` and constants)

**Why:** The spec says `internal/agent` is the single source of truth. `db.Agent`/`db.AgentClaude` etc. become aliases that point at the registry so existing call sites keep compiling.

- [ ] **Step 1: Replace the Agent type and constants in `internal/db/models.go`**

Edit `internal/db/models.go` lines 1–35 to:

```go
package db

import (
	"time"

	"github.com/kylemclaren/claude-tasks/internal/agent"
)

// Agent is an alias for agent.Name kept for backwards-compat with callers
// that imported db.Agent before the registry was extracted.
type Agent = agent.Name

const (
	AgentClaude = agent.Claude
	AgentGemini = agent.Gemini
	AgentCodex  = agent.Codex
)

// Task represents a scheduled task.
type Task struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Prompt         string     `json:"prompt"`
	Agent          Agent      `json:"agent"`
	Model          string     `json:"model"`
	CronExpr       string     `json:"cron_expr"`
	ScheduledAt    *time.Time `json:"scheduled_at,omitempty"`
	WorkingDir     string     `json:"working_dir"`
	DiscordWebhook string     `json:"discord_webhook,omitempty"`
	SlackWebhook   string     `json:"slack_webhook,omitempty"`
	Enabled        bool       `json:"enabled"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
}
```

(`Model` field added here too — it's used by Task 3, but adding it now lets the test scaffolding from Task 3 compile when written next.)

- [ ] **Step 2: Add Task helpers below the struct in `internal/db/models.go`**

```go
// IsOneOff returns true if this is a one-off (non-recurring) task.
func (t *Task) IsOneOff() bool {
	return t.CronExpr == ""
}

// ResolvedModel returns t.Model when set, else the agent's default model.
func (t *Task) ResolvedModel() string {
	if t.Model != "" {
		return t.Model
	}
	return agent.DefaultModel(t.Agent)
}

// Display returns "agent@model" using the resolved model.
func (t *Task) Display() string {
	return agent.Display(t.Agent, t.Model)
}
```

Remove the existing standalone `IsOneOff` if it was elsewhere — it lives here now.

- [ ] **Step 3: Build to confirm package compiles**

Run: `go build ./internal/db/...`
Expected: build succeeds. (DB CRUD still references columns that don't exist yet — fixed in Task 3 — but `go build` only needs the types to compile.)

If `go build` fails because `db.go` SELECT/INSERT lists reference `model` already, that's fine — Task 3 adds the column. If it fails due to a missing import, fix the import only.

- [ ] **Step 4: Commit**

```bash
git add internal/db/models.go internal/db/db_test.go
git commit -m "refactor(db): alias db.Agent to agent.Name and add Model field/helpers"
```

(The existing `db_test.go` still compiles because `db.AgentClaude == agent.Claude` etc. — the constants are just retyped.)

---

## Task 3: Add `model` column migration and CRUD wiring

**Files:**
- Modify: `internal/db/db.go:45-100` (migration block) and the CRUD functions
- Modify: `internal/db/db_test.go` (add a test for round-trip of the new field)

**Why:** Persist the `model` column. Existing rows get the empty default (`''`), which is interpreted as "use agent default" via `Task.ResolvedModel()`.

- [ ] **Step 1: Write the failing test**

Append to `internal/db/db_test.go`:

```go
func TestCreateAndGetTaskWithModel(t *testing.T) {
	d := setupTestDB(t)

	task := &db.Task{
		Name:     "claude opus task",
		Prompt:   "test",
		Agent:    db.AgentClaude,
		Model:    "claude-opus-4-7",
		CronExpr: "0 * * * * *",
		Enabled:  true,
	}
	if err := d.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := d.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want %q", got.Model, "claude-opus-4-7")
	}
}

func TestEmptyModelStaysEmptyInStorage(t *testing.T) {
	d := setupTestDB(t)

	task := &db.Task{
		Name:     "default model task",
		Prompt:   "test",
		Agent:    db.AgentClaude,
		// Model intentionally not set
		CronExpr: "0 * * * * *",
		Enabled:  true,
	}
	if err := d.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got, err := d.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Model != "" {
		t.Errorf("Model = %q, want empty (lazy default at read time)", got.Model)
	}
	if got.ResolvedModel() != "claude-sonnet-4-6" {
		t.Errorf("ResolvedModel() = %q, want %q", got.ResolvedModel(), "claude-sonnet-4-6")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/...`
Expected: FAIL — `no such column: model`.

- [ ] **Step 3: Add the migration in `internal/db/db.go`**

Inside `migrate()`, immediately after the existing `agent` migration (line ~97), add:

```go
// Migration: Add model column for per-agent model selection
_, _ = db.conn.Exec("ALTER TABLE tasks ADD COLUMN model TEXT NOT NULL DEFAULT ''")
```

- [ ] **Step 4: Update `CreateTask` in `internal/db/db.go`**

Replace the `INSERT INTO tasks (...)` statement with:

```go
result, err := db.conn.Exec(`
    INSERT INTO tasks (name, prompt, agent, model, cron_expr, scheduled_at, working_dir, discord_webhook, slack_webhook, enabled, created_at, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, task.Name, task.Prompt, task.Agent, task.Model, task.CronExpr, task.ScheduledAt, task.WorkingDir, task.DiscordWebhook, task.SlackWebhook, task.Enabled, time.Now(), time.Now())
```

- [ ] **Step 5: Update `GetTask`, `ListTasks`, `UpdateTask` in `internal/db/db.go`**

Each SELECT and the UPDATE must include `model`. Update all three column lists and `Scan` arg lists. Specifically:

- `GetTask` SELECT: add `model` after `agent`. Scan: add `&task.Model` after `&task.Agent`.
- `ListTasks` SELECT: same change. Same `Scan` change inside the loop.
- `UpdateTask` UPDATE: add `model = ?` after `agent = ?`. Args: add `task.Model` after `task.Agent`.

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/db/...`
Expected: PASS — all tests green including the two new ones.

- [ ] **Step 7: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go
git commit -m "feat(db): persist per-task model column with lazy default resolution"
```

---

## Task 4: Validate (agent, model) on save

**Files:**
- Modify: `internal/db/db.go` (`CreateTask`, `UpdateTask`)
- Modify: `internal/db/db_test.go` (add validation test)

**Why:** Reject invalid combinations at the DB boundary so neither TUI nor API can persist garbage. Backstop for the UI pickers.

- [ ] **Step 1: Write the failing test**

Append to `internal/db/db_test.go`:

```go
func TestCreateTaskRejectsInvalidModel(t *testing.T) {
	d := setupTestDB(t)
	task := &db.Task{
		Name:     "bogus",
		Prompt:   "test",
		Agent:    db.AgentGemini,
		Model:    "gpt-5.4", // wrong agent
		CronExpr: "0 * * * * *",
		Enabled:  true,
	}
	if err := d.CreateTask(task); err == nil {
		t.Fatal("expected error for invalid (agent, model), got nil")
	}
}

func TestUpdateTaskRejectsInvalidModel(t *testing.T) {
	d := setupTestDB(t)
	task := &db.Task{
		Name:     "ok",
		Prompt:   "test",
		Agent:    db.AgentClaude,
		Model:    "claude-sonnet-4-6",
		CronExpr: "0 * * * * *",
		Enabled:  true,
	}
	if err := d.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	task.Model = "not-a-model"
	if err := d.UpdateTask(task); err == nil {
		t.Fatal("expected error for invalid model on update, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/db/...`
Expected: FAIL — invalid models slip through.

- [ ] **Step 3: Add validation hook in `internal/db/db.go`**

At the top of `CreateTask` and `UpdateTask`, add:

```go
import "github.com/kylemclaren/claude-tasks/internal/agent"
```

Then at the start of each function:

```go
// Default agent to claude when unset (back-compat)
if task.Agent == "" {
    task.Agent = agent.Claude
}
if err := agent.Validate(task.Agent, task.Model); err != nil {
    return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/db/...`
Expected: PASS — all tests green.

- [ ] **Step 5: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go
git commit -m "feat(db): validate (agent, model) on CreateTask/UpdateTask"
```

---

## Task 5: Executor — registry-driven command construction

**Files:**
- Modify: `internal/executor/executor.go:44-135` (`Execute` function)
- Create: `internal/executor/executor_test.go`

**Why:** Make the executor agnostic to which CLI is being invoked. Add a `LookPath` preflight so missing binaries fail with a clear message. Keep the Anthropic usage gate but only apply it to claude tasks.

- [ ] **Step 1: Refactor `Execute` to extract a testable command builder**

Edit `internal/executor/executor.go`. Replace the `cmd := exec.CommandContext(ctx, "claude", ...)` block (currently around line 92) with a call to a new method, and add the method below `Execute`:

```go
// buildCommand constructs the *exec.Cmd to invoke the agent CLI for the task.
// Returns an error when the agent is unknown or the binary is not on PATH.
func (e *Executor) buildCommand(ctx context.Context, task *db.Task) (*exec.Cmd, error) {
	spec, ok := agent.Get(task.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", task.Agent)
	}
	if _, err := exec.LookPath(spec.Binary); err != nil {
		return nil, fmt.Errorf("binary %q not found in PATH", spec.Binary)
	}
	args := spec.BuildArgs(task.ResolvedModel(), task.Prompt)
	cmd := exec.CommandContext(ctx, spec.Binary, args...)
	cmd.Dir = task.WorkingDir
	return cmd, nil
}
```

Then in `Execute`, replace the hardcoded command with:

```go
cmd, err := e.buildCommand(ctx, task)
if err != nil {
    endTime := time.Now()
    run.Status = db.RunStatusFailed
    run.Error = err.Error()
    run.EndedAt = &endTime
    _ = e.db.UpdateTaskRun(run)
    task.LastRunAt = &endTime
    _ = e.db.UpdateTask(task)
    return &Result{Error: err, Duration: time.Since(startTime)}
}
```

Add the import: `"github.com/kylemclaren/claude-tasks/internal/agent"`.

- [ ] **Step 2: Gate the usage check on `claude` only**

In `Execute`, wrap the existing `if e.usageClient != nil { ... }` block (lines 49–77) with `if task.Agent == agent.Claude { ... }`. The whole threshold-skip path should only run for claude tasks.

- [ ] **Step 3: Write the executor tests**

Create `internal/executor/executor_test.go`:

```go
package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kylemclaren/claude-tasks/internal/agent"
	"github.com/kylemclaren/claude-tasks/internal/db"
)

// stubBinaryOnPath creates an empty executable named binName in a temp dir
// and prepends that dir to PATH for the duration of the test.
func stubBinaryOnPath(t *testing.T, binName string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, binName)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	old := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+old)
}

func TestBuildCommandClaude(t *testing.T) {
	stubBinaryOnPath(t, "claude")
	e := &Executor{}
	task := &db.Task{Agent: db.AgentClaude, Model: "claude-sonnet-4-6", Prompt: "hello", WorkingDir: "."}
	cmd, err := e.buildCommand(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cmd.Path, "claude") {
		t.Errorf("cmd.Path = %q, want suffix claude", cmd.Path)
	}
	want := []string{"claude", "-p", "--dangerously-skip-permissions", "--model", "claude-sonnet-4-6", "hello"}
	if !equalSlices(cmd.Args, want) {
		t.Errorf("cmd.Args = %v, want %v", cmd.Args, want)
	}
	if cmd.Dir != "." {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, ".")
	}
}

func TestBuildCommandGemini(t *testing.T) {
	stubBinaryOnPath(t, "gemini")
	e := &Executor{}
	task := &db.Task{Agent: db.AgentGemini, Model: "flash", Prompt: "hi", WorkingDir: "."}
	cmd, err := e.buildCommand(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gemini", "-p", "--approval-mode=yolo", "-m", "flash", "hi"}
	if !equalSlices(cmd.Args, want) {
		t.Errorf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

func TestBuildCommandCodex(t *testing.T) {
	stubBinaryOnPath(t, "codex")
	e := &Executor{}
	task := &db.Task{Agent: db.AgentCodex, Model: "gpt-5.4", Prompt: "go", WorkingDir: "."}
	cmd, err := e.buildCommand(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "-m", "gpt-5.4", "go"}
	if !equalSlices(cmd.Args, want) {
		t.Errorf("cmd.Args = %v, want %v", cmd.Args, want)
	}
}

func TestBuildCommandResolvesEmptyModel(t *testing.T) {
	stubBinaryOnPath(t, "gemini")
	e := &Executor{}
	task := &db.Task{Agent: db.AgentGemini, Model: "", Prompt: "x", WorkingDir: "."}
	cmd, err := e.buildCommand(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	// Must contain the gemini default model (flash).
	if !contains(cmd.Args, agent.DefaultModel(agent.Gemini)) {
		t.Errorf("cmd.Args = %v, expected default model %q", cmd.Args, agent.DefaultModel(agent.Gemini))
	}
}

func TestBuildCommandRejectsUnknownAgent(t *testing.T) {
	e := &Executor{}
	task := &db.Task{Agent: db.Agent("openai"), Prompt: "x", WorkingDir: "."}
	if _, err := e.buildCommand(context.Background(), task); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestBuildCommandRejectsMissingBinary(t *testing.T) {
	// PATH explicitly empty so no agent binary can be found.
	t.Setenv("PATH", "")
	e := &Executor{}
	task := &db.Task{Agent: db.AgentClaude, Model: "claude-sonnet-4-6", Prompt: "x", WorkingDir: "."}
	_, err := e.buildCommand(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("error = %v, want one mentioning PATH", err)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run all tests**

Run: `go test ./internal/executor/... ./internal/db/... ./internal/agent/...`
Expected: PASS — all tests green.

- [ ] **Step 5: Commit**

```bash
git add internal/executor/
git commit -m "feat(executor): drive subprocess via agent registry; gate usage check on claude only"
```

---

## Task 6: Webhooks — include `agent@model` in notifications

**Files:**
- Modify: `internal/webhook/discord.go` (find the embed title/footer line)
- Modify: `internal/webhook/slack.go` (find the title/header field)

**Why:** When a webhook arrives in Discord/Slack, the recipient should know which agent/model produced the run.

- [ ] **Step 1: Find the title/footer location in each file**

Run: `grep -n -E "Title|footer|Header" internal/webhook/discord.go internal/webhook/slack.go`

Expected: a few lines pointing at where the embed title or footer is constructed (the existing code formats task name + status).

- [ ] **Step 2: Append the display string to the existing title/footer**

In both files, locate the field that today shows the task name (e.g. `task.Name`) in the embed title or Slack header block. Append `" (" + task.Display() + ")"` so it renders as `Daily Code Review (claude@claude-sonnet-4-6)`. Do not change payload schema — it's a string-only edit.

If the file uses string interpolation, use:

```go
fmt.Sprintf("%s (%s)", task.Name, task.Display())
```

- [ ] **Step 3: Build to confirm it compiles**

Run: `go build ./...`
Expected: build succeeds.

- [ ] **Step 4: Commit**

```bash
git add internal/webhook/
git commit -m "feat(webhook): include agent@model in Discord and Slack notifications"
```

---

## Task 7: REST API — types, validation, and response wiring

**Files:**
- Modify: `internal/api/types.go`
- Modify: `internal/api/handlers.go` (`CreateTask`, `UpdateTask`, `taskToResponse`, `validateTaskRequest`)

**Why:** Surface agent/model to API clients with the same validation guarantees the DB layer enforces, and include `display` in responses so mobile/web don't recompute.

- [ ] **Step 1: Add fields to `internal/api/types.go`**

In `TaskRequest`, add:

```go
Agent string `json:"agent,omitempty"`            // optional; defaults to "claude"
Model string `json:"model,omitempty"`            // optional; defaults to agent's default
```

In `TaskResponse`, add:

```go
Agent   string `json:"agent"`
Model   string `json:"model"`           // always populated (resolved)
Display string `json:"display"`         // "claude@claude-sonnet-4-6"
```

- [ ] **Step 2: Update `taskToResponse` in `internal/api/handlers.go`**

Add inside the `TaskResponse` literal (after the existing fields):

```go
Agent:   string(task.Agent),
Model:   task.ResolvedModel(),
Display: task.Display(),
```

- [ ] **Step 3: Update `validateTaskRequest` in `internal/api/handlers.go`**

After the existing checks, add:

```go
import "github.com/kylemclaren/claude-tasks/internal/agent"
```

Then inside `validateTaskRequest`:

```go
if req.Agent == "" {
    req.Agent = string(agent.Claude)
}
if err := agent.Validate(agent.Name(req.Agent), req.Model); err != nil {
    return validationError(err.Error())
}
```

- [ ] **Step 4: Wire the new fields into task creation/update**

In `CreateTask` (around line 60–68), add to the `db.Task{...}` literal:

```go
Agent: agent.Name(req.Agent),
Model: req.Model,
```

In `UpdateTask` (around line 142–149), add:

```go
task.Agent = agent.Name(req.Agent)
task.Model = req.Model
```

- [ ] **Step 5: Add an API test for validation**

Create or extend `internal/api/handlers_test.go` (create if missing). Minimum coverage:

```go
package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/kylemclaren/claude-tasks/internal/api"
	"github.com/kylemclaren/claude-tasks/internal/db"
)

func newTestServer(t *testing.T) (*api.Server, func()) {
	t.Helper()
	tmp, err := os.CreateTemp("", "api-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	d, err := db.New(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() { d.Close(); os.Remove(tmp.Name()) }
	return api.NewServer(d, nil), cleanup
}

func TestCreateTaskRejectsInvalidModel(t *testing.T) {
	srv, done := newTestServer(t)
	defer done()

	body, _ := json.Marshal(map[string]any{
		"name":     "bad",
		"prompt":   "x",
		"agent":    "gemini",
		"model":    "claude-sonnet-4-6", // wrong agent
		"cron_expr":"0 * * * * *",
		"enabled":  true,
		"working_dir": ".",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d. body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestCreateTaskDefaultsAgentToClaude(t *testing.T) {
	srv, done := newTestServer(t)
	defer done()

	body, _ := json.Marshal(map[string]any{
		"name":     "ok",
		"prompt":   "x",
		"cron_expr":"0 * * * * *",
		"enabled":  true,
		"working_dir": ".",
		// no agent or model
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201. body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["agent"] != "claude" {
		t.Errorf("agent = %v, want \"claude\"", resp["agent"])
	}
	if resp["model"] != "claude-sonnet-4-6" {
		t.Errorf("model = %v, want default \"claude-sonnet-4-6\"", resp["model"])
	}
	if resp["display"] != "claude@claude-sonnet-4-6" {
		t.Errorf("display = %v, want \"claude@claude-sonnet-4-6\"", resp["display"])
	}
}
```

- [ ] **Step 6: Run all tests**

Run: `go test ./...`
Expected: PASS — including the two new API tests.

- [ ] **Step 7: Commit**

```bash
git add internal/api/types.go internal/api/handlers.go internal/api/handlers_test.go
git commit -m "feat(api): expose agent/model on tasks with validation and display"
```

---

## Task 8: REST API — `GET /api/v1/agents` endpoint

**Files:**
- Create: `internal/api/agents.go`
- Modify: `internal/api/api.go` (register the route)
- Modify: `internal/api/handlers_test.go` (add an endpoint test)

**Why:** Mobile and any other clients fetch the list of agents + allowed models from this endpoint instead of hardcoding lists.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/handlers_test.go`:

```go
func TestGetAgents(t *testing.T) {
	srv, done := newTestServer(t)
	defer done()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Agents []struct {
			Name         string   `json:"name"`
			DefaultModel string   `json:"default_model"`
			Models       []string `json:"models"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Agents) != 3 {
		t.Fatalf("got %d agents, want 3", len(resp.Agents))
	}
	for _, a := range resp.Agents {
		if len(a.Models) == 0 || a.Models[0] != a.DefaultModel {
			t.Errorf("agent %s: default_model %q must equal models[0] %v", a.Name, a.DefaultModel, a.Models)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/...`
Expected: FAIL with 404.

- [ ] **Step 3: Implement the handler**

Create `internal/api/agents.go`:

```go
package api

import (
	"net/http"

	"github.com/kylemclaren/claude-tasks/internal/agent"
)

type agentInfo struct {
	Name         string   `json:"name"`
	DefaultModel string   `json:"default_model"`
	Models       []string `json:"models"`
}

type agentListResponse struct {
	Agents []agentInfo `json:"agents"`
}

// ListAgents handles GET /api/v1/agents.
func (s *Server) ListAgents(w http.ResponseWriter, r *http.Request) {
	specs := agent.All()
	resp := agentListResponse{Agents: make([]agentInfo, len(specs))}
	for i, spec := range specs {
		resp.Agents[i] = agentInfo{
			Name:         string(spec.Name),
			DefaultModel: agent.DefaultModel(spec.Name),
			Models:       spec.AllowedModels,
		}
	}
	s.jsonResponse(w, http.StatusOK, resp)
}
```

- [ ] **Step 4: Register the route in `internal/api/api.go`**

Inside the `r.Route("/api/v1", func(r chi.Router) { ... })` block, add (alongside the other top-level routes):

```go
r.Get("/agents", s.ListAgents)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/agents.go internal/api/api.go internal/api/handlers_test.go
git commit -m "feat(api): add GET /api/v1/agents endpoint exposing the agent registry"
```

---

## Task 9: TUI — agent and model picker fields

**Files:**
- Modify: `internal/tui/app.go` — extensive (model state, form fields, key handler, render, save)

**Why:** Users need to choose agent and model when creating/editing a task. Picker style matches the existing TaskType toggle (`←/→` cycles options).

- [ ] **Step 1: Add picker state to the Model struct**

In `internal/tui/app.go`, find the `Add/Edit form` section of the `Model` struct (around line 125). After `formValidation map[int]string`, add:

```go
// Agent + model pickers (state stored directly, not in textinput)
selectedAgent agent.Name
selectedModel string
```

Add the import: `"github.com/kylemclaren/claude-tasks/internal/agent"`.

- [ ] **Step 2: Add new field constants**

In `internal/tui/app.go` around line 171, update the form-field iota block. Insert `fieldAgent` and `fieldModel` between `fieldPrompt` and `fieldTaskType`:

```go
const (
    fieldName = iota
    fieldPrompt
    fieldAgent
    fieldModel
    fieldTaskType
    fieldCron
    fieldScheduleMode
    fieldScheduledAt
    fieldWorkingDir
    fieldDiscordWebhook
    fieldSlackWebhook
    fieldCount
)
```

- [ ] **Step 3: Initialize defaults when opening the form**

Find the place where the form is opened for a new task (around line 461 — `m.isOneOff = false`). Add:

```go
m.selectedAgent = agent.Claude
m.selectedModel = agent.DefaultModel(agent.Claude)
```

In the edit-task block (around line 1030 — where `m.formInputs[fieldName].SetValue(...)` lives), add:

```go
m.selectedAgent = m.editingTask.Agent
if m.selectedAgent == "" {
    m.selectedAgent = agent.Claude
}
m.selectedModel = m.editingTask.Model
if m.selectedModel == "" {
    m.selectedModel = agent.DefaultModel(m.selectedAgent)
}
```

- [ ] **Step 4: Add cycle handlers in the key path**

Find where left/right key handling cycles taskType (around line 1196). Add parallel branches for the new fields. After the `fieldTaskType` and `fieldScheduleMode` handlers in the same switch, add:

```go
if m.formFocus == fieldAgent {
    specs := agent.All()
    idx := 0
    for i, s := range specs {
        if s.Name == m.selectedAgent {
            idx = i
            break
        }
    }
    if msg.String() == "left" {
        idx = (idx - 1 + len(specs)) % len(specs)
    } else {
        idx = (idx + 1) % len(specs)
    }
    m.selectedAgent = specs[idx].Name
    // Reset model to the new agent's default
    m.selectedModel = agent.DefaultModel(m.selectedAgent)
    return m, nil
}

if m.formFocus == fieldModel {
    spec, _ := agent.Get(m.selectedAgent)
    models := spec.AllowedModels
    idx := 0
    for i, mm := range models {
        if mm == m.selectedModel {
            idx = i
            break
        }
    }
    if msg.String() == "left" {
        idx = (idx - 1 + len(models)) % len(models)
    } else {
        idx = (idx + 1) % len(models)
    }
    m.selectedModel = models[idx]
    return m, nil
}
```

(Match the surrounding code style. Use the actual key-detection idiom present in the file — the snippet above assumes `msg.String()`; if the existing toggle uses `key.Matches(...)` or a direct rune compare, mirror that.)

- [ ] **Step 5: Make tab navigation skip nothing for the new fields**

In `getNextFormField` (around line 485), the existing `switch` lists fields that are always visible. Add `fieldAgent, fieldModel` to the always-visible case (the same case that includes `fieldName, fieldPrompt, fieldTaskType, fieldWorkingDir, ...`):

```go
case fieldName, fieldPrompt, fieldAgent, fieldModel, fieldTaskType, fieldWorkingDir, fieldDiscordWebhook, fieldSlackWebhook:
    return true
```

- [ ] **Step 6: Suppress textinput updates while focused on the new fields**

Find the block around line 1247 that already excludes `fieldTaskType` and `fieldScheduleMode` from textinput updates:

```go
} else if m.formFocus != fieldTaskType && m.formFocus != fieldScheduleMode {
```

Extend to:

```go
} else if m.formFocus != fieldTaskType && m.formFocus != fieldScheduleMode &&
    m.formFocus != fieldAgent && m.formFocus != fieldModel {
```

- [ ] **Step 7: Persist selections on save**

In the save block (around line 1308–1325, where the `db.Task{...}` literal is built), add to that literal:

```go
Agent: m.selectedAgent,
Model: m.selectedModel,
```

- [ ] **Step 8: Render the new pickers**

In `renderForm` (around line 1750, just after the Prompt block and before the Task Type block), add:

```go
// Agent picker
b.WriteString(inputLabelStyle.Render("Agent"))
b.WriteString("  ")
b.WriteString(subtitleStyle.Render("(←/→ to change)"))
b.WriteString("\n")
{
    var parts []string
    for _, spec := range agent.All() {
        label := string(spec.Name)
        if spec.Name == m.selectedAgent {
            label = "[" + label + "]"
        }
        parts = append(parts, label)
    }
    renderFocused(strings.Join(parts, "  "), m.formFocus == fieldAgent)
}

// Model picker (depends on selectedAgent)
b.WriteString(inputLabelStyle.Render("Model"))
b.WriteString("  ")
b.WriteString(subtitleStyle.Render("(←/→ to change)"))
b.WriteString("\n")
{
    spec, _ := agent.Get(m.selectedAgent)
    var parts []string
    for _, mm := range spec.AllowedModels {
        label := mm
        if mm == m.selectedModel {
            label = "[" + label + "]"
        }
        parts = append(parts, label)
    }
    renderFocused(strings.Join(parts, "  "), m.formFocus == fieldModel)
}
```

- [ ] **Step 9: Surface DB validation errors on save**

The existing save flow calls `m.db.UpdateTask(task)` / `m.db.CreateTask(task)`. If validation fails, that error already propagates. Confirm the existing path sets `m.statusMsg = err.Error()` and `m.statusErr = true`. If not, add:

```go
if err := m.db.CreateTask(task); err != nil {
    m.statusMsg = err.Error()
    m.statusErr = true
    return m, nil
}
```

(Adjust to match the actual save command — search for the existing CreateTask/UpdateTask calls and mirror their error handling.)

- [ ] **Step 10: Build and test**

Run: `go build ./... && go test ./...`
Expected: build + tests pass.

- [ ] **Step 11: Commit**

```bash
git add internal/tui/app.go
git commit -m "feat(tui): add agent and model picker fields to add/edit form"
```

---

## Task 10: TUI — show `agent@model` in the task list

**Files:**
- Modify: `internal/tui/app.go` — `calculateTableColumns` (around line 198) and the row-render path (search for the body of `renderList`).

**Why:** Users need to see which agent/model each task uses without opening it.

- [ ] **Step 1: Add an Agent column to the table layout**

In `calculateTableColumns` (line 198), add an `Agent` column. Adjust the percentages:

```go
// Was: Name 25%, Schedule 20%, Status 12%(fixed), Next 20%, Last 20%
// Now: Name 22%, Agent 18%, Schedule 18%, Status 10%(fixed), Next 16%, Last 16%
statusWidth := 10
agentWidth := 18 // proportional below
remaining := availableWidth - statusWidth - 10 // 10 for column separators (now 6 cols)

nameWidth := remaining * 22 / 90
agentWidth = remaining * 18 / 90
scheduleWidth := remaining * 18 / 90
nextWidth := remaining * 16 / 90
lastWidth := remaining * 16 / 90

// Minimum widths
if nameWidth < 12 { nameWidth = 12 }
if agentWidth < 14 { agentWidth = 14 }
if scheduleWidth < 14 { scheduleWidth = 14 }
if nextWidth < 12 { nextWidth = 12 }
if lastWidth < 12 { lastWidth = 12 }

return []table.Column{
    {Title: "Name", Width: nameWidth},
    {Title: "Agent", Width: agentWidth},
    {Title: "Schedule", Width: scheduleWidth},
    {Title: "Status", Width: statusWidth},
    {Title: "Next Run", Width: nextWidth},
    {Title: "Last Run", Width: lastWidth},
}
```

- [ ] **Step 2: Insert the Agent cell into row construction**

Find where the `table.Row` literals are built (search `table.Row{` in `app.go`). The current 5-element row matches the 5 columns. Add an `agent.ShortDisplay(task.Agent, task.Model)` cell right after the Name cell so it aligns with the new column order:

```go
row := table.Row{
    truncateName(task.Name, nameWidth),
    agent.ShortDisplay(task.Agent, task.Model),
    scheduleCell,
    statusCell,
    nextCell,
    lastCell,
}
```

(Variable names match what's already in the file. The exact construction expression for each cell stays unchanged — only insert the agent cell.)

- [ ] **Step 3: Build and check the TUI manually**

Run: `go build -o claude-tasks ./cmd/claude-tasks && ./claude-tasks`

Expected: TUI launches; the list shows an Agent column with `claude@sonnet-4-6` for existing rows. (Existing rows stored with empty model resolve to default.) Press `q` to exit.

If the manual check is impractical (no TTY in CI), at minimum confirm `go build ./...` and `go test ./...` pass.

- [ ] **Step 4: Commit**

```bash
git add internal/tui/app.go
git commit -m "feat(tui): show agent@model column in task list"
```

---

## Task 11: Mobile — TS types and agents API client

**Files:**
- Modify: `mobile/lib/types.ts`
- Modify: `mobile/lib/api.ts`

**Why:** Mobile needs typed access to the new fields and the new endpoint before any UI surface can use them.

- [ ] **Step 1: Add types in `mobile/lib/types.ts`**

Append:

```ts
export interface Task {
  id: number;
  name: string;
  prompt: string;
  agent: string;
  model: string;
  display: string;       // "claude@claude-sonnet-4-6"
  cron_expr: string;
  scheduled_at?: string;
  is_one_off: boolean;
  working_dir: string;
  discord_webhook?: string;
  slack_webhook?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  last_run_at?: string;
  next_run_at?: string;
  last_run_status?: 'pending' | 'running' | 'completed' | 'failed';
}

export interface TaskRequest {
  name: string;
  prompt: string;
  agent?: string;        // defaults to "claude" server-side
  model?: string;        // defaults to agent's default server-side
  cron_expr: string;
  scheduled_at?: string;
  working_dir: string;
  discord_webhook?: string;
  slack_webhook?: string;
  enabled: boolean;
}

export interface AgentInfo {
  name: string;
  default_model: string;
  models: string[];      // first entry equals default_model
}

export interface AgentListResponse {
  agents: AgentInfo[];
}
```

(Replace the existing `Task` and `TaskRequest` interfaces — the new versions are supersets.)

- [ ] **Step 2: Add the client method in `mobile/lib/api.ts`**

Inside the `ApiClient` class (after `getUsage`):

```ts
  async getAgents(): Promise<AgentListResponse> {
    return this.request('/agents');
  }
```

Add `AgentListResponse` to the import block at the top:

```ts
import type {
  // ... existing ...
  AgentListResponse,
} from './types';
```

- [ ] **Step 3: Build/typecheck mobile**

```bash
cd mobile
npm run typecheck 2>/dev/null || npx tsc --noEmit
```

Expected: no type errors. If the mobile tree has no typecheck script, `npx tsc --noEmit` from `mobile/` is the canonical check.

- [ ] **Step 4: Commit**

```bash
git add mobile/lib/types.ts mobile/lib/api.ts
git commit -m "feat(mobile): add agent/model TS types and getAgents API client"
```

---

## Task 12: Mobile — agent/model dropdowns in add/edit screens

**Files:**
- Modify: the add-task and edit-task screens under `mobile/app/task/` (locate via Step 1)
- Possibly: new `mobile/components/AgentModelPicker.tsx` if a shared picker makes sense

**Why:** Surface the new fields to the user.

- [ ] **Step 1: Locate the add/edit screens**

Run: `ls mobile/app/task/` and read each `.tsx` file inside to find which one renders the create/edit form. Look for existing form fields like `name`, `prompt`, `cron_expr`. The screens were added in commits `34f6c88` (one-off) and `5116fd0` (edit task) — `git log --oneline mobile/app/task/` will show the relevant files.

- [ ] **Step 2: Fetch agents on screen mount**

In each form screen, add a state hook:

```tsx
import type { AgentInfo } from '@/lib/types';
import { apiClient } from '@/lib/api';
import { useEffect, useState } from 'react';

const [agents, setAgents] = useState<AgentInfo[]>([]);
const [agent, setAgent] = useState<string>('claude');
const [model, setModel] = useState<string>('');

useEffect(() => {
  apiClient.getAgents().then(r => {
    setAgents(r.agents);
    // Initialize defaults — for new task: claude + its default; for edit: existing values.
    if (existingTask) {
      setAgent(existingTask.agent || 'claude');
      setModel(existingTask.model || '');
    } else {
      const claude = r.agents.find(a => a.name === 'claude');
      if (claude) {
        setAgent(claude.name);
        setModel(claude.default_model);
      }
    }
  }).catch(err => console.warn('failed to load agents', err));
}, []);
```

- [ ] **Step 3: Render the dropdowns**

Use whatever dropdown component the rest of the app uses (search the file for an existing picker — likely a `TouchableOpacity`-based selector or `Picker` from `@react-native-picker/picker`). Add two dropdowns in the form layout, between Prompt and Working Directory:

```tsx
<Text style={styles.label}>Agent</Text>
<Picker
  selectedValue={agent}
  onValueChange={(v) => {
    setAgent(v);
    const next = agents.find(a => a.name === v);
    if (next) setModel(next.default_model);
  }}>
  {agents.map(a => <Picker.Item key={a.name} label={a.name} value={a.name} />)}
</Picker>

<Text style={styles.label}>Model</Text>
<Picker
  selectedValue={model}
  onValueChange={setModel}>
  {(agents.find(a => a.name === agent)?.models ?? []).map(m =>
    <Picker.Item key={m} label={m} value={m} />)}
</Picker>
```

(If the project uses a custom dropdown, mirror its API. Don't introduce a new picker library.)

- [ ] **Step 4: Wire into the save payload**

In the existing `apiClient.createTask({...})` / `apiClient.updateTask(id, {...})` call site, add `agent, model` to the request object:

```ts
agent,
model,
```

- [ ] **Step 5: Typecheck and run**

```bash
cd mobile
npx tsc --noEmit
npm start  # spot-check the form in Expo
```

Expected: typecheck clean; both pickers render; switching agent updates the model list and pre-selects the agent's default.

- [ ] **Step 6: Commit**

```bash
git add mobile/app/task/
git commit -m "feat(mobile): add agent/model dropdowns to add and edit task screens"
```

---

## Task 13: Mobile — show `agent@model` chip on task cards

**Files:**
- Modify: `mobile/components/TaskCard.tsx`

**Why:** Quick visual reference of which agent/model each task uses.

- [ ] **Step 1: Read the existing card layout**

Read `mobile/components/TaskCard.tsx` to find where task metadata (cron expression, working directory, last run status) is rendered.

- [ ] **Step 2: Add a chip showing `task.display`**

Insert a styled `<Text>` next to the existing meta row:

```tsx
{task.display ? (
  <Text style={styles.agentChip}>{task.display}</Text>
) : null}
```

Add to the `StyleSheet.create({...})` block:

```ts
agentChip: {
  fontSize: 11,
  fontFamily: 'monospace',
  color: theme.colors.muted,
  paddingHorizontal: 6,
  paddingVertical: 2,
  borderRadius: 4,
  backgroundColor: theme.colors.chipBg,
},
```

(Use whatever color/background tokens already exist on `theme.colors` in `mobile/lib/theme.ts`. Don't introduce new tokens — reuse what's there.)

- [ ] **Step 3: Typecheck and visual verify**

```bash
cd mobile
npx tsc --noEmit
npm start  # confirm the chip renders on each task card
```

- [ ] **Step 4: Commit**

```bash
git add mobile/components/TaskCard.tsx
git commit -m "feat(mobile): show agent@model chip on task cards"
```

---

## Task 14: End-to-end smoke

**Files:** None (manual + script).

**Why:** Confirm the change works end-to-end across the three operating modes.

- [ ] **Step 1: Build and start the API server**

```bash
go build -o claude-tasks ./cmd/claude-tasks
./claude-tasks serve --port 8080 &
SERVE_PID=$!
sleep 1
```

- [ ] **Step 2: Verify the agents endpoint**

```bash
curl -s http://localhost:8080/api/v1/agents | python3 -m json.tool
```

Expected: JSON listing 3 agents with model arrays whose first element matches `default_model`.

- [ ] **Step 3: Create a task with each agent**

```bash
for AGENT in claude gemini codex; do
  curl -s -X POST http://localhost:8080/api/v1/tasks \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"test-$AGENT\",\"prompt\":\"hi\",\"agent\":\"$AGENT\",\"cron_expr\":\"0 0 * * * *\",\"working_dir\":\".\",\"enabled\":false}" \
    | python3 -m json.tool
done
```

Expected: each response has `agent`, `model` (the agent's default), and `display` populated.

- [ ] **Step 4: Reject a bad model**

```bash
curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H "Content-Type: application/json" \
  -d '{"name":"bad","prompt":"x","agent":"gemini","model":"claude-opus-4-7","cron_expr":"0 * * * * *","working_dir":".","enabled":false}'
```

Expected: HTTP 400 with an error message naming the bad model.

- [ ] **Step 5: Open the TUI and confirm the column + form pickers**

```bash
kill $SERVE_PID
./claude-tasks
```

Expected: list shows the three test rows with their `agent@model` shorthand. Press `e` on one — the form has Agent and Model picker rows that cycle with `←/→`. Press `esc` to exit, `q` to quit.

- [ ] **Step 6: Cleanup test rows**

```bash
sqlite3 ~/.claude-tasks/tasks.db "DELETE FROM tasks WHERE name LIKE 'test-%'"
```

- [ ] **Step 7: Commit (only if step 5 surfaced any tweaks)**

If any small fixes were needed, commit them now with a descriptive message. Otherwise no commit for this task.

---

## Self-Review Notes

- **Spec coverage** — each spec section maps to a task: registry → T1, DB schema → T2/T3/T4, executor → T5, webhooks → T6, REST API types → T7, `/agents` endpoint → T8, TUI form → T9, TUI list → T10, mobile types/api → T11, mobile dropdowns → T12, mobile chip → T13.
- **Type consistency** — `Task.Model` field name used identically across DB / API / TS types; `display` field name matches between API response and TS type and TUI helper. `selectedAgent` / `selectedModel` are the only two internal state names introduced and they are used consistently in Task 9.
- **No placeholders** — all code blocks contain runnable code (or precise edit instructions when modifying long existing files like `app.go`). The only "find this in the file" instructions are for surgical edits inside files too large to repeat verbatim, and each gives an anchor (line range or distinctive string) so the editor can locate it.
