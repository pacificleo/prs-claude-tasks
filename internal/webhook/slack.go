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

// Slack handles Slack webhook notifications
type Slack struct {
	client *http.Client
}

// NewSlack creates a new Slack webhook handler
func NewSlack() *Slack {
	return &Slack{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// SlackBlock represents a Slack Block Kit block
type SlackBlock struct {
	Type     string         `json:"type"`
	Text     *SlackTextObj  `json:"text,omitempty"`
	Fields   []SlackTextObj `json:"fields,omitempty"`
	Elements []SlackElement `json:"elements,omitempty"`
}

// SlackTextObj represents a Slack text object
type SlackTextObj struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emoji bool   `json:"emoji,omitempty"`
}

// SlackElement represents a Slack element (for context blocks)
type SlackElement struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// SlackAttachment represents a Slack attachment (for colored sidebar)
type SlackAttachment struct {
	Color  string       `json:"color"`
	Blocks []SlackBlock `json:"blocks"`
}

// SlackPayload represents the webhook payload
type SlackPayload struct {
	Text        string            `json:"text,omitempty"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
}

// SendResult sends a task result to Slack
func (s *Slack) SendResult(webhookURL string, task *db.Task, run *db.TaskRun) error {
	return s.send(webhookURL, s.buildPayload(task, run))
}

// buildPayload renders the tight notification payload: a single section line
// on success, plus a code-blocked error (or output fallback) on failure.
func (s *Slack) buildPayload(task *db.Task, run *db.TaskRun) SlackPayload {
	color, emoji := slackStatusStyle(run.Status)

	header := SlackBlock{
		Type: "section",
		Text: &SlackTextObj{
			Type: "mrkdwn",
			Text: fmt.Sprintf("%s *%s* · %s · %s", emoji, task.Name, runDuration(run), agent.ShortDisplay(task.Agent, task.Model)),
		},
	}
	blocks := []SlackBlock{header}

	if run.Status == db.RunStatusFailed {
		body := run.Error
		if body == "" {
			body = run.Output
		}
		if body != "" {
			// Slack section text limit is 3000; reserve room for fences and marker.
			const maxBody = 2900
			truncated := false
			if len(body) > maxBody {
				body = body[:maxBody]
				truncated = true
			}
			text := "```" + body + "```"
			if truncated {
				text += "\n_… (truncated)_"
			}
			blocks = append(blocks, SlackBlock{
				Type: "section",
				Text: &SlackTextObj{Type: "mrkdwn", Text: text},
			})
		}
	}

	return SlackPayload{
		Attachments: []SlackAttachment{{Color: color, Blocks: blocks}},
	}
}

func slackStatusStyle(status db.RunStatus) (string, string) {
	switch status {
	case db.RunStatusCompleted:
		return "#00FF00", ":white_check_mark:"
	case db.RunStatusFailed:
		return "#FF0000", ":x:"
	default:
		return "#FFFF00", ":hourglass:"
	}
}

func (s *Slack) send(webhookURL string, payload SlackPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}
