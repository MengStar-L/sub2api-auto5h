//go:build e2e

package codex

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

func upstreamEndpoint() (string, error) {
	raw := strings.TrimSpace(os.Getenv("SUB2API_AUTO5H_E2E_CODEX_URL"))
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("e2e Codex URL must be an absolute loopback HTTP URL")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", errors.New("e2e Codex URL must use a loopback host")
	}
	return strings.TrimRight(raw, "/"), nil
}
