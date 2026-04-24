package webhook

import (
	"strings"
	"testing"
	"time"

	"github.com/kylemclaren/claude-tasks/internal/db"
)

func sampleTask() *db.Task {
	return &db.Task{
		Name:       "Daily summary",
		Agent:      db.AgentClaude,
		Model:      "claude-sonnet-4-6",
		WorkingDir: "/Users/pwason/work/notes",
	}
}

func sampleRun(status db.RunStatus, dur time.Duration) *db.TaskRun {
	started := time.Date(2026, 4, 24, 10, 24, 0, 0, time.UTC)
	ended := started.Add(dur)
	return &db.TaskRun{
		StartedAt: started,
		EndedAt:   &ended,
		Status:    status,
	}
}

func TestDiscordTightSuccess(t *testing.T) {
	d := NewDiscord()
	task := sampleTask()
	run := sampleRun(db.RunStatusCompleted, 8*time.Second)
	run.Output = "## Summary\nReviewed 12 PRs, 3 need attention.\n"

	p := d.buildPayload(task, run)

	if len(p.Embeds) != 1 {
		t.Fatalf("got %d embeds, want 1", len(p.Embeds))
	}
	e := p.Embeds[0]
	want := "✅ Daily summary · 8s · claude@sonnet-4-6"
	if e.Title != want {
		t.Errorf("Title = %q, want %q", e.Title, want)
	}
	if e.Color != 0x00FF00 {
		t.Errorf("Color = %#x, want green 0x00FF00", e.Color)
	}
	if e.Description != "" {
		t.Errorf("Description = %q, want empty on success", e.Description)
	}
	if len(e.Fields) != 0 {
		t.Errorf("got %d fields, want 0 on success", len(e.Fields))
	}
	if e.Footer != nil {
		t.Errorf("Footer = %+v, want nil", e.Footer)
	}
}

func TestDiscordTightFailure(t *testing.T) {
	d := NewDiscord()
	task := sampleTask()
	run := sampleRun(db.RunStatusFailed, 2*time.Second)
	run.Error = "exit status 1\npanic: runtime error: invalid memory address"

	p := d.buildPayload(task, run)

	e := p.Embeds[0]
	if e.Title != "❌ Daily summary · 2s · claude@sonnet-4-6" {
		t.Errorf("Title = %q", e.Title)
	}
	if e.Color != 0xFF0000 {
		t.Errorf("Color = %#x, want red 0xFF0000", e.Color)
	}
	if !strings.HasPrefix(e.Description, "```") || !strings.HasSuffix(e.Description, "```") {
		t.Errorf("Description must be a code block, got %q", e.Description)
	}
	if !strings.Contains(e.Description, "exit status 1") {
		t.Errorf("Description should contain error text, got %q", e.Description)
	}
	if len(e.Fields) != 0 {
		t.Errorf("got %d fields, want 0", len(e.Fields))
	}
	if e.Footer != nil {
		t.Errorf("Footer = %+v, want nil", e.Footer)
	}
}

func TestDiscordTightFailureNoError(t *testing.T) {
	// Failure with empty error string falls back to output.
	d := NewDiscord()
	task := sampleTask()
	run := sampleRun(db.RunStatusFailed, 1*time.Second)
	run.Output = "stdout from a doomed run"

	p := d.buildPayload(task, run)
	e := p.Embeds[0]
	if !strings.Contains(e.Description, "stdout from a doomed run") {
		t.Errorf("expected output as fallback, got %q", e.Description)
	}
}

func TestDiscordFailureFitsEmbedLimit(t *testing.T) {
	d := NewDiscord()
	task := sampleTask()
	run := sampleRun(db.RunStatusFailed, 1*time.Second)
	run.Error = strings.Repeat("a", 10000)

	p := d.buildPayload(task, run)
	if got := len(p.Embeds[0].Description); got > 4096 {
		t.Errorf("description %d chars, exceeds Discord embed limit 4096", got)
	}
}
