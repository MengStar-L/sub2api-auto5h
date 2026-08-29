package secure

import (
	"strings"
	"testing"
)

func TestRedactRemovesCredentials(t *testing.T) {
	input := `Bearer eyJabc.def.ghi access_token=token-secret refresh_token:"refresh-secret" https://alice:proxy-secret@proxy.example:8443`
	redacted := Redact(input)
	for _, secret := range []string{"eyJabc.def.ghi", "token-secret", "refresh-secret", "alice", "proxy-secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted value still contains %q: %s", secret, redacted)
		}
	}
}
