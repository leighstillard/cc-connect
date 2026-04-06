package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

// ManifestTokens stores the Slack App Configuration Token state.
type ManifestTokens struct {
	AppID        string    `json:"app_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	LastSync     time.Time `json:"last_sync,omitempty"`
}

// IsExpired returns true when the access token is expired or about to expire.
func (t *ManifestTokens) IsExpired() bool {
	return time.Now().Add(5 * time.Minute).After(t.ExpiresAt)
}

// SyncDebounceOK reports whether enough time has passed since the last sync.
func (t *ManifestTokens) SyncDebounceOK() bool {
	return t.LastSync.IsZero() || time.Since(t.LastSync) > 5*time.Minute
}

// TokenManager handles reading and refreshing Slack manifest tokens.
type TokenManager struct {
	dir     string
	appID   string
	apiBase string
}

func NewTokenManager(dir, appID string) *TokenManager {
	return &TokenManager{dir: dir, appID: appID}
}

func (tm *TokenManager) path() string {
	return filepath.Join(tm.dir, fmt.Sprintf("manifest_token_%s.json", tm.appID))
}

// Save writes tokens to disk with restrictive permissions.
func (tm *TokenManager) Save(tokens *ManifestTokens) error {
	if err := os.MkdirAll(tm.dir, 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}
	if err := os.Chmod(tm.dir, 0o700); err != nil {
		return fmt.Errorf("chmod token dir: %w", err)
	}

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tokens: %w", err)
	}
	if err := core.AtomicWriteFile(tm.path(), data, 0o600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	return nil
}

// Load reads tokens from disk.
func (tm *TokenManager) Load() (*ManifestTokens, error) {
	data, err := os.ReadFile(tm.path())
	if err != nil {
		return nil, fmt.Errorf("read token file: %w", err)
	}
	var tokens ManifestTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("parse token file: %w", err)
	}
	return &tokens, nil
}

// RefreshAccessToken exchanges the refresh token for a new access token.
func (tm *TokenManager) RefreshAccessToken(ctx context.Context, tokens *ManifestTokens) (*ManifestTokens, error) {
	apiBase := strings.TrimRight(tm.apiBase, "/")
	if apiBase == "" {
		apiBase = defaultSlackAPIBase
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tokens.RefreshToken},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/api/tooling.tokens.rotate", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := core.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		OK           bool   `json:"ok"`
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
		ExpIn        int    `json:"exp_in"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("token refresh failed: %s", result.Error)
	}

	return &ManifestTokens{
		AppID:        tokens.AppID,
		AccessToken:  result.Token,
		RefreshToken: result.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(result.ExpIn) * time.Second).UTC(),
		LastSync:     tokens.LastSync,
	}, nil
}

// EnsureValidToken loads tokens from disk and refreshes them when needed.
func (tm *TokenManager) EnsureValidToken(ctx context.Context) (*ManifestTokens, error) {
	tokens, err := tm.Load()
	if err != nil {
		return nil, err
	}
	if !tokens.IsExpired() {
		return tokens, nil
	}

	refreshed, err := tm.RefreshAccessToken(ctx, tokens)
	if err != nil {
		return nil, &core.ManifestSyncError{
			Kind:  core.ManifestSyncErrorTokenExpired,
			AppID: tm.appID,
			Err:   err,
		}
	}
	if err := tm.Save(refreshed); err != nil {
		return nil, fmt.Errorf("save refreshed tokens: %w", err)
	}
	return refreshed, nil
}
