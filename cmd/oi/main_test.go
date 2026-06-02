package main

import (
	"testing"

	"github.com/zo-ll/oi/internal/config"
)

func TestKnownProviderProfile(t *testing.T) {
	got, ok := knownProviderProfile("openai")
	if !ok {
		t.Fatal("expected known provider")
	}
	want := config.ProviderConfig{BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"}
	if got != want {
		t.Fatalf("profile = %+v want %+v", got, want)
	}
	got, ok = knownProviderProfile("chatgpt")
	if !ok {
		t.Fatal("expected chatgpt alias")
	}
	want = config.ProviderConfig{BaseURL: "https://chatgpt.com/backend-api"}
	if got != want {
		t.Fatalf("profile = %+v want %+v", got, want)
	}
	got, ok = knownProviderProfile("openrouter")
	if !ok {
		t.Fatal("expected openrouter profile")
	}
	want = config.ProviderConfig{BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY"}
	if got != want {
		t.Fatalf("profile = %+v want %+v", got, want)
	}
}

func TestCanonicalProviderName(t *testing.T) {
	if got := canonicalProviderName("chatgpt"); got != "openai-codex" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeAPIKey(t *testing.T) {
	if got := normalizeAPIKey("Bearer sk-test\n"); got != "sk-test" {
		t.Fatalf("key = %q", got)
	}
}
