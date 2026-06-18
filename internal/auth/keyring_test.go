package auth

import (
	"strings"
	"testing"
)

func TestParseBearer(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantKID    string
		wantSecret string
		wantOK     bool
	}{
		{name: "gültig", header: "Bearer kid_ci01.W8xqT2vK9pL4mN6rS1dF3hJ5", wantKID: "kid_ci01", wantSecret: "W8xqT2vK9pL4mN6rS1dF3hJ5", wantOK: true},
		{name: "schema case-insensitiv", header: "bearer kid_ci01.geheim", wantKID: "kid_ci01", wantSecret: "geheim", wantOK: true},
		{name: "secret darf punkte enthalten", header: "Bearer kid_ci01.sec.ret.mit.punkten", wantKID: "kid_ci01", wantSecret: "sec.ret.mit.punkten", wantOK: true},
		{name: "kein punkt", header: "Bearer nurtokenohnepunkt"},
		{name: "nur kid mit punkt am ende", header: "Bearer kid_ci01."},
		{name: "führender punkt leerer kid", header: "Bearer .geheim"},
		{name: "leer", header: ""},
		{name: "nur schema", header: "Bearer "},
		{name: "falsches schema", header: "Basic kid_ci01.geheim"},
		{name: "nur ein punkt ohne werte", header: "Bearer ."},
		{name: "kein bearer nur token", header: "kid_ci01.geheim"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kid, secret, ok := ParseBearer(tc.header)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if kid != tc.wantKID {
				t.Errorf("kid = %q, want %q", kid, tc.wantKID)
			}
			if secret != tc.wantSecret {
				t.Errorf("secret = %q, want %q", secret, tc.wantSecret)
			}
		})
	}
}

func TestHashSecretDeterministischUndHex(t *testing.T) {
	h1 := HashSecret("geheim")
	h2 := HashSecret("geheim")
	if h1 != h2 {
		t.Fatalf("HashSecret nicht deterministisch: %q != %q", h1, h2)
	}
	if HashSecret("geheim") == HashSecret("anders") {
		t.Fatal("verschiedene secrets ergeben denselben hash")
	}
	if len(h1) != 64 { // sha256 hex
		t.Fatalf("hash-länge = %d, want 64", len(h1))
	}
	if strings.ContainsAny(h1, "GHIJKLMNOPQRSTUVWXYZ") {
		t.Fatalf("hash ist nicht hex-kodiert: %q", h1)
	}
}

func TestHasScope(t *testing.T) {
	k := Key{Scopes: []Scope{ScopeRead, ScopeWrite}}
	if !k.HasScope(ScopeRead) || !k.HasScope(ScopeWrite) {
		t.Fatal("erwartete scopes fehlen")
	}
	if k.HasScope(ScopeAdmin) {
		t.Fatal("admin-scope unerwartet vorhanden")
	}
}

func TestScopeValid(t *testing.T) {
	for _, s := range []Scope{ScopeRead, ScopeWrite, ScopeAdmin} {
		if !s.Valid() {
			t.Errorf("%q sollte gültig sein", s)
		}
	}
	if Scope("superuser").Valid() {
		t.Error("unbekannter scope sollte ungültig sein")
	}
}

func TestGenerateKeyEntropieUndKollisionsfreiheit(t *testing.T) {
	const n = 1000
	kids := make(map[string]struct{}, n)
	secrets := make(map[string]struct{}, n)
	hashes := make(map[string]struct{}, n)

	for i := 0; i < n; i++ {
		k, secret, err := GenerateKey("test", []Scope{ScopeRead})
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		if !strings.HasPrefix(k.KID, kidPrefix) {
			t.Fatalf("kid ohne präfix: %q", k.KID)
		}
		if k.Status != StatusActive {
			t.Fatalf("neuer key ist nicht active: %q", k.Status)
		}
		if k.RevokedAt != nil {
			t.Fatal("neuer key hat revokedAt gesetzt")
		}
		// Geheimnis muss ausreichend lang sein (160 Bit base32 ≈ 32 Zeichen).
		if len(secret) < 30 {
			t.Fatalf("secret zu kurz (%d zeichen): %q", len(secret), secret)
		}
		// Im Record steht der Hash, nicht das Geheimnis.
		if k.SecretHash == secret {
			t.Fatal("record enthält das klartext-secret statt des hashes")
		}
		if k.SecretHash != HashSecret(secret) {
			t.Fatal("gespeicherter hash passt nicht zum secret")
		}

		if _, dup := kids[k.KID]; dup {
			t.Fatalf("kid-kollision: %q", k.KID)
		}
		if _, dup := secrets[secret]; dup {
			t.Fatalf("secret-kollision: %q", secret)
		}
		kids[k.KID] = struct{}{}
		secrets[secret] = struct{}{}
		hashes[k.SecretHash] = struct{}{}
	}

	if len(kids) != n || len(secrets) != n || len(hashes) != n {
		t.Fatalf("kollisionen: kids=%d secrets=%d hashes=%d (von %d)", len(kids), len(secrets), len(hashes), n)
	}
}

