package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/zo-ll/oi/internal/agent"
	"github.com/zo-ll/oi/internal/config"
	"github.com/zo-ll/oi/internal/provider"
	"github.com/zo-ll/oi/internal/workspace"
)

type readyModelChoice struct {
	Provider string
	Model    provider.Model
}

func switchChatModel(cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, model string) (*agent.Runtime, config.Selection, error) {
	choice, err := resolveReadyModelChoice(model, sel.Provider)
	if err != nil {
		return nil, sel, err
	}
	return switchChatModelToChoice(cfg, sel, rt, reader, out, choice)
}

func switchChatModelToChoice(cfg *config.Config, sel config.Selection, rt *agent.Runtime, reader *bufio.Reader, out io.Writer, choice readyModelChoice) (*agent.Runtime, config.Selection, error) {
	nextSel := config.Selection{Provider: choice.Provider, Model: choice.Model.ID}
	cfg2, nextSel, err := loadSelection(commonOptions{provider: nextSel.Provider, model: nextSel.Model})
	if err != nil {
		return nil, sel, err
	}
	cfg2.SelectedProvider = choice.Provider
	cfg2.SelectedModel = choice.Model.ID
	if err := config.Save(cfg2); err != nil {
		return nil, sel, err
	}
	p, err := requireProvider(nextSel)
	if err != nil {
		return nil, sel, err
	}
	root, err := workspace.DetectRoot(rt.Policy.Root)
	if err != nil {
		return nil, sel, err
	}
	*cfg = *cfg2
	nextRT := buildRuntime(cfg, nextSel, p, root, reader, out, rt.Logger)
	currentLevel := "off"
	if rt != nil && rt.ThinkingLevel != "" {
		currentLevel = rt.ThinkingLevel
	}
	modelInfo := provider.Model{SupportsThinking: nextRT.ThinkingSupported, SupportedThinkingLevels: nextRT.SupportedThinkingLevels, ThinkingLevelValues: nextRT.ThinkingLevelValues}
	nextRT.ThinkingLevel = clampThinkingLevel(modelInfo, currentLevel)
	nextRT.ThinkingValue = thinkingValue(modelInfo, nextRT.ThinkingLevel)
	if nextRT.Session != nil {
		nextRT.Session.ThinkingLevel = nextRT.ThinkingLevel
	}
	fmt.Fprintf(out, "model set to %s\n", choice.Model.ID)
	return nextRT, nextSel, nil
}

func promptModelChoice(reader *bufio.Reader, out io.Writer, current config.Selection) (string, error) {
	choices, err := listReadyModelChoices()
	if err != nil {
		return "", err
	}
	return promptReadyModelChoice(reader, out, choices, current, "Switch model? [number/name, blank=keep] ")
}

func resolveReadyModelChoice(arg string, currentProvider string) (readyModelChoice, error) {
	choices, err := listReadyModelChoices()
	if err != nil {
		return readyModelChoice{}, err
	}
	if len(choices) == 0 {
		return readyModelChoice{}, fmt.Errorf("no ready models; use /login")
	}
	return resolveReadyModelChoiceFromList(choices, arg, currentProvider)
}

func resolveReadyModelChoiceFromList(choices []readyModelChoice, arg string, currentProvider string) (readyModelChoice, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return readyModelChoice{}, fmt.Errorf("model is required")
	}
	if idx, ok := parseSessionIndex(arg); ok {
		if idx < 1 || idx > len(choices) {
			return readyModelChoice{}, fmt.Errorf("model index out of range: %d", idx)
		}
		return choices[idx-1], nil
	}
	var matches []readyModelChoice
	for _, choice := range choices {
		if choice.Model.ID == arg || strings.EqualFold(choice.Model.ID, arg) {
			matches = append(matches, choice)
		}
	}
	if len(matches) == 0 {
		return readyModelChoice{}, fmt.Errorf("ready model not found: %s", arg)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	for _, choice := range matches {
		if choice.Provider == currentProvider {
			return choice, nil
		}
	}
	return readyModelChoice{}, fmt.Errorf("model %q is available from multiple providers; choose by index", arg)
}

func ensureReadyModelAfterLogin(reader *bufio.Reader, out io.Writer, cfg *config.Config, sel config.Selection) (*config.Config, config.Selection, provider.Provider, error) {
	choices, err := listReadyModelChoicesForProvider(sel.Provider)
	if err != nil || len(choices) == 0 || selectionHasReadyModel(sel, choices) {
		p, pErr := requireProvider(sel)
		return cfg, sel, p, pErr
	}
	choice, err := promptReadyModelChoice(reader, out, choices, sel, "Choose model? [number/name, blank=keep] ")
	if err != nil {
		return nil, sel, nil, err
	}
	if choice == "" {
		p, pErr := requireProvider(sel)
		return cfg, sel, p, pErr
	}
	nextCfg, nextSel, err := loadSelection(commonOptions{provider: sel.Provider, model: choice})
	if err != nil {
		return nil, sel, nil, err
	}
	p, err := requireProvider(nextSel)
	if err != nil {
		return nil, sel, nil, err
	}
	return nextCfg, nextSel, p, nil
}

