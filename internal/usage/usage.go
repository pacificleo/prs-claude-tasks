package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	// Negative cache: while errUntil is in the future, Fetch short-circuits
	// and returns errCache without hitting the API. Set from Retry-After on
	// 429 responses (with a default fallback for other failures), so a
	// rate-limit cooldown is respected exactly instead of poked again on the
	// next ticker fire.
	errCache error
	errUntil time.Time
	mu       sync.RWMutex
}

// RateLimitError is returned when the API responds with 429. RetryAfter is
// the absolute time when the cooldown ends; callers can render a live
// countdown by computing time.Until(RetryAfter) on each frame.
type RateLimitError struct {
	Status     int
	RetryAfter time.Time
}

func (e *RateLimitError) Error() string {
	secs := int(time.Until(e.RetryAfter).Seconds())
	if secs < 0 {
		secs = 0
	}
	return fmt.Sprintf("rate limited; retry in %ds", secs)
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
	if c.errCache != nil && time.Now().Before(c.errUntil) {
		err := c.errCache
		c.mu.RUnlock()
		return nil, err
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.cache != nil && time.Since(c.cacheTime) < c.cacheTTL {
		return c.cache, nil
	}
	if c.errCache != nil && time.Now().Before(c.errUntil) {
		return nil, c.errCache
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

	if resp.StatusCode == http.StatusTooManyRequests {
		retryDur := parseRetryAfter(resp.Header.Get("Retry-After"))
		if retryDur <= 0 {
			retryDur = 60 * time.Second // sensible default when the server omits the header
		}
		e := &RateLimitError{
			Status:     resp.StatusCode,
			RetryAfter: time.Now().Add(retryDur),
		}
		c.errCache = e
		c.errUntil = e.RetryAfter
		return nil, e
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		apiErr := formatAPIError(resp.StatusCode, body)
		c.errCache = apiErr
		c.errUntil = time.Now().Add(30 * time.Second) // back off briefly on other failures
		return nil, apiErr
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
	c.errCache = nil
	c.errUntil = time.Time{}

	return &usage, nil
}

// parseRetryAfter parses the HTTP Retry-After header. Per RFC 7231 it is
// either an integer seconds value or an HTTP-date.
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		return time.Until(t)
	}
	return 0
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
