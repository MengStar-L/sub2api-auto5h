package codex

import (
	"strings"
	"testing"
)

func TestParseSSECollectsReplyAndRequiresTerminal(t *testing.T) {
	stream := strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"2\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"1\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n" +
		"data: [DONE]\n\n")
	reply, terminal, err := parseSSE(stream)
	if err != nil || reply != "21" || terminal != "response.completed" {
		t.Fatalf("reply=%q terminal=%q err=%v", reply, terminal, err)
	}
	if _, _, err := parseSSE(strings.NewReader("data: [DONE]\n\n")); err == nil {
		t.Fatal("DONE without explicit completion must fail")
	}
}

func TestParseSSEPrefersCompletedOutputAndAllowsEmpty(t *testing.T) {
	stream := strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"wrong\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"content\":[{\"type\":\"output_text\",\"text\":\"21\"}]}]}}\n\n")
	reply, _, err := parseSSE(stream)
	if err != nil || reply != "21" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	reply, _, err = parseSSE(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"))
	if err != nil || reply != "" {
		t.Fatalf("empty reply=%q err=%v", reply, err)
	}
}

func TestParseSSEBoundsUnicodeReply(t *testing.T) {
	payload := `data: {"type":"response.output_text.done","text":"` + strings.Repeat("智", MaxReplyRunes+100) + `"}` + "\n\n" +
		`data: {"type":"response.completed","response":{"output":[]}}` + "\n\n"
	reply, _, err := parseSSE(strings.NewReader(payload))
	if err != nil || len([]rune(reply)) != MaxReplyRunes {
		t.Fatalf("runes=%d err=%v", len([]rune(reply)), err)
	}
}
