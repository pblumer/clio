package config

import "testing"

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv(envToken, "tok")
	t.Setenv(envAddr, "")
	t.Setenv(envDBPath, "")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	if cfg.APIToken != "tok" {
		t.Errorf("APIToken = %q, want %q", cfg.APIToken, "tok")
	}
	if cfg.Addr != defaultAddr {
		t.Errorf("Addr = %q, want default %q", cfg.Addr, defaultAddr)
	}
	if cfg.DBPath != defaultDBPath {
		t.Errorf("DBPath = %q, want default %q", cfg.DBPath, defaultDBPath)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	t.Setenv(envToken, "tok")
	t.Setenv(envAddr, ":9999")
	t.Setenv(envDBPath, "/data/clio.db")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	if cfg.Addr != ":9999" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, ":9999")
	}
	if cfg.DBPath != "/data/clio.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/data/clio.db")
	}
}

func TestFromEnvMissingToken(t *testing.T) {
	t.Setenv(envToken, "")

	if _, err := FromEnv(); err == nil {
		t.Fatal("erwartete fehler bei fehlendem token, bekam nil")
	}
}
