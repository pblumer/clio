# Social-Preview-Bild

`social-preview.png` ist das Open-Graph-Vorschaubild für `pblumer/clio`. Es ist
das „schöne Icon", das in Microsoft Teams, Slack, Discord & Co. erscheint, sobald
jemand den Repo-Link teilt.

Ohne hinterlegtes Social-Preview-Bild fällt GitHub auf ein automatisch generiertes
Bild zurück, das viele Clients (u. a. Teams) **gar nicht** anzeigen — dann sieht
man nur Titel + Text ohne Icon.

## Aktivieren (einmalig, im Browser)

Das Bild lässt sich **nicht** per Commit oder API setzen, sondern nur in den
Repo-Einstellungen:

1. **Settings → General → Social preview**
   (`https://github.com/pblumer/clio/settings`)
2. **Edit → Upload an image…** und `.github/social-preview.png` auswählen.
3. Speichern. Die Vorschau aktualisiert sich nach kurzer Zeit; Cache von Teams/Slack
   ggf. durch erneutes Teilen oder einen `?v=2`-Parameter an der URL anstoßen.

Empfohlene Maße: 1280×640 px (Min. 640×320, < 1 MB) — die mitgelieferte Datei ist
2560×1280 px (2×, für gestochen scharfe Darstellung) und liegt unter 1 MB.

## Neu erzeugen

Die Vorlage `social-preview.html` rendert das Bild im Clio-Look (Farben aus dem
Betriebs-Dashboard). Mit headless Chromium:

```sh
chrome --headless --no-sandbox --hide-scrollbars \
  --force-device-scale-factor=2 --window-size=1280,640 \
  --screenshot=.github/social-preview.png \
  "file://$PWD/.github/social-preview.html"
```
