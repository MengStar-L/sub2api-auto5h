package secure

import (
	"regexp"
	"strings"
)

var (
	bearerPattern   = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	jwtPattern      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
	secretPattern   = regexp.MustCompile(`(?i)("?(?:access_token|refresh_token|id_token|api_key|password)"?\s*[:=]\s*"?)[^"\s,}]+`)
	userinfoPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^@\s/]+@`)
)

func Redact(value string) string {
	value = bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = jwtPattern.ReplaceAllString(value, "[REDACTED]")
	value = secretPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = userinfoPattern.ReplaceAllString(value, `${1}[REDACTED]@`)
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 500 {
		value = string(runes[:500])
	}
	return value
}
