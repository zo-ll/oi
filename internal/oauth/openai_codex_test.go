package oauth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseAuthorizationInput(t *testing.T) {
	code, state := parseAuthorizationInput("https://localhost:1455/auth/callback?code=abc&state=xyz")
	if code != "abc" || state != "xyz" {
		t.Fatalf("got %q %q", code, state)
	}
	code, state = parseAuthorizationInput("abc#xyz")
	if code != "abc" || state != "xyz" {
		t.Fatalf("got %q %q", code, state)
	}
}

func TestExtractAccountID(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadJSON, _ := json.Marshal(map[string]any{
		openAICodexJWTClaimPath: map[string]any{"chatgpt_account_id": "acct_123"},
	})
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	token := header + "." + payload + ".sig"
	accountID, err := extractAccountID(token)
	if err != nil {
		t.Fatal(err)
	}
	if accountID != "acct_123" {
		t.Fatalf("account_id = %q", accountID)
	}
}
