package codex

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRateLimitsUsesWindowMinutesNotSlotOrder(t *testing.T) {
	header := http.Header{}
	header.Set("x-codex-primary-window-minutes", "10080")
	header.Set("x-codex-primary-used-percent", "12.5")
	header.Set("x-codex-primary-reset-after-seconds", "500000")
	header.Set("x-codex-secondary-window-minutes", "300")
	header.Set("x-codex-secondary-used-percent", "1")
	header.Set("x-codex-secondary-reset-after-seconds", "17990")
	now := time.Unix(1_800_000_000, 0)
	limits, err := ParseRateLimits(header, now)
	if err != nil {
		t.Fatal(err)
	}
	if limits.FiveHour == nil || limits.FiveHour.ResetAt != now.Unix()+17990 {
		t.Fatalf("five-hour=%#v", limits.FiveHour)
	}
	if limits.SevenDay == nil || limits.SevenDay.ResetAfterSeconds != 500000 {
		t.Fatalf("seven-day=%#v", limits.SevenDay)
	}
}

func TestParseRateLimitsRejectsIncompleteAndDuplicateWindows(t *testing.T) {
	for _, header := range []http.Header{
		{"x-codex-primary-window-minutes": []string{"300"}},
		{
			"x-codex-primary-window-minutes": []string{"300"}, "x-codex-primary-used-percent": []string{"1"}, "x-codex-primary-reset-after-seconds": []string{"100"},
			"x-codex-secondary-window-minutes": []string{"300"}, "x-codex-secondary-used-percent": []string{"2"}, "x-codex-secondary-reset-after-seconds": []string{"200"},
		},
		{
			"x-codex-primary-window-minutes": []string{"60"}, "x-codex-primary-used-percent": []string{"1"}, "x-codex-primary-reset-after-seconds": []string{"100"},
		},
		{
			"x-codex-primary-window-minutes": []string{"300"}, "x-codex-primary-used-percent": []string{"101"}, "x-codex-primary-reset-after-seconds": []string{"100"},
		},
		{
			"x-codex-primary-window-minutes": []string{"300"}, "x-codex-primary-used-percent": []string{"1"}, "x-codex-primary-reset-after-seconds": []string{"19000"},
		},
	} {
		if _, err := ParseRateLimits(header, time.Now()); err == nil {
			t.Fatal("expected invalid rate-limit headers")
		}
	}
}
