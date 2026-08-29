package sub2api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ActivationMaterial struct {
	AccessToken  string
	ExpiresAt    time.Time
	ChatGPTID    string
	Email        string
	IdentityHash string
	UserAgent    string
	Proxy        *ActivationProxy
}

type ActivationProxy struct {
	Protocol string
	Host     string
	Port     int
	Username string
	Password string
}

type dataPayload struct {
	Accounts []dataAccount `json:"accounts"`
	Proxies  []dataProxy   `json:"proxies"`
}

type dataAccount struct {
	Platform    string         `json:"platform"`
	Type        string         `json:"type"`
	Credentials map[string]any `json:"credentials"`
	Extra       map[string]any `json:"extra"`
	ProxyKey    *string        `json:"proxy_key"`
}

type dataProxy struct {
	ProxyKey string `json:"proxy_key"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Status   string `json:"status"`
}

func (c *Client) ActivationMaterial(ctx context.Context, accountID int64, expectedIdentityHash, expectedEmail string) (ActivationMaterial, error) {
	query := url.Values{}
	query.Set("ids", strconv.FormatInt(accountID, 10))
	query.Set("include_proxies", "true")
	data, _, err := c.request(ctx, http.MethodGet, "/api/v1/admin/accounts/data?"+query.Encode(), nil)
	if err != nil {
		return ActivationMaterial{}, err
	}
	var payload dataPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return ActivationMaterial{}, &APIError{Kind: ErrorSchema, Message: "invalid account export", Cause: err}
	}
	if len(payload.Accounts) != 1 {
		return ActivationMaterial{}, &APIError{Kind: ErrorSchema, Message: "single-account export did not return exactly one account"}
	}
	account := payload.Accounts[0]
	if account.Platform != "openai" || account.Type != "oauth" {
		return ActivationMaterial{}, &APIError{Kind: ErrorCompliance, Message: "exported account is not OpenAI OAuth"}
	}
	accessToken := mapString(account.Credentials, "access_token")
	chatGPTID := firstMapString(account.Credentials, "chatgpt_account_id", "organization_id", "workspace_id")
	email := firstMapString(account.Credentials, "email")
	if accessToken == "" || chatGPTID == "" {
		return ActivationMaterial{}, &APIError{Kind: ErrorSchema, Message: "exported account is missing required OAuth fields"}
	}
	identityHash := hashIdentity(chatGPTID)
	if expectedIdentityHash != "" {
		if identityHash == "" || identityHash != expectedIdentityHash {
			return ActivationMaterial{}, &APIError{Kind: ErrorCompliance, Message: "exported account identity does not match synchronized account"}
		}
	} else if normalizedEmail(expectedEmail) == "" || normalizedEmail(email) != normalizedEmail(expectedEmail) {
		return ActivationMaterial{}, &APIError{Kind: ErrorCompliance, Message: "exported account email does not match synchronized account"}
	}
	expiresAt, err := mapTime(account.Credentials, "expires_at")
	if err != nil {
		return ActivationMaterial{}, &APIError{Kind: ErrorSchema, Message: "exported access token expiry is invalid", Cause: err}
	}
	material := ActivationMaterial{
		AccessToken: accessToken, ExpiresAt: expiresAt, ChatGPTID: chatGPTID, Email: email,
		IdentityHash: identityHash, UserAgent: firstMapString(account.Credentials, "user_agent", "user-agent"),
	}
	if material.UserAgent == "" {
		material.UserAgent = firstMapString(account.Extra, "user_agent", "user-agent")
	}
	if account.ProxyKey != nil && strings.TrimSpace(*account.ProxyKey) != "" {
		proxy, err := selectProxy(payload.Proxies, strings.TrimSpace(*account.ProxyKey))
		if err != nil {
			return ActivationMaterial{}, err
		}
		material.Proxy = proxy
	}
	return material, nil
}

func (c *Client) RefreshAccessToken(ctx context.Context, accountID int64) error {
	_, _, err := c.request(ctx, http.MethodPost, fmt.Sprintf("/api/v1/admin/openai/accounts/%d/refresh", accountID), map[string]any{})
	return err
}

func selectProxy(proxies []dataProxy, key string) (*ActivationProxy, error) {
	var found *dataProxy
	for index := range proxies {
		if proxies[index].ProxyKey == key {
			if found != nil {
				return nil, &APIError{Kind: ErrorSchema, Message: "account export contains duplicate proxy keys"}
			}
			found = &proxies[index]
		}
	}
	if found == nil {
		return nil, &APIError{Kind: ErrorSchema, Message: "account proxy was not included in export"}
	}
	protocol := strings.ToLower(strings.TrimSpace(found.Protocol))
	switch protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, &APIError{Kind: ErrorCompliance, Message: "account proxy protocol is unsupported"}
	}
	if strings.TrimSpace(found.Host) == "" || found.Port < 1 || found.Port > 65535 {
		return nil, &APIError{Kind: ErrorSchema, Message: "account proxy endpoint is invalid"}
	}
	if found.Status != "" && found.Status != "active" {
		return nil, &APIError{Kind: ErrorCompliance, Message: "account proxy is not active"}
	}
	return &ActivationProxy{Protocol: protocol, Host: strings.TrimSpace(found.Host), Port: found.Port, Username: found.Username, Password: found.Password}, nil
}

func mapString(source map[string]any, key string) string {
	value, _ := source[key].(string)
	return strings.TrimSpace(value)
}

func firstMapString(source map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := mapString(source, key); value != "" {
			return value
		}
	}
	return ""
}

func mapTime(source map[string]any, key string) (time.Time, error) {
	value, ok := source[key]
	if !ok || value == nil {
		return time.Time{}, nil
	}
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return time.Time{}, nil
		}
		return time.Parse(time.RFC3339, typed)
	case float64:
		if typed <= 0 || typed != float64(int64(typed)) {
			return time.Time{}, fmt.Errorf("invalid unix expiry")
		}
		return time.Unix(int64(typed), 0).UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unexpected expiry type")
	}
}

func hashIdentity(identity string) string {
	if strings.TrimSpace(identity) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(identity)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func normalizedEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
