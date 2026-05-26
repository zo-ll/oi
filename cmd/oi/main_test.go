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
}

func TestNormalizeAPIKey(t *testing.T) {
	if got := normalizeAPIKey("Bearer sk-test\n"); got != "sk-test" {
		t.Fatalf("key = %q", got)
	}
}
