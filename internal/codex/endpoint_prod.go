//go:build !e2e

package codex

func upstreamEndpoint() (string, error) {
	return "https://chatgpt.com/backend-api/codex/responses", nil
}
