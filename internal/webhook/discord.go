package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/kylemclaren/claude-tasks/internal/agent"
	"github.com/kylemclaren/claude-tasks/internal/db"
)

// Discord handles Discord webhook notifications
type Discord struct {
	client *http.Client
}

// NewDiscord creates a new Discord webhook handler
func NewDiscord() *Discord {
	return &Discord{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// DiscordEmbed represents a Discord embed object
type DiscordEmbed struct {
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
}

// EmbedField represents a field in a Discord embed
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// EmbedFooter represents the footer of a Discord embed
type EmbedFooter struct {
	Text string `json:"text"`
}

// DiscordPayload represents the webhook payload
type DiscordPayload struct {
	Content string         `json:"content,omitempty"`
	Embeds  []DiscordEmbed `json:"embeds,omitempty"`
}

// SendResult sends a task result to Discord
func (d *Discord) SendResult(webhookURL string, task *db.Task, run *db.TaskRun) error {
	return d.send(webhookURL, d.buildPayload(task, run))
}

// buildPayload renders the tight notification payload: title-only on success,
// title plus a code-blocked error (or output fallback) on failure.
func (d *Discord) buildPayload(task *db.Task, run *db.TaskRun) DiscordPayload {
	color, emoji := discordStatusStyle(run.Status)

	embed := DiscordEmbed{
		Title:     fmt.Sprintf("%s %s · %s · %s", emoji, task.Name, runDuration(run), agent.ShortDisplay(task.Agent, task.Model)),
		Color:     color,
		Timestamp: run.StartedAt.Format(time.RFC3339),
	}

	if run.Status == db.RunStatusFailed {
		body := run.Error
		if body == "" {
			body = run.Output
		}
		if body != "" {
			// Discord embed description hard limit is 4096; reserve a little for
			// the code-block fence and the truncation marker.
			const maxBody = 3900
			truncated := false
			if len(body) > maxBody {
				body = body[:maxBody]
				truncated = true
			}
			embed.Description = "```\n" + body + "\n```"
			if truncated {
				embed.Description += "\n*… (truncated)*"
			}
		}
	}

	return DiscordPayload{Embeds: []DiscordEmbed{embed}}
}

func discordStatusStyle(status db.RunStatus) (int, string) {
	switch status {
	case db.RunStatusCompleted:
		return 0x00FF00, "✅"
	case db.RunStatusFailed:
		return 0xFF0000, "❌"
	default:
		return 0xFFFF00, "⏳"
	}
}

func runDuration(run *db.TaskRun) string {
	if run.EndedAt == nil {
		return "running"
	}
	return run.EndedAt.Sub(run.StartedAt).Round(time.Second).String()
}

func (d *Discord) send(webhookURL string, payload DiscordPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}
