package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/scheduler"
	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
	"github.com/MengStar-L/sub2api-auto5h/internal/store"
	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

func TestSetupCompletesWithoutLoadingAccountsOrQuota(t *testing.T) {
	var unexpected atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/system/version" {
			unexpected.Add(1)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		writeTestEnvelope(t, w, map[string]any{"version": sub2api.MinimumVersion})
	}))
	defer upstream.Close()

	data := openHTTPTestStore(t)
	const token = "setup-token"
	if err := data.SetSetupToken(context.Background(), secure.HashToken(token), time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	automation := scheduler.New(data, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := &Server{store: data, scheduler: automation, setupToken: token}
	body, err := json.Marshal(map[string]any{
		"username": "admin", "password": "long-test-password", "base_url": upstream.URL,
		"api_key": "admin-secret", "global_model": "gpt-text", "allow_private_http": true,
		"sync_interval_seconds": 300, "reset_grace_seconds": 30, "max_retries": 3,
		"retry_base_seconds": 30, "request_timeout_seconds": 90, "max_concurrency": 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/setup/complete", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Setup-Token", token)
	recorder := httptest.NewRecorder()

	server.setupComplete(recorder, request)
	if recorder.Code != http.StatusCreated || unexpected.Load() != 0 {
		t.Fatalf("status=%d unexpected=%d body=%s", recorder.Code, unexpected.Load(), recorder.Body.String())
	}
	complete, err := data.IsSetupComplete(context.Background())
	if err != nil || !complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response.Data["version"]; !ok {
		t.Fatal("setup response has no version")
	}
	if _, ok := response.Data["accounts_found"]; ok {
		t.Fatal("setup response still exposes accounts_found")
	}
}

func openHTTPTestStore(t *testing.T) *store.Store {
	t.Helper()
	box, err := secure.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })
	return data
}

func writeTestEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "success", "data": data}); err != nil {
		t.Fatal(err)
	}
}