func TestNewKeyWithSecret(t *testing.T) {
	k, err := NewKeyWithSecret("bootstrap-admin", []Scope{ScopeAdmin}, "operator-provided-secret")
	if err != nil {
		t.Fatalf("NewKeyWithSecret: %v", err)
	}
	if !strings.HasPrefix(k.KID, kidPrefix) {
		t.Fatalf("kid ohne präfix: %q", k.KID)
	}
	if k.SecretHash != HashSecret("operator-provided-secret") {
		t.Fatal("hash passt nicht zum vorgegebenen secret")
	}
	if k.Status != StatusActive || !k.HasScope(ScopeAdmin) {
		t.Fatalf("unerwarteter key: %+v", k)
	}
	// Der vorgegebene Wert lässt sich als kid.secret wieder zerlegen.
	kid, secret, ok := ParseBearer("Bearer " + k.KID + ".operator-provided-secret")
	if !ok || kid != k.KID || secret != "operator-provided-secret" {
		t.Fatalf("roundtrip fehlgeschlagen: ok=%v kid=%q secret=%q", ok, kid, secret)
	}
}

// TestGeneratedKeyRoundTripsThroughParseBearer stellt sicher, dass ein erzeugter
// Schlüssel als kid.secret wieder zerlegbar ist.
func TestGeneratedKeyRoundTripsThroughParseBearer(t *testing.T) {
	k, secret, err := GenerateKey("ci", []Scope{ScopeWrite})
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	header := "Bearer " + k.KID + "." + secret
	kid, gotSecret, ok := ParseBearer(header)
	if !ok {
		t.Fatal("erzeugter schlüssel ist nicht parsebar")
	}
	if kid != k.KID || gotSecret != secret {
		t.Fatalf("roundtrip: kid=%q secret=%q, want kid=%q secret=%q", kid, gotSecret, k.KID, secret)
	}
}

func TestResolveSource(t *testing.T) {
	tests := []struct {
		name      string
		allowed   []string
		requested string
		want      string
		wantErr   error
	}{
		// Leere Menge = keine Einschränkung (Legacy): requested unverändert.
		{name: "uneingeschränkt übernimmt requested", allowed: nil, requested: "https://a", want: "https://a"},
		{name: "uneingeschränkt lässt leer leer", allowed: nil, requested: "", want: ""},

		// Genau eine erlaubte Source.
		{name: "eine: weglassen setzt sie", allowed: []string{"svc-a"}, requested: "", want: "svc-a"},
		{name: "eine: nur whitespace setzt sie", allowed: []string{"svc-a"}, requested: "  ", want: "svc-a"},
		{name: "eine: passende erlaubt", allowed: []string{"svc-a"}, requested: "svc-a", want: "svc-a"},
		{name: "eine: passende mit whitespace normalisiert", allowed: []string{"svc-a"}, requested: " svc-a ", want: "svc-a"},
		{name: "eine: abweichende abgelehnt", allowed: []string{"svc-a"}, requested: "svc-b", wantErr: ErrSourceNotAllowed},

		// Mehrere erlaubte Sources.
		{name: "mehrere: fehlend ist pflicht", allowed: []string{"svc-a", "svc-b"}, requested: "", wantErr: ErrSourceRequired},
		{name: "mehrere: enthalten erlaubt", allowed: []string{"svc-a", "svc-b"}, requested: "svc-b", want: "svc-b"},
		{name: "mehrere: nicht enthalten abgelehnt", allowed: []string{"svc-a", "svc-b"}, requested: "svc-c", wantErr: ErrSourceNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSource(tc.allowed, tc.requested)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unerwarteter fehler: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ResolveSource(%v, %q) = %q, want %q", tc.allowed, tc.requested, got, tc.want)
			}
		})
	}
}

// TestIdentityResolveSource prüft, dass die Identity-Methode die erlaubte Menge
// des Tokens nutzt.
func TestIdentityResolveSource(t *testing.T) {
	id := Identity{KID: "kid_x", AllowedSources: []string{"gateway"}}
	got, err := id.ResolveSource("")
	if err != nil || got != "gateway" {
		t.Fatalf("Identity.ResolveSource = %q, %v; want \"gateway\", nil", got, err)
	}
	if _, err := id.ResolveSource("fremd"); err != ErrSourceNotAllowed {
		t.Fatalf("err = %v, want ErrSourceNotAllowed", err)
	}
}
