package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type responseEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Text     string          `json:"text"`
	Response json.RawMessage `json:"response"`
	Error    *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseSSE(body io.Reader) (string, string, error) {
	contents, err := io.ReadAll(io.LimitReader(body, MaxSSEBytes+1))
	if err != nil {
		return "", "", &Error{Kind: ErrorTransient, Message: "read Codex stream failed", Cause: err}
	}
	if len(contents) > MaxSSEBytes {
		return "", "", &Error{Kind: ErrorSchema, Message: "Codex stream exceeds 1 MiB"}
	}
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 64<<10), MaxSSEBytes)
	var deltas strings.Builder
	doneText := ""
	completedText := ""
	terminal := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event responseEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return "", "", &Error{Kind: ErrorSchema, Message: "invalid Codex SSE JSON", Cause: err}
		}
		switch event.Type {
		case "response.output_text.delta":
			appendBounded(&deltas, event.Delta)
		case "response.output_text.done":
			doneText = limitRunes(event.Text, MaxReplyRunes)
		case "response.completed", "response.done":
			terminal = event.Type
			if text := extractResponseText(event.Response); text != "" {
				completedText = limitRunes(text, MaxReplyRunes)
			}
		case "response.failed", "response.incomplete", "error":
			message := "Codex returned an error terminal"
			code := "TERMINAL_ERROR"
			if event.Error != nil {
				if strings.TrimSpace(event.Error.Message) != "" {
					message = event.Error.Message
				}
				if strings.TrimSpace(event.Error.Code) != "" {
					code = event.Error.Code
				}
			}
			return "", event.Type, &Error{Kind: ErrorRejected, Code: code, Message: message}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", &Error{Kind: ErrorSchema, Message: "Codex SSE line is too large", Cause: err}
	}
	if terminal == "" {
		return "", "", &Error{Kind: ErrorTransient, Code: "SSE_EOF", Message: "Codex stream ended without a successful terminal"}
	}
	if completedText != "" {
		return completedText, terminal, nil
	}
	if doneText != "" {
		return doneText, terminal, nil
	}
	return limitRunes(deltas.String(), MaxReplyRunes), terminal, nil
}

func extractResponseText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var response struct {
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return ""
	}
	var text strings.Builder
	for _, output := range response.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" || content.Type == "text" {
				appendBounded(&text, content.Text)
			}
		}
	}
	return text.String()
}

func appendBounded(builder *strings.Builder, value string) {
	remaining := MaxReplyRunes - len([]rune(builder.String()))
	if remaining <= 0 {
		return
	}
	builder.WriteString(limitRunes(value, remaining))
}

func limitRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func terminalSummary(terminal, reply string) string {
	return fmt.Sprintf("%s; reply_runes=%d", terminal, len([]rune(reply)))
}
