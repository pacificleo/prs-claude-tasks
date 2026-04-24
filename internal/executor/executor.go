package executor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/kylemclaren/claude-tasks/internal/agent"
	"github.com/kylemclaren/claude-tasks/internal/db"
	"github.com/kylemclaren/claude-tasks/internal/usage"
	"github.com/kylemclaren/claude-tasks/internal/webhook"
)

// Executor runs Claude CLI tasks
type Executor struct {
	db          *db.DB
	discord     *webhook.Discord
	slack       *webhook.Slack
	usageClient *usage.Client
}

// New creates a new executor
func New(database *db.DB) *Executor {
	usageClient, _ := usage.NewClient() // Ignore error, will be nil if credentials not found

	return &Executor{
		db:          database,
		discord:     webhook.NewDiscord(),
		slack:       webhook.NewSlack(),
		usageClient: usageClient,
	}
}

// Result represents the result of a task execution
type Result struct {
	Output     string
	Error      error
	Duration   time.Duration
	Skipped    bool
	SkipReason string
}

// Execute runs a Claude CLI command for the given task
func (e *Executor) Execute(ctx context.Context, task *db.Task) *Result {
	startTime := time.Now()

	// Check usage threshold before running (claude only — gemini/codex don't draw from Anthropic quota)
	if task.Agent == agent.Claude && e.usageClient != nil {
		threshold, _ := e.db.GetUsageThreshold()
		ok, usageData, err := e.usageClient.CheckThreshold(threshold)
		if err == nil && !ok {
			// Usage is above threshold, skip the task
			skipReason := fmt.Sprintf("Usage above threshold (%.0f%%): 5h=%.0f%%, 7d=%.0f%%. Resets in %s",
				threshold,
				usageData.FiveHour.Utilization,
				usageData.SevenDay.Utilization,
				usageData.FormatTimeUntilReset())

			// Create a skipped run record
			run := &db.TaskRun{
				TaskID:    task.ID,
				StartedAt: startTime,
				Status:    db.RunStatusFailed,
				Error:     skipReason,
			}
			endTime := time.Now()
			run.EndedAt = &endTime
			_ = e.db.CreateTaskRun(run)

			return &Result{
				Skipped:    true,
				SkipReason: skipReason,
				Duration:   time.Since(startTime),
			}
		}
	}

	// Create task run record
	run := &db.TaskRun{
		TaskID:    task.ID,
		StartedAt: startTime,
		Status:    db.RunStatusRunning,
	}
	if err := e.db.CreateTaskRun(run); err != nil {
		return &Result{Error: fmt.Errorf("failed to create run record: %w", err)}
	}

	// Build command via the agent registry. Fail the run cleanly if the agent
	// is unknown or its binary is missing on PATH.
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

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	endTime := time.Now()
	duration := endTime.Sub(startTime)

	// Update run record
	run.EndedAt = &endTime
	run.Output = stdout.String()
	if err != nil {
		run.Status = db.RunStatusFailed
		run.Error = fmt.Sprintf("%s\n%s", err.Error(), stderr.String())
	} else {
		run.Status = db.RunStatusCompleted
	}
	_ = e.db.UpdateTaskRun(run)

	// Update task's last run time
	task.LastRunAt = &endTime
	_ = e.db.UpdateTask(task)

	// Send webhook notifications if configured
	if task.DiscordWebhook != "" {
		_ = e.discord.SendResult(task.DiscordWebhook, task, run)
	}
	if task.SlackWebhook != "" {
		_ = e.slack.SendResult(task.SlackWebhook, task, run)
	}

	result := &Result{
		Output:   stdout.String(),
		Duration: duration,
	}
	if err != nil {
		result.Error = fmt.Errorf("%s: %s", err.Error(), stderr.String())
	}

	return result
}

// buildCommand constructs the *exec.Cmd to invoke the agent CLI for the task.
// Returns an error when the agent is unknown or the binary is not on PATH.
func (e *Executor) buildCommand(ctx context.Context, task *db.Task) (*exec.Cmd, error) {
	spec, ok := agent.Get(task.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", task.Agent)
	}
	model := task.ResolvedModel()
	binary := spec.Binary
	if spec.BinaryFor != nil {
		binary = spec.BinaryFor(model)
	}
	if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("binary %q not found in PATH", binary)
	}
	args := spec.BuildArgs(model, task.Prompt)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = task.WorkingDir
	return cmd, nil
}

// ExecuteAsync runs a task asynchronously
func (e *Executor) ExecuteAsync(task *db.Task) <-chan *Result {
	ch := make(chan *Result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		ch <- e.Execute(ctx, task)
		close(ch)
	}()
	return ch
}
