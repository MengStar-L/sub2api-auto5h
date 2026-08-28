package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMissingBuildIsExplicit(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable && recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
	if recorder.Code == http.StatusServiceUnavailable && !strings.Contains(recorder.Body.String(), "not built") {
		t.Fatalf("unexpected body %q", recorder.Body.String())
	}
}
