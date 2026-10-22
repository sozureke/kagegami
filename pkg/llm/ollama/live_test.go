package ollama

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sozureke/kagegami/pkg/llm"
)

// TestLiveOllama talks to a real Ollama. It is off unless KAGEGAMI_LIVE_OLLAMA is set, and it
// refuses to send anything to an address that is not this machine's own.
//
//	KAGEGAMI_LIVE_OLLAMA=1 go test ./pkg/llm/ollama -run Live -v
//	(KAGEGAMI_OLLAMA_MODEL picks the model, default llama3.1:8b; KAGEGAMI_OLLAMA_URL the address)
func TestLiveOllama(t *testing.T) {
	if os.Getenv("KAGEGAMI_LIVE_OLLAMA") == "" {
		t.Skip("set KAGEGAMI_LIVE_OLLAMA=1 to talk to a real Ollama")
	}
	base := os.Getenv("KAGEGAMI_OLLAMA_URL")
	if base == "" {
		base = DefaultBaseURL
	}
	if local, err := llm.IsLocal(bg, base, nil); err != nil || !local {
		t.Fatalf("%s is not this machine (%v): the live test sends only to localhost", base, err)
	}
	model := os.Getenv("KAGEGAMI_OLLAMA_MODEL")
	if model == "" {
		model = "llama3.1:8b"
	}
	p, err := New(Config{Model: model, BaseURL: base})
	if err != nil {
		t.Fatal(err)
	}

	models, err := p.Models(bg)
	if err != nil {
		t.Fatalf("Ollama is not reachable: %v", err)
	}
	if !slices.Contains(models, model) {
		t.Skipf("the model %s is not pulled (have %v)", model, models)
	}

	zero := 0.0
	resp, err := llm.Checked(p).Complete(bg, llm.Request{
		System:      "You read the output of a command and report the open TCP ports in it. Answer only with JSON.",
		Messages:    []llm.Message{{Role: llm.User, Content: "<output>\nPORT     STATE SERVICE\n22/tcp   open  ssh\n80/tcp   closed http\n8000/tcp open  http-alt\n</output>"}},
		Schema:      json.RawMessage(`{"type":"object","properties":{"open_ports":{"type":"array","items":{"type":"integer"}}},"required":["open_ports"]}`),
		MaxTokens:   200,
		Temperature: &zero,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("answer: %s (prompt %d tokens, answer %d tokens, finish %s)", strings.TrimSpace(resp.Text), resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Finish)
	var got struct {
		OpenPorts []int `json:"open_ports"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &got); err != nil {
		t.Fatalf("the answer is not the JSON that was asked for: %v", err)
	}
	if !slices.Contains(got.OpenPorts, 22) || !slices.Contains(got.OpenPorts, 8000) || slices.Contains(got.OpenPorts, 80) {
		t.Errorf("open_ports = %v, want 22 and 8000 and not 80", got.OpenPorts)
	}
	if resp.Usage.PromptTokens == 0 || resp.Usage.CompletionTokens == 0 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}
