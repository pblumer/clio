package config

import (
	"testing"
	"time"
)

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

// TestFromEnvAuthMaterialOptional dokumentiert, dass die Anwesenheit von
// Auth-Material nicht mehr in FromEnv geprüft wird (das wandert in den Bootstrap,
// WP-05, weil es den Store braucht). Alle drei Kombinationen ergeben hier kein
// Fehler.
func TestFromEnvAuthMaterialOptional(t *testing.T) {
	cases := []struct {
		name      string
		token     string
		bootstrap string
	}{
		{name: "nur token", token: "tok"},
		{name: "nur bootstrap", bootstrap: "geheim"},
		{name: "beides leer", token: "", bootstrap: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envToken, tc.token)
			t.Setenv(envBootstrap, tc.bootstrap)

			cfg, err := FromEnv()
			if err != nil {
				t.Fatalf("FromEnv: unerwarteter fehler: %v", err)
			}
			if cfg.APIToken != tc.token {
				t.Errorf("APIToken = %q, want %q", cfg.APIToken, tc.token)
			}
			if cfg.BootstrapAdminKey != tc.bootstrap {
				t.Errorf("BootstrapAdminKey = %q, want %q", cfg.BootstrapAdminKey, tc.bootstrap)
			}
		})
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

func TestFromEnvQueryTimeoutDefaultOff(t *testing.T) {
	t.Setenv(envToken, "tok")
	t.Setenv(envQueryTO, "")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	if cfg.QueryTimeout != 0 {
		t.Errorf("QueryTimeout = %v, want 0 (aus) ohne %s", cfg.QueryTimeout, envQueryTO)
	}
}

func TestFromEnvQueryTimeoutValid(t *testing.T) {
	t.Setenv(envToken, "tok")
	t.Setenv(envQueryTO, "90s")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	if cfg.QueryTimeout != 90*time.Second {
		t.Errorf("QueryTimeout = %v, want 90s", cfg.QueryTimeout)
	}
}

func TestFromEnvQueryTimeoutInvalid(t *testing.T) {
	for _, v := range []string{"schnell", "-5s"} {
		t.Setenv(envToken, "tok")
		t.Setenv(envQueryTO, v)
		if _, err := FromEnv(); err == nil {
			t.Errorf("erwartete fehler bei %s=%q, bekam nil", envQueryTO, v)
		}
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
