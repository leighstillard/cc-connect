package core

import (
	"os"
	"path/filepath"
	"testing"
)

type manifestTestAgent struct {
	stubAgent
}

func (a *manifestTestAgent) NativeCommands() []SlashCommandSpec {
	return []SlashCommandSpec{
		{Name: "help", Description: "Agent help"},
		{Name: "commit", Description: "Create a commit", UsageHint: "[message]"},
	}
}

func (a *manifestTestAgent) CLIDisplayName() string { return "Manifest Test Agent" }

func TestDiffSlashCommands(t *testing.T) {
	a := []SlashCommandSpec{
		{Name: "help", Description: "Show help", UsageHint: ""},
		{Name: "commit", Description: "Create commit", UsageHint: "[message]"},
	}

	b := []SlashCommandSpec{
		{Name: "commit", Description: "Create commit", UsageHint: "[message]"},
		{Name: "help", Description: "Show help", UsageHint: ""},
	}
	if DiffSlashCommands(a, b) {
		t.Fatal("expected no diff for identical command content in different order")
	}

	c := []SlashCommandSpec{
		{Name: "help", Description: "Changed", UsageHint: ""},
		{Name: "commit", Description: "Create commit", UsageHint: "[message]"},
	}
	if !DiffSlashCommands(a, c) {
		t.Fatal("expected diff when description changes")
	}

	d := []SlashCommandSpec{
		{Name: "help", Description: "Show help", UsageHint: ""},
		{Name: "commit", Description: "Create commit", UsageHint: "[text]"},
	}
	if !DiffSlashCommands(a, d) {
		t.Fatal("expected diff when usage hint changes")
	}
}

func TestEngineDiscoverAgentNativeGatewayCommands(t *testing.T) {
	agent := &manifestTestAgent{}
	e := NewEngine("test", agent, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	e.disabledCmds = map[string]bool{"help": true}

	got := e.DiscoverAgentNativeGatewayCommands()
	if len(got) != 1 {
		t.Fatalf("expected 1 native command after filtering, got %#v", got)
	}
	if got[0].Name != "commit" {
		t.Fatalf("native command = %q, want commit", got[0].Name)
	}
	if got[0].UsageHint != "[message]" {
		t.Fatalf("usage hint = %q, want %q", got[0].UsageHint, "[message]")
	}
}

func TestEngineDiscoverManifestCommands(t *testing.T) {
	agent := &manifestTestAgent{}
	e := NewEngine("test", agent, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	e.AddCommand("deploy", "Deploy app", "", "", "", "config")

	skillRoot := t.TempDir()
	skillDir := filepath.Join(skillRoot, "review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("Review code changes"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	e.skills.SetDirs([]string{skillRoot})

	got := e.DiscoverManifestCommands()
	byName := make(map[string]SlashCommandSpec, len(got))
	for _, cmd := range got {
		byName[cmd.Name] = cmd
	}

	for _, name := range []string{"btw", "cc", "help"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("expected builtin %q in manifest commands, got %#v", name, got)
		}
	}
	for _, name := range []string{"commit", "deploy", "review"} {
		if _, ok := byName[name]; ok {
			t.Fatalf("did not expect %q in manifest commands, got %#v", name, got)
		}
	}
	if byName["btw"].UsageHint != "[message]" {
		t.Fatalf("btw usage hint = %q, want %q", byName["btw"].UsageHint, "[message]")
	}
}
