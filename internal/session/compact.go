// Package session (continued) — compaction logic. When a transcript approaches
// the provider's context window, compactMessages collapses the history into a
// summary while preserving the most recent turns.
package session

import (
	"fmt"
	"strings"
)

const (
	defaultCompactBudgetTokens = 24000
	minCompactTailMessages     = 4
	maxCompactTailMessages     = 12
)

func EstimateTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += 6
		total += estimateTextTokens(m.Role)
		total += estimateTextTokens(m.Content)
		total += estimateTextTokens(m.Reasoning)
		total += estimateTextTokens(m.ToolCallID)
		for _, call := range m.ToolCalls {
			total += 4
			total += estimateTextTokens(call.ID)
			total += estimateTextTokens(call.Name)
			total += estimateTextTokens(string(call.Args))
		}
	}
	return total
}

func ForceCompactMessages(messages []Message) ([]Message, bool) {
	if len(messages) == 0 {
		return messages, false
	}
	if len(messages) == 1 && messages[0].Kind == "summary" {
		return messages, false
	}
	summary := SummarizeMessages(messages)
	if summary == "" {
		return messages, false
	}
	return []Message{{Role: "system", Kind: "summary", Content: summary}}, true
}

func CompactMessages(messages []Message, currentUsage, budgetTokens int) ([]Message, bool) {
	if len(messages) <= minCompactTailMessages {
		return messages, false
	}
	if budgetTokens <= 0 {
		budgetTokens = defaultCompactBudgetTokens
	}
	if currentUsage <= 0 {
		currentUsage = EstimateTokens(messages)
	}
	if currentUsage <= budgetTokens {
		return messages, false
	}

	tail := minInt(maxCompactTailMessages, len(messages)-1)
	for tail >= minCompactTailMessages {
		start := len(messages) - tail
		if start <= 0 {
			break
		}
		summary := SummarizeMessages(messages[:start])
		if summary == "" {
			break
		}
		compacted := append([]Message{{Role: "system", Kind: "summary", Content: summary}}, cloneMessages(messages[start:])...)
		if EstimateTokens(compacted) <= budgetTokens || tail == minCompactTailMessages {
			return compacted, true
		}
		tail--
	}
	return messages, false
}

func SummarizeMessages(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "Summary of earlier conversation:")
	for _, m := range messages {
		switch m.Kind {
		case "summary":
			text := trimSummaryPrefix(m.Content)
			if text != "" {
				lines = append(lines, "- prior summary: "+truncateCompact(text, 240))
			}
		case "tool_call":
			parts := make([]string, 0, len(m.ToolCalls))
			for _, call := range m.ToolCalls {
				part := call.Name
				if args := strings.TrimSpace(string(call.Args)); args != "" && args != "{}" {
					part += " " + truncateCompact(args, 80)
				}
				parts = append(parts, part)
			}
			text := "assistant called tools"
			if len(parts) > 0 {
				text += ": " + strings.Join(parts, "; ")
			}
			if extra := strings.TrimSpace(m.Content); extra != "" {
				text += "; said: " + truncateCompact(extra, 120)
			}
			if reason := strings.TrimSpace(m.Reasoning); reason != "" {
				text += "; reasoning: " + truncateCompact(reason, 120)
			}
			lines = append(lines, "- "+text)
		case "tool_result":
			text := strings.TrimSpace(m.Content)
			if text == "" {
				text = fmt.Sprintf("tool result for %s", m.ToolCallID)
			}
			lines = append(lines, "- tool result: "+truncateCompact(text, 160))
		default:
			prefix := m.Role
			if prefix == "" {
				prefix = "message"
			}
			text := strings.TrimSpace(m.Content)
			if text == "" {
				text = strings.TrimSpace(m.Reasoning)
			}
			if text == "" {
				continue
			}
			lines = append(lines, "- "+prefix+": "+truncateCompact(text, 180))
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func trimSummaryPrefix(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "Summary of earlier conversation:")
	return strings.TrimSpace(s)
}

func truncateCompact(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max || max <= 0 {
		return s
	}
	return string(r[:max-3]) + "..."
}

func estimateTextTokens(s string) int {
	r := len([]rune(s))
	if r == 0 {
		return 0
	}
	return (r + 3) / 4
}

func cloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, 0, len(messages))
	for _, m := range messages {
		clone := m
		if len(m.ToolCalls) > 0 {
			clone.ToolCalls = append([]ToolCall(nil), m.ToolCalls...)
		}
		out = append(out, clone)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
