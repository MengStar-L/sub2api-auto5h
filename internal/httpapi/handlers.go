package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
	"github.com/MengStar-L/sub2api-auto5h/internal/store"
	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, accounts)
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	account, err := s.store.GetAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, account)
}

type policyRequest struct {
	Enabled                  bool    `json:"enabled"`
	ModelOverride            *string `json:"model_override"`
	GraceOverrideSeconds     *int    `json:"grace_override_seconds"`
	MaxRetriesOverride       *int    `json:"max_retries_override"`
	RetryBaseOverrideSeconds *int    `json:"retry_base_override_seconds"`
}

func validatePolicy(request policyRequest) error {
	if request.ModelOverride != nil {
		trimmed := strings.TrimSpace(*request.ModelOverride)
		if trimmed == "" {
			request.ModelOverride = nil
		} else if err := validateModel(trimmed); err != nil {
			return err
		}
	}
	if request.GraceOverrideSeconds != nil && (*request.GraceOverrideSeconds < 0 || *request.GraceOverrideSeconds > 600) {
		return errors.New("grace override must be 0-600 seconds")
	}
	if request.MaxRetriesOverride != nil && (*request.MaxRetriesOverride < 0 || *request.MaxRetriesOverride > 6) {
		return errors.New("retry override must be 0-6")
	}
	if request.RetryBaseOverrideSeconds != nil && (*request.RetryBaseOverrideSeconds < 5 || *request.RetryBaseOverrideSeconds > 600) {
		return errors.New("retry base override must be 5-600 seconds")
	}
	return nil
}

func (s *Server) updatePolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	account, err := s.store.GetAccount(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var request policyRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := validatePolicy(request); err != nil {
		writeError(w, 400, "INVALID_POLICY", err.Error())
		return
	}
	if request.Enabled && (!account.Eligible || account.Missing) {
		writeError(w, 409, "ACCOUNT_INELIGIBLE", account.EligibilityReason)
		return
	}
	if request.ModelOverride != nil {
		trimmed := strings.TrimSpace(*request.ModelOverride)
		if trimmed == "" {
			request.ModelOverride = nil
		} else {
			request.ModelOverride = &trimmed
		}
	}
	err = s.store.SetPolicy(r.Context(), id, store.Policy{Enabled: request.Enabled, ModelOverride: request.ModelOverride,
		GraceOverrideSeconds: request.GraceOverrideSeconds, MaxRetriesOverride: request.MaxRetriesOverride,
		RetryBaseOverrideSeconds: request.RetryBaseOverrideSeconds})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	_ = s.store.AddEvent(r.Context(), "info", "admin", "policy_update", id, map[bool]string{true: "已启用自动激活", false: "已关闭自动激活"}[request.Enabled], "{}")
	s.scheduler.Wake()
	updated, _ := s.store.GetAccount(r.Context(), id)
	writeData(w, http.StatusOK, updated)
}

