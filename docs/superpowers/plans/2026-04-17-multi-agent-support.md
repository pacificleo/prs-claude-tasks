# Multi-Agent Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `agent` field to tasks so each task can run via `claude`, `gemini`, or `codex` CLI, with the executor building the correct subprocess command per agent.

**Architecture:** Add an `Agent` type and field to the `Task` model; migrate the DB; route command building through a new exported `BuildCommand(task)` function in the executor; thread the field through all layers (TUI form, REST API, mobile app).

**Tech Stack:** Go 1.24, SQLite (mattn/go-sqlite3), Bubble Tea TUI, chi HTTP router, React Native / Expo (TypeScript).

---

## File Map

| File | Change |
|------|--------|
| `internal/db/models.go` | Add `Agent` type + constants, add `Agent Agent` field to `Task` |
| `internal/db/db.go` | DB migration; update all queries to include `agent` column; default to `"claude"` |
| `internal/db/db_test.go` | **New** — test CreateTask/GetTask with agent, default agent |
| `internal/executor/executor.go` | Export `BuildCommand(task)`; call it in `Execute`; skip Claude usage check for non-claude agents |
| `internal/executor/executor_test.go` | **New** — unit-test `BuildCommand` for all three agents |
| `internal/tui/app.go` | Add `fieldAgent` constant; `selectedAgent db.Agent` field on `Model`; agent toggle in form render/save/load/reset |
| `internal/api/types.go` | Add `Agent` to `TaskRequest` and `TaskResponse` |
| `internal/api/handlers.go` | Propagate agent in `CreateTask`, `UpdateTask`, `taskToResponse` |
| `mobile/lib/types.ts` | Add `agent` to `Task` and `TaskRequest` |
| `mobile/app/task/new.tsx` | Add segmented agent selector |
| `mobile/app/task/edit/[id].tsx` | Add segmented agent selector |

---

## Task 1: DB layer — Agent type and migration

**Files:**
- Modify: `internal/db/models.go`
- Modify: `internal/db/db.go`
- Create: `internal/db/db_test.go`

- [ ] **Step 1: Add Agent type to models.go**

Replace the existing `models.go` content for the `Task` struct area. Add the `Agent` type above the existing `RunStatus` block and add `Agent Agent` to `Task`:

