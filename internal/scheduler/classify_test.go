package scheduler

import (
	"testing"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

func TestClassifyEligibility(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	base := sub2api.Account{Platform: "openai", Type: "oauth", Status: "active", Schedulable: true}
	for _, plan := range []string{"plus", "team", "self_serve_business", "self_serve_business_usage_based"} {
		if eligible, reason := Classify(base, plan, now); !eligible {
			t.Fatalf("%s should be eligible: %s", plan, reason)
		}
	}
	for _, plan := range []string{"", "free", "pro", "chatgptpro", "enterprise", "unknown"} {
		if eligible, _ := Classify(base, plan, now); eligible {
			t.Fatalf("%s should be ineligible", plan)
		}
	}
}

func TestClassifyRespectsRawSchedulableAndShadow(t *testing.T) {
	now := time.Now()
	account := sub2api.Account{Platform: "openai", Type: "oauth", Status: "active", Schedulable: false}
	if eligible, _ := Classify(account, "plus", now); eligible {
		t.Fatal("raw schedulable=false must be respected")
	}
	parent := int64(1)
	account.Schedulable = true
	account.ParentAccountID = &parent
	if eligible, _ := Classify(account, "plus", now); eligible {
		t.Fatal("shadow account must be excluded")
	}
}

func TestClassifyAllowsQuotaThresholdPauseOnly(t *testing.T) {
	now := time.Now()
	until := now.Add(time.Hour).Format(time.RFC3339)
	account := sub2api.Account{Platform: "openai", Type: "oauth", Status: "active", Schedulable: true, TempUnschedulableUntil: &until}
	account.TempUnschedulableReason = `{"source":"account_scheduling_threshold"}`
	if eligible, reason := Classify(account, "plus", now); !eligible {
		t.Fatalf("quota threshold pause should remain monitored: %s", reason)
	}
	account.TempUnschedulableReason = `{"source":"upstream_transport"}`
	if eligible, _ := Classify(account, "plus", now); eligible {
		t.Fatal("transport pause must not trigger automation")
	}
}
