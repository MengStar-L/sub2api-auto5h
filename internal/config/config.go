package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	EnvListen       = "SUB2API_AUTO5H_LISTEN"
	EnvDBPath       = "SUB2API_AUTO5H_DB_PATH"
	EnvMasterKey    = "SUB2API_AUTO5H_MASTER_KEY"
	EnvCookieSecure = "SUB2API_AUTO5H_COOKIE_SECURE"
)

type Config struct {
	Listen        string
	DBPath        string
	MasterKey     []byte
	SecretsLocked bool
	CookieSecure  bool
}

func Load() (Config, error) {
	cfg := Config{
		Listen: "0.0.0.0:2555",
		DBPath: "/opt/sub2apiauto5h/data/app.db",
	}
	if value := strings.TrimSpace(os.Getenv(EnvListen)); value != "" {
		cfg.Listen = value
	}
	if value := strings.TrimSpace(os.Getenv(EnvDBPath)); value != "" {
		cfg.DBPath = value
	}
	if value := strings.TrimSpace(os.Getenv(EnvCookieSecure)); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("%s must be true or false: %w", EnvCookieSecure, err)
		}
		cfg.CookieSecure = parsed
	}

	encoded := strings.TrimSpace(os.Getenv(EnvMasterKey))
	if encoded == "" {
		cfg.SecretsLocked = true
		return cfg, nil
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		cfg.SecretsLocked = true
		return cfg, nil
	}
	cfg.MasterKey = key
	return cfg, nil
}
