package scheduler

import (
	"fmt"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

func (s *Scheduler) resumeVerifications() {
	cycles, err := s.store.ListVerifyingCycles(s.ctx)
	if err != nil {
		s.log.Warn("load unfinished verifications", "error", err)
		return
	}
	for _, cycle := range cycles {
		acceptedAt := cycle.UpdatedAt
		if cycle.AcceptedAt != nil {
			acceptedAt = *cycle.AcceptedAt
		}
		s.startVerification(cycle.AccountID, cycle.ID, acceptedAt)
	}
}

func (s *Scheduler) startVerification(accountID, cycleID string, acceptedAt int64) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.verify(accountID, cycleID, acceptedAt)
	}()
}

func (s *Scheduler) verify(accountID, cycleID string, acceptedAt int64) {
	for _, elapsed := range []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second} {
		wait := time.Unix(acceptedAt, 0).Add(elapsed).Sub(s.clock.Now())
		if wait > 0 {
			select {
			case <-s.ctx.Done():
				return
			case <-s.clock.After(wait):
			}
		}
		account, err := s.store.GetAccount(s.ctx, accountID)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			continue
		}
		if !account.Policy.Enabled || account.Missing || account.RuntimeState == "identity_changed" {
			continue
		}
		settings, err := s.store.GetSettings(s.ctx)
		if err != nil {
			continue
		}
		client, err := s.factory(settings)
		if err != nil {
			continue
		}
		quota, err := client.Quota(s.ctx, account.RemoteID)
		if err != nil || !quota.FiveActive(time.Unix(acceptedAt, 0)) {
			continue
		}
		due := quota.FiveHour.ResetAt + int64(effectiveGrace(account.Policy, settings))
		marked, err := s.store.MarkCycleVerified(s.ctx, cycleID, "额度窗口已验证", "sub2api_quota", &due)
		if err != nil || !marked {
			return
		}
		identity := sub2api.QuotaIdentityHash(quota)
		if identity == "" {
			identity = account.IdentityHash
		}
		plan := quota.PlanType
		if plan == "" {
			plan = account.PlanType
		}
		changed, err := s.store.ApplyQuota(s.ctx, accountID, quotaUpdate(identity, plan, quota, &due, "verified", ""))
		if changed || err != nil {
			return
		}
		_, _ = s.store.EnsureCycle(s.ctx, accountID, account.IdentityGeneration, fmt.Sprintf("reset:%d", quota.FiveHour.ResetAt), "reset", &quota.FiveHour.ResetAt, due)
		_ = s.store.AddEvent(s.ctx, "info", "scheduler", "activation_verified", accountID, "新 5h 窗口已通过 sub2api quota 验证", "{}")
		return
	}

	account, err := s.store.GetAccount(s.ctx, accountID)
	if err != nil {
		return
	}
	fallback := acceptedAt + sub2api.FiveHoursSeconds + 30
	if account.NextActionAt != nil && *account.NextActionAt > acceptedAt {
		fallback = *account.NextActionAt
	} else if settings, settingsErr := s.store.GetSettings(s.ctx); settingsErr == nil {
		fallback = acceptedAt + sub2api.FiveHoursSeconds + int64(effectiveGrace(account.Policy, settings))
	}
	reason := "请求成功，60 秒内未获得 5h 额度证据"
	changed, err := s.store.FinalizeVerification(s.ctx, cycleID, s.clock.Now().Unix(), fallback, reason)
	if err != nil {
		s.log.Warn("finalize activation verification", "account_id", accountID, "error", err)
		return
	}
	if changed {
		_ = s.store.AddEvent(s.ctx, "info", "scheduler", "activation_accepted_unverified", accountID, reason, "{}")
	}
}