```go
// Agent represents the CLI agent to use for task execution
type Agent string

const (
	AgentClaude Agent = "claude"
	AgentGemini Agent = "gemini"
	AgentCodex  Agent = "codex"
)

// Task represents a scheduled Claude task
type Task struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Prompt         string     `json:"prompt"`
	Agent          Agent      `json:"agent"`                   // CLI agent: claude, gemini, codex
	CronExpr       string     `json:"cron_expr"`                // Empty for one-off tasks
	ScheduledAt    *time.Time `json:"scheduled_at,omitempty"`   // When one-off task should run
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

- [ ] **Step 2: Add DB migration for agent column in db.go**

In `db.go`, after the existing migrations at the bottom of `migrate()`, add:

```go
// Migration: Add agent column for multi-agent support
_, _ = db.conn.Exec("ALTER TABLE tasks ADD COLUMN agent TEXT NOT NULL DEFAULT 'claude'")
```

- [ ] **Step 3: Update all queries in db.go to include agent column**

Update `CreateTask`, `GetTask`, `ListTasks`, and `UpdateTask` to include `agent`.

In `CreateTask`:
```go
result, err := db.conn.Exec(`
    INSERT INTO tasks (name, prompt, agent, cron_expr, scheduled_at, working_dir, discord_webhook, slack_webhook, enabled, created_at, updated_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, task.Name, task.Prompt, task.Agent, task.CronExpr, task.ScheduledAt, task.WorkingDir, task.DiscordWebhook, task.SlackWebhook, task.Enabled, time.Now(), time.Now())
```

In `GetTask`, update SELECT and Scan to include agent (add after `prompt`):
```go
err := db.conn.QueryRow(`
    SELECT id, name, prompt, agent, cron_expr, scheduled_at, working_dir, discord_webhook, slack_webhook, enabled, created_at, updated_at, last_run_at, next_run_at
    FROM tasks WHERE id = ?
`, id).Scan(&task.ID, &task.Name, &task.Prompt, &task.Agent, &task.CronExpr, &task.ScheduledAt, &task.WorkingDir, &task.DiscordWebhook, &task.SlackWebhook, &task.Enabled, &task.CreatedAt, &task.UpdatedAt, &task.LastRunAt, &task.NextRunAt)
```

In `ListTasks`, update SELECT and Scan the same way:
```go
rows, err := db.conn.Query(`
    SELECT id, name, prompt, agent, cron_expr, scheduled_at, working_dir, discord_webhook, slack_webhook, enabled, created_at, updated_at, last_run_at, next_run_at
    FROM tasks ORDER BY created_at DESC
`)
// ...
err := rows.Scan(&task.ID, &task.Name, &task.Prompt, &task.Agent, &task.CronExpr, &task.ScheduledAt, &task.WorkingDir, &task.DiscordWebhook, &task.SlackWebhook, &task.Enabled, &task.CreatedAt, &task.UpdatedAt, &task.LastRunAt, &task.NextRunAt)
```

In `UpdateTask`:
```go
_, err := db.conn.Exec(`
    UPDATE tasks SET name = ?, prompt = ?, agent = ?, cron_expr = ?, scheduled_at = ?, working_dir = ?, discord_webhook = ?, slack_webhook = ?, enabled = ?, updated_at = ?, last_run_at = ?, next_run_at = ?
    WHERE id = ?
`, task.Name, task.Prompt, task.Agent, task.CronExpr, task.ScheduledAt, task.WorkingDir, task.DiscordWebhook, task.SlackWebhook, task.Enabled, task.UpdatedAt, task.LastRunAt, task.NextRunAt, task.ID)
```

- [ ] **Step 4: Write db_test.go**

Create `internal/db/db_test.go`:

```go
package db_test

import (
	"os"
	"testing"

	"github.com/kylemclaren/claude-tasks/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "claude-tasks-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	d, err := db.New(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCreateAndGetTaskWithAgent(t *testing.T) {
	d := setupTestDB(t)

	task := &db.Task{
		Name:     "gemini task",
		Prompt:   "summarize recent changes",
		Agent:    db.AgentGemini,
		CronExpr: "0 * * * * *",
		Enabled:  true,
	}
	if err := d.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	got, err := d.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Agent != db.AgentGemini {
		t.Errorf("Agent = %q, want %q", got.Agent, db.AgentGemini)
	}
	if got.Name != task.Name {
		t.Errorf("Name = %q, want %q", got.Name, task.Name)
	}
}

func TestDefaultAgentIsClaude(t *testing.T) {
	d := setupTestDB(t)

	task := &db.Task{
		Name:     "default task",
		Prompt:   "hello",
		CronExpr: "0 * * * * *",
		Enabled:  true,
		// Agent intentionally not set
	}
	if err := d.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := d.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	// Empty string is also acceptable; the DB default fills it in only if agent="" is treated as default
	if got.Agent != db.AgentClaude && got.Agent != "" {
		t.Errorf("Agent = %q, want %q or empty string", got.Agent, db.AgentClaude)
	}
}

func TestListTasksPreservesAgent(t *testing.T) {
	d := setupTestDB(t)

	agents := []db.Agent{db.AgentClaude, db.AgentGemini, db.AgentCodex}
	for i, agent := range agents {
		task := &db.Task{
			Name:     "task",
			Prompt:   "prompt",
			Agent:    agent,
			CronExpr: "0 * * * * *",
			Enabled:  true,
		}
		if err := d.CreateTask(task); err != nil {
			t.Fatalf("CreateTask[%d]: %v", i, err)
		}
	}

	tasks, err := d.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("ListTasks returned %d tasks, want 3", len(tasks))
	}
	// Tasks ordered by created_at DESC so agents[2] is first
	want := []db.Agent{db.AgentCodex, db.AgentGemini, db.AgentClaude}
	for i, task := range tasks {
		if task.Agent != want[i] {
			t.Errorf("tasks[%d].Agent = %q, want %q", i, task.Agent, want[i])
		}
	}
}

func TestUpdateTaskAgent(t *testing.T) {
	d := setupTestDB(t)

	task := &db.Task{
		Name:     "task",
		Prompt:   "prompt",
		Agent:    db.AgentClaude,
		CronExpr: "0 * * * * *",
		Enabled:  true,
	}
	if err := d.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	task.Agent = db.AgentCodex
	if err := d.UpdateTask(task); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	got, err := d.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Agent != db.AgentCodex {
		t.Errorf("Agent = %q, want %q", got.Agent, db.AgentCodex)
	}
}
```

- [ ] **Step 5: Run tests to verify they fail (db layer not yet wired)**

```bash
cd /Volumes/Personal/code/prs-claude-tasks
go test -v ./internal/db/...
```

Expected: Tests will fail because the agent column isn't in the INSERT/SELECT queries yet (you've added migration but not updated queries yet — or if you did all steps in order, they should pass here).

