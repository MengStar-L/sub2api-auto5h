package sub2api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestActivationMaterialExportsOneMatchingAccount(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/accounts/data" || r.URL.Query().Get("ids") != "7" || r.URL.Query().Get("include_proxies") != "true" {
			t.Fatalf("unexpected request %s", r.URL.String())
		}
		writeEnvelope(t, w, map[string]any{
			"accounts": []any{map[string]any{
				"platform": "openai", "type": "oauth", "proxy_key": "proxy-key",
				"credentials": map[string]any{"access_token": "secret", "expires_at": expires.Format(time.RFC3339), "chatgpt_account_id": "workspace", "email": "plus@example.com"},
			}},
			"proxies": []any{map[string]any{"proxy_key": "proxy-key", "protocol": "socks5h", "host": "proxy.example", "port": 1080, "username": "u", "password": "p", "status": "active"}},
		})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "admin", true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	expected := AccountIdentityHash(Account{Credentials: map[string]any{"chatgpt_account_id": "workspace"}})
	material, err := client.ActivationMaterial(context.Background(), 7, expected, "plus@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if material.AccessToken != "secret" || material.ChatGPTID != "workspace" || !material.ExpiresAt.Equal(expires) {
		t.Fatalf("material=%#v", material)
	}
	if material.Proxy == nil || material.Proxy.Protocol != "socks5h" || material.Proxy.Password != "p" {
		t.Fatalf("proxy=%#v", material.Proxy)
	}
}

func TestActivationMaterialRejectsAmbiguousAndMismatchedExports(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
	}{
		{name: "zero accounts", data: map[string]any{"accounts": []any{}, "proxies": []any{}}},
		{name: "multiple accounts", data: map[string]any{"accounts": []any{map[string]any{}, map[string]any{}}, "proxies": []any{}}},
		{name: "identity mismatch", data: map[string]any{"accounts": []any{map[string]any{
			"platform": "openai", "type": "oauth", "credentials": map[string]any{"access_token": "secret", "chatgpt_account_id": "other", "email": "plus@example.com"},
		}}, "proxies": []any{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeEnvelope(t, w, test.data) }))
			defer server.Close()
			client, _ := NewClient(server.URL, "admin", true, time.Second)
			expected := AccountIdentityHash(Account{Credentials: map[string]any{"chatgpt_account_id": "workspace"}})
			if _, err := client.ActivationMaterial(context.Background(), 7, expected, "plus@example.com"); err == nil {
				t.Fatal("expected export rejection")
			}
		})
	}
}

func TestRefreshAccessTokenUsesSub2API(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/admin/openai/accounts/7/refresh" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(t, w, map[string]any{"refreshed": true})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "admin", true, time.Second)
	if err := client.RefreshAccessToken(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
}
