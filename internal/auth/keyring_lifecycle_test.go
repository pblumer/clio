package auth

import (
	"testing"
	"time"
)

func TestExpiredAndUsable(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	cases := []struct {
		name       string
		status     Status
		expires    *time.Time
		wantExpire bool
		wantUsable bool
	}{
		{"aktiv ohne ablauf", StatusActive, nil, false, true},
		{"aktiv abgelaufen", StatusActive, &past, true, false},
		{"aktiv künftig", StatusActive, &future, false, true},
		{"aktiv exakt jetzt", StatusActive, &now, true, false}, // inklusiv
		{"widerrufen ohne ablauf", StatusRevoked, nil, false, false},
		{"widerrufen künftig", StatusRevoked, &future, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := Key{Status: tc.status, ExpiresAt: tc.expires}
			if got := k.Expired(now); got != tc.wantExpire {
				t.Errorf("Expired = %v, want %v", got, tc.wantExpire)
			}
			if got := k.Usable(now); got != tc.wantUsable {
				t.Errorf("Usable = %v, want %v", got, tc.wantUsable)
			}
		})
	}
}

func TestGenerateKeyWithMeta(t *testing.T) {
	exp := time.Now().UTC().Add(24 * time.Hour)
	k, secret, err := GenerateKeyWithMeta("billing", []Scope{ScopeRead}, KeyMeta{
		Description: "  read-only reporting  ",
		Owner:       "team-billing",
		Purpose:     "dashboards",
		ExpiresAt:   &exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || k.SecretHash != HashSecret(secret) {
		t.Fatal("secret/hash inkonsistent")
	}
	if k.Description != "read-only reporting" { // getrimmt
		t.Errorf("Description = %q", k.Description)
	}
	if k.Owner != "team-billing" || k.Purpose != "dashboards" {
		t.Errorf("owner/purpose nicht übernommen: %+v", k)
	}
	if k.ExpiresAt == nil || !k.ExpiresAt.Equal(exp) {
		t.Errorf("ExpiresAt = %v, want %v", k.ExpiresAt, exp)
	}
	// Backwards-Compat: GenerateKey ohne Meta lässt die Felder leer.
	k2, _, _ := GenerateKey("x", []Scope{ScopeRead})
	if k2.Owner != "" || k2.ExpiresAt != nil {
		t.Errorf("GenerateKey sollte keine Metadaten setzen: %+v", k2)
	}
}

func TestNewSecretUnique(t *testing.T) {
	a, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if a == "" || a == b {
		t.Fatalf("secrets nicht eindeutig: %q / %q", a, b)
	}
}