- [ ] **Step 6: Verify tests pass after all db.go changes**

```bash
go test -v ./internal/db/...
```

Expected: all 4 tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/db/models.go internal/db/db.go internal/db/db_test.go
git commit -m "feat: add Agent field to Task model with DB migration and tests"
```

---

## Task 2: Executor — command building per agent

**Files:**
- Modify: `internal/executor/executor.go`
- Create: `internal/executor/executor_test.go`

- [ ] **Step 1: Write the failing test for BuildCommand**

Create `internal/executor/executor_test.go`:

```go
package executor_test

import (
	"testing"

	"github.com/kylemclaren/claude-tasks/internal/db"
	"github.com/kylemclaren/claude-tasks/internal/executor"
)

func TestBuildCommand(t *testing.T) {
	tests := []struct {
		name     string
		agent    db.Agent
		prompt   string
		wantCmd  string
		wantArgs []string
	}{
		{
			name:     "claude agent",
			agent:    db.AgentClaude,
			prompt:   "review recent changes",
			wantCmd:  "claude",
			wantArgs: []string{"-p", "--dangerously-skip-permissions", "review recent changes"},
		},
		{
			name:     "empty agent defaults to claude",
			agent:    "",
			prompt:   "review recent changes",
			wantCmd:  "claude",
			wantArgs: []string{"-p", "--dangerously-skip-permissions", "review recent changes"},
		},
		{
			name:     "gemini agent",
			agent:    db.AgentGemini,
			prompt:   "summarize the codebase",
			wantCmd:  "gemini",
			wantArgs: []string{"-p", "summarize the codebase"},
		},
		{
			name:     "codex agent",
			agent:    db.AgentCodex,
			prompt:   "fix the failing tests",
			wantCmd:  "codex",
			wantArgs: []string{"--approval-mode", "full-auto", "fix the failing tests"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &db.Task{Prompt: tt.prompt, Agent: tt.agent}
			gotCmd, gotArgs := executor.BuildCommand(task)

			if gotCmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", gotCmd, tt.wantCmd)
			}
			if len(gotArgs) != len(tt.wantArgs) {
				t.Errorf("args = %v, want %v", gotArgs, tt.wantArgs)
				return
			}
			for i := range gotArgs {
				if gotArgs[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], tt.wantArgs[i])
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -v ./internal/executor/...
```

Expected: FAIL — `executor.BuildCommand` undefined.

- [ ] **Step 3: Add BuildCommand to executor.go and update Execute**

In `internal/executor/executor.go`, add `BuildCommand` as an exported function and update `Execute` to use it. Also skip the Claude usage check for non-claude agents.

Add after the imports block, before `Execute`:

```go
// BuildCommand returns the executable name and arguments for the given task's agent.
// Exported for testing.
func BuildCommand(task *db.Task) (string, []string) {
	switch task.Agent {
	case db.AgentGemini:
		return "gemini", []string{"-p", task.Prompt}
	case db.AgentCodex:
		return "codex", []string{"--approval-mode", "full-auto", task.Prompt}
	default: // AgentClaude or empty string — backward compatible
		return "claude", []string{"-p", "--dangerously-skip-permissions", task.Prompt}
	}
}
```

In `Execute`, replace the usage check guard with an agent-aware version:

```go
// Only check Claude API usage for claude tasks
if e.usageClient != nil && (task.Agent == db.AgentClaude || task.Agent == "") {
    threshold, _ := e.db.GetUsageThreshold()
    ok, usageData, err := e.usageClient.CheckThreshold(threshold)
    if err == nil && !ok {
        // ... existing skip logic unchanged ...
    }
}
```

Replace the hardcoded command construction:

```go
// Was:
// cmd := exec.CommandContext(ctx, "claude", "-p", "--dangerously-skip-permissions", task.Prompt)

// Now:
cmdName, cmdArgs := BuildCommand(task)
cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -v ./internal/executor/...
```

Expected: all 4 TestBuildCommand subtests PASS.

- [ ] **Step 5: Verify everything still compiles**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/executor/executor.go internal/executor/executor_test.go
git commit -m "feat: route executor command building per agent (claude/gemini/codex)"
```

---

## Task 3: TUI form — agent selector

**Files:**
- Modify: `internal/tui/app.go`

This is the largest single change. The form uses integer constants for fields; we insert `fieldAgent` after `fieldTaskType`, shifting subsequent constants by 1.

- [ ] **Step 1: Update field constants**

In `app.go`, replace the field constants block:

```go
// Form field indices
const (
	fieldName = iota
	fieldPrompt
	fieldTaskType      // "Recurring" or "One-off"
	fieldAgent         // "claude", "gemini", "codex"  ← NEW
	fieldCron          // Only shown for recurring tasks
	fieldScheduleMode  // "Run Now" or "Schedule for" - only for one-off
	fieldScheduledAt   // Datetime input - only for scheduled one-off
	fieldWorkingDir
	fieldDiscordWebhook
	fieldSlackWebhook
	fieldCount
)
```

- [ ] **Step 2: Add selectedAgent field to Model struct**

In the `Model` struct, add `selectedAgent` next to the other task-type state fields:

```go
// Task type (0 = recurring, 1 = one-off)
isOneOff      bool
runNow        bool
scheduledAt   textinput.Model
selectedAgent db.Agent // which CLI agent to use
```

- [ ] **Step 3: Update initFormInputs to initialise selectedAgent and add placeholder for fieldAgent**

In `initFormInputs()`, after the `fieldTaskType` placeholder, add:

```go
// Agent selector placeholder (not a real input, rendered as toggle)
m.formInputs[fieldAgent] = textinput.New()
m.formInputs[fieldAgent].Width = inputWidth
```

Also reset the selected agent:

```go
// Reset task type state
m.isOneOff = false
m.runNow = true
m.selectedAgent = db.AgentClaude  // ← add this line
```

- [ ] **Step 4: Update shouldShowField to always show fieldAgent**

In `shouldShowField`:

```go
case fieldName, fieldPrompt, fieldTaskType, fieldAgent, fieldWorkingDir, fieldDiscordWebhook, fieldSlackWebhook:
    return true
```

- [ ] **Step 5: Update updateForm to handle left/right on fieldAgent**

In `updateForm`, within the `case "left", "right", "h", "l":` block, add:

```go
if m.formFocus == fieldAgent {
    agents := []db.Agent{db.AgentClaude, db.AgentGemini, db.AgentCodex}
    for i, a := range agents {
        if a == m.selectedAgent {
            if msg.String() == "left" || msg.String() == "h" {
                m.selectedAgent = agents[(i-1+len(agents))%len(agents)]
            } else {
                m.selectedAgent = agents[(i+1)%len(agents)]
            }
            m.validateForm()
            return m, nil
        }
    }
}
```

Also in `updateForm`, in the non-toggle input update block, add `fieldAgent` to the "don't treat as text input" list:

```go
} else if m.formFocus != fieldTaskType && m.formFocus != fieldScheduleMode && m.formFocus != fieldAgent {
    m.formInputs[m.formFocus], cmd = m.formInputs[m.formFocus].Update(msg)
}
```

- [ ] **Step 6: Update renderForm to show agent toggle**

In `renderForm`, after the Task Type toggle block and before the conditional cron/schedule section, add:

```go
// Agent selector
b.WriteString(inputLabelStyle.Render("Agent"))
b.WriteString("  ")
b.WriteString(subtitleStyle.Render("(←/→ to change)"))
b.WriteString("\n")
{
    claudeLabel := "Claude"
    geminiLabel := "Gemini"
    codexLabel  := "Codex"
    switch m.selectedAgent {
    case db.AgentGemini:
        geminiLabel = "[" + geminiLabel + "]"
    case db.AgentCodex:
        codexLabel = "[" + codexLabel + "]"
    default:
        claudeLabel = "[" + claudeLabel + "]"
    }
    toggleContent := claudeLabel + "  " + geminiLabel + "  " + codexLabel
    renderFocused(toggleContent, m.formFocus == fieldAgent)
}
```

- [ ] **Step 7: Update saveTask to include agent**

In `saveTask()`, in the `task := &db.Task{...}` literal, add:

```go
task := &db.Task{
    Name:           name,
    Prompt:         prompt,
    Agent:          m.selectedAgent,   // ← add this
    WorkingDir:     workingDir,
    DiscordWebhook: discordWebhook,
    SlackWebhook:   slackWebhook,
    Enabled:        true,
}
```

- [ ] **Step 8: Update edit task loading to set selectedAgent**

In `updateList`, in the `case "e":` block, after `m.isOneOff = m.editingTask.IsOneOff()`, add:

```go
m.selectedAgent = m.editingTask.Agent
if m.selectedAgent == "" {
    m.selectedAgent = db.AgentClaude
}
```

- [ ] **Step 9: Update resetForm to clear selectedAgent**

In `resetForm()`:

```go
func (m *Model) resetForm() {
    m.initFormInputs()
    m.formFocus = 0
    m.formInputs[fieldName].Focus()
    m.editingTask = nil
    m.isOneOff = false
    m.runNow = true
    m.selectedAgent = db.AgentClaude  // ← add this
}
```

- [ ] **Step 10: Build and verify no compile errors**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 11: Manual smoke test**

```bash
./claude-tasks
```

- Press `a` to add a task
- Tab through fields; verify "Agent" field appears after "Task Type"
- Press left/right on Agent field; verify it cycles Claude → Gemini → Codex → Claude
- Fill in Name, Prompt, Cron, save with ctrl+s
- Press `e` on the saved task; verify Agent field shows the saved agent
- Verify existing tasks (agent="" in DB) load correctly as Claude

- [ ] **Step 12: Commit**

```bash
git add internal/tui/app.go
git commit -m "feat: add agent selector toggle to TUI task form"
```

---

## Task 4: REST API — agent field

**Files:**
- Modify: `internal/api/types.go`
- Modify: `internal/api/handlers.go`

- [ ] **Step 1: Add Agent to API types**

In `types.go`, update `TaskRequest` and `TaskResponse`:

```go
// TaskRequest represents a task creation/update request
type TaskRequest struct {
	Name           string  `json:"name"`
	Prompt         string  `json:"prompt"`
	Agent          string  `json:"agent,omitempty"`           // ← add: "claude", "gemini", "codex"
	CronExpr       string  `json:"cron_expr"`
	ScheduledAt    *string `json:"scheduled_at,omitempty"`
	WorkingDir     string  `json:"working_dir"`
	DiscordWebhook string  `json:"discord_webhook,omitempty"`
	SlackWebhook   string  `json:"slack_webhook,omitempty"`
	Enabled        bool    `json:"enabled"`
}

// TaskResponse represents a task in API responses
type TaskResponse struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Prompt         string     `json:"prompt"`
	Agent          string     `json:"agent"`                     // ← add
	CronExpr       string     `json:"cron_expr"`
	// ... all other fields unchanged ...
}
```

- [ ] **Step 2: Update handlers.go to propagate agent**

In `CreateTask`, in the `task := &db.Task{...}` literal:

```go
task := &db.Task{
    Name:           req.Name,
    Prompt:         req.Prompt,
    Agent:          db.Agent(req.Agent),   // ← add; empty string handled as claude in executor
    CronExpr:       req.CronExpr,
    WorkingDir:     req.WorkingDir,
    DiscordWebhook: req.DiscordWebhook,
    SlackWebhook:   req.SlackWebhook,
    Enabled:        req.Enabled,
}
```

In `UpdateTask`, in the field-update block:

```go
task.Name = req.Name
task.Prompt = req.Prompt
task.Agent = db.Agent(req.Agent)   // ← add
task.CronExpr = req.CronExpr
// ... rest unchanged ...
```

In `taskToResponse`, add:

```go
resp := TaskResponse{
    ID:             task.ID,
    Name:           task.Name,
    Prompt:         task.Prompt,
    Agent:          string(task.Agent),    // ← add
    CronExpr:       task.CronExpr,
    // ... rest unchanged ...
}
```

- [ ] **Step 3: Build to verify no compile errors**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 4: Smoke-test API**

```bash
# Terminal 1
./claude-tasks serve

# Terminal 2 — create a gemini task
curl -s -X POST http://localhost:8080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"name":"gemini test","prompt":"list files","agent":"gemini","cron_expr":"0 * * * * *","working_dir":".","enabled":true}' | jq .

# Verify agent field in response
curl -s http://localhost:8080/api/v1/tasks | jq '.tasks[].agent'
```

Expected: response includes `"agent": "gemini"`.

- [ ] **Step 5: Commit**

```bash
git add internal/api/types.go internal/api/handlers.go
git commit -m "feat: add agent field to REST API task types and handlers"
```

---

## Task 5: Mobile app — agent selector

**Files:**
- Modify: `mobile/lib/types.ts`
- Modify: `mobile/app/task/new.tsx`
- Modify: `mobile/app/task/edit/[id].tsx`

- [ ] **Step 1: Update types.ts**

In `mobile/lib/types.ts`, update `Task` and `TaskRequest`:

```typescript
export type AgentType = 'claude' | 'gemini' | 'codex';

export interface Task {
  id: number;
  name: string;
  prompt: string;
  agent: AgentType;              // ← add
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
  agent: AgentType;              // ← add
  cron_expr: string;
  scheduled_at?: string;
  working_dir: string;
  discord_webhook?: string;
  slack_webhook?: string;
  enabled: boolean;
}
```

- [ ] **Step 2: Add agent state and selector to new.tsx**

In `mobile/app/task/new.tsx`, add agent state after the `isOneOff` state line:

```tsx
const [agent, setAgent] = useState<AgentType>('claude');
```

Add the `AgentType` import at the top:

```tsx
import { AgentType } from '../../lib/types';
```

In `handleSubmit`, include agent in the request:

```tsx
const request: Parameters<typeof createTask.mutate>[0] = {
    name: name.trim(),
    prompt: prompt.trim(),
    agent,                          // ← add
    cron_expr: isOneOff ? '' : cronExpr.trim(),
    working_dir: workingDir.trim() || '.',
    enabled: true,
};
```

Add the agent segmented control in the JSX, after the Task Type field and before the schedule fields:

```tsx
<View style={styles.field}>
  <Text style={[styles.label, { color: colors.textSecondary }]}>Agent</Text>
  <View style={[styles.segmentedControl, { backgroundColor: colors.surfaceSecondary }]}>
    {(['claude', 'gemini', 'codex'] as AgentType[]).map((a) => (
      <Pressable
        key={a}
        style={[
          styles.segment,
          agent === a && [styles.segmentActive, { backgroundColor: colors.surface }],
        ]}
        onPress={() => setAgent(a)}
      >
        <Text
          style={[
            styles.segmentText,
            { color: agent === a ? colors.textPrimary : colors.textMuted },
          ]}
        >
          {a.charAt(0).toUpperCase() + a.slice(1)}
        </Text>
      </Pressable>
    ))}
  </View>
</View>
```

- [ ] **Step 3: Add agent selector to edit/[id].tsx**

Open `mobile/app/task/edit/[id].tsx`. Add `AgentType` import from `../../../lib/types`.

Add state: `const [agent, setAgent] = useState<AgentType>(task?.agent ?? 'claude');`

When loading the existing task (wherever the form is pre-populated), set: `setAgent(task.agent ?? 'claude')`.

In the submit handler, include `agent` in the update request.

Add the same three-option segmented control JSX as in new.tsx, placed after the Task Type selector.

- [ ] **Step 4: Start the mobile dev server and verify**

```bash
cd mobile
npm start
```

Open the app on simulator:
- Create a new task — verify Agent selector shows Claude / Gemini / Codex
- Select Gemini, save
- Open the task — verify agent shown correctly
- Edit the task — verify Gemini is pre-selected

- [ ] **Step 5: Commit**

```bash
git add mobile/lib/types.ts mobile/app/task/new.tsx mobile/app/task/edit/[id].tsx
git commit -m "feat: add agent selector to mobile app task forms"
```

---

## Self-Review

### Spec coverage
- ✅ Three agents: claude, gemini, codex
- ✅ CLI command builds correctly per agent
- ✅ DB field with migration (backward-compatible default)
- ✅ TUI form toggle
- ✅ REST API field
- ✅ Mobile app selector
- ✅ Usage tracking skipped for non-claude agents
- ✅ Existing tasks with empty `agent` column work as claude (fallback in `BuildCommand`)

### No placeholders: all code shown with exact content.

### Type consistency
- `db.Agent` type flows from `models.go` → `executor.go` → `handlers.go` (via `db.Agent(req.Agent)`)
- `string(task.Agent)` in `taskToResponse` — correct
- Mobile `AgentType` matches the three string values in `db.Agent` constants

### Missing: the `fieldCount` update will automatically include the new `fieldAgent` slot since it uses `iota`. The `formInputs` slice in `initFormInputs` creates `make([]textinput.Model, fieldCount)` — this auto-grows by 1 since `fieldCount` increases. ✅
