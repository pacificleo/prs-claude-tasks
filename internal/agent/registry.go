// Package agent owns all per-agent metadata: binary names, CLI flags,
// allowed models, defaults, and argv construction. It is the single source
// of truth used by db validation, the executor, the REST API, and TUI/mobile pickers.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// Name identifies a CLI agent.
type Name string

const (
	Claude Name = "claude"
	Gemini Name = "gemini"
	Codex  Name = "codex"
	Shell  Name = "shell"
)

// Spec describes one agent: how to invoke it and which models are allowed.
type Spec struct {
	Name          Name
	Binary        string
	BinaryFor     func(model string) string           // optional; takes precedence over Binary when non-nil
	AllowedModels []string                            // first entry is the default
	BuildArgs     func(model, prompt string) []string // argv after Binary
}

var registry = map[Name]Spec{
	Claude: {
		Name:   Claude,
		Binary: "claude",
		AllowedModels: []string{
			"claude-sonnet-4-6",
			"claude-opus-4-7",
			"claude-haiku-4-5",
		},
		BuildArgs: func(model, prompt string) []string {
			return []string{"-p", "--dangerously-skip-permissions", "--model", model, prompt}
		},
	},
	Gemini: {
		Name:          Gemini,
		Binary:        "gemini",
		// Canonical names from https://geminicli.com/docs/cli/model/. The bare
		// aliases (`pro`, `flash`) the CLI also accepts route through preview
		// tiers that hit "no capacity" errors, so they're omitted in favor of
		// concrete stable names + the documented `auto` aliases.
		AllowedModels: []string{
			"gemini-2.5-pro",         // default — stable, capable
			"gemini-2.5-flash",       // stable, fast
			"gemini-3-pro-preview",   // preview
			"gemini-3.1-pro-preview", // preview
			"gemini-3-flash-preview", // preview
			"auto",                   // routes Gemini 3 pro/flash by complexity (preview)
			"auto-2.5",               // routes Gemini 2.5 pro/flash (stable)
		},
		BuildArgs: func(model, prompt string) []string {
			// gemini's `-p` is strict: it refuses values that look like flags
			// (e.g. `--approval-mode=yolo`) and errors with "Not enough
			// arguments following: p". The prompt value must come immediately
			// after `-p`; other flags follow.
			return []string{"-p", prompt, "--approval-mode=yolo", "-m", model}
		},
	},
	Codex: {
		Name:          Codex,
		Binary:        "codex",
		AllowedModels: []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.2"},
		BuildArgs: func(model, prompt string) []string {
			return []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "-m", model, prompt}
		},
	},
	Shell: {
		Name: Shell,
		// Binary varies with the chosen model (the shell name itself).
		BinaryFor:     func(model string) string { return model },
		AllowedModels: shellModels(),
		BuildArgs: func(_, prompt string) []string {
			return []string{"-c", prompt}
		},
	},
}

// order in which agents are returned by All() and rendered in pickers.
var order = []Name{Claude, Gemini, Codex, Shell}

// Get returns the spec for an agent.
func Get(n Name) (Spec, bool) {
	s, ok := registry[n]
	return s, ok
}

// All returns all registered agents in display order.
func All() []Spec {
	out := make([]Spec, 0, len(order))
	for _, n := range order {
		out = append(out, registry[n])
	}
	return out
}

// DefaultModel returns the default model for an agent, or "" if the agent is unknown.
func DefaultModel(n Name) string {
	s, ok := registry[n]
	if !ok || len(s.AllowedModels) == 0 {
		return ""
	}
	return s.AllowedModels[0]
}

// Validate returns nil if (n, model) is a legal combination. An empty model
// is treated as "use the default" and accepted.
func Validate(n Name, model string) error {
	s, ok := registry[n]
	if !ok {
		return fmt.Errorf("unknown agent %q", n)
	}
	if model == "" {
		return nil
	}
	for _, m := range s.AllowedModels {
		if m == model {
			return nil
		}
	}
	return fmt.Errorf("invalid model %q for agent %q", model, n)
}

// resolveModel returns model if non-empty, else the agent's default.
func resolveModel(n Name, model string) string {
	if model != "" {
		return model
	}
	return DefaultModel(n)
}

// Display returns "agent@model" using the resolved model.
func Display(n Name, model string) string {
	return string(n) + "@" + resolveModel(n, model)
}

// ShortDisplay returns "agent@<short-model>". For claude models it strips the
// "claude-" prefix to keep TUI columns narrow. Other agents pass through.
func ShortDisplay(n Name, model string) string {
	m := resolveModel(n, model)
	if n == Claude && len(m) > 7 && m[:7] == "claude-" {
		m = m[7:]
	}
	return string(n) + "@" + m
}

// shellModels returns the allowed shells for the Shell agent. The user's
// current $SHELL (basename) is listed first as the default; "bash" is always
// included as a fallback. If $SHELL is unset or empty, the list is just
// {"bash"}.
func shellModels() []string {
	current := filepath.Base(os.Getenv("SHELL"))
	if current == "" || current == "." || current == "/" {
		return []string{"bash"}
	}
	if current == "bash" {
		return []string{"bash"}
	}
	return []string{current, "bash"}
}
