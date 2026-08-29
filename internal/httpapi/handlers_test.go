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
	"testing"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/scheduler"
	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
	"github.com/MengStar-L/sub2api-auto5h/internal/store"
	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

func TestBatchPolicyAllFailuresReturnsEmptySucceededArray(t *testing.T) {
	box, err := secure.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = data.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	automation := scheduler.New(data, nil, logger)
	server := &Server{store: data, scheduler: automation}
	request := httptest.NewRequest(http.MethodPost, "/api/accounts/batch-policy", bytes.NewBufferString(`{"ids":["missing-account"],"enabled":true}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.batchPolicy(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Succeeded json.RawMessage `json:"succeeded"`
		} `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if string(response.Data.Succeeded) != "[]" {
		t.Fatalf("succeeded encoded as %s", response.Data.Succeeded)
	}
}

func TestSettingsRequireRiskAcknowledgementForDirectWakeup(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/system/version" {
			http.NotFound(w, r)
			return
		}
		writeTestEnvelope(t, w, map[string]any{"version": sub2api.MinimumVersion})
	}))
	defer upstream.Close()

	data := configuredHTTPTestStore(t, upstream.URL)
	automation := scheduler.New(data, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := &Server{store: data, scheduler: automation}
	body := map[string]any{
		"base_url": upstream.URL, "api_key": "", "global_model": "gpt-text", "allow_private_http": true,
		"sync_interval_seconds": 300, "reset_grace_seconds": 30, "max_retries": 3,
		"retry_base_seconds": 30, "request_timeout_seconds": 90, "max_concurrency": 4,
		"direct_wakeup_enabled": true,
	}

	recorder := putSettingsRequest(t, server, body)
	if recorder.Code != http.StatusBadRequest || !bytes.Contains(recorder.Body.Bytes(), []byte("DIRECT_WAKEUP_ACK_REQUIRED")) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	settings, err := data.GetSettings(context.Background())
	if err != nil || settings.DirectWakeupEnabled {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}

	body["direct_wakeup_risk_acknowledged"] = true
	recorder = putSettingsRequest(t, server, body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	settings, err = data.GetSettings(context.Background())
	if err != nil || !settings.DirectWakeupEnabled {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
}

func TestManualRunRejectsWhenDirectWakeupIsDisabled(t *testing.T) {
	data := configuredHTTPTestStore(t, "https://sub2api.example.com")
	automation := scheduler.New(data, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := &Server{store: data, scheduler: automation}
	request := httptest.NewRequest(http.MethodPost, "/api/accounts/account/run", bytes.NewBufferString("{}"))
	request.SetPathValue("id", "account")
	recorder := httptest.NewRecorder()

	server.runAccount(recorder, request)

	if recorder.Code != http.StatusConflict || !bytes.Contains(recorder.Body.Bytes(), []byte("DIRECT_WAKEUP_DISABLED")) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func configuredHTTPTestStore(t *testing.T, baseURL string) *store.Store {
	t.Helper()
	data := openHTTPTestStore(t)
	tokenHash := secure.HashToken("setup-token")
	if err := data.SetSetupToken(context.Background(), tokenHash, time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	settings := store.Settings{
		ConnectionUUID: "connection", BaseURL: baseURL, APIKey: "admin-secret", GlobalModel: "gpt-text",
		AllowPrivateHTTP: true, SyncIntervalSeconds: 300, ResetGraceSeconds: 30, MaxRetries: 3,
		RetryBaseSeconds: 30, RequestTimeoutSeconds: 90, MaxConcurrency: 4,
	}
	if err := data.CompleteSetup(context.Background(), tokenHash, "admin", "password-hash", settings); err != nil {
		t.Fatal(err)
	}
	return data
}

func putSettingsRequest(t *testing.T, server *Server, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.putSettings(recorder, request)
	return recorder
}
