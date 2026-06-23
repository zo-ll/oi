// Package chat (continued) — completion engine for file paths and slash
// commands. Tracks the current completion state (candidates, index),
// exposes the pickerUI interface, and defines tab-completion helpers.
package chat

import "strings"

type completionState struct {
	candidates []string
	index      int
}

type pickerUI interface {
	overlayPicker(title string, items []string) (string, bool)
}

type inputUI interface {
	overlayInput(title, prompt, initial string) (string, bool)
}

const maxCompletionMatchesShown = 7

func (c *completionContext) completeAtPath(current string) (next string, status string, matches []string, changed bool, err error) {
	matches, err = c.completionMatchesForText(current)
	if err != nil {
		return current, "", nil, false, err
	}
	if len(matches) == 0 {
		c.completion = completionState{}
		return current, "", nil, false, nil
	}
	if exact := exactMatch(current, matches); exact != "" {
		c.completion = completionState{candidates: []string{exact}, index: 0}
		return replaceTrailingToken(current, exact), exact, matches, true, nil
	}
	if len(matches) == 1 {
		c.completion = completionState{candidates: matches, index: 0}
		return replaceTrailingToken(current, matches[0]), matches[0], matches, true, nil
	}
	c.completion = completionState{}
	return current, formatCompletionMatches(matches), matches, false, nil
}

func (c *completionContext) completionMatchesForText(current string) ([]string, error) {
	if c == nil {
		return nil, nil
	}
	start, _, token, ok := trailingToken(current)
	if !ok {
		return nil, nil
	}
	if strings.HasPrefix(token, "@") {
		query := strings.TrimSpace(strings.TrimPrefix(token, "@"))
		files, err := c.workspaceFiles()
		if err != nil {
			return nil, err
		}
		if query == "" {
			if len(files) > maxCompletionMatchesShown*3 {
				files = files[:maxCompletionMatchesShown*3]
			}
			if len(files) == 0 {
				return nil, nil
			}
			return files, nil
		}
		return fuzzyFileMatches(query, files, maxCompletionMatchesShown+1), nil
	}
	if start == 0 && strings.HasPrefix(token, "/") {
		query := strings.TrimSpace(strings.TrimPrefix(token, "/"))
		cmds := chatCommands()
		if query == "" {
			return cmds, nil
		}
		return filterByPrefix(cmds, query), nil
	}
	return nil, nil
}
