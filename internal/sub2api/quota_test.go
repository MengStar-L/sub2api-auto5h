package sub2api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeValidatesVersionWithoutLoadingAccounts(t *testing.T) {
	var unexpected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/system/version" {
			unexpected.Add(1)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		writeEnvelope(t, w, map[string]any{"version": MinimumVersion})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin-secret", true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	version, err := client.Probe(context.Background())
	if err != nil || version != MinimumVersion {
		t.Fatalf("version=%q err=%v", version, err)
	}
	if unexpected.Load() != 0 {
		t.Fatalf("probe made %d non-version requests", unexpected.Load())
	}
}

func TestProbeRejectsInvalidManagementConnection(t *testing.T) {
	tests := []struct {
		name  string
		kind  ErrorKind
		serve func(http.ResponseWriter)
	}{
		{
			name: "unsupported version",
			kind: ErrorSchema,
			serve: func(w http.ResponseWriter) {
				writeEnvelope(t, w, map[string]any{"version": "0.1.182"})
			},
		},
		{
			name: "invalid admin key",
			kind: ErrorAuth,
			serve: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "invalid admin API key", "data": nil})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/admin/system/version" {
					http.NotFound(w, r)
					return
				}
				test.serve(w)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "admin-secret", true, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Probe(context.Background())
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Kind != test.kind {
				t.Fatalf("error=%#v", err)
			}
		})
	}
}

func TestParseQuotaWindowsByDuration(t *testing.T) {
	payload := []byte(`{
      "plan_type":"plus","account_id":"workspace-1","fetched_at":1800000000,
      "rate_limit":{"allowed":true,"limit_reached":false,
        "primary_window":{"used_percent":12.5,"limit_window_seconds":604800,"reset_after_seconds":500,"reset_at":1800000500},
        "secondary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_after_seconds":300,"reset_at":1800000300}}
    }`)
	quota, err := ParseQuota(payload)
	if err != nil {
		t.Fatal(err)
	}
	if quota.FiveHour == nil || quota.FiveHour.ResetAt != 1800000300 {
		t.Fatalf("wrong five-hour window: %#v", quota.FiveHour)
	}
	if quota.SevenDay == nil || quota.SevenDay.ResetAt != 1800000500 {
		t.Fatalf("wrong seven-day window: %#v", quota.SevenDay)
	}
}

func TestParseQuotaKnownEmptyAllowsOmittedSlots(t *testing.T) {
	quota, err := ParseQuota([]byte(`{"fetched_at":1800000000,"rate_limit":{"allowed":true,"limit_reached":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !quota.KnownIdle(time.Unix(1800000000, 0)) {
		t.Fatal("valid empty rate_limit should be idle")
	}
}

func TestParseQuotaRejectsMissingRateLimitAndDuplicates(t *testing.T) {
	for _, payload := range []string{
		`{"fetched_at":1800000000}`,
		`{"fetched_at":1800000000,"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":1,"reset_at":2},"secondary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":1,"reset_at":2}}}`,
		`{"fetched_at":1800000000,"rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":0,"limit_window_seconds":3600,"reset_after_seconds":1,"reset_at":2}}}`,
	} {
		if _, err := ParseQuota([]byte(payload)); err == nil {
			t.Fatalf("expected schema error for %s", payload)
		}
	}
}

func TestAccountsPaginationAndSSE(t *testing.T) {
	var pages int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "admin-secret" {
			t.Fatalf("missing admin key")
		}
		switch {
		case r.URL.Path == "/api/v1/admin/accounts":
			pages++
			if r.URL.Query().Get("status") != "" {
				t.Fatal("status filter must not be sent")
			}
			items := make([]Account, 0)
			if pages == 1 {
				for index := 1; index <= 100; index++ {
					items = append(items, Account{ID: int64(index), CreatedAt: "2026-08-27T00:00:00Z"})
				}
			} else {
				items = append(items, Account{ID: 101, CreatedAt: "2026-08-27T00:00:00Z"})
			}
			writeEnvelope(t, w, map[string]any{"items": items, "total": 101})
		case strings.HasSuffix(r.URL.Path, "/test"):
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["prompt"] != "hi" || body["mode"] != "default" || body["model_id"] != "gpt-text" {
				t.Fatalf("unexpected test body: %#v", body)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"content\",\"text\":\"ok\"}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"test_complete\",\"success\":true}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "admin-secret", true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := client.Accounts(context.Background())
	if err != nil || len(accounts) != 101 || pages != 2 {
		t.Fatalf("accounts=%d pages=%d err=%v", len(accounts), pages, err)
	}
	result, err := client.TestAccount(context.Background(), 7, "gpt-text")
	if err != nil || !result.Success {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestEmptyCollectionsMarshalAsArrays(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/accounts":
			writeEnvelope(t, w, map[string]any{"items": []Account{}, "total": 0})
		case "/api/v1/admin/accounts/7/models":
			writeEnvelope(t, w, []string{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin-secret", true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := client.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.Models(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{accounts, models} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != "[]" {
			t.Fatalf("empty client collection encoded as %s", encoded)
		}
	}
}

func TestSSERequiresTerminalSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content\",\"text\":\"ok\"}\n\n")
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "key", true, time.Second)
	if _, err := client.TestAccount(context.Background(), 1, "gpt-text"); err == nil {
		t.Fatal("EOF without test_complete must fail")
	}
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "success", "data": data}); err != nil {
		t.Fatal(err)
	}
}