func selectionHasReadyModel(sel config.Selection, choices []readyModelChoice) bool {
	if strings.TrimSpace(sel.Model) == "" {
		return false
	}
	for _, choice := range choices {
		if choice.Provider == sel.Provider && choice.Model.ID == sel.Model {
			return true
		}
	}
	return false
}

func listReadyModelChoicesForProvider(providerName string) ([]readyModelChoice, error) {
	choices, err := listReadyModelChoices()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(providerName) == "" {
		return choices, nil
	}
	var out []readyModelChoice
	for _, choice := range choices {
		if choice.Provider == providerName {
			out = append(out, choice)
		}
	}
	return out, nil
}

func promptReadyModelChoice(reader *bufio.Reader, out io.Writer, choices []readyModelChoice, current config.Selection, prompt string) (string, error) {
	if len(choices) == 0 {
		fmt.Fprintln(out, "no ready models; use /login")
		return "", nil
	}
	if picker, ok := out.(pickerUI); ok {
		return pickReadyModelChoice(picker, choices, current)
	}
	fmt.Fprintf(out, "current model: %s\n", valueOr(current.Model, "(none)"))
	printReadyModels(out, choices, current)
	fmt.Fprint(out, prompt)
	choice, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		return "", nil
	}
	if _, err := resolveReadyModelChoiceFromList(choices, choice, current.Provider); err != nil {
		return "", err
	}
	return choice, nil
}

func pickReadyModelChoice(picker pickerUI, choices []readyModelChoice, current config.Selection) (string, error) {
	labels, byLabel := readyModelChoiceLabels(choices, current)
	selected, ok := picker.overlayPicker("choose model", labels)
	if !ok || selected == "" {
		return "", nil
	}
	if choice, found := byLabel[selected]; found {
		return choice.Model.ID, nil
	}
	return selected, nil
}

func readyModelChoiceLabels(choices []readyModelChoice, current config.Selection) ([]string, map[string]readyModelChoice) {
	singleProvider := len(choices) > 0
	providerName := ""
	if singleProvider {
		providerName = choices[0].Provider
	}
	for _, choice := range choices[1:] {
		if choice.Provider != providerName {
			singleProvider = false
			break
		}
	}
	labels := make([]string, 0, len(choices))
	byLabel := make(map[string]readyModelChoice, len(choices))
	for _, choice := range choices {
		label := choice.Model.ID
		if !singleProvider {
			label += "  [" + choice.Provider + "]"
		}
		if choice.Provider == current.Provider && choice.Model.ID == current.Model {
			label += "  (current)"
		}
		labels = append(labels, label)
		byLabel[label] = choice
	}
	return labels, byLabel
}

func listReadyModelChoices() ([]readyModelChoice, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	auth, err := config.LoadAuth()
	if err != nil {
		return nil, err
	}
	var out []readyModelChoice
	for _, name := range config.ProviderNames(cfg) {
		sel, err := config.ResolveSelection(cfg, auth, name, "", "")
		if err != nil {
			continue
		}
		p, err := requireProvider(sel)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		models, err := p.ListModels(ctx)
		cancel()
		if err != nil {
			continue
		}
		for _, model := range models {
			out = append(out, readyModelChoice{Provider: name, Model: model})
		}
	}
	return out, nil
}

func modelPickerPick(picker pickerUI, currentProvider string) (string, error) {
	choices, err := listReadyModelChoices()
	if err != nil {
		return "", err
	}
	if len(choices) == 0 {
		return "", fmt.Errorf("no ready models; use /login")
	}
	labels, modelToLabel := readyModelChoiceLabels(choices, config.Selection{Provider: currentProvider})
	selected, ok := picker.overlayPicker("choose model", labels)
	if !ok || selected == "" {
		return "", nil
	}
	if mc, found := modelToLabel[selected]; found {
		return mc.Model.ID, nil
	}
	return selected, nil
}

func printReadyModels(out io.Writer, choices []readyModelChoice, current config.Selection) {
	singleProvider := true
	if len(choices) > 1 {
		providerName := choices[0].Provider
		for _, choice := range choices[1:] {
			if choice.Provider != providerName {
				singleProvider = false
				break
			}
		}
	}
	for i, choice := range choices {
		marker := " "
		if choice.Provider == current.Provider && choice.Model.ID == current.Model {
			marker = "*"
		}
		label := choice.Model.ID
		if strings.TrimSpace(choice.Model.Name) != "" && choice.Model.Name != choice.Model.ID {
			label += "  " + choice.Model.Name
		}
		if singleProvider {
			fmt.Fprintf(out, "%2d. %s %s\n", i+1, marker, label)
			continue
		}
		fmt.Fprintf(out, "%2d. %s %s  [%s]\n", i+1, marker, label, choice.Provider)
	}
}
