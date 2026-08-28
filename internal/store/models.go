package store

import "time"

type Settings struct {
	ConnectionUUID        string    `json:"connection_uuid"`
	BaseURL               string    `json:"base_url"`
	APIKey                string    `json:"-"`
	GlobalModel           string    `json:"global_model"`
	SyncIntervalSeconds   int       `json:"sync_interval_seconds"`
	ResetGraceSeconds     int       `json:"reset_grace_seconds"`
	MaxRetries            int       `json:"max_retries"`
	RetryBaseSeconds      int       `json:"retry_base_seconds"`
	RequestTimeoutSeconds int       `json:"request_timeout_seconds"`
	MaxConcurrency        int       `json:"max_concurrency"`
	AllowPrivateHTTP      bool      `json:"allow_private_http"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type RemoteAccountInput struct {
	RemoteID                int64
	RemoteCreatedAt         string
	IdentityHash            string
	Name                    string
	Email                   string
	PlanType                string
	Platform                string
	AccountType             string
	Status                  string
	Schedulable             bool
	ParentAccountID         *int64
	ExpiresAt               string
	AutoPauseOnExpired      bool
	RateLimitResetAt        string
	TempUnschedulableUntil  string
	TempUnschedulableReason string
	Eligible                bool
	EligibilityReason       string
}

type Account struct {
	ID                      string    `json:"id"`
	ConnectionUUID          string    `json:"-"`
	RemoteID                int64     `json:"remote_id"`
	RemoteCreatedAt         string    `json:"remote_created_at"`
	IdentityHash            string    `json:"-"`
	IdentityGeneration      int       `json:"identity_generation"`
	Name                    string    `json:"name"`
	Email                   string    `json:"email"`
	PlanType                string    `json:"plan_type"`
	Platform                string    `json:"platform"`
	AccountType             string    `json:"account_type"`
	Status                  string    `json:"status"`
	Schedulable             bool      `json:"schedulable"`
	ParentAccountID         *int64    `json:"parent_account_id"`
	ExpiresAt               string    `json:"expires_at,omitempty"`
	AutoPauseOnExpired      bool      `json:"auto_pause_on_expired"`
	RateLimitResetAt        string    `json:"rate_limit_reset_at,omitempty"`
	TempUnschedulableUntil  string    `json:"temp_unschedulable_until,omitempty"`
	TempUnschedulableReason string    `json:"temp_unschedulable_reason,omitempty"`
	Missing                 bool      `json:"missing"`
	Eligible                bool      `json:"eligible"`
	EligibilityReason       string    `json:"eligibility_reason"`
	FiveResetAt             *int64    `json:"five_reset_at,omitempty"`
	FiveUsedPercent         *float64  `json:"five_used_percent,omitempty"`
	SevenResetAt            *int64    `json:"seven_reset_at,omitempty"`
	SevenUsedPercent        *float64  `json:"seven_used_percent,omitempty"`
	QuotaFetchedAt          *int64    `json:"quota_fetched_at,omitempty"`
	QuotaState              string    `json:"quota_state"`
	NextActionAt            *int64    `json:"next_action_at,omitempty"`
	RuntimeState            string    `json:"runtime_state"`
	LastError               string    `json:"last_error,omitempty"`
	LastAnswerStatus        string    `json:"last_answer_status"`
	LastAnswerText          string    `json:"last_answer_text"`
	LastAnswerAt            *int64    `json:"last_answer_at,omitempty"`
	LastSeenAt              time.Time `json:"last_seen_at"`
	Policy                  Policy    `json:"policy"`
}

type Policy struct {
	Enabled                  bool    `json:"enabled"`
	EnableGeneration         int     `json:"enable_generation"`
	ModelOverride            *string `json:"model_override"`
	GraceOverrideSeconds     *int    `json:"grace_override_seconds"`
	MaxRetriesOverride       *int    `json:"max_retries_override"`
	RetryBaseOverrideSeconds *int    `json:"retry_base_override_seconds"`
}

type QuotaUpdate struct {
	IdentityHash     string
	PlanType         string
	FiveResetAt      *int64
	FiveUsedPercent  *float64
	SevenResetAt     *int64
	SevenUsedPercent *float64
	FetchedAt        int64
	State            string
	NextActionAt     *int64
	RuntimeState     string
	LastError        string
}

type Cycle struct {
	ID                 string `json:"id"`
	AccountID          string `json:"account_id"`
	IdentityGeneration int    `json:"identity_generation"`
	CycleKey           string `json:"cycle_key"`
	Kind               string `json:"kind"`
	SourceResetAt      *int64 `json:"source_reset_at,omitempty"`
	DueAt              int64  `json:"due_at"`
	Status             string `json:"status"`
	AttemptCount       int    `json:"attempt_count"`
	LeaseUntil         *int64 `json:"lease_until,omitempty"`
	AcceptedAt         *int64 `json:"accepted_at,omitempty"`
	NextAttemptAt      *int64 `json:"next_attempt_at,omitempty"`
	Reason             string `json:"reason"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

type Attempt struct {
	ID            string `json:"id"`
	CycleID       string `json:"cycle_id"`
	AttemptNumber int    `json:"attempt_number"`
	StartedAt     int64  `json:"started_at"`
	EndedAt       *int64 `json:"ended_at,omitempty"`
	Outcome       string `json:"outcome"`
	HTTPStatus    *int   `json:"http_status,omitempty"`
	ErrorCode     string `json:"error_code"`
	Message       string `json:"message"`
	AnswerStatus  string `json:"answer_status"`
	AnswerText    string `json:"answer_text"`
}

type Event struct {
	ID           int64  `json:"id"`
	Level        string `json:"level"`
	Actor        string `json:"actor"`
	Action       string `json:"action"`
	AccountID    string `json:"account_id,omitempty"`
	Message      string `json:"message"`
	MetadataJSON string `json:"metadata_json"`
	CreatedAt    int64  `json:"created_at"`
}

type Session struct {
	TokenHash         string
	CSRFHash          string
	CreatedAt         int64
	LastSeenAt        int64
	IdleExpiresAt     int64
	AbsoluteExpiresAt int64
}
