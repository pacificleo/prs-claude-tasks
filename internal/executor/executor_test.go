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
