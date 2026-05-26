package provider

import (
	"fmt"

	"github.com/zo-ll/oi/internal/config"
)

// NewForSelection constructs the correct provider for a resolved selection.
func NewForSelection(sel config.Selection) (Provider, error) {
	if sel.Provider == "" {
		return nil, fmt.Errorf("no provider selected")
	}
	switch sel.Provider {
	case "openai-codex":
		return NewOpenAICodex(sel.Provider, sel.BaseURL, sel.APIKey, sel.AccountID, sel.Model)
	default:
		if sel.APIKey == "" {
			return nil, fmt.Errorf("no API key resolved for provider %q", sel.Provider)
		}
		return NewOpenAI(sel.Provider, sel.BaseURL, sel.APIKey, sel.Model)
	}
}
