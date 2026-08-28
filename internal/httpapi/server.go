package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/scheduler"
	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
	"github.com/MengStar-L/sub2api-auto5h/internal/store"
	"github.com/MengStar-L/sub2api-auto5h/internal/sub2api"
)

const (
	sessionCookie = "sub2api_auto5h_session"
	csrfCookie    = "sub2api_auto5h_csrf"
	maxBody       = 64 << 10
	setupTTL      = 30 * time.Minute
	idleTTL       = 12 * time.Hour
	absoluteTTL   = 7 * 24 * time.Hour
)

type Server struct {
	store         *store.Store
	scheduler     *scheduler.Scheduler
	log           *slog.Logger
	cookieSecure  bool
	mux           *http.ServeMux
	setupToken    string
	secretsLocked bool
	loginMu       sync.Mutex
	loginFails    map[string][]time.Time
}

type contextKey string

const sessionContextKey contextKey = "session"

func New(data *store.Store, automation *scheduler.Scheduler, logger *slog.Logger, cookieSecure bool, secretsLocked bool) (*Server, error) {
	server := &Server{store: data, scheduler: automation, log: logger, cookieSecure: cookieSecure, secretsLocked: secretsLocked, mux: http.NewServeMux(), loginFails: make(map[string][]time.Time)}
	complete, err := data.IsSetupComplete(context.Background())
	if err != nil {
		return nil, err
	}
	if !complete && !secretsLocked {
		plain, hash, err := secure.GenerateToken(32)
		if err != nil {
			return nil, err
		}
		if err := data.SetSetupToken(context.Background(), hash, time.Now().Add(setupTTL).Unix()); err != nil {
			return nil, err
		}
		server.setupToken = plain
	}
	server.routes()
	return server, nil
}

func (s *Server) SetupToken() string                  { return s.setupToken }
func (s *Server) Handler() http.Handler               { return securityHeaders(s.mux) }
func (s *Server) MountFrontend(frontend http.Handler) { s.mux.Handle("/", frontend) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /readyz", s.ready)
	s.mux.HandleFunc("GET /api/setup/status", s.setupStatus)
	s.mux.HandleFunc("POST /api/setup/complete", s.setupComplete)
	s.mux.HandleFunc("POST /api/auth/login", s.login)
	s.mux.Handle("POST /api/auth/logout", s.requireAuth(http.HandlerFunc(s.logout)))
	s.mux.Handle("GET /api/auth/session", s.requireAuth(http.HandlerFunc(s.session)))
	s.mux.Handle("PUT /api/auth/password", s.requireAuth(http.HandlerFunc(s.changePassword)))

	s.mux.Handle("GET /api/status", s.requireAuth(http.HandlerFunc(s.status)))
	s.mux.Handle("GET /api/accounts", s.requireAuth(http.HandlerFunc(s.accounts)))
	s.mux.Handle("GET /api/accounts/{id}", s.requireAuth(http.HandlerFunc(s.account)))
	s.mux.Handle("PUT /api/accounts/{id}/policy", s.requireAuth(http.HandlerFunc(s.updatePolicy)))
	s.mux.Handle("POST /api/accounts/batch-policy", s.requireAuth(http.HandlerFunc(s.batchPolicy)))
	s.mux.Handle("POST /api/accounts/sync", s.requireAuth(http.HandlerFunc(s.syncAccounts)))
	s.mux.Handle("POST /api/accounts/{id}/quota-refresh", s.requireAuth(http.HandlerFunc(s.refreshQuota)))
	s.mux.Handle("POST /api/accounts/{id}/run", s.requireAuth(http.HandlerFunc(s.runAccount)))
	s.mux.Handle("GET /api/accounts/{id}/models", s.requireAuth(http.HandlerFunc(s.models)))
	s.mux.Handle("GET /api/accounts/{id}/cycles", s.requireAuth(http.HandlerFunc(s.cycles)))
	s.mux.Handle("GET /api/cycles/{id}/attempts", s.requireAuth(http.HandlerFunc(s.attempts)))
	s.mux.Handle("GET /api/events", s.requireAuth(http.HandlerFunc(s.events)))
	s.mux.Handle("GET /api/settings", s.requireAuth(http.HandlerFunc(s.getSettings)))
	s.mux.Handle("PUT /api/settings", s.requireAuth(http.HandlerFunc(s.putSettings)))
	s.mux.Handle("POST /api/settings/test", s.requireAuth(http.HandlerFunc(s.testSettings)))
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "API endpoint not found")
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.secretsLocked {
		writeError(w, http.StatusServiceUnavailable, "SECRETS_LOCKED", "master key is missing or invalid")
		return
	}
	complete, err := s.store.IsSetupComplete(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE", "database is unavailable")
		return
	}
	if !complete {
		writeData(w, http.StatusOK, map[string]any{"status": "setup_required"})
		return
	}
	if err := s.store.Ready(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "SECRETS_LOCKED", "encrypted settings cannot be opened with the configured master key")
		return
	}
	writeData(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	complete, err := s.store.IsSetupComplete(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DATABASE_ERROR", "cannot read setup state")
		return
	}
	writeData(w, http.StatusOK, map[string]any{"setup_complete": complete, "setup_available": !complete && s.setupToken != ""})
}

