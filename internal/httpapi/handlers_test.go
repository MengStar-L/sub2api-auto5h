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

	"github.com/MengStar-L/sub2api-auto5h/internal/scheduler"
	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
	"github.com/MengStar-L/sub2api-auto5h/internal/store"
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