func (s *Server) batchPolicy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		IDs     []string `json:"ids"`
		Enabled bool     `json:"enabled"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.IDs) == 0 || len(request.IDs) > 100 {
		writeError(w, 400, "INVALID_BATCH", "select 1-100 accounts")
		return
	}
	succeeded := make([]string, 0, len(request.IDs))
	failed := map[string]string{}
	for _, id := range request.IDs {
		account, err := s.store.GetAccount(r.Context(), id)
		if err != nil {
			failed[id] = "账号不存在"
			continue
		}
		if request.Enabled && (!account.Eligible || account.Missing) {
			failed[id] = account.EligibilityReason
			continue
		}
		policy := account.Policy
		policy.Enabled = request.Enabled
		if err := s.store.SetPolicy(r.Context(), id, policy); err != nil {
			failed[id] = err.Error()
			continue
		}
		succeeded = append(succeeded, id)
	}
	_ = s.store.AddEvent(r.Context(), "info", "admin", "batch_policy", "", "批量账号策略已更新", "{}")
	s.scheduler.Wake()
	writeData(w, http.StatusOK, map[string]any{"succeeded": succeeded, "failed": failed})
}

func (s *Server) syncAccounts(w http.ResponseWriter, r *http.Request) {
	if err := s.scheduler.Sync(r.Context()); err != nil {
		writeRemoteError(w, err)
		return
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"accounts": accounts, "count": len(accounts)})
}

func (s *Server) refreshQuota(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.scheduler.RefreshQuota(r.Context(), id); err != nil {
		writeRemoteError(w, err)
		return
	}
	_ = s.store.AddEvent(r.Context(), "info", "admin", "quota_refresh", id, "已只读刷新额度", "{}")
	account, _ := s.store.GetAccount(r.Context(), id)
	writeData(w, http.StatusOK, account)
}

func (s *Server) runAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	account, err := s.store.GetAccount(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	allowed := map[string]bool{"due": true, "retry_wait": true, "attention": true, "quota_retry": true, "pending_check": true, "failed": true, "success_unverified": true}
	if !account.Policy.Enabled || !allowed[account.RuntimeState] {
		writeError(w, 409, "MANUAL_RUN_NOT_ALLOWED", "当前状态只允许只读检查，不能发起激活")
		return
	}
	if err := s.scheduler.RunNow(r.Context(), id); err != nil {
		writeRemoteError(w, err)
		return
	}
	_ = s.store.AddEvent(r.Context(), "info", "admin", "manual_run", id, "管理员触发了受控预检", "{}")
	account, _ = s.store.GetAccount(r.Context(), id)
	writeData(w, http.StatusOK, account)
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	models, err := s.scheduler.Models(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRemoteError(w, err)
		return
	}
	writeData(w, http.StatusOK, models)
}

func (s *Server) cycles(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListCycles(r.Context(), r.PathValue("id"), 100)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (s *Server) attempts(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListAttempts(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	before := parseInt(r.URL.Query().Get("before"), 0)
	items, err := s.store.ListEvents(r.Context(), before, 100)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, items)
}

func settingsView(settings store.Settings) map[string]any {
	return map[string]any{
		"base_url": settings.BaseURL, "api_key_configured": settings.APIKey != "", "global_model": settings.GlobalModel,
		"sync_interval_seconds": settings.SyncIntervalSeconds, "reset_grace_seconds": settings.ResetGraceSeconds,
		"max_retries": settings.MaxRetries, "retry_base_seconds": settings.RetryBaseSeconds,
		"request_timeout_seconds": settings.RequestTimeoutSeconds, "max_concurrency": settings.MaxConcurrency,
		"allow_private_http": settings.AllowPrivateHTTP, "updated_at": settings.UpdatedAt,
	}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeData(w, http.StatusOK, settingsView(settings))
}

type settingsRequest struct {
	BaseURL               string `json:"base_url"`
	APIKey                string `json:"api_key"`
	GlobalModel           string `json:"global_model"`
	AllowPrivateHTTP      bool   `json:"allow_private_http"`
	SyncIntervalSeconds   int    `json:"sync_interval_seconds"`
	ResetGraceSeconds     int    `json:"reset_grace_seconds"`
	MaxRetries            int    `json:"max_retries"`
	RetryBaseSeconds      int    `json:"retry_base_seconds"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
	MaxConcurrency        int    `json:"max_concurrency"`
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var request settingsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	current, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	next := store.Settings{ConnectionUUID: current.ConnectionUUID, BaseURL: request.BaseURL, APIKey: request.APIKey,
		GlobalModel: strings.TrimSpace(request.GlobalModel), AllowPrivateHTTP: request.AllowPrivateHTTP,
		SyncIntervalSeconds: request.SyncIntervalSeconds, ResetGraceSeconds: request.ResetGraceSeconds,
		MaxRetries: request.MaxRetries, RetryBaseSeconds: request.RetryBaseSeconds,
		RequestTimeoutSeconds: request.RequestTimeoutSeconds, MaxConcurrency: request.MaxConcurrency}
	replaceKey := strings.TrimSpace(request.APIKey) != ""
	if !replaceKey {
		next.APIKey = current.APIKey
	}
	if err := validateSettings(next); err != nil {
		writeError(w, 400, "INVALID_SETTINGS", err.Error())
		return
	}
	if _, err := probe(r.Context(), next); err != nil {
		writeRemoteError(w, err)
		return
	}
	oldURL, _ := sub2api.ValidateBaseURL(current.BaseURL, current.AllowPrivateHTTP)
	newURL, _ := sub2api.ValidateBaseURL(next.BaseURL, next.AllowPrivateHTTP)
	rotate := oldURL != newURL
	if rotate {
		next.ConnectionUUID, _, err = secure.GenerateToken(18)
		if err != nil {
			writeError(w, 500, "RANDOM_ERROR", "cannot generate connection identity")
			return
		}
	}
	if err := s.store.UpdateSettings(r.Context(), next, replaceKey, rotate); err != nil {
		writeStoreError(w, err)
		return
	}
	_ = s.store.AddEvent(r.Context(), "info", "admin", "settings_update", "", "连接与调度设置已更新", "{}")
	s.scheduler.Wake()
	updated, _ := s.store.GetSettings(r.Context())
	writeData(w, http.StatusOK, settingsView(updated))
}

func (s *Server) testSettings(w http.ResponseWriter, r *http.Request) {
	var request settingsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	current, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	settings := store.Settings{BaseURL: request.BaseURL, APIKey: request.APIKey, GlobalModel: strings.TrimSpace(request.GlobalModel),
		AllowPrivateHTTP: request.AllowPrivateHTTP, SyncIntervalSeconds: request.SyncIntervalSeconds,
		ResetGraceSeconds: request.ResetGraceSeconds, MaxRetries: request.MaxRetries, RetryBaseSeconds: request.RetryBaseSeconds,
		RequestTimeoutSeconds: request.RequestTimeoutSeconds, MaxConcurrency: request.MaxConcurrency}
	if settings.APIKey == "" {
		settings.APIKey = current.APIKey
	}
	if err := validateSettings(settings); err != nil {
		writeError(w, 400, "INVALID_SETTINGS", err.Error())
		return
	}
	version, err := probe(r.Context(), settings)
	if err != nil {
		writeRemoteError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"version": version})
}
