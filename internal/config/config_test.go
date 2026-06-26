package config

import (
	"strconv"
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

func TestFromEnvDBInitialMB(t *testing.T) {
	cases := []struct {
		name string
		val  string
		want int
	}{
		{"default leer", "", 0},
		{"gesetzt", "4096", 4096},
		{"unlesbar -> default", "viel", 0},
		{"negativ -> auf 0 geklemmt", "-5", 0},
		{"über max -> auf max geklemmt", strconv.Itoa(maxInitMB + 1), maxInitMB},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envToken, "tok")
			t.Setenv(envDBInitMB, tc.val)
			cfg, err := FromEnv()
			if err != nil {
				t.Fatalf("unerwarteter fehler: %v", err)
			}
			if cfg.DBInitialMB != tc.want {
				t.Errorf("DBInitialMB = %d, want %d", cfg.DBInitialMB, tc.want)
			}
		})
	}
}

func TestFromEnvPartitions(t *testing.T) {
	cases := []struct {
		name      string
		val       string
		wantParts int
	}{
		{"default leer -> 1", "", defaultPartitions},
		{"gesetzt", "8", 8},
		{"unlesbar -> default", "viele", defaultPartitions},
		{"null -> auf min 1 geklemmt", "0", 1},
		{"negativ -> auf min 1 geklemmt", "-3", 1},
		{"über max -> auf max geklemmt", strconv.Itoa(maxPartitions + 1), maxPartitions},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envToken, "tok")
			t.Setenv(envPartition, tc.val)
			cfg, err := FromEnv()
			if err != nil {
				t.Fatalf("unerwarteter fehler: %v", err)
			}
			if cfg.Partitions != tc.wantParts {
				t.Errorf("Partitions = %d, want %d", cfg.Partitions, tc.wantParts)
			}
		})
	}
}

func TestFromEnvPartitionVNodesDefault(t *testing.T) {
	t.Setenv(envToken, "tok")
	t.Setenv(envPartVNode, "")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	if cfg.PartitionVNodes != defaultVNodes {
		t.Errorf("PartitionVNodes = %d, want %d", cfg.PartitionVNodes, defaultVNodes)
	}
}

func TestFromEnvDBMonitorInterval(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv(envToken, "tok")
		t.Setenv(envDBMonInt, "")
		cfg, err := FromEnv()
		if err != nil {
			t.Fatalf("unerwarteter fehler: %v", err)
		}
		if cfg.DBMonitorInterval != defaultMonInterval {
			t.Errorf("DBMonitorInterval = %v, want %v", cfg.DBMonitorInterval, defaultMonInterval)
		}
	})
	t.Run("gesetzt", func(t *testing.T) {
		t.Setenv(envToken, "tok")
		t.Setenv(envDBMonInt, "30s")
		cfg, err := FromEnv()
		if err != nil {
			t.Fatalf("unerwarteter fehler: %v", err)
		}
		if cfg.DBMonitorInterval != 30*time.Second {
			t.Errorf("DBMonitorInterval = %v, want 30s", cfg.DBMonitorInterval)
		}
	})
	t.Run("unlesbar -> fehler", func(t *testing.T) {
		t.Setenv(envToken, "tok")
		t.Setenv(envDBMonInt, "bald")
		if _, err := FromEnv(); err == nil {
			t.Fatal("erwartete fehler bei ungültigem CLIO_DB_MONITOR_INTERVAL")
		}
	})
}

func TestFromEnvDBGrowThresholdPct(t *testing.T) {
	cases := []struct {
		val  string
		want int
	}{
		{"", defaultGrowPct},
		{"75", 75},
		{"0", 1},    // auf min geklemmt
		{"150", 99}, // auf max geklemmt
	}
	for _, tc := range cases {
		t.Run(tc.val, func(t *testing.T) {
			t.Setenv(envToken, "tok")
			t.Setenv(envDBGrowPct, tc.val)
			cfg, err := FromEnv()
			if err != nil {
				t.Fatalf("unerwarteter fehler: %v", err)
			}
			if cfg.DBGrowThresholdPct != tc.want {
				t.Errorf("DBGrowThresholdPct = %d, want %d", cfg.DBGrowThresholdPct, tc.want)
			}
		})
	}
}

func TestFromEnvDBCompaction(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv(envToken, "tok")
		t.Setenv(envDBCompact, "")
		t.Setenv(envDBCompInt, "")
		cfg, err := FromEnv()
		if err != nil {
			t.Fatalf("unerwarteter fehler: %v", err)
		}
		if cfg.DBCompactEnabled {
			t.Error("DBCompactEnabled = true, want false (Default aus)")
		}
		if cfg.DBCompactIntervalH != defaultCompactH {
			t.Errorf("DBCompactIntervalH = %d, want %d", cfg.DBCompactIntervalH, defaultCompactH)
		}
	})
	t.Run("aktiviert mit Intervall", func(t *testing.T) {
		t.Setenv(envToken, "tok")
		t.Setenv(envDBCompact, "true")
		t.Setenv(envDBCompInt, "12")
		cfg, err := FromEnv()
		if err != nil {
			t.Fatalf("unerwarteter fehler: %v", err)
		}
		if !cfg.DBCompactEnabled {
			t.Error("DBCompactEnabled = false, want true")
		}
		if cfg.DBCompactIntervalH != 12 {
			t.Errorf("DBCompactIntervalH = %d, want 12", cfg.DBCompactIntervalH)
		}
	})
	t.Run("Intervall geklemmt", func(t *testing.T) {
		t.Setenv(envToken, "tok")
		t.Setenv(envDBCompInt, "0")
		cfg, err := FromEnv()
		if err != nil {
			t.Fatalf("unerwarteter fehler: %v", err)
		}
		if cfg.DBCompactIntervalH != 1 {
			t.Errorf("DBCompactIntervalH = %d, want 1 (min)", cfg.DBCompactIntervalH)
		}
	})
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

func TestFromEnvPresenceAndAuthEvents(t *testing.T) {
	t.Setenv(envToken, "tok")

	// Defaults: 60s-Fenster, Auth-Events aus.
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	if cfg.PresenceWindow != defaultPresenceWindow {
		t.Errorf("PresenceWindow = %v, want default %v", cfg.PresenceWindow, defaultPresenceWindow)
	}
	if cfg.AuthEvents || cfg.AuthDeniedEvents {
		t.Errorf("Auth-Events sollen per Default aus sein, sind %v/%v", cfg.AuthEvents, cfg.AuthDeniedEvents)
	}

	// Overrides.
	t.Setenv(envPresence, "30s")
	t.Setenv(envAuthEv, "true")
	t.Setenv(envAuthDenEv, "1")
	cfg, err = FromEnv()
	if err != nil {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
	if cfg.PresenceWindow != 30*time.Second {
		t.Errorf("PresenceWindow = %v, want 30s", cfg.PresenceWindow)
	}
	if !cfg.AuthEvents || !cfg.AuthDeniedEvents {
		t.Errorf("Auth-Events sollen aktiv sein, sind %v/%v", cfg.AuthEvents, cfg.AuthDeniedEvents)
	}

	// Ungültige Dauer ist ein Fehler.
	t.Setenv(envPresence, "nope")
	if _, err := FromEnv(); err == nil {
		t.Error("ungültige CLIO_PRESENCE_WINDOW soll einen Fehler ergeben")
	}
}
