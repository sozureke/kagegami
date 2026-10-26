package cli

import (
	"strings"
	"testing"

	"github.com/sozureke/kagegami/internal/config"
)

func TestTheCardNamesBothModels(t *testing.T) {
	for _, tt := range []struct {
		m    config.Model
		want []string
	}{
		{config.Model{Tier1: "openrouter/openai/gpt-4o-mini", Tier2: "openrouter/openai/gpt-5.6-luna"}, []string{"tier 1 · gpt-4o-mini", "tier 2 · gpt-5.6-luna"}},
		{config.Model{Tier1: "ollama/llama3.1:8b"}, []string{"tier 1 · llama3.1:8b", "tier 2 · same model"}},
		{config.Model{Tier1: "ollama/llama3.1:8b", Tier2: "ollama/llama3.1:8b"}, []string{"tier 1 · llama3.1:8b", "tier 2 · same model"}},
	} {
		if got := modelFacts(tt.m); strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("modelFacts(%+v) = %v, want %v", tt.m, got, tt.want)
		}
	}
}
