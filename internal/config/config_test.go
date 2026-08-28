package config

import "testing"

func TestLoadUsesPackagedDefaults(t *testing.T) {
	for _, key := range []string{EnvListen, EnvDBPath, EnvMasterKey, EnvCookieSecure} {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:2555" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	if cfg.DBPath != "/opt/sub2apiauto5h/data/app.db" {
		t.Fatalf("db path = %q", cfg.DBPath)
	}
}
