package webui

import (
	"regexp"
	"strings"
	"testing"
)

// Das Feature „interaktive Event-Links" lebt — ADR-020-konform (reine
// View-Schicht, Vanilla JS, kein neuer Endpoint) — im eingebetteten
// dashboard.js: Sieht ein Payload-Feld wie ein Fremdschlüssel aus, wird sein
// String-Wert als klickbarer Link auf die Event-Ansicht des referenzierten
// Subjects gerendert. Es gibt deshalb keinen serverseitigen Renderer, den man
// direkt aufrufen könnte; diese Tests prüfen daher (a) das VERHALTEN der
// Feld→Collection-Regel als ausführbare Spezifikation und (b) dass das
// ausgelieferte Asset genau diese Regel samt sicherem Link-Rendering enthält.

// refFieldRE spiegelt die Heuristik aus dashboard.js (REF_FIELD_RE). Der
// Asset-Test TestDashboardJSEmbedsReferenceHeuristic pinnt das JS auf dasselbe
// Regex-Literal, sodass dieser Go-Spiegel und die JS-Laufzeit nicht auseinander
// laufen können.
var refFieldRE = regexp.MustCompile(`^(.+?)(ids|id|refs|ref)$`)

// referenceCollection ist der Go-Spiegel der gleichnamigen JS-Funktion: best-effort
// Plural des Stamms, "" wenn kein Fremdschlüssel. Siehe Kommentar in dashboard.js.
func referenceCollection(field string) (string, bool) {
	m := refFieldRE.FindStringSubmatch(strings.ToLower(field))
	if m == nil {
		return "", false
	}
	stem := m[1]
	if strings.HasSuffix(stem, "s") {
		return stem, true
	}
	return stem + "s", true
}

func TestReferenceCollection(t *testing.T) {
	cases := []struct {
		field      string
		collection string
		ok         bool
	}{
		{"employeeId", "employees", true},
		{"customerId", "customers", true},
		{"tagIds", "tags", true},
		{"productRef", "products", true},
		{"orderRefs", "orders", true},
		{"EmployeeID", "employees", true}, // Gross-/Kleinschreibung egal
		// Stamm endet bereits auf "s" → kein doppeltes "ss".
		{"addressId", "address", true},
		// Bewusste v1-Grenze: camelCase wird zusammengezogen, NICHT zu /primary-accounts/.
		{"primaryAccountId", "primaryaccounts", true},
		// Keine Referenzen:
		{"id", "", false},   // blosses "id" ist ausgeschlossen
		{"ids", "", false},  // ebenso der blosse Plural
		{"ref", "", false},  // blosses Suffix ohne Stamm
		{"refs", "", false}, //
		{"", "", false},     // leeres Feld
		{"name", "", false}, // gewöhnliches Feld
		{"status", "", false},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			coll, ok := referenceCollection(c.field)
			if ok != c.ok || coll != c.collection {
				t.Fatalf("referenceCollection(%q) = (%q, %v), want (%q, %v)",
					c.field, coll, ok, c.collection, c.ok)
			}
		})
	}
}

// TestDashboardJSEmbedsReferenceHeuristic stellt sicher, dass das Asset genau die
// oben getestete Regel und das Link-Rendering trägt (sonst läuft der Go-Spiegel
// gegen eine andere Laufzeit-Implementierung).
func TestDashboardJSEmbedsReferenceHeuristic(t *testing.T) {
	js := string(mustReadAsset("static/js/dashboard.js"))
	markers := []string{
		`/^(.+?)(ids|id|refs|ref)$/`,          // exakt dieselbe Regex wie der Go-Spiegel
		"function referenceCollection(field)", // Feld→Collection
		"function appendPayload(",             // sicherer Payload-Renderer
		`a.className = "ev-ref"`,              // Link-Markup
		"a.dataset.subject =",                 // Ziel-Subject als data-Attribut
		`appendPayload(pre, ev.data, 0, "")`,  // renderEvent nutzt den neuen Renderer
	}
	for _, m := range markers {
		if !strings.Contains(js, m) {
			t.Errorf("dashboard.js enthält Marker nicht: %q", m)
		}
	}
	// Der alte, link-lose Pfad darf nicht zurückbleiben (sonst kein Link-Rendering).
	if strings.Contains(js, "pre.textContent = JSON.stringify(ev.data") {
		t.Error("dashboard.js rendert ev.data noch via textContent (kein Link-Rendering)")
	}
}

// TestDashboardCSSStylesReference prüft, dass die Linkfarbe ausschliesslich über
// das Theme-Token --accent kommt (keine Literalfarbe) und Unterstreichung erst
// bei Hover/Fokus greift.
func TestDashboardCSSStylesReference(t *testing.T) {
	css := string(mustReadAsset("static/css/dashboard.css"))
	if !strings.Contains(css, ".ev-ref { color: var(--accent)") {
		t.Error("dashboard.css: .ev-ref nutzt nicht das Theme-Token var(--accent)")
	}
	if !strings.Contains(css, ".ev-ref:hover, .ev-ref:focus-visible { text-decoration: underline; }") {
		t.Error("dashboard.css: .ev-ref hat keine Unterstreichung bei :hover/:focus-visible")
	}
}
