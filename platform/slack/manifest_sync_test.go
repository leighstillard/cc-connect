package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestSyncManifest_NoTokenSkips(t *testing.T) {
	p := &Platform{}
	err := p.SyncManifest(context.Background(), []core.SlashCommandSpec{
		{Name: "test", Description: "Test command"},
	})
	if err != nil {
		t.Fatalf("expected nil error when no token manager is configured, got %v", err)
	}
}

func TestSyncManifest_NoChanges(t *testing.T) {
	var updateCalled bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/apps.manifest.export":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"manifest": map[string]any{
					"features": map[string]any{
						"slash_commands": []any{
							map[string]any{"command": "/help", "description": "Show help"},
						},
					},
				},
			})
		case "/api/apps.manifest.validate":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/api/apps.manifest.update":
			updateCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			t.Fatalf("unexpected API path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	tm := NewTokenManager(t.TempDir(), "A0TEST")
	if err := tm.Save(&ManifestTokens{
		AppID:       "A0TEST",
		AccessToken: "test-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := &Platform{
		manifestTokenMgr: tm,
		manifestAPIBase:  server.URL,
	}
	if err := p.SyncManifest(context.Background(), []core.SlashCommandSpec{
		{Name: "help", Description: "Show help"},
	}); err != nil {
		t.Fatalf("SyncManifest: %v", err)
	}
	if updateCalled {
		t.Fatal("apps.manifest.update should not be called when there is no drift")
	}
}

func TestSyncManifest_RejectsTooManyCommands(t *testing.T) {
	tm := NewTokenManager(t.TempDir(), "A0TEST")
	if err := tm.Save(&ManifestTokens{
		AppID:       "A0TEST",
		AccessToken: "test-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var commands []core.SlashCommandSpec
	for i := 0; i < maxSlackManifestSlashCommands+1; i++ {
		commands = append(commands, core.SlashCommandSpec{
			Name:        fmt.Sprintf("cmd-%d", i),
			Description: "Test command",
		})
	}

	p := &Platform{
		manifestTokenMgr: tm,
	}
	err := p.SyncManifest(context.Background(), commands)
	if err == nil {
		t.Fatal("expected too many commands error")
	}
	if got := err.Error(); got == "" || got == "invalid_manifest" {
		t.Fatalf("expected explicit limit error, got %q", got)
	}
}

func TestSyncManifest_FiltersReservedNames(t *testing.T) {
	filtered, skipped := filterManifestCommandsForSlack([]core.SlashCommandSpec{
		{Name: "search", Description: "Search"},
		{Name: "help", Description: "Help"},
		{Name: "status", Description: "Status"},
	})

	if len(filtered) != 1 || filtered[0].Name != "help" {
		t.Fatalf("filtered = %#v, want only /help", filtered)
	}
	if len(skipped) != 2 || skipped[0] != "/search" || skipped[1] != "/status" {
		t.Fatalf("skipped = %#v, want [/search /status]", skipped)
	}
}

func TestSyncManifest_UpdatesOnDrift(t *testing.T) {
	var updateCalled bool
	var updatedPayload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/apps.manifest.export":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"manifest": map[string]any{
					"features": map[string]any{
						"slash_commands": []any{
							map[string]any{"command": "/help", "description": "Show help"},
						},
					},
					"oauth_config": map[string]any{
						"scopes": "preserved",
					},
				},
			})
		case "/api/apps.manifest.validate":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/api/apps.manifest.update":
			updateCalled = true
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &updatedPayload); err != nil {
				t.Fatalf("Unmarshal update payload: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			t.Fatalf("unexpected API path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	tm := NewTokenManager(t.TempDir(), "A0TEST")
	if err := tm.Save(&ManifestTokens{
		AppID:       "A0TEST",
		AccessToken: "test-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := &Platform{
		manifestTokenMgr: tm,
		manifestAPIBase:  server.URL,
	}
	if err := p.SyncManifest(context.Background(), []core.SlashCommandSpec{
		{Name: "help", Description: "Show help"},
		{Name: "commit", Description: "Create a git commit", UsageHint: "[message]"},
	}); err != nil {
		t.Fatalf("SyncManifest: %v", err)
	}
	if !updateCalled {
		t.Fatal("apps.manifest.update should be called when there is drift")
	}

	manifest, ok := updatedPayload["manifest"].(map[string]any)
	if !ok {
		t.Fatalf("updated payload missing manifest: %#v", updatedPayload)
	}
	if _, ok := manifest["oauth_config"]; !ok {
		t.Fatal("expected oauth_config to be preserved during update")
	}
}

func TestSyncManifest_ValidateIncludesDetailedErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/apps.manifest.export":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"manifest": map[string]any{
					"features": map[string]any{},
				},
			})
		case "/api/apps.manifest.validate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "invalid_manifest",
				"errors": []map[string]any{
					{
						"code":    "invalid_name",
						"message": "The slash command has an invalid name",
						"pointer": "/features/slash_commands",
					},
				},
			})
		case "/api/apps.manifest.update":
			t.Fatal("update should not be called when validation fails")
		default:
			t.Fatalf("unexpected API path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	tm := NewTokenManager(t.TempDir(), "A0TEST")
	if err := tm.Save(&ManifestTokens{
		AppID:       "A0TEST",
		AccessToken: "test-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := &Platform{
		manifestTokenMgr: tm,
		manifestAPIBase:  server.URL,
	}
	err := p.SyncManifest(context.Background(), []core.SlashCommandSpec{
		{Name: "help", Description: "Show help"},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "invalid_manifest") || !strings.Contains(err.Error(), "invalid_name") {
		t.Fatalf("error = %q, want detailed manifest validation info", err)
	}
}
