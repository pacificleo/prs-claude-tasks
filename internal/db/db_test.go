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
