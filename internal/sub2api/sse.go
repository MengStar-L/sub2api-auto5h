package sub2api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxSSEBody = 1 << 20

func (c *Client) TestAccount(ctx context.Context, accountID int64, model string) (TestResult, error) {
	payload, err := json.Marshal(map[string]string{"model_id": model, "prompt": "hi", "mode": "default"})
	if err != nil {
		return TestResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/accounts/%d/test", c.baseURL, accountID), bytes.NewReader(payload))
	if err != nil {
		return TestResult{}, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return TestResult{}, &APIError{Kind: ErrorTransient, Message: "test request failed", Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var wrapped envelope
		_ = json.Unmarshal(body, &wrapped)
		return TestResult{HTTPStatus: resp.StatusCode}, classifyStatus(resp.StatusCode, fmt.Sprint(wrapped.Code), truncateMessage(wrapped.Message))
	}
	reader := bufio.NewScanner(io.LimitReader(resp.Body, maxSSEBody+1))
	reader.Buffer(make([]byte, 4096), 128<<10)
	consumed := 0
	for reader.Scan() {
		line := reader.Text()
		consumed += len(line) + 1
		if consumed > maxSSEBody {
			return TestResult{HTTPStatus: resp.StatusCode}, &APIError{Kind: ErrorSchema, StatusCode: resp.StatusCode, Message: "test SSE exceeds 1 MiB"}
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Success bool   `json:"success"`
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return TestResult{HTTPStatus: resp.StatusCode}, &APIError{Kind: ErrorSchema, StatusCode: resp.StatusCode, Message: "invalid test SSE JSON", Cause: err}
		}
		switch event.Type {
		case "test_complete":
			if event.Success {
				return TestResult{HTTPStatus: resp.StatusCode, Success: true}, nil
			}
			return TestResult{HTTPStatus: resp.StatusCode, Message: event.Message}, &APIError{Kind: ErrorRejected, StatusCode: resp.StatusCode, Code: "TEST_FAILED", Message: truncateMessage(event.Message)}
		case "error":
			message := event.Message
			if message == "" {
				message = event.Error
			}
			return TestResult{HTTPStatus: resp.StatusCode, Message: message}, &APIError{Kind: ErrorRejected, StatusCode: resp.StatusCode, Code: "TEST_ERROR", Message: truncateMessage(message)}
		}
	}
	if err := reader.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return TestResult{HTTPStatus: resp.StatusCode}, &APIError{Kind: ErrorTransient, StatusCode: resp.StatusCode, Message: "test SSE timed out", Cause: err}
		}
		return TestResult{HTTPStatus: resp.StatusCode}, &APIError{Kind: ErrorTransient, StatusCode: resp.StatusCode, Message: "test SSE read failed", Cause: err}
	}
	return TestResult{HTTPStatus: resp.StatusCode}, &APIError{Kind: ErrorTransient, StatusCode: resp.StatusCode, Code: "SSE_EOF", Message: "test SSE ended without test_complete"}
}
