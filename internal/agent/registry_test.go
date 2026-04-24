package agent_test

import (
	"strings"
	"testing"

	"github.com/kylemclaren/claude-tasks/internal/agent"
)

func TestAllAgentsRegistered(t *testing.T) {
	all := agent.All()
	if len(all) != 4 {
		t.Fatalf("All() returned %d agents, want 4", len(all))
	}
	want := map[agent.Name]bool{agent.Claude: true, agent.Gemini: true, agent.Codex: true, agent.Shell: true}
	for _, s := range all {
		if !want[s.Name] {
			t.Errorf("unexpected agent: %s", s.Name)
		}
	}
}

func TestDefaultModels(t *testing.T) {
	cases := map[agent.Name]string{
		agent.Claude: "claude-sonnet-4-6",
		agent.Gemini: "gemini-2.5-pro",
		agent.Codex:  "gpt-5.4",
	}
	for n, want := range cases {
		if got := agent.DefaultModel(n); got != want {
			t.Errorf("DefaultModel(%s) = %q, want %q", n, got, want)
		}
	}
}

func TestDefaultModelIsFirstInAllowedList(t *testing.T) {
	for _, s := range agent.All() {
		if s.AllowedModels[0] != agent.DefaultModel(s.Name) {
			t.Errorf("agent %s: AllowedModels[0]=%q != DefaultModel=%q",
				s.Name, s.AllowedModels[0], agent.DefaultModel(s.Name))
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		agent   agent.Name
		model   string
		wantErr bool
	}{
		{"claude valid", agent.Claude, "claude-opus-4-7", false},
		{"claude empty model ok", agent.Claude, "", false},
		{"claude bad model", agent.Claude, "gpt-5.4", true},
		{"gemini valid", agent.Gemini, "gemini-2.5-pro", false},
		{"gemini auto alias", agent.Gemini, "auto", false},
		{"gemini bad", agent.Gemini, "ultra", true},
		{"gemini legacy pro alias rejected", agent.Gemini, "pro", true},
		{"codex valid", agent.Codex, "gpt-5.4-mini", false},
		{"shell bash always allowed", agent.Shell, "bash", false},
		{"shell empty model ok", agent.Shell, "", false},
		{"shell bad model", agent.Shell, "powershell", true},
		{"unknown agent", agent.Name("openai"), "gpt-5.4", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := agent.Validate(tc.agent, tc.model)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate(%s, %q) err=%v, wantErr=%v", tc.agent, tc.model, err, tc.wantErr)
			}
		})
	}
}

func TestBuildArgsClaude(t *testing.T) {
	spec, _ := agent.Get(agent.Claude)
	args := spec.BuildArgs("claude-sonnet-4-6", "hello world")
	want := []string{"-p", "--dangerously-skip-permissions", "--model", "claude-sonnet-4-6", "hello world"}
	if !equalSlices(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestBuildArgsGemini(t *testing.T) {
	spec, _ := agent.Get(agent.Gemini)
	args := spec.BuildArgs("gemini-2.5-pro", "hello world")
	want := []string{"-p", "hello world", "--approval-mode=yolo", "-m", "gemini-2.5-pro"}
	if !equalSlices(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestBuildArgsCodex(t *testing.T) {
	spec, _ := agent.Get(agent.Codex)
	args := spec.BuildArgs("gpt-5.4", "hello world")
	want := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "-m", "gpt-5.4", "hello world"}
	if !equalSlices(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestBuildArgsShell(t *testing.T) {
	spec, _ := agent.Get(agent.Shell)
	args := spec.BuildArgs("bash", "echo hi")
	want := []string{"-c", "echo hi"}
	if !equalSlices(args, want) {
		t.Errorf("got %v, want %v", args, want)
	}
}

func TestShellBinaryForReturnsModel(t *testing.T) {
	spec, _ := agent.Get(agent.Shell)
	if spec.BinaryFor == nil {
		t.Fatal("Shell spec must define BinaryFor")
	}
	if got := spec.BinaryFor("zsh"); got != "zsh" {
		t.Errorf("BinaryFor(\"zsh\") = %q, want %q", got, "zsh")
	}
	if got := spec.BinaryFor("bash"); got != "bash" {
		t.Errorf("BinaryFor(\"bash\") = %q, want %q", got, "bash")
	}
}

func TestShellDefaultModelFromEnv(t *testing.T) {
	// We can't change the registry after init, so we just assert the
	// invariants: default is one of the allowed models, and "bash" is always
	// in the allowed list.
	def := agent.DefaultModel(agent.Shell)
	if def == "" {
		t.Fatal("Shell default model must not be empty")
	}
	spec, _ := agent.Get(agent.Shell)
	hasBash := false
	hasDefault := false
	for _, m := range spec.AllowedModels {
		if m == "bash" {
			hasBash = true
		}
		if m == def {
			hasDefault = true
		}
	}
	if !hasBash {
		t.Errorf("Shell allowed models %v must contain \"bash\"", spec.AllowedModels)
	}
	if !hasDefault {
		t.Errorf("Shell default %q not in allowed models %v", def, spec.AllowedModels)
	}
}

func TestDisplay(t *testing.T) {
	got := agent.Display(agent.Claude, "claude-sonnet-4-6")
	if got != "claude@claude-sonnet-4-6" {
		t.Errorf("Display = %q, want %q", got, "claude@claude-sonnet-4-6")
	}
}

func TestShortDisplayStripsClaudePrefix(t *testing.T) {
	cases := map[string]struct {
		agent       agent.Name
		model, want string
	}{
		"claude opus":   {agent.Claude, "claude-opus-4-7", "claude@opus-4-7"},
		"claude sonnet": {agent.Claude, "claude-sonnet-4-6", "claude@sonnet-4-6"},
		"gemini 2.5 pro": {agent.Gemini, "gemini-2.5-pro", "gemini@gemini-2.5-pro"},
		"codex gpt":     {agent.Codex, "gpt-5.4-mini", "codex@gpt-5.4-mini"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := agent.ShortDisplay(tc.agent, tc.model)
			if got != tc.want {
				t.Errorf("ShortDisplay = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShortDisplayResolvesEmptyModelToDefault(t *testing.T) {
	got := agent.ShortDisplay(agent.Claude, "")
	if !strings.HasPrefix(got, "claude@") {
		t.Errorf("ShortDisplay with empty model = %q, want claude@<default>", got)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
