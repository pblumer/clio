package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// assetRequest baut eine Anfrage an den AssetHandler mit gesetztem path-Wert
// (sonst nur über den Mux belegt).
func assetRequest(p string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/ui/"+p, nil)
	r.SetPathValue("path", p)
	return r
}

func TestAssetHandlerServesCSS(t *testing.T) {
	rec := httptest.NewRecorder()
	AssetHandler().ServeHTTP(rec, assetRequest("css/dashboard.css"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("Content-Type = %q, want text/css…", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Fatal("ETag fehlt")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("leerer CSS-Body")
	}
}

func TestAssetHandlerETagRevalidation(t *testing.T) {
	rec := httptest.NewRecorder()
	AssetHandler().ServeHTTP(rec, assetRequest("css/dashboard.css"))
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag fehlt")
	}

	rec = httptest.NewRecorder()
	req := assetRequest("css/dashboard.css")
	req.Header.Set("If-None-Match", etag)
	AssetHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("304 mit Body (%d Bytes), want leer", rec.Body.Len())
	}
}

func TestAssetHandlerUnknownReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	AssetHandler().ServeHTTP(rec, assetRequest("css/gibtsnicht.css"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAssetHandlerEmptyPathRedirectsToUI(t *testing.T) {
	rec := httptest.NewRecorder()
	AssetHandler().ServeHTTP(rec, assetRequest(""))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/ui" {
		t.Fatalf("Location = %q, want /ui", loc)
	}
}

// TestAssetHandlerNoTraversal stellt sicher, dass "../" nicht aus dem
// static-Verzeichnis ausbricht (z. B. die Go-Quelldatei zu lesen).
func TestAssetHandlerNoTraversal(t *testing.T) {
	rec := httptest.NewRecorder()
	AssetHandler().ServeHTTP(rec, assetRequest("../webui.go"))
	if rec.Code == http.StatusOK {
		t.Fatalf("Pfad-Traversal hätte nicht 200 liefern dürfen")
	}
}

// TestDashboardHTMLReferencesExternalCSS prüft, dass das Markup das ausgelagerte
// Stylesheet einbindet und kein Inline-<style> mehr enthält.
func TestDashboardHTMLReferencesExternalCSS(t *testing.T) {
	html := string(dashboardHTML)
	if !strings.Contains(html, `<link rel="stylesheet" href="/ui/css/dashboard.css">`) {
		t.Fatal("dashboard.html verweist nicht auf das ausgelagerte Stylesheet")
	}
	if strings.Contains(html, "<style>") {
		t.Fatal("dashboard.html enthält noch ein Inline-<style> (sollte ausgelagert sein)")
	}
}
