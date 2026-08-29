package codex

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func ParseRateLimits(header http.Header, now time.Time) (RateLimits, error) {
	var result RateLimits
	recognized := 0
	for _, slot := range []string{"primary", "secondary"} {
		prefix := "x-codex-" + slot + "-"
		windowValue := strings.TrimSpace(header.Get(prefix + "window-minutes"))
		usedValue := strings.TrimSpace(header.Get(prefix + "used-percent"))
		resetValue := strings.TrimSpace(header.Get(prefix + "reset-after-seconds"))
		if windowValue == "" && usedValue == "" && resetValue == "" {
			continue
		}
		if windowValue == "" || usedValue == "" || resetValue == "" {
			return RateLimits{}, fmt.Errorf("%s rate-limit window is incomplete", slot)
		}
		minutes, err := strconv.Atoi(windowValue)
		if err != nil {
			return RateLimits{}, fmt.Errorf("%s window-minutes is invalid", slot)
		}
		if minutes != FiveHourMinutes && minutes != SevenDayMinutes {
			continue
		}
		used, err := strconv.ParseFloat(usedValue, 64)
		if err != nil || used < 0 || used > 100 {
			return RateLimits{}, fmt.Errorf("%s used-percent is invalid", slot)
		}
		resetAfter, err := strconv.ParseInt(resetValue, 10, 64)
		if err != nil || resetAfter <= 0 || resetAfter > int64(minutes*60)+300 {
			return RateLimits{}, fmt.Errorf("%s reset-after-seconds is invalid", slot)
		}
		window := &RateWindow{UsedPercent: used, ResetAfterSeconds: resetAfter, ResetAt: now.Unix() + resetAfter}
		if minutes == FiveHourMinutes {
			if result.FiveHour != nil {
				return RateLimits{}, fmt.Errorf("duplicate five-hour rate-limit window")
			}
			result.FiveHour = window
		} else {
			if result.SevenDay != nil {
				return RateLimits{}, fmt.Errorf("duplicate seven-day rate-limit window")
			}
			result.SevenDay = window
		}
		recognized++
	}
	if recognized == 0 {
		return RateLimits{}, fmt.Errorf("no recognized Codex rate-limit window")
	}
	return result, nil
}
