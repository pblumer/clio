package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesDashboard(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html…", ct)
	}
	// no-cache (revalidieren), NICHT max-age — sonst bliebe nach einem Deploy eine
	// veraltete Dashboard-Version im Cache hängen.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatal("ETag fehlt")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("leerer Body")
	}
}

func TestHandlerETagRevalidation(t *testing.T) {
	// Erst den aktuellen ETag holen …
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui", nil))
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag fehlt")
	}

	// … passender If-None-Match → 304 ohne Body.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	req.Header.Set("If-None-Match", etag)
	Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("304 mit Body (%d Bytes), want leer", rec.Body.Len())
	}

	// … nicht passender ETag (z. B. nach einem Deploy) → 200 mit frischer Seite.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ui", nil)
	req.Header.Set("If-None-Match", `"veraltet"`)
	Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("leerer Body bei nicht passendem ETag")
	}
}
