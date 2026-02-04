package main

import (
	"testing"
)

func TestParseParamBillions(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"Qwen3-8B-GGUF", 8.0},
		{"Phi-3.5-mini-1.5B-instruct", 1.5},
		{"some-model-70B-chat", 70.0},
		{"no-match-here", 0},
		{"model-0.5B-tiny", 0.5},
	}
	for _, tt := range tests {
		got := parseParamBillions(tt.input)
		if got != tt.want {
			t.Errorf("parseParamBillions(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestEstimateQ4GB(t *testing.T) {
	tests := []struct {
		billions float64
		want     float64
	}{
		{8.0, 5.3},
		{1.0, 1.1},
		{70.0, 42.5},
		{3.0, 2.3},
	}
	for _, tt := range tests {
		got := estimateQ4GB(tt.billions)
		if got != tt.want {
			t.Errorf("estimateQ4GB(%v) = %v, want %v", tt.billions, got, tt.want)
		}
	}
}

func TestSizeTags(t *testing.T) {
	tests := []struct {
		mem  float64
		want []string
	}{
		{4, []string{"1b", "3b"}},
		{8, []string{"1b", "3b", "7b", "8b"}},
		{16, []string{"1b", "3b", "7b", "8b", "14b"}},
		{32, []string{"1b", "3b", "7b", "8b", "14b", "30b", "32b", "34b"}},
		{64, []string{"1b", "3b", "7b", "8b", "14b", "30b", "32b", "34b", "70b", "72b"}},
	}
	for _, tt := range tests {
		got := sizeTags(tt.mem)
		if len(got) != len(tt.want) {
			t.Errorf("sizeTags(%v) = %v, want %v", tt.mem, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("sizeTags(%v)[%d] = %v, want %v", tt.mem, i, got[i], tt.want[i])
			}
		}
	}
}

func TestDeriveCliId(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Qwen/Qwen3-8B-GGUF", "qwen3-8b"},
		{"meta-llama/Llama-3-8B-Instruct-GGUF", "llama-3-8b"},
		{"user/Model.Name_v2-GGUF", "model-name-v2"},
		{"org/Some-Model-Instruct-GGUF", "some-model"},
	}
	for _, tt := range tests {
		got := deriveCliID(tt.input)
		if got != tt.want {
			t.Errorf("deriveCliID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildFilenameTemplate(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Qwen3-8B-Q4_K_M.gguf", "Qwen3-8B-{quant}.gguf"},
		{"model-IQ2_M.gguf", "model-{quant}.gguf"},
		{"model-q4_0.gguf", "model-{quant}.gguf"},
		{"noQuant.gguf", "noQuant.gguf"},
	}
	for _, tt := range tests {
		got := buildFilenameTemplate(tt.input)
		if got != tt.want {
			t.Errorf("buildFilenameTemplate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDeduplication(t *testing.T) {
	// Simulate deduplication logic
	seen := make(map[string]Model)

	m1 := Model{ID: "qwen3-8b", downloads: 100}
	m2 := Model{ID: "qwen3-8b", downloads: 200}
	m3 := Model{ID: "qwen3-8b", downloads: 50}

	for _, m := range []Model{m1, m2, m3} {
		if existing, ok := seen[m.ID]; ok {
			if existing.downloads >= m.downloads {
				continue
			}
		}
		seen[m.ID] = m
	}

	if seen["qwen3-8b"].downloads != 200 {
		t.Errorf("dedup kept downloads=%d, want 200", seen["qwen3-8b"].downloads)
	}
}

func TestFilterSharded(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"model-00001-of-00003.gguf", true},
		{"model-Q4_K_M.gguf", false},
		{"model-00002-of-00010.gguf", true},
	}
	for _, tt := range tests {
		got := filterSharded(tt.input)
		if got != tt.want {
			t.Errorf("filterSharded(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1234567, "1,234,567"},
	}
	for _, tt := range tests {
		got := formatNumber(tt.input)
		if got != tt.want {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
