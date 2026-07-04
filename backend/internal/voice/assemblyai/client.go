// Package assemblyai is the AssemblyAI Universal-Streaming HTTP adapter behind
// the api module's VoiceTokenMinter port (09 §6) — the one place AssemblyAI
// vocabulary is legal. It mints the short-lived streaming token the client
// uses to open the STT WebSocket directly; audio never transits our backend
// (09 §2), and the API key never leaves /backend (02 §2).
//
// Endpoint (v3): GET {BaseURL}/v3/token?expires_in_seconds=<ttl>, Authorization
// header = the raw API key. Response {token, expires_in_seconds}; expiresAt is
// computed here (now + provider window) so the port returns an absolute time.
package assemblyai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DefaultBaseURL is AssemblyAI's streaming host (09 §2). DefaultTTL is the
// token redemption window; <= 10 min per 09 §6. requestTimeout bounds the
// backend's wait on the mint call.
const (
	DefaultBaseURL = "https://streaming.assemblyai.com"
	DefaultTTL     = 8 * time.Minute
	requestTimeout = 10 * time.Second
)

// maxErrorBody caps how much of a provider error response we read.
const maxErrorBody = 1 << 20

// errMintToken is the static base for a token-mint failure (err113: wrapped
// static errors, never dynamic ones).
var errMintToken = errors.New("assemblyai: mint token")

// Config is read at the composition root (04 §8); the API key never leaves
// /backend (02 §2).
type Config struct {
	APIKey  string        // ASSEMBLYAI_API_KEY (secret) — Authorization header
	BaseURL string        // ASSEMBLYAI_BASE_URL, default DefaultBaseURL
	TTL     time.Duration // token redemption window, default DefaultTTL (<= 10 min)
}

// Client is the AssemblyAI token adapter. It satisfies api.VoiceTokenMinter.
type Client struct {
	apiKey  string
	baseURL string
	ttl     time.Duration
	http    *http.Client
}

// New builds a Client, applying defaults for BaseURL and TTL.
func New(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Client{
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
		ttl:     ttl,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// tokenResponse is AssemblyAI's GET /v3/token body.
type tokenResponse struct {
	Token           string `json:"token"`
	ExpiresInSecond int    `json:"expires_in_seconds"`
}

// MintStreamingToken mints a short-lived streaming token (09 §6). expiresAt is
// now + the requested TTL — the absolute expiry the client refreshes before.
func (c *Client) MintStreamingToken(ctx context.Context) (string, time.Time, error) {
	var tr tokenResponse
	if err := c.fetchToken(ctx, &tr); err != nil {
		return "", time.Time{}, err
	}
	if tr.Token == "" {
		return "", time.Time{}, fmt.Errorf("%w: empty token in response", errMintToken)
	}
	return tr.Token, time.Now().Add(c.ttl), nil
}

// fetchToken issues GET /v3/token and decodes the 200 body into out. The lone
// named error return lets the deferred body-close surface its error without a
// blank assignment (errcheck check-blank), the same shape as the amika
// adapter's do.
func (c *Client) fetchToken(ctx context.Context, out *tokenResponse) (err error) {
	secs := max(int(c.ttl/time.Second), 1)
	u := fmt.Sprintf("%s/v3/token?%s", c.baseURL, url.Values{
		"expires_in_seconds": {strconv.Itoa(secs)},
	}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("assemblyai: build token request: %w", err)
	}
	req.Header.Set("Authorization", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", errMintToken, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("assemblyai: close token response: %w", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if rerr != nil {
			return fmt.Errorf("%w: status %d", errMintToken, resp.StatusCode)
		}
		return fmt.Errorf("%w: status %d: %s", errMintToken, resp.StatusCode, string(body))
	}
	if derr := json.NewDecoder(resp.Body).Decode(out); derr != nil {
		return fmt.Errorf("assemblyai: decode token: %w", derr)
	}
	return nil
}
