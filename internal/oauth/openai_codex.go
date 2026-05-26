package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	openAICodexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAICodexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	openAICodexTokenURL     = "https://auth.openai.com/oauth/token"
	openAICodexRedirectURI  = "http://localhost:1455/auth/callback"
	openAICodexScope        = "openid profile email offline_access"
	openAICodexJWTClaimPath = "https://api.openai.com/auth"
)

// OpenAICodexCredentials stores refreshable ChatGPT Codex OAuth credentials.
type OpenAICodexCredentials struct {
	Access    string    `json:"access"`
	Refresh   string    `json:"refresh"`
	AccountID string    `json:"account_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AuthInfo describes the browser auth step.
type AuthInfo struct {
	URL          string
	Instructions string
}

// LoginOpenAICodex performs the ChatGPT Codex OAuth flow.
func LoginOpenAICodex(ctx context.Context, onAuth func(AuthInfo), prompt func(string) (string, error)) (OpenAICodexCredentials, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return OpenAICodexCredentials{}, err
	}
	state, err := randomHex(16)
	if err != nil {
		return OpenAICodexCredentials{}, err
	}
	loginURL, err := buildAuthorizationURL(challenge, state, "oi")
	if err != nil {
		return OpenAICodexCredentials{}, err
	}
	server, err := startCallbackServer(state)
	if err != nil {
		return OpenAICodexCredentials{}, err
	}
	defer server.Close()
	if onAuth != nil {
		onAuth(AuthInfo{
			URL:          loginURL,
			Instructions: "Complete login in the browser. If the redirect does not complete automatically, paste the final redirect URL or code back into oi.",
		})
	}

	manualCh := make(chan manualCodeResult, 1)
	if prompt != nil {
		go func() {
			input, err := prompt("Paste authorization code or full redirect URL (optional): ")
			manualCh <- manualCodeResult{input: input, err: err}
		}()
	}

	var code string
	for code == "" {
		select {
		case <-ctx.Done():
			return OpenAICodexCredentials{}, ctx.Err()
		case result := <-server.Codes:
			if result.err != nil {
				return OpenAICodexCredentials{}, result.err
			}
			code = result.code
		case manual := <-manualCh:
			if manual.err != nil {
				return OpenAICodexCredentials{}, manual.err
			}
			parsedCode, parsedState := parseAuthorizationInput(manual.input)
			if parsedState != "" && parsedState != state {
				return OpenAICodexCredentials{}, fmt.Errorf("state mismatch")
			}
			code = parsedCode
		}
	}

	creds, err := exchangeOpenAICodexCode(ctx, code, verifier)
	if err != nil {
		return OpenAICodexCredentials{}, err
	}
	return creds, nil
}

// RefreshOpenAICodexToken refreshes an existing ChatGPT Codex token set.
func RefreshOpenAICodexToken(ctx context.Context, refreshToken string) (OpenAICodexCredentials, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", strings.TrimSpace(refreshToken))
	form.Set("client_id", openAICodexClientID)
	return exchangeOpenAICodexForm(ctx, form)
}

// RefreshOpenAICodexTokenIfNeeded refreshes credentials if expired or near expiry.
func RefreshOpenAICodexTokenIfNeeded(ctx context.Context, creds OpenAICodexCredentials) (OpenAICodexCredentials, bool, error) {
	if strings.TrimSpace(creds.Access) == "" || strings.TrimSpace(creds.Refresh) == "" {
		return OpenAICodexCredentials{}, false, fmt.Errorf("missing oauth credentials")
	}
	if time.Until(creds.ExpiresAt) > 30*time.Second {
		return creds, false, nil
	}
	fresh, err := RefreshOpenAICodexToken(ctx, creds.Refresh)
	if err != nil {
		return OpenAICodexCredentials{}, false, err
	}
	return fresh, true, nil
}

type callbackServer struct {
	listener net.Listener
	server   *http.Server
	Codes    chan callbackCodeResult
}

type callbackCodeResult struct {
	code string
	err  error
}

type manualCodeResult struct {
	input string
	err   error
}

func startCallbackServer(state string) (*callbackServer, error) {
	ln, err := net.Listen("tcp", "localhost:1455")
	if err != nil {
		return nil, err
	}
	codes := make(chan callbackCodeResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			select {
			case codes <- callbackCodeResult{err: fmt.Errorf("state mismatch")}:
			default:
			}
			return
		}
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			select {
			case codes <- callbackCodeResult{err: fmt.Errorf("missing authorization code")}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body><p>OpenAI Codex authentication completed. You can close this window.</p></body></html>")
		select {
		case codes <- callbackCodeResult{code: code}:
		default:
		}
	})
	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(ln)
	}()
	return &callbackServer{listener: ln, server: srv, Codes: codes}, nil
}

func (s *callbackServer) Close() {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
	_ = s.listener.Close()
}

func buildAuthorizationURL(challenge, state, originator string) (string, error) {
	u, err := url.Parse(openAICodexAuthorizeURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", openAICodexClientID)
	q.Set("redirect_uri", openAICodexRedirectURI)
	q.Set("scope", openAICodexScope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", originator)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func generatePKCE() (verifier string, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func exchangeOpenAICodexCode(ctx context.Context, code, verifier string) (OpenAICodexCredentials, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", openAICodexClientID)
	form.Set("code", strings.TrimSpace(code))
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", openAICodexRedirectURI)
	return exchangeOpenAICodexForm(ctx, form)
}

func exchangeOpenAICodexForm(ctx context.Context, form url.Values) (OpenAICodexCredentials, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAICodexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OpenAICodexCredentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return OpenAICodexCredentials{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return OpenAICodexCredentials{}, err
	}
	if resp.StatusCode >= 400 {
		return OpenAICodexCredentials{}, fmt.Errorf("oauth token exchange failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return OpenAICodexCredentials{}, fmt.Errorf("parse oauth token response: %w", err)
	}
	if raw.AccessToken == "" || raw.RefreshToken == "" || raw.ExpiresIn <= 0 {
		return OpenAICodexCredentials{}, fmt.Errorf("oauth token response missing fields")
	}
	accountID, err := extractAccountID(raw.AccessToken)
	if err != nil {
		return OpenAICodexCredentials{}, err
	}
	return OpenAICodexCredentials{
		Access:    raw.AccessToken,
		Refresh:   raw.RefreshToken,
		AccountID: accountID,
		ExpiresAt: time.Now().UTC().Add(time.Duration(raw.ExpiresIn) * time.Second),
	}, nil
}

func parseAuthorizationInput(input string) (code string, state string) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", ""
	}
	if u, err := url.Parse(value); err == nil && u.Scheme != "" && u.Host != "" {
		return strings.TrimSpace(u.Query().Get("code")), strings.TrimSpace(u.Query().Get("state"))
	}
	if strings.Contains(value, "code=") {
		q, err := url.ParseQuery(value)
		if err == nil {
			return strings.TrimSpace(q.Get("code")), strings.TrimSpace(q.Get("state"))
		}
	}
	if strings.Contains(value, "#") {
		parts := strings.SplitN(value, "#", 2)
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return value, ""
}

func extractAccountID(accessToken string) (string, error) {
	parts := strings.Split(strings.TrimSpace(accessToken), ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid access token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode access token payload: %w", err)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse access token payload: %w", err)
	}
	var authClaims struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	}
	if raw, ok := claims[openAICodexJWTClaimPath]; ok {
		if err := json.Unmarshal(raw, &authClaims); err == nil && strings.TrimSpace(authClaims.ChatGPTAccountID) != "" {
			return strings.TrimSpace(authClaims.ChatGPTAccountID), nil
		}
	}
	return "", fmt.Errorf("failed to extract account id from access token")
}