type setupRequest struct {
	Username              string `json:"username"`
	Password              string `json:"password"`
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

func (s *Server) setupComplete(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Setup-Token")
	if token == "" || subtle.ConstantTimeCompare([]byte(secure.HashToken(token)), []byte(secure.HashToken(s.setupToken))) != 1 {
		writeError(w, http.StatusUnauthorized, "INVALID_SETUP_TOKEN", "setup token is invalid or expired")
		return
	}
	var request setupRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`).MatchString(request.Username) {
		writeError(w, http.StatusBadRequest, "INVALID_USERNAME", "username must contain 3-64 letters, numbers, dots, underscores, or dashes")
		return
	}
	passwordHash, err := secure.HashPassword(request.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PASSWORD", err.Error())
		return
	}
	settings, err := settingsFromSetup(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_SETTINGS", err.Error())
		return
	}
	version, err := probe(r.Context(), settings)
	if err != nil {
		writeRemoteError(w, err)
		return
	}
	connectionID, _, err := secure.GenerateToken(18)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RANDOM_ERROR", "cannot generate connection identity")
		return
	}
	settings.ConnectionUUID = connectionID
	if err := s.store.CompleteSetup(r.Context(), secure.HashToken(token), request.Username, passwordHash, settings); err != nil {
		writeStoreError(w, err)
		return
	}
	s.setupToken = ""
	_ = s.store.AddEvent(r.Context(), "info", "admin", "setup_complete", "", "初始设置已完成", "{}")
	s.scheduler.Wake()
	writeData(w, http.StatusCreated, map[string]any{"version": version})
}

func settingsFromSetup(request setupRequest) (store.Settings, error) {
	settings := store.Settings{
		BaseURL: request.BaseURL, APIKey: request.APIKey, GlobalModel: strings.TrimSpace(request.GlobalModel),
		SyncIntervalSeconds: request.SyncIntervalSeconds, ResetGraceSeconds: request.ResetGraceSeconds,
		MaxRetries: request.MaxRetries, RetryBaseSeconds: request.RetryBaseSeconds,
		RequestTimeoutSeconds: request.RequestTimeoutSeconds, MaxConcurrency: request.MaxConcurrency,
		AllowPrivateHTTP: request.AllowPrivateHTTP,
	}
	applySettingDefaults(&settings)
	return settings, validateSettings(settings)
}

func applySettingDefaults(settings *store.Settings) {
	if settings.SyncIntervalSeconds == 0 {
		settings.SyncIntervalSeconds = 300
	}
	if settings.ResetGraceSeconds == 0 {
		settings.ResetGraceSeconds = 30
	}
	if settings.MaxRetries == 0 {
		settings.MaxRetries = 3
	}
	if settings.RetryBaseSeconds == 0 {
		settings.RetryBaseSeconds = 30
	}
	if settings.RequestTimeoutSeconds == 0 {
		settings.RequestTimeoutSeconds = 90
	}
	if settings.MaxConcurrency == 0 {
		settings.MaxConcurrency = 4
	}
}

func validateSettings(settings store.Settings) error {
	if _, err := sub2api.ValidateBaseURL(settings.BaseURL, settings.AllowPrivateHTTP); err != nil {
		return err
	}
	if settings.APIKey == "" {
		return errors.New("sub2api admin API key is required")
	}
	if err := validateModel(settings.GlobalModel); err != nil {
		return err
	}
	if settings.SyncIntervalSeconds < 60 || settings.SyncIntervalSeconds > 3600 {
		return errors.New("sync interval must be 60-3600 seconds")
	}
	if settings.ResetGraceSeconds < 0 || settings.ResetGraceSeconds > 600 {
		return errors.New("reset grace must be 0-600 seconds")
	}
	if settings.MaxRetries < 0 || settings.MaxRetries > 6 {
		return errors.New("max retries must be 0-6")
	}
	if settings.RetryBaseSeconds < 5 || settings.RetryBaseSeconds > 600 {
		return errors.New("retry base must be 5-600 seconds")
	}
	if settings.RequestTimeoutSeconds < 15 || settings.RequestTimeoutSeconds > 300 {
		return errors.New("request timeout must be 15-300 seconds")
	}
	if settings.MaxConcurrency < 1 || settings.MaxConcurrency > 16 {
		return errors.New("max concurrency must be 1-16")
	}
	return nil
}

func validateModel(model string) error {
	model = strings.TrimSpace(model)
	if len(model) < 1 || len(model) > 128 {
		return errors.New("text model must contain 1-128 characters")
	}
	lower := strings.ToLower(model)
	for _, fragment := range []string{"gpt-image", "dall-e", "chatgpt-image", "video", "tts", "transcribe"} {
		if strings.Contains(lower, fragment) {
			return errors.New("media models cannot be used for automatic activation")
		}
	}
	return nil
}

func probe(ctx context.Context, settings store.Settings) (string, error) {
	client, err := sub2api.NewClient(settings.BaseURL, settings.APIKey, settings.AllowPrivateHTTP, time.Duration(settings.RequestTimeoutSeconds)*time.Second)
	if err != nil {
		return "", err
	}
	return client.Probe(ctx)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var request struct{ Username, Password string }
	if !decodeJSON(w, r, &request) {
		return
	}
	key := strings.ToLower(strings.TrimSpace(request.Username)) + "|" + remoteIP(r)
	if s.loginBlocked(key) {
		writeError(w, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "too many login attempts; retry in 15 minutes")
		return
	}
	hash, err := s.store.AdminPassword(r.Context(), strings.TrimSpace(request.Username))
	if err != nil || !secure.VerifyPassword(hash, request.Password) {
		s.recordLoginFailure(key)
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "username or password is incorrect")
		return
	}
	s.clearLoginFailures(key)
	plain, tokenHash, err := secure.GenerateToken(32)
	if err != nil {
		writeError(w, 500, "RANDOM_ERROR", "cannot create session")
		return
	}
	csrf, csrfHash, err := secure.GenerateToken(24)
	if err != nil {
		writeError(w, 500, "RANDOM_ERROR", "cannot create session")
		return
	}
	now := time.Now()
	if err := s.store.CreateSession(r.Context(), store.Session{TokenHash: tokenHash, CSRFHash: csrfHash, CreatedAt: now.Unix(), LastSeenAt: now.Unix(), IdleExpiresAt: now.Add(idleTTL).Unix(), AbsoluteExpiresAt: now.Add(absoluteTTL).Unix()}); err != nil {
		writeError(w, 500, "DATABASE_ERROR", "cannot create session")
		return
	}
	s.setCookie(w, sessionCookie, plain, true, now.Add(absoluteTTL))
	s.setCookie(w, csrfCookie, csrf, false, now.Add(absoluteTTL))
	writeData(w, http.StatusOK, map[string]any{"username": request.Username, "csrf_token": csrf})
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) loginBlocked(key string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	cutoff := time.Now().Add(-15 * time.Minute)
	items := s.loginFails[key][:0]
	for _, item := range s.loginFails[key] {
		if item.After(cutoff) {
			items = append(items, item)
		}
	}
	s.loginFails[key] = items
	return len(items) >= 5
}

func (s *Server) recordLoginFailure(key string) {
	s.loginMu.Lock()
	s.loginFails[key] = append(s.loginFails[key], time.Now())
	s.loginMu.Unlock()
}
func (s *Server) clearLoginFailures(key string) {
	s.loginMu.Lock()
	delete(s.loginFails, key)
	s.loginMu.Unlock()
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, httpOnly bool, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: httpOnly, Secure: s.cookieSecure, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "login required")
			return
		}
		now := time.Now()
		session, err := s.store.GetSession(r.Context(), secure.HashToken(cookie.Value), now.Unix())
		if err != nil {
			writeError(w, http.StatusUnauthorized, "SESSION_EXPIRED", "session expired")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			csrf := r.Header.Get("X-CSRF-Token")
			if csrf == "" || subtle.ConstantTimeCompare([]byte(secure.HashToken(csrf)), []byte(session.CSRFHash)) != 1 || !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "CSRF_FAILED", "CSRF validation failed")
				return
			}
		}
		_ = s.store.TouchSession(r.Context(), session.TokenHash, now.Unix(), now.Add(idleTTL).Unix())
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey, session)))
	})
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, r.Host)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, _ := r.Context().Value(sessionContextKey).(store.Session)
	_ = s.store.DeleteSession(r.Context(), session.TokenHash)
	s.setCookie(w, sessionCookie, "", true, time.Unix(1, 0))
	s.setCookie(w, csrfCookie, "", false, time.Unix(1, 0))
	writeData(w, http.StatusOK, map[string]any{"logged_out": true})
}

func (s *Server) session(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, map[string]any{"authenticated": true})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	currentHash, err := s.store.AdminPassword(r.Context(), strings.TrimSpace(request.Username))
	if err != nil || !secure.VerifyPassword(currentHash, request.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "current username or password is incorrect")
		return
	}
	newHash, err := secure.HashPassword(request.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PASSWORD", err.Error())
		return
	}
	session, _ := r.Context().Value(sessionContextKey).(store.Session)
	if err := s.store.UpdateAdminPassword(r.Context(), newHash, session.TokenHash); err != nil {
		writeStoreError(w, err)
		return
	}
	_ = s.store.AddEvent(r.Context(), "info", "admin", "password_change", "", "管理员密码已更新，其他会话已退出", "{}")
	writeData(w, http.StatusOK, map[string]any{"password_changed": true})
}
func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	writeData(w, http.StatusOK, s.scheduler.Status())
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "JSON_REQUIRED", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body is invalid")
		return false
	}
	return true
}

func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "NOT_FOUND", "record not found")
	case errors.Is(err, store.ErrInvalidToken):
		writeError(w, 401, "INVALID_SETUP_TOKEN", err.Error())
	case errors.Is(err, store.ErrSetupComplete):
		writeError(w, 409, "SETUP_COMPLETE", err.Error())
	case errors.Is(err, store.ErrSecretsLocked):
		writeError(w, 503, "SECRETS_LOCKED", err.Error())
	default:
		writeError(w, 500, "DATABASE_ERROR", "database operation failed")
	}
}

func writeRemoteError(w http.ResponseWriter, err error) {
	var apiErr *sub2api.APIError
	if errors.As(err, &apiErr) {
		status := http.StatusBadGateway
		if apiErr.Kind == sub2api.ErrorAuth {
			status = http.StatusUnauthorized
		}
		if apiErr.Kind == sub2api.ErrorCompliance {
			status = http.StatusLocked
		}
		if apiErr.Kind == sub2api.ErrorSchema || apiErr.Kind == sub2api.ErrorRejected {
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, strings.ToUpper(string(apiErr.Kind)), apiErr.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "REMOTE_ERROR", err.Error())
}

func parseInt(value string, fallback int64) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
