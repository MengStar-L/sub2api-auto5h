package sub2api

import "time"

const (
	FiveHoursSeconds = int64(18_000)
	SevenDaysSeconds = int64(604_800)
	MinimumVersion   = "0.1.183"
)

type Account struct {
	ID                      int64          `json:"id"`
	Name                    string         `json:"name"`
	Platform                string         `json:"platform"`
	Type                    string         `json:"type"`
	Credentials             map[string]any `json:"credentials"`
	Extra                   map[string]any `json:"extra"`
	Status                  string         `json:"status"`
	Schedulable             bool           `json:"schedulable"`
	ParentAccountID         *int64         `json:"parent_account_id"`
	CreatedAt               string         `json:"created_at"`
	ExpiresAt               *string        `json:"expires_at"`
	AutoPauseOnExpired      bool           `json:"auto_pause_on_expired"`
	RateLimitResetAt        *string        `json:"rate_limit_reset_at"`
	TempUnschedulableUntil  *string        `json:"temp_unschedulable_until"`
	TempUnschedulableReason string         `json:"temp_unschedulable_reason"`
}

type Window struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type Quota struct {
	PlanType     string
	AccountID    string
	FetchedAt    int64
	Allowed      bool
	LimitReached bool
	FiveHour     *Window
	SevenDay     *Window
}

func (q Quota) FiveActive(now time.Time) bool {
	return q.FiveHour != nil && q.FiveHour.UsedPercent > 0 && q.FiveHour.ResetAt > now.Unix()
}

func (q Quota) SevenExhausted(now time.Time) bool {
	return q.SevenDay != nil && q.SevenDay.UsedPercent >= 100 && q.SevenDay.ResetAt > now.Unix()
}

func (q Quota) KnownIdle(now time.Time) bool {
	fiveIdle := q.FiveHour == nil || q.FiveHour.UsedPercent == 0 || q.FiveHour.ResetAt <= now.Unix()
	return fiveIdle && q.Allowed && !q.LimitReached && !q.SevenExhausted(now)
}
