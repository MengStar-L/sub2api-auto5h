package sub2api

import (
	"encoding/json"
	"errors"
	"fmt"
)

func ParseQuota(data []byte) (Quota, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return Quota{}, schemaError("quota data is not an object", err)
	}
	var quota Quota
	_ = json.Unmarshal(top["plan_type"], &quota.PlanType)
	_ = json.Unmarshal(top["account_id"], &quota.AccountID)
	if fetched, ok := top["fetched_at"]; !ok || json.Unmarshal(fetched, &quota.FetchedAt) != nil || quota.FetchedAt <= 0 {
		return Quota{}, schemaError("quota fetched_at is missing or invalid", nil)
	}
	rateRaw, ok := top["rate_limit"]
	if !ok || string(rateRaw) == "null" {
		return Quota{}, schemaError("quota rate_limit is missing", nil)
	}
	var rate map[string]json.RawMessage
	if err := json.Unmarshal(rateRaw, &rate); err != nil {
		return Quota{}, schemaError("quota rate_limit is not an object", err)
	}
	if err := requireBool(rate, "allowed", &quota.Allowed); err != nil {
		return Quota{}, err
	}
	if err := requireBool(rate, "limit_reached", &quota.LimitReached); err != nil {
		return Quota{}, err
	}
	seen := map[int64]bool{}
	for _, field := range []string{"primary_window", "secondary_window"} {
		raw, exists := rate[field]
		if !exists || string(raw) == "null" {
			continue
		}
		window, err := parseWindow(raw, field)
		if err != nil {
			return Quota{}, err
		}
		if window.LimitWindowSeconds != FiveHoursSeconds && window.LimitWindowSeconds != SevenDaysSeconds {
			return Quota{}, schemaError(fmt.Sprintf("%s has unsupported window duration %d", field, window.LimitWindowSeconds), nil)
		}
		if seen[window.LimitWindowSeconds] {
			return Quota{}, schemaError(fmt.Sprintf("duplicate %d-second quota window", window.LimitWindowSeconds), nil)
		}
		seen[window.LimitWindowSeconds] = true
		if window.LimitWindowSeconds == FiveHoursSeconds {
			quota.FiveHour = &window
		} else {
			quota.SevenDay = &window
		}
	}
	return quota, nil
}

func requireBool(object map[string]json.RawMessage, key string, target *bool) error {
	raw, ok := object[key]
	if !ok || json.Unmarshal(raw, target) != nil {
		return schemaError("quota rate_limit."+key+" is missing or invalid", nil)
	}
	return nil
}

func parseWindow(raw json.RawMessage, name string) (Window, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return Window{}, schemaError(name+" is not an object", err)
	}
	var window Window
	fields := []struct {
		name   string
		target any
	}{
		{"used_percent", &window.UsedPercent},
		{"limit_window_seconds", &window.LimitWindowSeconds},
		{"reset_after_seconds", &window.ResetAfterSeconds},
		{"reset_at", &window.ResetAt},
	}
	for _, field := range fields {
		value, ok := object[field.name]
		if !ok || json.Unmarshal(value, field.target) != nil {
			return Window{}, schemaError(name+"."+field.name+" is missing or invalid", nil)
		}
	}
	if window.UsedPercent < 0 || window.ResetAfterSeconds < 0 || window.ResetAt <= 0 {
		return Window{}, schemaError(name+" contains an out-of-range value", nil)
	}
	return window, nil
}

func schemaError(message string, cause error) error {
	if cause == nil {
		cause = errors.New(message)
	}
	return &APIError{Kind: ErrorSchema, Message: message, Cause: cause}
}
