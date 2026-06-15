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

func TestFromEnvSyncDefault(t *testing.T) {
	t.Setenv(envToken, "tok")
	t.Setenv(envSync, "")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	if cfg.Sync != defaultSync {
		t.Errorf("Sync = %q, want default %q", cfg.Sync, defaultSync)
	}
}

func TestFromEnvSyncValid(t *testing.T) {
	for _, v := range []string{"group", "always", "off"} {
		t.Setenv(envToken, "tok")
		t.Setenv(envSync, v)
		cfg, err := FromEnv()
		if err != nil {
			t.Fatalf("sync %q: unerwarteter fehler: %v", v, err)
		}
		if cfg.Sync != v {
			t.Errorf("Sync = %q, want %q", cfg.Sync, v)
		}
	}
}

func TestFromEnvSyncInvalid(t *testing.T) {
	t.Setenv(envToken, "tok")
	t.Setenv(envSync, "turbo")

	if _, err := FromEnv(); err == nil {
		t.Fatal("erwartete fehler bei ungültigem CLIO_SYNC, bekam nil")
	}
}

func TestFromEnvDevModeDefaultsOff(t *testing.T) {
	t.Setenv(envToken, "tok")
	t.Setenv(envDevMode, "")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	if cfg.DevMode {
		t.Error("DevMode = true, want false ohne CLIO_DEV_MODE")
	}
}

func TestFromEnvDevMode(t *testing.T) {
	on := []string{"1", "t", "true", "TRUE"}
	for _, v := range on {
		t.Setenv(envToken, "tok")
		t.Setenv(envDevMode, v)
		cfg, err := FromEnv()
		if err != nil {
			t.Fatalf("dev-mode %q: unerwarteter fehler: %v", v, err)
		}
		if !cfg.DevMode {
			t.Errorf("DevMode = false für CLIO_DEV_MODE=%q, want true", v)
		}
	}

	// Unlesbare Werte bleiben sicherheitshalber aus (kein Reset versehentlich frei).
	for _, v := range []string{"0", "false", "nope"} {
		t.Setenv(envToken, "tok")
		t.Setenv(envDevMode, v)
		cfg, err := FromEnv()
		if err != nil {
			t.Fatalf("dev-mode %q: unerwarteter fehler: %v", v, err)
		}
		if cfg.DevMode {
			t.Errorf("DevMode = true für CLIO_DEV_MODE=%q, want false", v)
		}
	}
}
