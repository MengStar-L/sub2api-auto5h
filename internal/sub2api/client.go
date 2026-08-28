package sub2api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxJSONBody = 2 << 20

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func ValidateBaseURL(raw string, allowPrivateHTTP bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("base URL must be an absolute http or https origin")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("base URL scheme must be http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("base URL must not contain user info, a path, query, or fragment")
	}
	if parsed.Scheme == "http" {
		if !allowPrivateHTTP {
			return "", errors.New("HTTP requires explicit private-network confirmation")
		}
		host := strings.ToLower(parsed.Hostname())
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || (!ip.IsPrivate() && !ip.IsLoopback())) {
			return "", errors.New("HTTP is permitted only for localhost or a literal private IP")
		}
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func NewClient(rawURL, apiKey string, allowPrivateHTTP bool, timeout time.Duration) (*Client, error) {
	baseURL, err := ValidateBaseURL(rawURL, allowPrivateHTTP)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("sub2api admin API key is required")
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(apiKey),
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("sub2api redirects are disabled")
			},
		},
	}, nil
}

func (c *Client) request(ctx context.Context, method, path string, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, &APIError{Kind: ErrorTransient, Message: "request failed", Cause: err}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxJSONBody+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return nil, resp.StatusCode, &APIError{Kind: ErrorTransient, StatusCode: resp.StatusCode, Message: "read response failed", Cause: err}
	}
	if len(contents) > maxJSONBody {
		return nil, resp.StatusCode, &APIError{Kind: ErrorSchema, StatusCode: resp.StatusCode, Message: "response exceeds 2 MiB"}
	}
	var wrapped envelope
	if err := json.Unmarshal(contents, &wrapped); err != nil {
		kind := ErrorSchema
		if resp.StatusCode >= 500 {
			kind = ErrorTransient
		}
		return nil, resp.StatusCode, &APIError{Kind: kind, StatusCode: resp.StatusCode, Message: "invalid JSON response", Cause: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || wrapped.Code != 0 {
		return nil, resp.StatusCode, classifyStatus(resp.StatusCode, strconv.Itoa(wrapped.Code), truncateMessage(wrapped.Message))
	}
	if len(wrapped.Data) == 0 || string(wrapped.Data) == "null" {
		return nil, resp.StatusCode, &APIError{Kind: ErrorSchema, StatusCode: resp.StatusCode, Message: "success response has no data"}
	}
	return wrapped.Data, resp.StatusCode, nil
}

func (c *Client) Version(ctx context.Context) (string, error) {
	data, _, err := c.request(ctx, http.MethodGet, "/api/v1/admin/system/version", nil)
	if err != nil {
		return "", err
	}
	var object struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &object); err == nil && object.Version != "" {
		return strings.TrimPrefix(strings.TrimSpace(object.Version), "v"), nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err == nil && value != "" {
		return strings.TrimPrefix(strings.TrimSpace(value), "v"), nil
	}
	return "", &APIError{Kind: ErrorSchema, Message: "version response has no version field"}
}

func versionAtLeast(current, minimum string) bool {
	parse := func(value string) [3]int {
		var result [3]int
		parts := strings.SplitN(strings.TrimPrefix(value, "v"), "-", 2)
		for index, item := range strings.Split(parts[0], ".") {
			if index >= 3 {
				break
			}
			result[index], _ = strconv.Atoi(item)
		}
		return result
	}
	a, b := parse(current), parse(minimum)
	for index := range a {
		if a[index] != b[index] {
			return a[index] > b[index]
		}
	}
	return true
}

func (c *Client) Probe(ctx context.Context) (string, []Account, error) {
	version, err := c.Version(ctx)
	if err != nil {
		return "", nil, err
	}
	if !versionAtLeast(version, MinimumVersion) {
		return version, nil, &APIError{Kind: ErrorSchema, Message: "sub2api " + version + " is older than required " + MinimumVersion}
	}
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return version, nil, err
	}
	return version, accounts, nil
}

func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	const pageSize = 100
	all := make([]Account, 0)
	for page := 1; page <= 100; page++ {
		path := fmt.Sprintf("/api/v1/admin/accounts?platform=openai&type=oauth&page=%d&page_size=%d", page, pageSize)
		data, _, err := c.request(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var result struct {
			Items    []Account `json:"items"`
			Accounts []Account `json:"accounts"`
			Total    int       `json:"total"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, &APIError{Kind: ErrorSchema, Message: "invalid accounts page", Cause: err}
		}
		items := result.Items
		if items == nil {
			items = result.Accounts
		}
		if items == nil {
			var direct []Account
			if err := json.Unmarshal(data, &direct); err != nil {
				return nil, &APIError{Kind: ErrorSchema, Message: "accounts page has no items array"}
			}
			items = direct
		}
		for _, item := range items {
			if item.ID <= 0 || strings.TrimSpace(item.CreatedAt) == "" {
				return nil, &APIError{Kind: ErrorSchema, Message: "account is missing id or created_at"}
			}
		}
		all = append(all, items...)
		if len(items) < pageSize || (result.Total > 0 && len(all) >= result.Total) {
			return all, nil
		}
	}
	return nil, &APIError{Kind: ErrorSchema, Message: "account pagination exceeded 100 pages"}
}

func (c *Client) Quota(ctx context.Context, accountID int64) (Quota, error) {
	data, _, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/api/v1/admin/openai/accounts/%d/quota", accountID), nil)
	if err != nil {
		return Quota{}, err
	}
	return ParseQuota(data)
}

func (c *Client) Models(ctx context.Context, accountID int64) ([]string, error) {
	data, _, err := c.request(ctx, http.MethodGet, fmt.Sprintf("/api/v1/admin/accounts/%d/models", accountID), nil)
	if err != nil {
		return nil, err
	}
	var raw []any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, &APIError{Kind: ErrorSchema, Message: "invalid models response", Cause: err}
	}
	seen := map[string]bool{}
	models := make([]string, 0)
	for _, entry := range raw {
		var model string
		switch value := entry.(type) {
		case string:
			model = value
		case map[string]any:
			model, _ = value["id"].(string)
			if model == "" {
				model, _ = value["name"].(string)
			}
		}
		model = strings.TrimSpace(model)
		if model != "" && !seen[model] {
			seen[model] = true
			models = append(models, model)
		}
	}
	return models, nil
}

func CredentialString(account Account, keys ...string) string {
	for _, source := range []map[string]any{account.Credentials, account.Extra} {
		for _, key := range keys {
			if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func AccountIdentityHash(account Account) string {
	identity := CredentialString(account, "chatgpt_account_id", "organization_id", "workspace_id")
	if identity == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(identity))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func QuotaIdentityHash(quota Quota) string {
	if quota.AccountID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(quota.AccountID))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func truncateMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[:500]
	}
	return value
}
