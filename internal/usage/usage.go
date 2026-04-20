package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const usageAPIURL = "https://api.anthropic.com/api/oauth/usage"

// Bucket represents a usage time bucket (five_hour or seven_day)
type Bucket struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

// Response represents the API response from /api/oauth/usage
type Response struct {
	FiveHour Bucket `json:"five_hour"`
	SevenDay Bucket `json:"seven_day"`
}

// Credentials represents the structure of credentials JSON
type Credentials struct {
	ClaudeAIOAuth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

// Client handles usage API interactions
type Client struct {
	httpClient *http.Client
	token      string
	cache      *Response
	cacheTime  time.Time
	cacheTTL   time.Duration
	mu         sync.RWMutex
}

// NewClient creates a new usage client
func NewClient() (*Client, error) {
	token, err := readCredentials()
	if err != nil {
		return nil, err
	}

	return &Client{
		httpClient: &http.Client{Timeout: 4 * time.Second},
		token:      token,
		cacheTTL:   30 * time.Second, // Cache for 30 seconds
	}, nil
}

func readCredentials() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}

	credPath := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		return "", fmt.Errorf("credentials not found at %s: %w", credPath, err)
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", fmt.Errorf("failed to parse credentials: %w", err)
	}

	if creds.ClaudeAIOAuth.AccessToken == "" {
		return "", fmt.Errorf("no access token found in credentials")
	}

	return creds.ClaudeAIOAuth.AccessToken, nil
}

// Fetch retrieves usage data from the API (with caching)
func (c *Client) Fetch() (*Response, error) {
	c.mu.RLock()
	if c.cache != nil && time.Since(c.cacheTime) < c.cacheTTL {
		cached := c.cache
		c.mu.RUnlock()
		return cached, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.cache != nil && time.Since(c.cacheTime) < c.cacheTTL {
		return c.cache, nil
	}

	req, err := http.NewRequest("GET", usageAPIURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch usage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, formatAPIError(resp.StatusCode, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var usage Response
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, fmt.Errorf("failed to parse usage response: %w", err)
	}

	c.cache = &usage
	c.cacheTime = time.Now()

	return &usage, nil
}

// CheckThreshold returns true if usage is below the threshold, false if above
// threshold is a percentage (0-100)
func (c *Client) CheckThreshold(threshold float64) (bool, *Response, error) {
	usage, err := c.Fetch()
	if err != nil {
		return false, nil, err
	}

	// Check both 5h and 7d buckets
	if usage.FiveHour.Utilization >= threshold || usage.SevenDay.Utilization >= threshold {
		return false, usage, nil
	}

	return true, usage, nil
}

// MaxUtilization returns the higher of the two utilization values
func (r *Response) MaxUtilization() float64 {
	if r.FiveHour.Utilization > r.SevenDay.Utilization {
		return r.FiveHour.Utilization
	}
	return r.SevenDay.Utilization
}

// TimeUntilReset returns duration until the 5-hour bucket resets
func (r *Response) TimeUntilReset() time.Duration {
	resetTime, err := time.Parse(time.RFC3339, r.FiveHour.ResetsAt)
	if err != nil {
		return 0
	}
	return time.Until(resetTime)
}

// FormatTimeUntilReset returns a human-readable time until reset
func (r *Response) FormatTimeUntilReset() string {
	d := r.TimeUntilReset()
	if d < 0 {
		return "now"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 && minutes > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	} else if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	} else if minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return "now"
}

// formatAPIError turns an Anthropic error response into a short human string.
// Anthropic returns errors as {"type":"error","error":{"type":"...","message":"..."}};
// we surface the type and message instead of dumping the raw JSON, which
// otherwise wraps and breaks the TUI header layout.
func formatAPIError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Type != "" {
		if envelope.Error.Message != "" {
			return fmt.Errorf("HTTP %d %s: %s", status, envelope.Error.Type, envelope.Error.Message)
		}
		return fmt.Errorf("HTTP %d %s", status, envelope.Error.Type)
	}
	if status == http.StatusTooManyRequests {
		return fmt.Errorf("HTTP %d rate limited", status)
	}
	return fmt.Errorf("HTTP %d", status)
}
