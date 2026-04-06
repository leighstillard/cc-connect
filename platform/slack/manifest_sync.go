package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

const defaultSlackAPIBase = "https://slack.com"
const maxSlackManifestSlashCommands = 50

// Slack rejects certain built-in names during manifest validation even though
// the rest of the slash command payload is valid. Keep this list platform-local
// so core discovery stays platform-agnostic.
var slackReservedManifestCommandNames = map[string]bool{
	"search": true,
	"status": true,
}

// SyncManifest implements core.ManifestSyncer for Slack's App Manifest API.
func (p *Platform) SyncManifest(ctx context.Context, commands []core.SlashCommandSpec) error {
	if p.manifestTokenMgr == nil {
		return nil
	}
	commands, skipped := filterManifestCommandsForSlack(commands)
	if len(skipped) > 0 {
		slog.Warn("slack: skipping reserved manifest command names", "commands", skipped)
	}
	if len(commands) > maxSlackManifestSlashCommands {
		return fmt.Errorf("too many slash commands for Slack manifest: %d > %d", len(commands), maxSlackManifestSlashCommands)
	}

	tokens, err := p.manifestTokenMgr.EnsureValidToken(ctx)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	if !tokens.SyncDebounceOK() {
		slog.Debug("slack: manifest sync skipped due to debounce", "last_sync", tokens.LastSync)
		return nil
	}

	apiBase := strings.TrimRight(p.manifestAPIBase, "/")
	if apiBase == "" {
		apiBase = defaultSlackAPIBase
	}

	manifest, err := p.exportManifest(ctx, apiBase, tokens)
	if err != nil {
		return fmt.Errorf("export manifest: %w", err)
	}

	current := extractSlashCommands(manifest)
	if !core.DiffSlashCommands(commands, current) {
		slog.Debug("slack: manifest already up to date")
		return nil
	}

	setSlashCommands(manifest, commandsToManifestFormat(commands))
	if err := p.validateManifest(ctx, apiBase, tokens, manifest); err != nil {
		return fmt.Errorf("validate manifest: %w", err)
	}
	if err := p.updateManifest(ctx, apiBase, tokens, manifest); err != nil {
		return fmt.Errorf("update manifest: %w", err)
	}

	tokens.LastSync = time.Now().UTC()
	if err := p.manifestTokenMgr.Save(tokens); err != nil {
		slog.Warn("slack: failed to persist manifest sync timestamp", "error", err)
	}

	slog.Info("slack: manifest updated", "commands", len(commands))
	return nil
}

func (p *Platform) exportManifest(ctx context.Context, apiBase string, tokens *ManifestTokens) (map[string]any, error) {
	form := url.Values{"app_id": {tokens.AppID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/api/apps.manifest.export", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create export request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := core.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("perform export request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		OK       bool           `json:"ok"`
		Manifest map[string]any `json:"manifest"`
		Error    string         `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse export response: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("slack API error: %s", result.Error)
	}
	return result.Manifest, nil
}

func (p *Platform) validateManifest(ctx context.Context, apiBase string, tokens *ManifestTokens, manifest map[string]any) error {
	payload := map[string]any{
		"app_id":   tokens.AppID,
		"manifest": manifest,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal validate payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/api/apps.manifest.validate", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create validate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := core.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform validate request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if apiErr := parseSlackManifestAPIError(respBody); apiErr != "" {
		return fmt.Errorf("slack API error: %s", apiErr)
	}
	return nil
}

func (p *Platform) updateManifest(ctx context.Context, apiBase string, tokens *ManifestTokens, manifest map[string]any) error {
	payload := map[string]any{
		"app_id":   tokens.AppID,
		"manifest": manifest,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal update payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/api/apps.manifest.update", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create update request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := core.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform update request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if apiErr := parseSlackManifestAPIError(respBody); apiErr != "" {
		return fmt.Errorf("slack API error: %s", apiErr)
	}
	return nil
}

func extractSlashCommands(manifest map[string]any) []core.SlashCommandSpec {
	features, _ := manifest["features"].(map[string]any)
	if features == nil {
		return nil
	}
	rawCommands, _ := features["slash_commands"].([]any)
	var commands []core.SlashCommandSpec
	for _, raw := range rawCommands {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["command"].(string)
		description, _ := entry["description"].(string)
		usageHint, _ := entry["usage_hint"].(string)
		commands = append(commands, core.SlashCommandSpec{
			Name:        strings.TrimPrefix(name, "/"),
			Description: description,
			UsageHint:   usageHint,
		})
	}
	return commands
}

func commandsToManifestFormat(commands []core.SlashCommandSpec) []map[string]any {
	result := make([]map[string]any, 0, len(commands))
	for _, command := range commands {
		entry := map[string]any{
			"command":       "/" + command.Name,
			"description":   truncateManifestDescription(command.Description, 100),
			"should_escape": false,
		}
		if command.UsageHint != "" {
			entry["usage_hint"] = command.UsageHint
		}
		result = append(result, entry)
	}
	return result
}

func setSlashCommands(manifest map[string]any, commands []map[string]any) {
	features, _ := manifest["features"].(map[string]any)
	if features == nil {
		features = make(map[string]any)
		manifest["features"] = features
	}
	features["slash_commands"] = commands
}

func truncateManifestDescription(desc string, max int) string {
	desc = strings.TrimSpace(desc)
	if max <= 0 || len(desc) <= max {
		return desc
	}
	if max <= 3 {
		return desc[:max]
	}
	return strings.TrimSpace(desc[:max-3]) + "..."
}

func filterManifestCommandsForSlack(commands []core.SlashCommandSpec) ([]core.SlashCommandSpec, []string) {
	filtered := make([]core.SlashCommandSpec, 0, len(commands))
	var skipped []string
	for _, command := range commands {
		if slackReservedManifestCommandNames[strings.ToLower(command.Name)] {
			skipped = append(skipped, "/"+command.Name)
			continue
		}
		filtered = append(filtered, command)
	}
	return filtered, skipped
}

func parseSlackManifestAPIError(respBody []byte) string {
	var result struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Pointer string `json:"pointer"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return ""
	}
	if result.OK {
		return ""
	}
	if len(result.Errors) == 0 {
		return result.Error
	}
	var details []string
	for _, detail := range result.Errors {
		part := strings.TrimSpace(detail.Message)
		if detail.Pointer != "" {
			part = fmt.Sprintf("%s (%s)", part, detail.Pointer)
		}
		if detail.Code != "" {
			part = fmt.Sprintf("%s [%s]", part, detail.Code)
		}
		details = append(details, part)
	}
	return fmt.Sprintf("%s: %s", result.Error, strings.Join(details, "; "))
}
