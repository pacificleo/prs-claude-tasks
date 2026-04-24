package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/kylemclaren/claude-tasks/internal/api"
	"github.com/kylemclaren/claude-tasks/internal/db"
)

func newTestServer(t *testing.T) (*api.Server, func()) {
	t.Helper()
	tmp, err := os.CreateTemp("", "api-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	d, err := db.New(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() { d.Close(); os.Remove(tmp.Name()) }
	return api.NewServer(d, nil), cleanup
}

func TestCreateTaskRejectsInvalidModel(t *testing.T) {
	srv, done := newTestServer(t)
	defer done()

	body, _ := json.Marshal(map[string]any{
		"name":        "bad",
		"prompt":      "x",
		"agent":       "gemini",
		"model":       "claude-sonnet-4-6", // wrong agent
		"cron_expr":   "0 * * * * *",
		"enabled":     true,
		"working_dir": ".",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d. body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestCreateTaskDefaultsAgentToClaude(t *testing.T) {
	srv, done := newTestServer(t)
	defer done()

	body, _ := json.Marshal(map[string]any{
		"name":        "ok",
		"prompt":      "x",
		"cron_expr":   "0 * * * * *",
		"enabled":     true,
		"working_dir": ".",
		// no agent or model
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201. body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["agent"] != "claude" {
		t.Errorf("agent = %v, want \"claude\"", resp["agent"])
	}
	if resp["model"] != "claude-sonnet-4-6" {
		t.Errorf("model = %v, want default \"claude-sonnet-4-6\"", resp["model"])
	}
	if resp["display"] != "claude@claude-sonnet-4-6" {
		t.Errorf("display = %v, want \"claude@claude-sonnet-4-6\"", resp["display"])
	}
}

func TestGetAgents(t *testing.T) {
	srv, done := newTestServer(t)
	defer done()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Agents []struct {
			Name         string   `json:"name"`
			DefaultModel string   `json:"default_model"`
			Models       []string `json:"models"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Agents) != 4 {
		t.Fatalf("got %d agents, want 4", len(resp.Agents))
	}
	for _, a := range resp.Agents {
		if len(a.Models) == 0 || a.Models[0] != a.DefaultModel {
			t.Errorf("agent %s: default_model %q must equal models[0] %v", a.Name, a.DefaultModel, a.Models)
		}
	}
}
