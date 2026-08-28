package codex

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
)

const defaultUserAgent = "codex-tui/0.146.0 (Mac OS 26.5.0; arm64) iTerm.app/3.6.10 (codex-tui; 0.146.0)"

type Service struct{}

func NewService() *Service { return &Service{} }

func (s *Service) Activate(ctx context.Context, request Request) (Result, error) {
	endpoint, err := upstreamEndpoint()
	if err != nil {
		return Result{}, err
	}
	return s.activateAt(ctx, endpoint, request, nil)
}

func (s *Service) activateAt(ctx context.Context, endpoint string, request Request, tlsConfig *tls.Config) (Result, error) {
	if strings.TrimSpace(request.AccessToken) == "" || strings.TrimSpace(request.AccountID) == "" || strings.TrimSpace(request.Model) == "" {
		return Result{}, &Error{Kind: ErrorSchema, Message: "Codex request is missing authentication or model"}
	}
	transport, path, err := transportFor(request.Proxy, tlsConfig)
	if err != nil {
		return Result{}, err
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("Codex redirects are disabled")
		},
	}
	payload := map[string]any{
		"model": request.Model,
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": WakeupPrompt}},
		}},
		"instructions": NumericOnlyInstruction,
		"store":        false,
		"stream":       true,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Result{}, err
	}
	userAgent := strings.TrimSpace(request.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	req.Header.Set("Authorization", "Bearer "+request.AccessToken)
	req.Header.Set("ChatGPT-Account-Id", request.AccountID)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("Originator", "codex-tui")
	req.Header.Set("User-Agent", userAgent)
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{TransportPath: path}, &Error{Kind: ErrorTransient, Message: secure.Redact("Codex request failed: " + err.Error()), Cause: err}
	}
	defer resp.Body.Close()
	limits, limitErr := ParseRateLimits(resp.Header, started)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, code := readSafeError(resp.Body)
		kind := ErrorRejected
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			kind = ErrorUnauthorized
		case http.StatusForbidden:
			kind = ErrorForbidden
		case http.StatusNotFound:
			kind = ErrorNotFound
		case http.StatusTooManyRequests:
			kind = ErrorRateLimited
		default:
			if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout {
				kind = ErrorTransient
			}
		}
		if kind == ErrorRateLimited && limitErr != nil {
			return Result{HTTPStatus: resp.StatusCode, TransportPath: path}, &Error{Kind: ErrorSchema, StatusCode: resp.StatusCode, Code: code, Message: "rate-limited response has invalid quota headers", Cause: limitErr}
		}
		return Result{HTTPStatus: resp.StatusCode, TransportPath: path, RateLimits: limits}, &Error{Kind: kind, StatusCode: resp.StatusCode, Code: code, Message: message, RateLimits: limits}
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return Result{HTTPStatus: resp.StatusCode, TransportPath: path}, &Error{Kind: ErrorSchema, StatusCode: resp.StatusCode, Message: "Codex response is not an event stream"}
	}
	reply, terminal, err := parseSSE(resp.Body)
	if err != nil {
		return Result{HTTPStatus: resp.StatusCode, TransportPath: path, RateLimits: limits}, err
	}
	if limitErr != nil {
		limits = RateLimits{}
	}
	return Result{HTTPStatus: resp.StatusCode, Reply: reply, Terminal: terminal, TransportPath: path, RateLimits: limits}, nil
}

func transportFor(proxy *Proxy, tlsConfig *tls.Config) (*http.Transport, string, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 15 * time.Second
	transport.ResponseHeaderTimeout = 45 * time.Second
	transport.ExpectContinueTimeout = time.Second
	transport.IdleConnTimeout = 30 * time.Second
	transport.MaxIdleConns = 2
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig.Clone()
	}
	if proxy == nil {
		return transport, "direct", nil
	}
	protocol := strings.ToLower(strings.TrimSpace(proxy.Protocol))
	switch protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, "", &Error{Kind: ErrorSchema, Message: "unsupported account proxy protocol"}
	}
	if strings.TrimSpace(proxy.Host) == "" || proxy.Port < 1 || proxy.Port > 65535 {
		return nil, "", &Error{Kind: ErrorSchema, Message: "account proxy endpoint is invalid"}
	}
	proxyURL := &url.URL{Scheme: protocol, Host: net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port))}
	if proxy.Username != "" {
		proxyURL.User = url.UserPassword(proxy.Username, proxy.Password)
	}
	transport.Proxy = http.ProxyURL(proxyURL)
	return transport, "proxy:" + protocol, nil
}

func readSafeError(body io.Reader) (string, string) {
	contents, err := io.ReadAll(io.LimitReader(body, 64<<10))
	if err != nil {
		return "cannot read Codex error response", ""
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(contents, &payload) == nil {
		message := secure.Redact(payload.Error.Message)
		if message == "" {
			message = "Codex request was rejected"
		}
		return message, secure.Redact(payload.Error.Code)
	}
	return "Codex request was rejected", ""
}
