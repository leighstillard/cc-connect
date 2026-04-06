package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenManager_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	tm := NewTokenManager(dir, "A0TEST123")

	tokens := &ManifestTokens{
		AppID:        "A0TEST123",
		AccessToken:  "xoxe.xoxp-test-access",
		RefreshToken: "xoxe-test-refresh",
		ExpiresAt:    time.Now().Add(12 * time.Hour),
	}

	if err := tm.Save(tokens); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "manifest_token_A0TEST123.json"))
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 permissions, got %o", info.Mode().Perm())
	}

	loaded, err := tm.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.AccessToken != tokens.AccessToken {
		t.Fatalf("access token = %q, want %q", loaded.AccessToken, tokens.AccessToken)
	}
	if loaded.RefreshToken != tokens.RefreshToken {
		t.Fatalf("refresh token = %q, want %q", loaded.RefreshToken, tokens.RefreshToken)
	}
}

func TestManifestTokens_IsExpired(t *testing.T) {
	tokens := &ManifestTokens{ExpiresAt: time.Now().Add(-time.Hour)}
	if !tokens.IsExpired() {
		t.Fatal("expected expired token to report expired")
	}

	tokens.ExpiresAt = time.Now().Add(time.Hour)
	if tokens.IsExpired() {
		t.Fatal("expected fresh token to report not expired")
	}

	tokens.ExpiresAt = time.Now().Add(3 * time.Minute)
	if !tokens.IsExpired() {
		t.Fatal("expected near-expiry token to report expired")
	}
}

func TestTokenManager_LoadMissing(t *testing.T) {
	tm := NewTokenManager(t.TempDir(), "A0MISSING")
	if _, err := tm.Load(); err == nil {
		t.Fatal("expected missing token file to return an error")
	}
}

func TestTokenManager_EnsureValidTokenRefreshes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tooling.tokens.rotate" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", got)
		}
		if got := r.Form.Get("refresh_token"); got != "xoxe-refresh" {
			t.Fatalf("refresh_token = %q, want xoxe-refresh", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":            true,
			"token":         "xoxe-access-new",
			"refresh_token": "xoxe-refresh-new",
			"exp_in":        3600,
		})
	}))
	defer server.Close()

	tm := NewTokenManager(t.TempDir(), "A0TEST123")
	tm.apiBase = server.URL
	if err := tm.Save(&ManifestTokens{
		AppID:        "A0TEST123",
		AccessToken:  "xoxe-access-old",
		RefreshToken: "xoxe-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tokens, err := tm.EnsureValidToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureValidToken: %v", err)
	}
	if tokens.AccessToken != "xoxe-access-new" {
		t.Fatalf("access token = %q, want refreshed token", tokens.AccessToken)
	}
	if tokens.RefreshToken != "xoxe-refresh-new" {
		t.Fatalf("refresh token = %q, want refreshed token", tokens.RefreshToken)
	}

	loaded, err := tm.Load()
	if err != nil {
		t.Fatalf("Load after refresh: %v", err)
	}
	if loaded.AccessToken != "xoxe-access-new" {
		t.Fatalf("persisted access token = %q, want refreshed token", loaded.AccessToken)
	}
}
