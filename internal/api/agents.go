package api

import (
	"net/http"

	"github.com/kylemclaren/claude-tasks/internal/agent"
)

type agentInfo struct {
	Name         string   `json:"name"`
	DefaultModel string   `json:"default_model"`
	Models       []string `json:"models"`
}

type agentListResponse struct {
	Agents []agentInfo `json:"agents"`
}

// ListAgents handles GET /api/v1/agents.
func (s *Server) ListAgents(w http.ResponseWriter, r *http.Request) {
	specs := agent.All()
	resp := agentListResponse{Agents: make([]agentInfo, len(specs))}
	for i, spec := range specs {
		resp.Agents[i] = agentInfo{
			Name:         string(spec.Name),
			DefaultModel: agent.DefaultModel(spec.Name),
			Models:       spec.AllowedModels,
		}
	}
	s.jsonResponse(w, http.StatusOK, resp)
}
