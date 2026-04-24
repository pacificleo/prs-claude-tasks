package webhook

import (
	"strings"
	"testing"
	"time"

	"github.com/kylemclaren/claude-tasks/internal/db"
)

func TestSlackTightSuccess(t *testing.T) {
	s := NewSlack()
	task := sampleTask()
	run := sampleRun(db.RunStatusCompleted, 8*time.Second)
	run.Output = "ok"

	p := s.buildPayload(task, run)

	if len(p.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(p.Attachments))
	}
	a := p.Attachments[0]
	if a.Color != "#00FF00" {
		t.Errorf("Color = %q, want green #00FF00", a.Color)
	}
	if len(a.Blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 on success", len(a.Blocks))
	}
	b := a.Blocks[0]
	if b.Type != "section" {
		t.Errorf("block type = %q, want section", b.Type)
	}
	want := ":white_check_mark: *Daily summary* · 8s · claude@sonnet-4-6"
	if b.Text == nil || b.Text.Text != want {
		got := ""
		if b.Text != nil {
			got = b.Text.Text
		}
		t.Errorf("text = %q, want %q", got, want)
	}
	if b.Text != nil && b.Text.Type != "mrkdwn" {
		t.Errorf("text type = %q, want mrkdwn", b.Text.Type)
	}
}

func TestSlackTightFailure(t *testing.T) {
	s := NewSlack()
	task := sampleTask()
	run := sampleRun(db.RunStatusFailed, 2*time.Second)
	run.Error = "exit status 1\npanic: runtime error"

	p := s.buildPayload(task, run)

	a := p.Attachments[0]
	if a.Color != "#FF0000" {
		t.Errorf("Color = %q, want red #FF0000", a.Color)
	}
	if len(a.Blocks) != 2 {
		t.Fatalf("got %d blocks, want 2 (header + error)", len(a.Blocks))
	}
	header := a.Blocks[0]
	if header.Type != "section" || header.Text == nil {
		t.Errorf("header block malformed: %+v", header)
	}
	if !strings.Contains(header.Text.Text, ":x:") || !strings.Contains(header.Text.Text, "Daily summary") {
		t.Errorf("header text = %q", header.Text.Text)
	}
	errBlock := a.Blocks[1]
	if errBlock.Type != "section" || errBlock.Text == nil {
		t.Errorf("error block malformed: %+v", errBlock)
	}
	if !strings.Contains(errBlock.Text.Text, "exit status 1") {
		t.Errorf("error block missing error text: %q", errBlock.Text.Text)
	}
	if !strings.Contains(errBlock.Text.Text, "```") {
		t.Errorf("error block must use code block, got %q", errBlock.Text.Text)
	}
}

func TestSlackTightFailureNoError(t *testing.T) {
	// Failure with empty error falls back to output.
	s := NewSlack()
	task := sampleTask()
	run := sampleRun(db.RunStatusFailed, 1*time.Second)
	run.Output = "stdout from a doomed run"

	p := s.buildPayload(task, run)
	a := p.Attachments[0]
	if len(a.Blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(a.Blocks))
	}
	if !strings.Contains(a.Blocks[1].Text.Text, "stdout from a doomed run") {
		t.Errorf("expected output as fallback, got %q", a.Blocks[1].Text.Text)
	}
}

func TestSlackFailureFitsSectionLimit(t *testing.T) {
	s := NewSlack()
	task := sampleTask()
	run := sampleRun(db.RunStatusFailed, 1*time.Second)
	run.Error = strings.Repeat("a", 10000)

	p := s.buildPayload(task, run)
	for i, b := range p.Attachments[0].Blocks {
		if b.Text != nil && len(b.Text.Text) > 3000 {
			t.Errorf("block %d text %d chars, exceeds Slack section limit 3000", i, len(b.Text.Text))
		}
	}
}
