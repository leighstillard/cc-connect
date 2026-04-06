package core

import (
	"fmt"
	"sort"
	"strings"
)

// SlashCommandSpec represents a slash command to be registered in a messaging platform.
type SlashCommandSpec struct {
	Name        string
	Description string
	UsageHint   string
}

type ManifestSyncErrorKind string

const (
	ManifestSyncErrorGeneric      ManifestSyncErrorKind = "generic"
	ManifestSyncErrorTokenExpired ManifestSyncErrorKind = "token_expired"
)

// ManifestSyncError carries structured platform sync errors back to core without
// making core depend on any platform-specific error types.
type ManifestSyncError struct {
	Kind  ManifestSyncErrorKind
	AppID string
	Err   error
}

func (e *ManifestSyncError) Error() string {
	if e == nil {
		return ""
	}
	if e.AppID != "" {
		return fmt.Sprintf("manifest sync %s for app %s: %v", e.Kind, e.AppID, e.Err)
	}
	return fmt.Sprintf("manifest sync %s: %v", e.Kind, e.Err)
}

func (e *ManifestSyncError) Unwrap() error { return e.Err }

var builtinCommandUsageHints = map[string]string{
	"allow":     "<tool>",
	"alias":     "[add|del]",
	"bind":      "[project|-project|remove]",
	"btw":       "[message]",
	"cc":        "<command> [args]",
	"commands":  "[add|del]",
	"config":    "[get|set|reload] [key] [value]",
	"cron":      "[add|list|del|enable|disable]",
	"delete":    "<number>|1,2,3|3-7|1,3-5,8",
	"heartbeat": "[status|pause|resume|run|interval <mins>]",
	"history":   "[n]",
	"lang":      "[en|zh|zh-TW|ja|es|auto]",
	"memory":    "[add|global|global add]",
	"mode":      "[default|edit|plan|yolo]",
	"model":     "[name]",
	"name":      "[number] <text>",
	"new":       "[name]",
	"provider":  "[list|add|remove|switch|clear]",
	"quiet":     "[global]",
	"reasoning": "[low|medium|high]",
	"search":    "<keyword>",
	"shell":     "<command>",
	"switch":    "<number>",
	"tts":       "[always|voice_only]",
	"workspace": "[bind|route|init|unbind|list|shared]",
}

// DiffSlashCommands reports whether two slash-command sets differ in content.
// Comparison is case-insensitive on the command name and ignores ordering.
func DiffSlashCommands(desired, current []SlashCommandSpec) bool {
	if len(desired) != len(current) {
		return true
	}

	type signature struct {
		description string
		usageHint   string
	}

	currentMap := make(map[string]signature, len(current))
	for _, cmd := range current {
		currentMap[strings.ToLower(cmd.Name)] = signature{
			description: cmd.Description,
			usageHint:   cmd.UsageHint,
		}
	}

	for _, cmd := range desired {
		got, ok := currentMap[strings.ToLower(cmd.Name)]
		if !ok {
			return true
		}
		if got.description != cmd.Description || got.usageHint != cmd.UsageHint {
			return true
		}
	}

	return false
}

// DiscoverManifestCommands returns the stable cc-connect command surface that
// can be safely registered on platforms with limited slash-command capacity.
// Native agent commands remain available via /cc.
func (e *Engine) DiscoverManifestCommands() []SlashCommandSpec {
	e.userRolesMu.RLock()
	disabledCmds := e.disabledCmds
	e.userRolesMu.RUnlock()

	return discoverBuiltinSlashCommands(e.i18n, disabledCmds)
}

// DiscoverAgentNativeGatewayCommands returns native agent commands reachable
// through the /cc gateway, filtered by the current disabled-command policy.
func (e *Engine) DiscoverAgentNativeGatewayCommands() []SlashCommandSpec {
	e.userRolesMu.RLock()
	disabledCmds := e.disabledCmds
	e.userRolesMu.RUnlock()

	return discoverAgentNativeCommands(e.agent, disabledCmds)
}

func discoverBuiltinSlashCommands(i18n *I18n, disabled map[string]bool) []SlashCommandSpec {
	var result []SlashCommandSpec
	seen := make(map[string]bool)

	add := func(name, description string) {
		if disabled != nil && disabled[strings.ToLower(name)] {
			return
		}
		key := normalizeCommandName(name)
		if seen[key] {
			return
		}
		seen[key] = true
		result = append(result, SlashCommandSpec{
			Name:        name,
			Description: description,
			UsageHint:   builtinCommandUsageHints[name],
		})
	}

	add("btw", i18n.T(MsgBuiltinCmdBtw))
	for _, bc := range builtinCommands {
		if len(bc.names) == 0 {
			continue
		}
		add(bc.id, i18n.T(MsgKey(bc.id)))
	}

	return result
}

func discoverAgentNativeCommands(agent Agent, disabled map[string]bool) []SlashCommandSpec {
	ncp, ok := agent.(NativeCommandProvider)
	if !ok {
		return nil
	}
	commands := filterDisabledSlashCommands(append([]SlashCommandSpec(nil), ncp.NativeCommands()...), disabled)
	sortSlashCommandsByName(commands)
	return commands
}

func filterDisabledSlashCommands(commands []SlashCommandSpec, disabled map[string]bool) []SlashCommandSpec {
	if disabled == nil {
		return commands
	}
	filtered := commands[:0]
	for _, cmd := range commands {
		if disabled[strings.ToLower(cmd.Name)] {
			continue
		}
		filtered = append(filtered, cmd)
	}
	return filtered
}

func sortSlashCommandsByName(commands []SlashCommandSpec) {
	sort.Slice(commands, func(i, j int) bool {
		left := strings.ToLower(commands[i].Name)
		right := strings.ToLower(commands[j].Name)
		if left == right {
			return commands[i].Name < commands[j].Name
		}
		return left < right
	})
}

func agentCommandDisplayName(agent Agent) string {
	if agent == nil {
		return ""
	}
	if info, ok := agent.(AgentDoctorInfo); ok && info.CLIDisplayName() != "" {
		return info.CLIDisplayName()
	}
	return agent.Name()
}
