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

const maxReplyRunes = 2000

const WakeupPrompt = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

苹果味 桃子味 西瓜味
圆形 7 9 8
五角星形 7 6 4

只能返回一个阿拉伯数字，不要解释，不要添加标点或其他内容。`

func (c *Client) TestAccount(ctx context.Context, accountID int64, model string) (TestResult, error) {
	payload, err := json.Marshal(map[string]string{"model_id": model, "prompt": WakeupPrompt, "mode": "default"})
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
	reply := make([]rune, 0, 64)
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
			Text    string `json:"text"`
			Success bool   `json:"success"`
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return TestResult{HTTPStatus: resp.StatusCode}, &APIError{Kind: ErrorSchema, StatusCode: resp.StatusCode, Message: "invalid test SSE JSON", Cause: err}
		}
		switch event.Type {
		case "content":
			if len(reply) < maxReplyRunes && event.Text != "" {
				text := []rune(event.Text)
				remaining := maxReplyRunes - len(reply)
				if len(text) > remaining {
					text = text[:remaining]
				}
				reply = append(reply, text...)
			}
		case "test_complete":
			if event.Success {
				return TestResult{HTTPStatus: resp.StatusCode, Success: true, Reply: string(reply)}, nil
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
