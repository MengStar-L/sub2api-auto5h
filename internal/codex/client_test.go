package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestActivateSendsFixedOfficialRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer access-secret" || r.Header.Get("ChatGPT-Account-Id") != "workspace" {
			t.Fatalf("unexpected request headers: %#v", r.Header)
		}
		if r.Header.Get("Originator") != "codex-tui" || r.Header.Get("OpenAI-Beta") != "responses=experimental" {
			t.Fatalf("missing official headers: %#v", r.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "gpt-5.6-sol" || payload["instructions"] != NumericOnlyInstruction || payload["store"] != false || payload["stream"] != true {
			t.Fatalf("payload=%#v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-codex-secondary-window-minutes", "300")
		w.Header().Set("x-codex-secondary-used-percent", "1")
		w.Header().Set("x-codex-secondary-reset-after-seconds", "17990")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.done\",\"text\":\"21\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n"))
	}))
	defer server.Close()
	result, err := NewService().activateAt(context.Background(), server.URL, Request{
		AccessToken: "access-secret", AccountID: "workspace", Model: "gpt-5.6-sol", Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "21" || result.Terminal != "response.completed" || result.TransportPath != "direct" || result.RateLimits.FiveHour == nil {
		t.Fatalf("result=%#v", result)
	}
}

func TestTransportForSupportsConfiguredProxyProtocols(t *testing.T) {
	for _, protocol := range []string{"http", "https", "socks5", "socks5h"} {
		transport, path, err := transportFor(&Proxy{Protocol: protocol, Host: "127.0.0.1", Port: 1080}, nil)
		if err != nil || transport.Proxy == nil || path != "proxy:"+protocol {
			t.Fatalf("protocol=%s path=%s err=%v", protocol, path, err)
		}
	}
}
