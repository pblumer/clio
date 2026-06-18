"use strict";
const $ = (id) => document.getElementById(id);
const tokenInput = $("token"), intervalSel = $("interval");
let timer = null, lastReqTotal = null, lastReqAt = null;

tokenInput.value = sessionStorage.getItem("clioToken") || "";

function setStatus(state, text) {
  $("dot").className = "dot" + (state ? " " + state : "");
  $("statusText").textContent = text;
}
function fmtInt(n) { return (n == null || isNaN(n)) ? "–" : Number(n).toLocaleString("de-CH"); }
// fmtCompact kürzt große Zahlen lesbar (k / Mio / Mrd) — für Schätzungen.
function fmtCompact(n) {
  if (n == null || isNaN(n)) return "–";
  if (n >= 1e9) return (n / 1e9).toFixed(1).replace(/\.0$/, "") + " Mrd";
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, "") + " Mio";
  if (n >= 1e4) return (n / 1e3).toFixed(0) + "k";
  return fmtInt(n);
}
function fmtBytes(b) {
  if (b == null || b < 0) return "–";
  const u = ["B", "KiB", "MiB", "GiB", "TiB"]; let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return (i === 0 ? b : b.toFixed(1)) + " " + u[i];
}
function fmtDuration(s) {
  if (s == null || s < 0) return "–";
  s = Math.floor(s);
  const d = Math.floor(s / 86400); s %= 86400;
  const h = Math.floor(s / 3600); s %= 3600;
  const m = Math.floor(s / 60); const sec = s % 60;
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m ${sec}s`;
  if (m > 0) return `${m}m ${sec}s`;
  return `${sec}s`;
}
function fmtMs(sec) {
  if (sec == null) return "–";
  const ms = sec * 1000;
  return ms < 10 ? ms.toFixed(2) + " ms" : ms < 1000 ? ms.toFixed(1) + " ms" : (ms / 1000).toFixed(2) + " s";
}

// ---------- Tabs ----------
function switchView(name) {
  for (const v of ["dashboard", "live", "explorer", "generate", "query", "keys", "help"]) {
    $("view-" + v).classList.toggle("active", v === name);
    $("tab-" + v).classList.toggle("active", v === name);
  }
  // Keys: beim ersten Öffnen die Liste laden, sofern ein Token vorliegt.
  if (name === "keys" && !keysLoaded && tokenInput.value.trim()) loadKeys();
  // Explorer beim ersten Öffnen automatisch laden, sofern ein Token vorliegt.
  if (name === "explorer" && !explorerLoaded && tokenInput.value.trim()) loadExplorer();
  // Erzeugen: Vorschlagslisten beim ersten Öffnen befüllen.
  if (name === "generate" && !genLoaded && tokenInput.value.trim()) { genLoaded = true; loadGenSuggestions(); }
  // Query-Tab: Feld-Vorschläge beim ersten Öffnen aus echten Events lernen.
  if (name === "query") { qEditor && qEditor.focus(); if (!qSampleLoaded && tokenInput.value.trim()) refreshQuerySchema(); }
  // Dashboard: Canvas nach erneutem Einblenden frisch vermessen.
  if (name === "dashboard") { evResize(); redrawSparks(); }
}
document.querySelectorAll("nav.tabs button").forEach((b) =>
  b.addEventListener("click", () => switchView(b.dataset.view)));

// ---------- Dashboard ----------
function parseProm(text) {
  const out = {};
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line || line[0] === "#") continue;
    const sp = line.lastIndexOf(" ");
    if (sp < 0) continue;
    const left = line.slice(0, sp), value = parseFloat(line.slice(sp + 1));
    const brace = left.indexOf("{");
    const name = brace < 0 ? left : left.slice(0, brace);
    const labels = {};
    if (brace >= 0) {
      const inner = left.slice(brace + 1, left.lastIndexOf("}"));
      for (const m of inner.matchAll(/(\w+)="([^"]*)"/g)) labels[m[1]] = m[2];
    }
    (out[name] ||= []).push({ labels, value });
  }
  return out;
}
function gauge(m, name) { return m[name] && m[name].length ? m[name][0].value : null; }
function sum(m, name) { return (m[name] || []).reduce((a, s) => a + s.value, 0); }

function quantile(buckets, count, q) {
  if (!buckets || !buckets.length || !count) return null;
  const sorted = buckets
    .map((b) => ({ le: b.labels.le === "+Inf" ? Infinity : parseFloat(b.labels.le), c: b.value }))
    .sort((a, b) => a.le - b.le);
  const target = q * count;
  let prevLe = 0, prevC = 0;
  for (const b of sorted) {
    if (b.c >= target) {
      if (b.le === Infinity) return prevLe;
      const span = b.c - prevC;
      if (span <= 0) return b.le;
      return prevLe + (b.le - prevLe) * ((target - prevC) / span);
    }
    prevLe = b.le; prevC = b.c;
  }
  return prevLe;
}

async function refresh() {
  const token = tokenInput.value.trim();
  if (!token) { setStatus("err", "Token fehlt"); return; }
  try {
    const headers = { Authorization: "Bearer " + token };
    const [infoRes, metricsRes] = await Promise.all([
      fetch("/api/v1/info", { headers, cache: "no-store" }),
      fetch("/metrics", { cache: "no-store" }),
    ]);
    if (infoRes.status === 401) { setStatus("err", "Token ungültig (401)"); markNotice("Token ungültig — Zugriff verweigert (401).", true); return; }
    if (!infoRes.ok) throw new Error("info HTTP " + infoRes.status);
    if (!metricsRes.ok) throw new Error("metrics HTTP " + metricsRes.status);

    const info = await infoRes.json();
    const m = parseProm(await metricsRes.text());
    render(info, m);
    if (!live.active) setStatus("ok", "verbunden");
    clearNotice();
    $("lastUpdate").textContent = "aktualisiert " + new Date().toLocaleTimeString("de-CH");
  } catch (e) {
    if (!live.active) setStatus("err", "Fehler");
    markNotice("Verbindung fehlgeschlagen: " + e.message, true);
  }
}

// ---------- Telemetrie-Charts (Canvas, ohne Abhängigkeiten) ----------
const SPARK_LEN = 60;
const tel = { cpu: [], mem: [], thr: [], req: [], prevCpu: null, prevEvents: null, prevReq: null, prevAt: null };
function pushBuf(a, v) { a.push(v); if (a.length > SPARK_LEN) a.shift(); }
function drawSpark(canvas, data, color) {
  if (!canvas) return;
  const dpr = window.devicePixelRatio || 1;
  const cssW = canvas.clientWidth || 230, cssH = canvas.clientHeight || 56;
  if (canvas.width !== Math.round(cssW * dpr) || canvas.height !== Math.round(cssH * dpr)) {
    canvas.width = Math.round(cssW * dpr); canvas.height = Math.round(cssH * dpr);
  }
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, cssW, cssH);
  ctx.strokeStyle = "rgba(120,150,220,.10)"; ctx.lineWidth = 1;
  for (let g = 0; g <= 2; g++) { const y = (cssH - 2) * g / 2 + 1; ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(cssW, y); ctx.stroke(); }
  if (data.length < 2) return;
  const max = Math.max(...data), min = Math.min(...data, 0), span = (max - min) || 1, pad = 4;
  const X = (i) => i / (data.length - 1) * cssW;
  const Y = (v) => cssH - pad - (v - min) / span * (cssH - 2 * pad);
  const grad = ctx.createLinearGradient(0, 0, 0, cssH);
  grad.addColorStop(0, color + "55"); grad.addColorStop(1, color + "00");
  ctx.beginPath(); ctx.moveTo(0, cssH);
  data.forEach((v, i) => ctx.lineTo(X(i), Y(v)));
  ctx.lineTo(cssW, cssH); ctx.closePath(); ctx.fillStyle = grad; ctx.fill();
  ctx.beginPath(); data.forEach((v, i) => i ? ctx.lineTo(X(i), Y(v)) : ctx.moveTo(X(i), Y(v)));
  ctx.strokeStyle = color; ctx.lineWidth = 1.8; ctx.shadowColor = color; ctx.shadowBlur = 8; ctx.stroke();
  const lx = X(data.length - 1), ly = Y(data[data.length - 1]);
  ctx.beginPath(); ctx.arc(lx, ly, 2.6, 0, Math.PI * 2); ctx.fillStyle = color; ctx.fill(); ctx.shadowBlur = 0;
}
function redrawSparks() {
  drawSpark($("spark-cpu"), tel.cpu, "#22d3ee"); drawSpark($("spark-mem"), tel.mem, "#f472b6");
  drawSpark($("spark-thr"), tel.thr, "#34f5c5"); drawSpark($("spark-req"), tel.req, "#a78bfa");
}
function updateTelemetry(info, m, now) {
  const cpuSecs = gauge(m, "clio_process_cpu_seconds_total");
  const numCpu = gauge(m, "clio_num_cpu") || 1;
  const heap = gauge(m, "clio_memory_heap_bytes");
  const written = sum(m, "clio_events_written_total");
  const reqTotal = sum(m, "clio_http_requests_total");
  const dt = tel.prevAt != null ? (now - tel.prevAt) / 1000 : 0;

  if (cpuSecs != null) {
    $("t-cpu-unit").textContent = "% · " + numCpu + " Kerne";
    if (tel.prevCpu != null && dt > 0) {
      const pct = Math.max(0, (cpuSecs - tel.prevCpu) / dt / numCpu * 100);
      pushBuf(tel.cpu, pct); $("t-cpu").textContent = pct.toFixed(1);
    }
    tel.prevCpu = cpuSecs;
  } else { $("t-cpu").textContent = "n/a"; $("t-cpu-unit").textContent = "nicht verfügbar"; }

  if (heap != null) { const mib = heap / 1048576; pushBuf(tel.mem, mib); $("t-mem").textContent = mib.toFixed(1); }

  if (tel.prevEvents != null && dt > 0) {
    const thr = Math.max(0, (written - tel.prevEvents) / dt);
    pushBuf(tel.thr, thr); $("t-thr").textContent = thr.toFixed(thr < 10 ? 2 : 0);
  }
  tel.prevEvents = written;

  if (tel.prevReq != null && dt > 0) {
    const rr = Math.max(0, (reqTotal - tel.prevReq) / dt);
    pushBuf(tel.req, rr); $("t-req").textContent = rr.toFixed(rr < 10 ? 2 : 0);
  }
  tel.prevReq = reqTotal;

  tel.prevAt = now;
  redrawSparks();
}
window.addEventListener("resize", redrawSparks);

// ---------- Eventstrom (Diagramm seit Serverstart + Live-Liste) ----------
// Zwei entkoppelte Quellen:
//  - Das Zeitachsen-Diagramm liest GET /api/v1/event-stats (serverseitiges
//    Histogramm der Eventmengen SEIT SERVERSTART) — gepollt, ohne die Historie
//    zu streamen.
//  - Die einklappbare Live-Liste hängt an einem observe-Stream auf "/" (rekursiv)
//    mit lowerBound = höchste ID + 1, liefert also NUR NEUE Events ab dem Verbinden.
const estream = {
  active: false, ctrl: null,
  connected: false,              // läuft gerade eine offene Leseschleife?
  lastId: null,                  // höchste gesehene Event-ID (für lückenlosen Reconnect)
  retry: 0, reconnectT: null,    // Backoff-Zähler und Reconnect-Timer
  count: 0,                      // neue Events seit Verbinden (für die Live-Liste)
  mode: "rate",                  // "rate" = Events/Abschnitt, "cum" = kumuliert
  yscale: "lin",                 // "lin" oder "log" (y-Achse)
  split: "total",                // "total" = eine Linie, "source" = nach source aufgeschlüsselt
  srender: "stacked",            // Source-Modus: "stacked" | "area" (überlagert) | "lines"
  trender: "area",               // Gesamt-Modus: "area" (gefüllt) | "line"
  smooth: false,                 // gleitender Mittelwert (glättet das Rate-Zappeln)
  cv: null, ctx: null, w: 0, h: 0, dpr: 1,
  stats: null,                   // Gesamt-Histogramm (Envelope) aus /api/v1/event-stats
  series: null,                  // [{key,color,counts,total}] im Source-Modus (Top-N + „andere")
  hidden: new Set(),             // im Source-Modus per Legende ausgeblendete source-keys
  statsAt: 0,                    // letzter Abruf (ms) — drosselt Live-Refetch
  zoom: null,                    // {x0,x1 (ms), y0,y1 (Werte)} — gewählter Ausschnitt
  sel: null,                     // {x0,y0,x1,y1} Pixel — Auswahl-Rechteck beim Ziehen
  geom: null,                    // zuletzt gezeichnete Geometrie (für Maus-Inversion)
  redrawPending: false,
  mode: "stream",                // "stream" (observe) oder "poll" (Fallback bei puffernden Proxies)
  probeT: null,                  // Timer der Proxy-Puffer-Erkennung
};
const DASH_MAX_ROWS = 200;

function evResize() {
  const cv = estream.cv; if (!cv) return;
  estream.dpr = window.devicePixelRatio || 1;
  estream.w = cv.clientWidth || 600; estream.h = cv.clientHeight || 170;
  cv.width = Math.round(estream.w * estream.dpr); cv.height = Math.round(estream.h * estream.dpr);
  drawEvChart();
}
function evClock(ms) { return new Date(ms).toLocaleTimeString("de-CH"); }

function drawEvChart() {
  const ctx = estream.ctx; if (!ctx) return;
  const dpr = estream.dpr, W = estream.w, H = estream.h;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, W, H);
  const padL = 46, padR = 12, padT = 12, padB = 22;
  const plotW = Math.max(1, W - padL - padR), plotH = Math.max(1, H - padT - padB);

  // Grid
  ctx.strokeStyle = "rgba(34,211,238,.06)"; ctx.lineWidth = 1;
  for (let i = 0; i <= 6; i++) { const gx = padL + plotW * i / 6; ctx.beginPath(); ctx.moveTo(gx, padT); ctx.lineTo(gx, padT + plotH); ctx.stroke(); }
  for (let i = 0; i <= 4; i++) { const gy = padT + plotH * i / 4; ctx.beginPath(); ctx.moveTo(padL, gy); ctx.lineTo(padL + plotW, gy); ctx.stroke(); }

  const st = estream.stats;
  const buckets = st && st.counts ? st.counts : [];
  const N = buckets.length;
  const startMs = st ? st.startMs : Date.now();
  const bucketMs = st && st.bucketSeconds ? st.bucketSeconds * 1000 : 1000;
  const fullEndMs = st ? (st.serverTimeMs || startMs + N * bucketMs) : Date.now();

  const tot = $("ev-total"); if (tot) tot.textContent = st ? fmtInt(st.total) : "–";
  const peak = $("ev-peak");
  const reset = $("ev-reset"); if (reset) reset.style.display = estream.zoom ? "" : "none";

  if (N === 0) {
    ctx.fillStyle = "#8b93a7"; ctx.font = "10px ui-monospace, Menlo, monospace";
    ctx.textAlign = "center"; ctx.textBaseline = "middle";
    ctx.fillText(st ? "noch keine Events" : "verbinden, um Eventmengen zu sehen", padL + plotW / 2, padT + plotH / 2);
    ctx.textAlign = "left"; ctx.textBaseline = "alphabetic"; ctx.fillText(evClock(startMs), padL, H - 6);
    if (peak) peak.textContent = "–";
    estream.geom = null;
    return;
  }

  // Zeit der Bucket-Mitte je Index.
  const times = new Array(N);
  for (let i = 0; i < N; i++) times[i] = startMs + (i + 0.5) * bucketMs;

  // Serien bauen: im Source-Modus die vorbereiteten Top-N + „andere", sonst der
  // Gesamtstrom als eine Serie. Rendering: Source-Modus "stacked"|"area"|"lines",
  // Gesamt-Modus "area" (gefüllt) | "lines" (reine Linie). Nur "stacked" summiert
  // die Serien für die y-Skala; "area"/"lines" überlagern → Einzel-Maximum.
  const srcMode = estream.split === "source" && estream.series && estream.series.length > 0;
  const render = srcMode ? estream.srender : (estream.trender === "line" ? "lines" : "area");
  const stackedScale = render === "stacked";
  const seriesList = srcMode
    ? estream.series.filter((s) => !estream.hidden.has(s.key)).map((s) => ({ name: s.key, color: s.color, vals: bucketVals(s.counts, N, estream.mode) }))
    : [{ name: "Gesamt", color: "#34f5c5", vals: bucketVals(buckets, N, estream.mode) }];

  // y-Transform: bei Log werden Werte als log10(v+1) abgebildet (0 bleibt 0),
  // damit ein stetiger kleiner Strom neben hohen Spitzen sichtbar wird. Die
  // y-Domäne (yd0/yd1) und der Zoom leben durchgehend in DIESEM transformierten
  // Raum; Achsenbeschriftung/Peak rechnen per evInv in echte Werte zurück.
  // Ohne Zoom: volle Zeit, y von 0 bis sichtbares Maximum (bei „stacked" die Summe
  // der Serien je Bucket, bei Linien das größte Einzel-Maximum).
  const z = estream.zoom;
  const xd0 = z ? z.x0 : startMs;
  const xd1 = z ? z.x1 : fullEndMs;
  let yd0, yd1;
  if (z) { yd0 = z.y0; yd1 = z.y1; }
  else {
    let maxV = 0;
    for (let i = 0; i < N; i++) {
      if (times[i] < xd0 || times[i] > xd1) continue;
      if (stackedScale) { let sum = 0; for (const s of seriesList) sum += s.vals[i]; if (sum > maxV) maxV = sum; }
      else { for (const s of seriesList) if (s.vals[i] > maxV) maxV = s.vals[i]; }
    }
    yd0 = 0; yd1 = evTf(Math.max(1, maxV));
  }
  const xSpan = Math.max(1, xd1 - xd0), ySpan = Math.max(1e-9, yd1 - yd0);
  const X = (t) => padL + (t - xd0) / xSpan * plotW;
  const Y = (v) => padT + plotH - (evTf(v) - yd0) / ySpan * plotH;

  // Auf den Plotbereich clippen (gezoomte Werte liegen außerhalb).
  ctx.save();
  ctx.beginPath(); ctx.rect(padL, padT, plotW, plotH); ctx.clip();
  if (render === "stacked") {
    // Gestapelte Flächen: je Serie das Band zwischen laufender Unter- und
    // Oberkante. Größte Serie zuerst (= unten). Im Gesamt-Modus genau eine Fläche.
    const single = seriesList.length === 1;
    const bottom = new Array(N).fill(0);
    for (const s of seriesList) {
      ctx.beginPath();
      for (let i = 0; i < N; i++) { const x = X(times[i]), y = Y(bottom[i] + s.vals[i]); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); }
      for (let i = N - 1; i >= 0; i--) ctx.lineTo(X(times[i]), Y(bottom[i]));
      ctx.closePath();
      ctx.fillStyle = hexA(s.color, single ? 0.10 : 0.30); ctx.fill();
      ctx.beginPath();
      for (let i = 0; i < N; i++) { const x = X(times[i]), y = Y(bottom[i] + s.vals[i]); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); }
      ctx.lineWidth = single ? 2 : 1.2; ctx.strokeStyle = s.color; ctx.shadowColor = s.color; ctx.shadowBlur = single ? 8 : 4; ctx.lineJoin = "round"; ctx.stroke(); ctx.shadowBlur = 0;
      for (let i = 0; i < N; i++) bottom[i] += s.vals[i];
    }
  } else if (render === "area") {
    // Überlagerte Flächen: je Serie eine bis zur Grundlinie gefüllte, halb-
    // transparente Fläche (nicht gestapelt). Größte zuerst → kleinere liegen
    // sichtbar darüber. Im Gesamt-Modus genau eine (kräftiger gefüllte) Fläche.
    const single = seriesList.length === 1;
    const y0px = Y(0);
    for (const s of seriesList) {
      ctx.beginPath();
      for (let i = 0; i < N; i++) { const x = X(times[i]), y = Y(s.vals[i]); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); }
      ctx.lineTo(X(times[N - 1]), y0px); ctx.lineTo(X(times[0]), y0px); ctx.closePath();
      ctx.fillStyle = hexA(s.color, single ? 0.10 : 0.18); ctx.fill();
      ctx.beginPath();
      for (let i = 0; i < N; i++) { const x = X(times[i]), y = Y(s.vals[i]); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); }
      ctx.lineWidth = single ? 2 : 1.4; ctx.strokeStyle = s.color; ctx.shadowColor = s.color; ctx.shadowBlur = single ? 8 : 4; ctx.lineJoin = "round"; ctx.stroke(); ctx.shadowBlur = 0;
    }
  } else {
    // Überlagerte Linien: je Serie eine Linie (Vergleich absoluter Raten).
    for (const s of seriesList) {
      ctx.beginPath();
      for (let i = 0; i < N; i++) { const x = X(times[i]), y = Y(s.vals[i]); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); }
      ctx.lineWidth = 1.8; ctx.strokeStyle = s.color; ctx.shadowColor = s.color; ctx.shadowBlur = 6; ctx.lineJoin = "round"; ctx.stroke(); ctx.shadowBlur = 0;
    }
  }
  ctx.restore();

  // Achsenbeschriftung
  ctx.fillStyle = "#8b93a7"; ctx.font = "10px ui-monospace, Menlo, monospace";
  ctx.textAlign = "right"; ctx.textBaseline = "middle";
  ctx.fillText(fmtInt(Math.round(evInv(yd1))), padL - 6, padT + 6); ctx.fillText(fmtInt(Math.round(evInv(yd0))), padL - 6, padT + plotH);
  ctx.textBaseline = "alphabetic";
  ctx.textAlign = "left"; ctx.fillText(evClock(xd0), padL, H - 6);
  ctx.textAlign = "right"; ctx.fillText(evClock(xd1), padL + plotW, H - 6);

  // Auswahl-Rechteck beim Ziehen.
  if (estream.sel) {
    const s = estream.sel;
    const rx = Math.min(s.x0, s.x1), ry = Math.min(s.y0, s.y1), rw = Math.abs(s.x1 - s.x0), rh = Math.abs(s.y1 - s.y0);
    ctx.fillStyle = "rgba(110,168,254,.18)"; ctx.strokeStyle = "rgba(110,168,254,.85)"; ctx.lineWidth = 1;
    ctx.fillRect(rx, ry, rw, rh); ctx.strokeRect(rx + 0.5, ry + 0.5, rw, rh);
  }

  estream.geom = { padL, padT, plotW, plotH, xd0, xd1, yd0, yd1 };
  if (peak) {
    const base = estream.mode === "cum" ? "kumuliert" : "max " + fmtInt(Math.round(evInv(yd1))) + " / Abschnitt · " + Math.round(bucketMs / 1000) + "s";
    peak.textContent = base + (estream.yscale === "log" ? " · log" : "") + (estream.smooth && estream.mode !== "cum" ? " · geglättet" : "") + (z ? " · 🔍 gezoomt (Esc)" : " · ziehen zum Zoomen");
  }
}

// fetchEventStats holt das serverseitige Histogramm (seit Serverstart) und
// zeichnet das Diagramm neu. Best effort.
async function fetchEventStats() {
  const token = tokenInput.value.trim(); if (!token) return;
  estream.statsAt = Date.now();
  const bySource = estream.split === "source";
  try {
    const r = await fetch("/api/v1/event-stats" + (bySource ? "?by=source" : ""), { headers: { Authorization: "Bearer " + token }, cache: "no-store" });
    if (!r.ok) return;
    const j = await r.json();
    estream.stats = {
      startMs: Date.parse(j.start),
      bucketSeconds: j.bucketSeconds,
      counts: j.counts || [],
      total: j.total || 0,
      serverTimeMs: j.serverTime ? Date.parse(j.serverTime) : Date.now(),
    };
    estream.series = bySource ? buildSourceSeries(j.sources || {}) : null;
    renderEvLegend();
    drawEvChart();
  } catch (_) {}
}

// Farbpalette für Source-Serien (HUD-Neon); „andere" bekommt ein gedämpftes Grau.
const EV_PALETTE = ["#34f5c5", "#6ea8fe", "#f472b6", "#fbbf24", "#a78bfa", "#4ade80", "#fb7185", "#22d3ee"];
const EV_OTHER_COLOR = "#7c8aa5";
const EV_TOPN = 8;

// bucketVals macht aus den Roh-Counts die darzustellenden Werte je Bucket
// (Rate = Count, kumuliert = laufende Summe).
function bucketVals(counts, N, mode) {
  const v = new Array(N); let cum = 0;
  for (let i = 0; i < N; i++) { const c = (counts && counts[i]) || 0; cum += c; v[i] = mode === "cum" ? cum : c; }
  return maybeSmooth(v);
}

// maybeSmooth legt bei aktiver Glättung einen zentrierten gleitenden Mittelwert
// (Fensterbreite EV_SMOOTH_W) über die Werte — macht aus dem Rate-Zappeln einen
// lesbaren Trend. Im kumulierten Modus sinnlos (bereits monoton) → unverändert.
const EV_SMOOTH_W = 5;
function maybeSmooth(v) {
  if (!estream.smooth || estream.mode === "cum") return v;
  const n = v.length, out = new Array(n), half = (EV_SMOOTH_W - 1) / 2;
  for (let i = 0; i < n; i++) {
    let sum = 0, cnt = 0;
    for (let j = Math.max(0, i - half); j <= Math.min(n - 1, i + half); j++) { sum += v[j]; cnt++; }
    out[i] = sum / cnt;
  }
  return out;
}

// hexA wandelt "#rrggbb" + Alpha in eine rgba()-Farbe.
function hexA(hex, a) {
  const n = parseInt(hex.replace("#", ""), 16);
  return "rgba(" + ((n >> 16) & 255) + "," + ((n >> 8) & 255) + "," + (n & 255) + "," + a + ")";
}

// buildSourceSeries macht aus dem {source: counts[]}-Objekt eine nach Menge
// sortierte Serienliste: die Top-N behalten ihre Farbe, der Rest wird (mit einer
// evtl. schon vom Server gelieferten „andere"-Serie) zu einer „andere"-Serie
// zusammengefasst, damit das Diagramm lesbar und der Stapel = Gesamtstrom bleibt.
function buildSourceSeries(sources) {
  const arr = Object.keys(sources).map((k) => {
    const counts = sources[k] || [];
    let total = 0; for (const c of counts) total += c;
    return { key: k, counts, total };
  });
  arr.sort((a, b) => (b.total - a.total) || (a.key < b.key ? -1 : 1));

  const out = arr.slice(0, EV_TOPN).map((s, i) => ({
    key: s.key, counts: s.counts.slice(), total: s.total,
    color: s.key === "andere" ? EV_OTHER_COLOR : EV_PALETTE[i % EV_PALETTE.length],
  }));

  // Abgeschnittenen Rest in „andere" bündeln (ggf. mit vorhandener „andere"-Serie).
  const rest = arr.slice(EV_TOPN);
  if (rest.length) {
    let other = out.find((s) => s.key === "andere");
    if (!other) { other = { key: "andere", counts: [], total: 0, color: EV_OTHER_COLOR }; out.push(other); }
    for (const s of rest) {
      for (let i = 0; i < s.counts.length; i++) other.counts[i] = (other.counts[i] || 0) + s.counts[i];
      other.total += s.total;
    }
  }
  return out;
}

// renderEvLegend füllt die Farb-Legende (nur im Source-Modus). Jeder Eintrag ist
// ein Button: Klick blendet die Source im Diagramm aus/ein (estream.hidden).
// Source-Werte stammen aus Event-Daten → bewusst per textContent gesetzt.
function renderEvLegend() {
  const el = $("ev-legend"); if (!el) return;
  el.textContent = "";
  if (estream.split !== "source" || !estream.series || !estream.series.length) { el.classList.remove("on"); return; }
  el.classList.add("on");
  // Ausgeblendete Keys aufräumen, die es nicht mehr gibt (z. B. nach Top-N-Wechsel).
  const live = new Set(estream.series.map((s) => s.key));
  for (const k of [...estream.hidden]) if (!live.has(k)) estream.hidden.delete(k);
  for (const s of estream.series) {
    const off = estream.hidden.has(s.key);
    const item = document.createElement("button"); item.type = "button";
    item.className = "lg" + (off ? " off" : ""); item.style.color = s.color;
    item.setAttribute("aria-pressed", off ? "false" : "true");
    item.title = (off ? "einblenden: " : "ausblenden: ") + s.key;
    const sw = document.createElement("span"); sw.className = "sw"; item.appendChild(sw);
    const name = document.createElement("span"); name.className = "lg-name"; name.textContent = s.key; item.appendChild(name);
    const n = document.createElement("span"); n.className = "lg-n"; n.textContent = fmtInt(s.total); item.appendChild(n);
    item.addEventListener("click", () => { toggleSourceHidden(s.key); });
    el.appendChild(item);
  }
}

// toggleSourceHidden blendet eine Source im Diagramm aus/ein und zeichnet neu.
function toggleSourceHidden(key) {
  if (estream.hidden.has(key)) estream.hidden.delete(key); else estream.hidden.add(key);
  estream.zoom = null;   // Wertebereich neu autoskalieren, sonst „springt" die y-Achse
  renderEvLegend();
  drawEvChart();
}
// Bei Live-Events das Diagramm gedrosselt nachziehen (auch ohne Auto-Refresh).
function maybeFetchStats() { if (Date.now() - estream.statsAt > 1500) fetchEventStats(); }

function dashAppendEvent(ev) {
  const host = $("dash-events"); if (!host) return;
  if (estream.count === 1) host.innerHTML = "";
  host.insertBefore(renderEvent(ev), host.firstChild);
  while (host.children.length > DASH_MAX_ROWS) host.removeChild(host.lastChild);
  const c = $("ev-live-count"); if (c) c.textContent = fmtInt(estream.count) + " Events";
}
function estreamHandle(ev) {
  const id = Number(ev.id);
  if (Number.isFinite(id) && (estream.lastId == null || id > estream.lastId)) estream.lastId = id;
  estream.count++;
  dashAppendEvent(ev);   // Live-Liste (nur neue Events)
  maybeFetchStats();     // Diagramm gedrosselt nachziehen
}

function stopEventStream() {
  estream.active = false; estream.connected = false; estream.mode = "stream";
  if (estream.probeT) { clearTimeout(estream.probeT); estream.probeT = null; }
  if (estream.reconnectT) { clearTimeout(estream.reconnectT); estream.reconnectT = null; }
  if (estream.ctrl) { estream.ctrl.abort(); estream.ctrl = null; }
}

// startEventStream startet den Dashboard-Live-Ticker frisch (nur neue Events ab
// jetzt) und hält ihn anschließend über runEventStream offen — inkl. Reconnect.
async function startEventStream() {
  const token = tokenInput.value.trim();
  if (!token) return;
  stopEventStream();
  estream.active = true;
  estream.count = 0; estream.lastId = null; estream.retry = 0;
  fetchEventStats(); // Diagramm sofort (seit Serverstart) befüllen
  $("dash-events").innerHTML = '<div class="empty">verbinde …</div>';
  // Nur NEUE Events: lowerBound hinter die aktuell höchste Event-ID setzen.
  // Event-IDs sind global monoton ab 1, daher entspricht die höchste ID der
  // Gesamtzahl (eventsTotal aus /api/v1/info). So liefert observe keine History.
  try {
    const r = await fetch("/api/v1/info", { headers: { Authorization: "Bearer " + token }, cache: "no-store" });
    if (r.ok) { const j = await r.json(); const tot = Number(j.eventsTotal); if (Number.isFinite(tot)) estream.lastId = tot; }
  } catch (_) {}
  if (!estream.active) return;
  runEventStream(token);
}

// STREAM_PROBE_MS: Kommt nach dem Öffnen eines observe-Streams binnen dieser Zeit
// kein einziges Byte (auch nicht die sofortige Verbindungs-Quittung), puffert ein
// Proxy/Gateway den Stream — dann automatisch auf Polling umschalten. LIVE_POLL_MS
// ist das Poll-Intervall im Fallback (kurze, abgeschlossene read-Requests, die auch
// hinter puffernden Gateways durchkommen).
const STREAM_PROBE_MS = 6000;
const LIVE_POLL_MS = 3000;
const evSleep = (ms) => new Promise((r) => setTimeout(r, ms));

// runEventStream hält die Observe-Verbindung und baut sie nach Abriss (EOF wegen
// Broker-Drop, oder Netzwerkfehler) automatisch wieder auf. lowerBound =
// höchste gesehene ID + 1 setzt lückenlos und ohne Dubletten fort — genau der
// im Broker dokumentierte Reconnect, den das Dashboard bisher nicht umsetzte.
async function runEventStream(token) {
  // Vorherige Verbindung sicher schließen, bevor eine neue geöffnet wird —
  // sonst leakt bei einem erneuten Aufruf (z. B. visibilitychange) die alte
  // Observe-Verbindung und belegt einen der ~6 Browser-Connection-Slots.
  if (estream.ctrl) estream.ctrl.abort();
  const ctrl = new AbortController(); estream.ctrl = ctrl;
  const lower = estream.lastId != null ? estream.lastId + 1 : null;
  // Proxy-Puffer-Erkennung: bleibt das erste Byte aus, auf Polling umschalten.
  let gotData = false;
  clearTimeout(estream.probeT);
  estream.probeT = setTimeout(() => {
    if (gotData || !estream.active || estream.ctrl !== ctrl) return;
    ctrl.abort();
    fallbackEventPolling(token);
  }, STREAM_PROBE_MS);
  try {
    const url = "/api/v1/events?watch=true&recursive=true" + (lower != null ? "&lowerBound=" + lower : "");
    const res = await fetch(url, {
      headers: { Authorization: "Bearer " + token }, signal: ctrl.signal, cache: "no-store",
    });
    if (ctrl.signal.aborted) return; // zwischenzeitlich durch einen neueren Lauf ersetzt
    if (res.status === 401) { clearTimeout(estream.probeT); stopEventStream(); $("dash-events").innerHTML = '<div class="empty">Token ungültig (401).</div>'; return; }
    if (!res.ok || !res.body) throw new Error("HTTP " + res.status);
    estream.connected = true; estream.retry = 0; // erfolgreiche Verbindung -> Backoff zurücksetzen
    if (estream.count === 0) $("dash-events").innerHTML = '<div class="empty">wartet auf neue Events …</div>';
    const reader = res.body.getReader(); const dec = new TextDecoder(); let buf = "";
    for (;;) {
      const { done, value } = await reader.read(); if (done) break;
      if (!gotData) { gotData = true; clearTimeout(estream.probeT); } // Stream liefert -> Erkennung bestanden
      buf += dec.decode(value, { stream: true });
      let nl;
      while ((nl = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, nl); buf = buf.slice(nl + 1);
        if (line.trim()) { try { estreamHandle(JSON.parse(line)); } catch (_) {} }
      }
    }
  } catch (e) {
    if (e.name === "AbortError" || !estream.active) return; // manuell gestoppt/ersetzt
  } finally {
    clearTimeout(estream.probeT);
    if (estream.ctrl === ctrl) estream.connected = false;
  }
  // Nur der jeweils aktuelle Lauf plant einen Reconnect (kein Timer-Stapeln);
  // im Polling-Modus übernimmt die Poll-Schleife selbst.
  if (estream.active && estream.ctrl === ctrl && estream.mode === "stream") scheduleEventReconnect(token);
}

// fallbackEventPolling schaltet den Dashboard-Ticker dauerhaft (bis Stop/Neustart)
// auf Polling um — für Umgebungen, in denen ein Gateway den Stream puffert.
function fallbackEventPolling(token) {
  if (estream.mode === "poll" || !estream.active) return;
  estream.mode = "poll"; estream.connected = false;
  if (estream.count === 0) $("dash-events").innerHTML = '<div class="empty">Live-Stream wird geblockt — Polling aktiv, warte auf Events …</div>';
  pollEventStream(token);
}

// pollEventStream fragt periodisch nur die Events ab lastId+1 ab (endlicher
// read-Request, ohne watch) und verarbeitet sie wie der Stream.
async function pollEventStream(token) {
  while (estream.active && estream.mode === "poll") {
    try {
      const lower = estream.lastId != null ? estream.lastId + 1 : null;
      const url = "/api/v1/events?recursive=true" + (lower != null ? "&lowerBound=" + lower : "");
      const res = await fetch(url, { headers: { Authorization: "Bearer " + token }, cache: "no-store" });
      if (res.status === 401) { stopEventStream(); $("dash-events").innerHTML = '<div class="empty">Token ungültig (401).</div>'; return; }
      if (res.ok) {
        estream.connected = true; estream.retry = 0;
        const text = await res.text();
        for (const line of text.split("\n")) if (line.trim()) { try { estreamHandle(JSON.parse(line)); } catch (_) {} }
      }
    } catch (_) { estream.connected = false; }
    await evSleep(LIVE_POLL_MS);
  }
}

function scheduleEventReconnect(token) {
  estream.retry = Math.min(estream.retry + 1, 6);
  const delay = Math.min(1000 * 2 ** (estream.retry - 1), 15000); // 1s,2s,4s,…,15s
  if (estream.count === 0) {
    $("dash-events").innerHTML = '<div class="empty">Verbindung unterbrochen — neuer Versuch in ' + Math.round(delay / 1000) + ' s …</div>';
  }
  if (estream.reconnectT) clearTimeout(estream.reconnectT);
  estream.reconnectT = setTimeout(() => { if (estream.active) runEventStream(token); }, delay);
}

// evTf/evInv transformieren Werte für die y-Achse (Identität bei "lin",
// log10(v+1) bei "log" — 0 bleibt 0, große Spitzen werden gestaucht).
function evTf(v) { return estream.yscale === "log" ? Math.log10(v + 1) : v; }
function evInv(u) { return estream.yscale === "log" ? Math.pow(10, u) - 1 : u; }

function setEvMode(mode) {
  estream.mode = mode;
  estream.zoom = null; // Rate/kumuliert haben andere y-Skalen -> Zoom verwerfen
  $("ev-mode-rate").classList.toggle("active", mode === "rate");
  $("ev-mode-cum").classList.toggle("active", mode === "cum");
  drawEvChart();
}
function setEvScale(scale) {
  estream.yscale = scale;
  estream.zoom = null; // y-Domäne wechselt den Raum -> Zoom verwerfen
  $("ev-scale-lin").classList.toggle("active", scale === "lin");
  $("ev-scale-log").classList.toggle("active", scale === "log");
  drawEvChart();
}
function setEvSplit(split) {
  estream.split = split;
  estream.zoom = null; // andere Daten/y-Skala -> Zoom verwerfen
  $("ev-split-total").classList.toggle("active", split === "total");
  $("ev-split-source").classList.toggle("active", split === "source");
  const src = split === "source";
  // Render-Umschalter je nach Modus: Source -> gestapelt/Fläche/Linien, Gesamt -> Fläche/Linie.
  for (const id of ["ev-srender-stacked", "ev-srender-area", "ev-srender-lines"]) $(id).style.display = src ? "" : "none";
  for (const id of ["ev-trender-area", "ev-trender-line"]) $(id).style.display = src ? "none" : "";
  renderEvLegend();
  fetchEventStats(); // anderes Endpoint (?by=source) -> neu laden, zeichnet neu
}
function setEvSrender(r) {
  estream.srender = r;
  estream.zoom = null; // gestapelt vs. Fläche/Linien haben andere y-Skalen
  $("ev-srender-stacked").classList.toggle("active", r === "stacked");
  $("ev-srender-area").classList.toggle("active", r === "area");
  $("ev-srender-lines").classList.toggle("active", r === "lines");
  drawEvChart();
}
function setEvTrender(r) {
  estream.trender = r;
  $("ev-trender-area").classList.toggle("active", r === "area");
  $("ev-trender-line").classList.toggle("active", r === "line");
  drawEvChart();
}
function setEvSmooth(on) {
  estream.smooth = on;
  estream.zoom = null; // geglättete Werte haben andere Maxima -> Zoom verwerfen
  $("ev-smooth").classList.toggle("active", on);
  drawEvChart();
}
function resetEvZoom() { if (!estream.zoom && !estream.sel) return; estream.zoom = null; estream.sel = null; drawEvChart(); }
// Maus-Box-Zoom: Bereich aufziehen -> auf x- (Zeit) und y- (Wert) Ausschnitt zoomen.
function initEvZoom() {
  const cv = estream.cv; if (!cv) return;
  cv.style.cursor = "crosshair";
  const pos = (e) => { const r = cv.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; };
  const inPlot = (g, p) => p.x >= g.padL && p.x <= g.padL + g.plotW && p.y >= g.padT && p.y <= g.padT + g.plotH;
  let dragging = false, down = null;
  cv.addEventListener("mousedown", (e) => {
    const g = estream.geom; if (!g) return;
    const p = pos(e); if (!inPlot(g, p)) return;
    dragging = true; down = p; estream.sel = { x0: p.x, y0: p.y, x1: p.x, y1: p.y };
    e.preventDefault();
  });
  window.addEventListener("mousemove", (e) => {
    if (!dragging) return; const g = estream.geom; if (!g) return; const p = pos(e);
    const cx = Math.max(g.padL, Math.min(g.padL + g.plotW, p.x)), cy = Math.max(g.padT, Math.min(g.padT + g.plotH, p.y));
    estream.sel = { x0: down.x, y0: down.y, x1: cx, y1: cy }; drawEvChart();
  });
  window.addEventListener("mouseup", () => {
    if (!dragging) return; dragging = false;
    const g = estream.geom, sel = estream.sel; estream.sel = null;
    if (!g || !sel) { drawEvChart(); return; }
    if (Math.abs(sel.x1 - sel.x0) < 5 || Math.abs(sel.y1 - sel.y0) < 5) { drawEvChart(); return; }
    const toT = (px) => g.xd0 + (px - g.padL) / g.plotW * (g.xd1 - g.xd0);
    const toV = (py) => g.yd0 + (g.padT + g.plotH - py) / g.plotH * (g.yd1 - g.yd0);
    estream.zoom = {
      x0: Math.min(toT(sel.x0), toT(sel.x1)), x1: Math.max(toT(sel.x0), toT(sel.x1)),
      y0: Math.max(0, Math.min(toV(sel.y0), toV(sel.y1))), y1: Math.max(toV(sel.y0), toV(sel.y1)),
    };
    drawEvChart();
  });
  cv.addEventListener("dblclick", resetEvZoom);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") resetEvZoom(); });
  $("ev-reset").addEventListener("click", resetEvZoom);
}
function setDashLiveCollapsed(collapsed) {
  $("dash-live").classList.toggle("collapsed", collapsed);
  $("ev-collapse-icon").textContent = collapsed ? "▸" : "▾";
  $("ev-collapse").setAttribute("aria-expanded", String(!collapsed));
  try { localStorage.setItem("clioDashLiveCollapsed", collapsed ? "1" : "0"); } catch (_) {}
}
function initEventStream() {
  estream.cv = $("evchart"); if (!estream.cv) return;
  estream.ctx = estream.cv.getContext("2d");
  evResize();
  $("ev-mode-rate").addEventListener("click", () => setEvMode("rate"));
  $("ev-mode-cum").addEventListener("click", () => setEvMode("cum"));
  $("ev-scale-lin").addEventListener("click", () => setEvScale("lin"));
  $("ev-scale-log").addEventListener("click", () => setEvScale("log"));
  $("ev-split-total").addEventListener("click", () => setEvSplit("total"));
  $("ev-split-source").addEventListener("click", () => setEvSplit("source"));
  $("ev-srender-stacked").addEventListener("click", () => setEvSrender("stacked"));
  $("ev-srender-area").addEventListener("click", () => setEvSrender("area"));
  $("ev-srender-lines").addEventListener("click", () => setEvSrender("lines"));
  $("ev-trender-area").addEventListener("click", () => setEvTrender("area"));
  $("ev-trender-line").addEventListener("click", () => setEvTrender("line"));
  $("ev-smooth").addEventListener("click", () => setEvSmooth(!estream.smooth));
  const toggle = () => setDashLiveCollapsed(!$("dash-live").classList.contains("collapsed"));
  $("ev-collapse").addEventListener("click", toggle);
  $("ev-collapse").addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); toggle(); } });
  let collapsed = false; try { collapsed = localStorage.getItem("clioDashLiveCollapsed") === "1"; } catch (_) {}
  setDashLiveCollapsed(collapsed);
  initEvZoom();
  // Diagramm regelmäßig aus /event-stats auffrischen, solange das Dashboard
  // sichtbar ist und ein Token vorliegt (zusätzlich zum Auto-Refresh/Live-Events).
  setInterval(() => { if ($("view-dashboard").classList.contains("active") && tokenInput.value.trim()) fetchEventStats(); }, 5000);
}
window.addEventListener("resize", evResize);

// pickNum liefert den ersten endlichen Zahlenwert (überspringt null/undefined).
function pickNum(...vals) {
  for (const v of vals) { if (v == null) continue; const n = Number(v); if (Number.isFinite(n)) return n; }
  return NaN;
}
// renderDbUsage zeigt die DB-Dateigröße plus die echte Wachstumsreserve: der
// Balken bildet die DISK-Belegung ab (nicht den internen bbolt-Füllgrad, der bei
// append-only fast immer ~100 % steht und nichts über die echte Reserve sagt).
// Unterzeile: freier/gesamter Plattenplatz und eine Schätzung, wie viele Events
// noch passen (ø-Eventgröße = usedBytes / eventsTotal). Ohne Disk-Info (Windows)
// fällt es auf den bisherigen internen Füllgrad zurück.
function renderDbUsage(info, m) {
  const file = pickNum(info.databaseFileBytes, gauge(m, "clio_db_size_bytes"));
  $("m-dbsize").textContent = fmtBytes(Number.isFinite(file) ? file : null);
  const used = pickNum(info.databaseUsedBytes, gauge(m, "clio_db_used_bytes"));
  const free = pickNum(info.databaseFreeBytes, gauge(m, "clio_db_free_bytes"));
  const bar = $("m-dbfill");

  const diskFree = pickNum(info.diskFreeBytes, gauge(m, "clio_disk_free_bytes"));
  const diskTotal = pickNum(info.diskTotalBytes, gauge(m, "clio_disk_total_bytes"));
  if (Number.isFinite(diskFree) && Number.isFinite(diskTotal) && diskTotal > 0) {
    const pct = Math.max(0, Math.min(100, (diskTotal - diskFree) / diskTotal * 100));
    bar.style.width = Math.max(2, pct) + "%";
    bar.classList.toggle("warn", pct >= 80 && pct < 95);
    bar.classList.toggle("crit", pct >= 95);
    let sub = fmtBytes(diskFree) + " von " + fmtBytes(diskTotal) + " Disk frei";
    const total = pickNum(info.eventsTotal, gauge(m, "clio_events_total"));
    if (Number.isFinite(used) && used > 0 && Number.isFinite(total) && total > 0) {
      const remain = Math.floor(diskFree / (used / total)); // ø-Eventgröße
      if (Number.isFinite(remain)) sub += " · noch ~" + fmtCompact(remain) + " Events";
    }
    $("m-dbsub").textContent = sub;
    return;
  }

  // Fallback ohne Disk-Info: bisheriger interner bbolt-Füllgrad.
  if (!Number.isFinite(file) || file <= 0 || !Number.isFinite(used)) {
    bar.style.width = "0"; bar.classList.remove("warn", "crit");
    $("m-dbsub").textContent = "Datenbankdatei"; return;
  }
  let fill = pickNum(info.databaseFillPercent);
  if (!Number.isFinite(fill)) fill = used / file * 100;
  bar.style.width = Math.max(2, Math.min(100, fill)) + "%";
  bar.classList.toggle("warn", fill >= 80 && fill < 95);
  bar.classList.toggle("crit", fill >= 95);
  let sub = (fill < 10 ? fill.toFixed(1) : Math.round(fill)) + "% belegt";
  if (Number.isFinite(free) && free > 0 && free / file >= 0.05) sub += " · " + fmtBytes(free) + " frei (compact)";
  $("m-dbsub").textContent = sub;
}

// renderHeadroom visualisiert den Remap-Headroom: den genutzten Umfang
// (Highwater-Mark) gegen die vorbelegte Grenze (CLIO_DB_INITIAL_MB). Nähert er
// sich der Grenze, drohen Schreib-Latenzspitzen durch bbolt-Remaps. Ohne
// Vorbelegung gibt es keine Grenze — die Karte zeigt dann "aus".
function renderHeadroom(info, m) {
  const data = pickNum(info.databaseDataBytes, gauge(m, "clio_db_data_bytes"));
  const initial = pickNum(info.databaseInitialBytes, gauge(m, "clio_db_initial_bytes"));
  const bar = $("m-headroom-fill"), val = $("m-headroom"), sub = $("m-headroom-sub");
  if (!Number.isFinite(initial) || initial <= 0) {
    val.textContent = "aus";
    bar.style.width = "0"; bar.classList.remove("warn", "crit");
    sub.textContent = "nicht vorbelegt (CLIO_DB_INITIAL_MB)";
    return;
  }
  let pct = pickNum(info.databaseInitialFillPercent);
  if (!Number.isFinite(pct) && Number.isFinite(data)) pct = data / initial * 100;
  pct = Math.max(0, Math.min(100, pct));
  const thr = pickNum(info.dbGrowThresholdPct);
  const warnAt = Number.isFinite(thr) ? thr : 80;
  val.textContent = (pct < 10 ? pct.toFixed(1) : Math.round(pct)) + " %";
  bar.style.width = Math.max(2, pct) + "%";
  bar.classList.toggle("warn", pct >= warnAt && pct < 95);
  bar.classList.toggle("crit", pct >= 95);
  let s = (Number.isFinite(data) ? fmtBytes(data) : "?") + " von " + fmtBytes(initial) + " genutzt";
  if (pct >= warnAt) s += " · Grenze nah — CLIO_DB_INITIAL_MB erhöhen";
  sub.textContent = s;
}

function render(info, m) {
  $("m-events").textContent = fmtInt(gauge(m, "clio_events_total") ?? info.eventsTotal);
  renderDbUsage(info, m);
  renderHeadroom(info, m);
  $("m-observers").textContent = fmtInt(gauge(m, "clio_active_observers"));
  $("m-uptime").textContent = fmtDuration(info.uptimeSeconds);
  $("m-started").textContent = info.startedAt ? "seit " + new Date(info.startedAt).toLocaleString("de-CH") : "seit Start";
  $("m-written").textContent = fmtInt(sum(m, "clio_events_written_total"));
  $("m-pc").textContent = fmtInt(sum(m, "clio_precondition_failures_total"));

  const reqTotal = sum(m, "clio_http_requests_total");
  $("m-requests").textContent = fmtInt(reqTotal);
  const now = Date.now();
  if (lastReqTotal != null && lastReqAt != null && now > lastReqAt) {
    const rate = (reqTotal - lastReqTotal) / ((now - lastReqAt) / 1000);
    $("m-rate").textContent = rate >= 0 ? rate.toFixed(2) + " req/s" : "gesamt";
  }
  lastReqTotal = reqTotal; lastReqAt = now;

  const count = gauge(m, "clio_http_request_duration_seconds_count");
  const p50 = quantile(m["clio_http_request_duration_seconds_bucket"], count, 0.5);
  const p99 = quantile(m["clio_http_request_duration_seconds_bucket"], count, 0.99);
  $("m-latency").textContent = (p50 == null) ? "–" : fmtMs(p50) + " / " + fmtMs(p99);

  $("i-version").textContent = info.version ?? "–";
  $("i-sync").textContent = info.syncMode ?? "–";
  $("i-addr").textContent = info.httpListenAddr ?? "–";
  $("i-db").textContent = info.databaseFilePath ?? "–";
  // Storage-Betriebsstatus: Vorbelegung, Auto-Compaction, letzter Online-Compact.
  const initialB = pickNum(info.databaseInitialBytes, gauge(m, "clio_db_initial_bytes"));
  $("i-prealloc").textContent = (Number.isFinite(initialB) && initialB > 0) ? fmtBytes(initialB) + " vorbelegt" : "aus";
  $("i-compact").textContent = info.dbCompactEnabled ? ("alle " + (info.dbCompactIntervalH ?? "?") + " h") : "aus";
  const lc = info.databaseLastCompaction;
  if (lc && lc.at) {
    let txt = new Date(lc.at).toLocaleString("de-CH");
    if (Number.isFinite(lc.oldBytes) && lc.oldBytes > 0 && Number.isFinite(lc.newBytes)) {
      const red = Math.max(0, (1 - lc.newBytes / lc.oldBytes) * 100);
      txt += " · " + fmtBytes(lc.oldBytes) + " → " + fmtBytes(lc.newBytes) + " (−" + red.toFixed(0) + " %)";
    }
    $("i-lastcompact").textContent = txt;
  } else {
    $("i-lastcompact").textContent = "—";
  }
  $("i-time").textContent = info.serverTime ? new Date(info.serverTime).toLocaleString("de-CH") : "–";

  // Dev-Zone nur einblenden, wenn der Server im Dev-Mode läuft.
  $("dev-zone").style.display = info.devMode ? "block" : "none";

  updateTelemetry(info, m, now);
}

function markNotice(text, isErr) {
  const n = $("notice"); n.style.display = "block";
  n.className = "notice" + (isErr ? " err" : ""); n.innerHTML = text;
}
function clearNotice() { $("notice").style.display = "none"; }

function schedule() {
  if (timer) { clearInterval(timer); timer = null; }
  const ms = parseInt(intervalSel.value, 10);
  if (ms > 0) timer = setInterval(refresh, ms);
}
function connect() {
  sessionStorage.setItem("clioToken", tokenInput.value.trim());
  refresh(); schedule(); startEventStream();
}

$("connect").addEventListener("click", connect);
tokenInput.addEventListener("keydown", (e) => { if (e.key === "Enter") connect(); });
intervalSel.addEventListener("change", schedule);

// Hintergrund-Tabs werden vom Browser gedrosselt, wodurch ein offener
// Observe-Stream abreißen kann. Beim Zurückkehren sofort neu verbinden, statt
// auf das nächste Backoff-Intervall zu warten.
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState !== "visible") return;
  const token = tokenInput.value.trim(); if (!token) return;
  if (estream.active && !estream.connected && estream.mode === "stream") {
    if (estream.reconnectT) { clearTimeout(estream.reconnectT); estream.reconnectT = null; }
    estream.retry = 0; runEventStream(token);
  }
  if (live.active && !live.connected && live.mode === "stream") {
    if (live.reconnectT) { clearTimeout(live.reconnectT); live.reconnectT = null; }
    live.retry = 0; runLive(token);
  }
});

// ---------- Dev-Zone · Tabula Rasa (Supernova-Reset, nur im Dev-Mode) ----------
// Gegen versehentliches Auslösen: der Knopf muss gedrückt gehalten werden, bis
// die „Ladung" voll ist (Hold-to-fire). Loslassen vorher bricht ab. Erst dann
// geht POST /api/v1/dev/reset-database raus. Ein bisschen Gamification: lokaler
// Zähler, verglühte Events und ein aufsteigender Rang (alles in localStorage).
const DZ_HOLD_MS = 1400;
const dz = { raf: null, start: 0, firing: false };

function dzRank(n) {
  if (n >= 25) return "Entropie-Lord";
  if (n >= 10) return "Urknall-Meister";
  if (n >= 5) return "Supernova-Veteran";
  if (n >= 1) return "Sternenstaub-Sammler";
  return "Schöpfer";
}
function dzGet(key) { return parseInt(localStorage.getItem(key) || "0", 10) || 0; }
function dzRenderStats() {
  const resets = dzGet("clioResets"), vap = dzGet("clioVaporized");
  $("dz-resets").textContent = fmtInt(resets);
  $("dz-vaporized").textContent = fmtInt(vap);
  $("dz-rank").textContent = dzRank(resets);
}
function dzReset() {
  if (dz.raf) cancelAnimationFrame(dz.raf);
  dz.raf = null;
  $("dz-fire").classList.remove("armed");
  $("dz-charge").style.width = "0";
  if (!dz.firing) $("dz-hint").textContent = "gedrückt halten zum Zünden";
}
function dzTick(now) {
  const pct = Math.min(1, (now - dz.start) / DZ_HOLD_MS);
  $("dz-charge").style.width = (pct * 100) + "%";
  if (pct >= 1) { dzReset(); fireSupernova(); return; }
  $("dz-hint").textContent = "lädt … " + (((1 - pct) * DZ_HOLD_MS) / 1000).toFixed(1) + "s";
  dz.raf = requestAnimationFrame(dzTick);
}
function dzArm() {
  if (dz.firing) return;
  if (!tokenInput.value.trim()) {
    $("dz-result").className = "gen-result err";
    $("dz-result").textContent = "Erst Token eingeben und verbinden.";
    return;
  }
  $("dz-fire").classList.add("armed");
  dz.start = performance.now();
  dz.raf = requestAnimationFrame(dzTick);
}
async function fireSupernova() {
  dz.firing = true;
  $("dz-hint").textContent = "💥 …";
  const flash = $("supernova-flash");
  flash.classList.remove("boom"); void flash.offsetWidth; flash.classList.add("boom");
  try {
    const res = await fetch("/api/v1/dev/reset-database", {
      method: "POST", headers: { Authorization: "Bearer " + tokenInput.value.trim() }, cache: "no-store",
    });
    if (!res.ok) {
      $("dz-result").className = "gen-result err";
      $("dz-result").textContent = "Reset fehlgeschlagen (HTTP " + res.status + ") " + (await res.text());
      return;
    }
    const out = await res.json();
    const before = dzGet("clioResets");
    const resets = before + 1, vap = dzGet("clioVaporized") + (out.deletedEvents || 0);
    localStorage.setItem("clioResets", String(resets));
    localStorage.setItem("clioVaporized", String(vap));
    dzRenderStats();
    const rankUp = dzRank(resets) !== dzRank(before);
    $("dz-result").className = "gen-result ok";
    $("dz-result").textContent = "☄ " + (out.message || "Tabula rasa.") + " — " +
      fmtInt(out.deletedEvents || 0) + " Events verglüht · Reset #" + resets +
      (rankUp ? " · neuer Rang: " + dzRank(resets) + "! 🏆" : "");
    refresh(); // Kennzahlen sofort auffrischen (Chart startet bei null neu).
  } catch (e) {
    $("dz-result").className = "gen-result err";
    $("dz-result").textContent = "Reset fehlgeschlagen: " + e.message;
  } finally {
    dz.firing = false;
    dzReset();
  }
}
(function initDevZone() {
  const btn = $("dz-fire");
  if (!btn) return;
  btn.addEventListener("mousedown", dzArm);
  btn.addEventListener("touchstart", (e) => { e.preventDefault(); dzArm(); }, { passive: false });
  for (const ev of ["mouseup", "mouseleave", "touchend", "touchcancel"]) btn.addEventListener(ev, dzReset);
  dzRenderStats();
})();

// ---------- Live-Event-Viewer ----------
const MAX_ROWS = 500;
const live = { active: false, ctrl: null, paused: false, pending: [], count: 0, lastId: null, retry: 0, reconnectT: null, connected: false, onlyNew: false, mode: "stream", probeT: null };
const eventsEl = $("events");

function liveStatus(state, text) {
  // Live-Status hat Vorrang in der Kopfzeile, solange der Stream läuft.
  setStatus(state, text);
}
// eventsPath baut den GET-Pfad der Komfort-Leseroute für ein Subject
// (URL-kodierte Segmente). "/" -> /api/v1/events; "/a/b" -> /api/v1/events/a/b.
function eventsPath(subj) {
  let path = "/api/v1/events";
  if (subj && subj !== "/") {
    const parts = subj.replace(/^\/+/, "").split("/").filter(Boolean).map(encodeURIComponent);
    if (parts.length) path += "/" + parts.join("/");
  }
  return path;
}
function buildPath(watch = true) {
  let path = eventsPath($("live-subject").value.trim());
  const p = new URLSearchParams();
  if (watch) p.set("watch", "true");
  p.set("recursive", $("live-recursive").checked ? "true" : "false");
  for (const t of $("live-types").value.split(",").map((s) => s.trim()).filter(Boolean)) p.append("type", t);
  return path + "?" + p.toString();
}
// fmtEvTime zeigt Datum + Uhrzeit eines Events kompakt an (de-CH). Heute wird auf
// die Uhrzeit verkürzt, ältere Events tragen das Datum davor — der exakte
// ISO-Zeitstempel hängt im title der Zeit-Spalte.
function fmtEvTime(d) {
  const t = d.toLocaleTimeString("de-CH");
  const now = new Date();
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  return sameDay ? t : d.toLocaleDateString("de-CH") + " " + t;
}
function renderEvent(ev) {
  const row = document.createElement("div");
  row.className = "ev";
  const head = document.createElement("div");
  head.className = "ev-head";
  head.innerHTML =
    `<span class="ev-id">#${ev.id ?? "?"}</span>` +
    `<span class="ev-type"></span>` +
    `<span class="ev-subject"></span>` +
    `<span class="ev-source"></span>` +
    `<span class="ev-time"></span>`;
  head.children[1].textContent = ev.type ?? "?";
  head.children[2].textContent = ev.subject ?? "";
  // Grunddaten: Herkunft (source) und voller Zeitstempel mit Datum. Der exakte
  // RFC-3339-Wert steht jeweils im title (Tooltip), die Anzeige bleibt kompakt.
  const src = ev.source ?? "";
  if (src) { head.children[3].textContent = "src " + src; head.children[3].title = "source: " + src; }
  if (ev.time) {
    const d = new Date(ev.time);
    head.children[4].textContent = fmtEvTime(d);
    head.children[4].title = ev.time;
  }
  row.appendChild(head);
  if (ev.data !== undefined) {
    const pre = document.createElement("pre");
    pre.className = "ev-data";
    pre.textContent = JSON.stringify(ev.data, null, 2);
    row.appendChild(pre);
    head.addEventListener("click", () => row.classList.toggle("open"));
  }
  return row;
}
function appendEvent(ev) {
  if (live.count === 0) eventsEl.innerHTML = "";
  eventsEl.insertBefore(renderEvent(ev), eventsEl.firstChild);
  live.count++;
  while (eventsEl.children.length > MAX_ROWS) eventsEl.removeChild(eventsEl.lastChild);
  $("live-count").textContent = fmtInt(live.count) + " Events";
}
function handleEvent(ev) {
  const id = Number(ev.id);
  if (Number.isFinite(id) && (live.lastId == null || id > live.lastId)) live.lastId = id;
  if (live.paused) {
    live.pending.push(ev);
    const pe = $("live-pending");
    pe.style.display = ""; pe.textContent = fmtInt(live.pending.length) + " neue";
  } else {
    appendEvent(ev);
  }
}
function flushPending() {
  for (const ev of live.pending) appendEvent(ev);
  live.pending = [];
  $("live-pending").style.display = "none";
}

// liveBeobachteText: Statuszeile „live: beobachte <subject>" inkl. Hinweis, wenn
// nur neue Events angezeigt werden.
function liveBeobachteText() {
  return "live: beobachte " + ($("live-subject").value.trim() || "/") + (live.onlyNew ? " (nur neue)" : "");
}

async function startLive() {
  const token = tokenInput.value.trim();
  if (!token) { liveStatus("err", "Token fehlt"); return; }
  sessionStorage.setItem("clioToken", token);
  live.active = true; live.mode = "stream";
  live.count = 0; live.lastId = null; live.retry = 0;
  live.onlyNew = $("live-only-new").checked;
  $("live-start").textContent = "Stoppen";
  $("live-pause").disabled = false;
  $("live-subject").disabled = $("live-recursive").disabled = $("live-types").disabled = $("live-only-new").disabled = true;
  liveStatus("live", liveBeobachteText());
  // „nur neue": höchste Event-ID im Hintergrund holen (eventsTotal aus
  // /api/v1/info) und lowerBound dahinter setzen. So streamt der Server keine
  // History und die UI lädt nichts nach — es erscheinen nur Events ab jetzt.
  if (live.onlyNew) {
    try {
      const r = await fetch("/api/v1/info", { headers: { Authorization: "Bearer " + token }, cache: "no-store" });
      if (r.ok) { const j = await r.json(); const tot = Number(j.eventsTotal); if (Number.isFinite(tot)) live.lastId = tot; }
    } catch (_) {}
    if (!live.active) return; // zwischenzeitlich gestoppt
  }
  runLive(token);
}

// runLive hält den Live-Tab-Stream offen und verbindet nach einem Abriss
// automatisch neu. Erstverbindung ohne lowerBound (zeigt zuerst die History);
// jeder Reconnect setzt ab der höchsten gesehenen ID + 1 fort (keine Dubletten).
async function runLive(token) {
  // Wie beim Dashboard-Ticker: alte Verbindung schließen, bevor eine neue
  // geöffnet wird — sonst leakt sie und belegt einen Browser-Connection-Slot.
  if (live.ctrl) live.ctrl.abort();
  const ctrl = new AbortController(); live.ctrl = ctrl;
  let url = buildPath();
  if (live.lastId != null) url += "&lowerBound=" + (live.lastId + 1);
  // Proxy-Puffer-Erkennung: bleibt das erste Byte aus, auf Polling umschalten.
  let gotData = false;
  clearTimeout(live.probeT);
  live.probeT = setTimeout(() => {
    if (gotData || !live.active || live.ctrl !== ctrl) return;
    ctrl.abort();
    fallbackLivePolling(token);
  }, STREAM_PROBE_MS);
  try {
    const res = await fetch(url, {
      headers: { Authorization: "Bearer " + token }, signal: ctrl.signal, cache: "no-store",
    });
    if (ctrl.signal.aborted) return; // durch einen neueren Lauf ersetzt
    if (res.status === 401) { clearTimeout(live.probeT); liveStatus("err", "Token ungültig (401)"); stopLive(); return; }
    if (!res.ok || !res.body) throw new Error("HTTP " + res.status);
    live.connected = true; live.retry = 0;
    if (live.lastId != null) liveStatus("live", liveBeobachteText());
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (!gotData) { gotData = true; clearTimeout(live.probeT); } // Stream liefert -> Erkennung bestanden
      buf += dec.decode(value, { stream: true });
      let nl;
      while ((nl = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, nl); buf = buf.slice(nl + 1);
        if (line.trim()) { try { handleEvent(JSON.parse(line)); } catch (_) { /* Teilzeile ignorieren */ } }
      }
    }
  } catch (e) {
    if (e.name === "AbortError" || !live.active) return; // manuell gestoppt/ersetzt
  } finally {
    clearTimeout(live.probeT);
    if (live.ctrl === ctrl) live.connected = false;
  }
  if (live.active && live.ctrl === ctrl && live.mode === "stream") scheduleLiveReconnect(token); // Abriss -> erneut verbinden
}

// fallbackLivePolling schaltet den Live-Tab dauerhaft (bis Stop/Neustart) auf
// Polling um — für Umgebungen, in denen ein Gateway den Stream puffert.
function fallbackLivePolling(token) {
  if (live.mode === "poll" || !live.active) return;
  live.mode = "poll"; live.connected = false;
  liveStatus("live", liveBeobachteText() + " · Polling");
  pollLive(token);
}

// pollLive fragt periodisch nur die Events ab lastId+1 ab (endlicher read-Request
// ohne watch) und verarbeitet sie wie der Stream. Beim ersten Poll ohne lowerBound
// (Modus „nicht nur neue") kommt zunächst die History — wie im Stream.
async function pollLive(token) {
  while (live.active && live.mode === "poll") {
    try {
      let url = buildPath(false);
      if (live.lastId != null) url += "&lowerBound=" + (live.lastId + 1);
      const res = await fetch(url, { headers: { Authorization: "Bearer " + token }, cache: "no-store" });
      if (res.status === 401) { liveStatus("err", "Token ungültig (401)"); stopLive(); return; }
      if (res.ok) {
        live.connected = true; live.retry = 0;
        liveStatus("live", liveBeobachteText() + " · Polling");
        const text = await res.text();
        for (const line of text.split("\n")) if (line.trim()) { try { handleEvent(JSON.parse(line)); } catch (_) {} }
      }
    } catch (_) { live.connected = false; }
    await evSleep(LIVE_POLL_MS);
  }
}

function scheduleLiveReconnect(token) {
  live.retry = Math.min(live.retry + 1, 6);
  const delay = Math.min(1000 * 2 ** (live.retry - 1), 15000);
  liveStatus("err", "Verbindung unterbrochen — neuer Versuch in " + Math.round(delay / 1000) + " s …");
  if (live.reconnectT) clearTimeout(live.reconnectT);
  live.reconnectT = setTimeout(() => { if (live.active) runLive(token); }, delay);
}
function stopLive() {
  if (live.probeT) { clearTimeout(live.probeT); live.probeT = null; }
  if (live.reconnectT) { clearTimeout(live.reconnectT); live.reconnectT = null; }
  if (live.ctrl) live.ctrl.abort();
  finishLive();
}
function finishLive() {
  live.active = false; live.connected = false; live.mode = "stream";
  $("live-start").textContent = "Beobachten";
  $("live-pause").disabled = true; $("live-pause").textContent = "Pause"; live.paused = false;
  flushPending();
  $("live-subject").disabled = $("live-recursive").disabled = $("live-types").disabled = $("live-only-new").disabled = false;
  if ($("dot").classList.contains("live")) setStatus(tokenInput.value ? "ok" : "", tokenInput.value ? "verbunden" : "getrennt");
}

$("live-start").addEventListener("click", () => { live.active ? stopLive() : startLive(); });
$("live-pause").addEventListener("click", () => {
  live.paused = !live.paused;
  $("live-pause").textContent = live.paused ? "Fortsetzen" : "Pause";
  if (!live.paused) flushPending();
});
$("live-pending").addEventListener("click", () => {
  live.paused = false; $("live-pause").textContent = "Pause"; flushPending();
});
$("live-clear").addEventListener("click", () => {
  live.count = 0; live.pending = []; $("live-pending").style.display = "none";
  $("live-count").textContent = "0 Events";
  eventsEl.innerHTML = '<div class="empty">Noch nichts beobachtet.</div>';
});

// ---------- Explorer (read-only) ----------
const EXP_MAX = 200;
let explorerLoaded = false, expSelLabel = null;

function setExpStatus(t) { $("exp-status").textContent = t; }
async function authGet(path) {
  const token = tokenInput.value.trim();
  if (!token) throw new Error("Token fehlt");
  const r = await fetch(path, { headers: { Authorization: "Bearer " + token }, cache: "no-store" });
  if (r.status === 401) throw new Error("401");
  return r;
}
async function getJSON(path) { const r = await authGet(path); if (!r.ok) throw new Error("HTTP " + r.status); return r.json(); }
async function getNDJSON(path) {
  const r = await authGet(path); if (!r.ok) throw new Error("HTTP " + r.status);
  return (await r.text()).split("\n").map((s) => s.trim()).filter(Boolean).map((l) => JSON.parse(l));
}

function loadExplorer() {
  const prefix = $("exp-prefix").value.trim();
  if (prefix && prefix[0] !== "/") { setExpStatus("Prefix muss mit / beginnen"); return; }
  explorerLoaded = true;
  setExpStatus("lädt …");
  Promise.all([loadTree(prefix), loadTypes(), loadIntegrity()])
    .then(() => setExpStatus("geladen " + new Date().toLocaleTimeString("de-CH")))
    .catch((e) => setExpStatus(e.message === "401" ? "Token ungültig" : "Fehler"));
}

// EXP_TREE_PAGE: Kinder werden seitenweise nachgeladen, damit der Baum auch bei
// Millionen Subjects unter einem Knoten flüssig bleibt (kein Voll-Baum-Render).
const EXP_TREE_PAGE = 500;

function fetchChildren(parent, after) {
  let p = "/api/v1/read-subjects?children=" + encodeURIComponent(parent) + "&limit=" + EXP_TREE_PAGE;
  if (after) p += "&after=" + encodeURIComponent(after);
  return getJSON(p);
}

async function loadTree(prefix) {
  const el = $("exp-tree");
  el.innerHTML = '<div class="empty">lädt …</div>';
  try {
    const root = prefix || "/";
    const data = await fetchChildren(root, "");
    el.innerHTML = "";
    if ((!data.children || !data.children.length) && !data.total) {
      el.innerHTML = '<div class="empty">Keine Subjects.</div>'; return;
    }
    // Wurzelknoten rendern und direkt aufklappen (erste Kinderseite ist da).
    renderTreeNode({ subject: root, count: data.count || 0, total: data.total || 0, hasChildren: true }, el, 0, true, data);
  } catch (e) {
    el.innerHTML = '<div class="empty">' + (e.message === "401" ? "Token ungültig (401)" : "Fehler: " + e.message) + "</div>";
    throw e;
  }
}

// renderTreeNode erzeugt eine Baumzeile. Kinder werden lazy beim Aufklappen über
// read-subjects?children=… geladen (seitenweise via „mehr laden"). node hat die
// Form {subject, count, total, hasChildren}. preloaded (optional) ist eine bereits
// geladene erste Kinderseite ({children, nextAfter}); dann wird sofort aufgeklappt.
function renderTreeNode(node, container, depth, isRoot, preloaded) {
  const row = document.createElement("div");
  row.className = "tnode";
  row.style.paddingLeft = depth * 14 + "px";
  const expandable = isRoot || node.hasChildren;
  const tog = document.createElement("span");
  tog.className = "tog"; tog.textContent = expandable ? "▸" : "";
  const label = document.createElement("span");
  label.className = "tlabel";
  label.textContent = isRoot ? node.subject : node.subject.split("/").pop();
  label.title = node.subject;
  const cnt = document.createElement("span");
  cnt.className = "tcount";
  cnt.textContent = node.count === node.total ? String(node.total) : node.count + "·Σ" + node.total;
  row.append(tog, label, cnt);
  container.appendChild(row);

  if (expandable) {
    const kids = document.createElement("div");
    kids.className = "tchildren collapsed";
    container.appendChild(kids);
    let loaded = false, nextAfter = "";

    // Hängt eine Kinderseite an (Daten optional bereits vorhanden).
    const appendPage = (data) => {
      [...kids.children].forEach((n) => { if (n.classList.contains("tmore") || n.classList.contains("empty")) n.remove(); });
      for (const c of (data.children || [])) renderTreeNode(c, kids, depth + 1, false);
      nextAfter = data.nextAfter || "";
      if (!kids.children.length) { kids.innerHTML = '<div class="empty">leer</div>'; return; }
      if (nextAfter) {
        const more = document.createElement("div");
        more.className = "tmore"; more.style.paddingLeft = (depth + 1) * 14 + "px";
        more.textContent = "… mehr laden";
        more.addEventListener("click", async (e) => {
          e.stopPropagation(); more.textContent = "lädt …";
          try { appendPage(await fetchChildren(node.subject, nextAfter)); }
          catch (err) { more.textContent = "Fehler: " + err.message; }
        });
        kids.appendChild(more);
      }
    };
    const expand = async () => {
      if (loaded) return;
      loaded = true;
      kids.innerHTML = '<div class="empty">lädt …</div>';
      try { appendPage(await fetchChildren(node.subject, "")); }
      catch (err) { loaded = false; kids.innerHTML = '<div class="empty">Fehler: ' + err.message + '</div>'; }
    };
    tog.addEventListener("click", async () => {
      const collapsed = kids.classList.toggle("collapsed");
      tog.textContent = collapsed ? "▸" : "▾";
      if (!collapsed) await expand();
    });
    if (preloaded) { // Wurzel: erste Seite ist schon da → sofort offen zeigen.
      loaded = true; kids.classList.remove("collapsed"); tog.textContent = "▾";
      appendPage(preloaded);
    }
  }
  label.addEventListener("click", () => selectSubject(node.subject, label));
}

async function selectSubject(subj, labelEl) {
  if (expSelLabel) expSelLabel.classList.remove("sel");
  if (labelEl) { labelEl.classList.add("sel"); expSelLabel = labelEl; }
  $("exp-events-label").textContent = "Events · " + subj + " (nicht rekursiv)";
  const el = $("exp-events");
  el.innerHTML = '<div class="empty">lädt …</div>';
  try {
    const evs = await getNDJSON(eventsPath(subj) + "?recursive=false");
    if (!evs.length) { el.innerHTML = '<div class="empty">Keine Events direkt auf diesem Subject.</div>'; return; }
    el.innerHTML = "";
    const shown = evs.slice(-EXP_MAX).reverse(); // neueste oben
    for (const ev of shown) el.appendChild(renderEvent(ev));
    if (evs.length > EXP_MAX) {
      const note = document.createElement("div");
      note.className = "empty";
      note.textContent = "… " + (evs.length - EXP_MAX) + " ältere ausgeblendet (von " + evs.length + ")";
      el.appendChild(note);
    }
  } catch (e) {
    el.innerHTML = '<div class="empty">' + (e.message === "401" ? "Token ungültig (401)" : "Fehler: " + e.message) + "</div>";
  }
}

async function loadTypes() {
  const el = $("exp-types");
  el.innerHTML = '<div class="empty">lädt …</div>';
  try {
    const types = await getNDJSON("/api/v1/read-event-types");
    if (!types.length) { el.innerHTML = '<div class="empty">Keine Event-Typen.</div>'; return; }
    el.innerHTML = "";
    for (const t of types) el.appendChild(renderTypeItem(t));
  } catch (e) {
    el.innerHTML = '<div class="empty">' + (e.message === "401" ? "Token ungültig (401)" : "Fehler: " + e.message) + "</div>";
    throw e;
  }
}
function renderTypeItem(t) {
  const wrap = document.createElement("div");
  wrap.className = "type-item";
  const row = document.createElement("div");
  row.className = "type-row";
  const name = document.createElement("span");
  name.className = "tname"; name.textContent = t.type;
  const cnt = document.createElement("span");
  cnt.className = "badge"; cnt.textContent = fmtInt(t.count) + " Events";
  const sch = document.createElement("span");
  sch.className = "badge";
  row.append(name, cnt, sch);
  wrap.appendChild(row);
  if (t.hasSchema) {
    sch.textContent = "Schema ▸"; sch.classList.add("pending");
    const pre = document.createElement("pre");
    pre.className = "ev-data";
    wrap.appendChild(pre);
    let loaded = false;
    sch.addEventListener("click", async () => {
      const open = wrap.classList.toggle("open");
      sch.textContent = open ? "Schema ▾" : "Schema ▸";
      if (open && !loaded) {
        pre.textContent = "lädt …";
        try {
          const r = await getJSON("/api/v1/read-event-schema?type=" + encodeURIComponent(t.type));
          pre.textContent = JSON.stringify(r.schema, null, 2); loaded = true;
        } catch (e) { pre.textContent = "Fehler: " + e.message; }
      }
    });
  } else {
    sch.textContent = "kein Schema";
  }
  return wrap;
}

async function loadIntegrity() {
  const chain = $("ig-chain");
  try {
    const v = await getJSON("/api/v1/verify");
    chain.textContent = "";
    const span = document.createElement("span");
    if (v.ok) { span.style.color = "var(--ok)"; span.textContent = "✓ intakt"; }
    else {
      span.style.color = "var(--err)";
      span.textContent = "✗ gebrochen" + (v.brokenAt ? " bei #" + v.brokenAt : "") + (v.reason ? " (" + v.reason + ")" : "");
    }
    chain.appendChild(span);
    $("ig-count").textContent = fmtInt(v.count);
    $("ig-head").textContent = v.head || "–";
  } catch (e) {
    chain.textContent = e.message === "401" ? "Token ungültig (401)" : "Fehler: " + e.message;
    throw e;
  }
  try {
    const r = await authGet("/api/v1/public-key");
    if (r.status === 404) $("ig-sign").textContent = "nicht aktiviert (nur Integrität)";
    else if (r.ok) { const k = await r.json(); $("ig-sign").textContent = (k.algorithm || "") + " · " + (k.publicKey || ""); }
    else $("ig-sign").textContent = "HTTP " + r.status;
  } catch (e) { $("ig-sign").textContent = "Fehler: " + e.message; }
}

$("exp-load").addEventListener("click", loadExplorer);
$("exp-prefix").addEventListener("keydown", (e) => { if (e.key === "Enter") loadExplorer(); });

// ---------- Query (CEL-Editor mit IDE-Unterstützung) ----------
const qEditor = $("q-editor"), qHl = $("q-hl").firstChild, qAc = $("q-ac");
let qSampleLoaded = false;

// Was die Standard-CEL-Umgebung von clio kennt (event-Variable + stdlib).
const EVENT_FIELDS = [
  { name: "id", hint: "ID (String)" }, { name: "type", hint: "Event-Typ" },
  { name: "subject", hint: "Stream-Pfad" }, { name: "source", hint: "Herkunft" },
  { name: "time", hint: "Zeitstempel (String)" }, { name: "data", hint: "Nutzdaten (dynamisch)" },
];
const STRING_FIELDS = ["id", "type", "subject", "source", "time"];
const STRING_METHODS = [
  { name: "startsWith", hint: "Präfix?" }, { name: "endsWith", hint: "Suffix?" },
  { name: "contains", hint: "enthält?" }, { name: "matches", hint: "Regex?" },
];
const CEL_FUNCS = [
  { name: "has", hint: "Feld vorhanden? has(event.data.x)" }, { name: "size", hint: "Länge" },
  { name: "startsWith", hint: "s.startsWith(p)" }, { name: "endsWith", hint: "s.endsWith(p)" },
  { name: "contains", hint: "s.contains(p)" }, { name: "matches", hint: "Regex" },
  { name: "int", hint: "→ int" }, { name: "double", hint: "→ double" }, { name: "string", hint: "→ string" },
  { name: "bool", hint: "→ bool" }, { name: "timestamp", hint: "String → Zeit" }, { name: "duration", hint: "Dauer" },
  { name: "type", hint: "Typ von x" }, { name: "exists", hint: "l.exists(x, p)" }, { name: "all", hint: "l.all(x, p)" },
  { name: "filter", hint: "l.filter(x, p)" }, { name: "map", hint: "l.map(x, e)" },
];
const CEL_KEYWORDS = ["true", "false", "null", "in"];
const FUNC_NAMES = new Set(CEL_FUNCS.map((f) => f.name).concat(STRING_METHODS.map((m) => m.name)));
let dataPaths = []; // aus echten Events gelernte event.data.*-Pfade

// --- Syntax-Highlighting (Token-Overlay hinter dem Textarea) ---
function escHtml(s) { return s.replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c])); }
const HL_RE = /(\/\/[^\n]*)|('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*")|(\b\d+(?:\.\d+)?\b)|([A-Za-z_]\w*)|([=!<>]=|&&|\|\||[+\-*/%<>!.?:]|\bin\b)/g;
function highlight(src) {
  let out = "", last = 0, m;
  HL_RE.lastIndex = 0;
  while ((m = HL_RE.exec(src))) {
    out += escHtml(src.slice(last, m.index));
    last = HL_RE.lastIndex;
    if (m[1]) out += `<span class="tk-com">${escHtml(m[1])}</span>`;
    else if (m[2]) out += `<span class="tk-str">${escHtml(m[2])}</span>`;
    else if (m[3]) out += `<span class="tk-num">${escHtml(m[3])}</span>`;
    else if (m[4]) {
      const w = m[4];
      const after = src.slice(HL_RE.lastIndex).match(/^\s*\(/);
      let cls = "tk-fld";
      if (CEL_KEYWORDS.includes(w)) cls = "tk-kw";
      else if (after && FUNC_NAMES.has(w)) cls = "tk-fn";
      else if (w === "event") cls = "tk-fld";
      out += `<span class="${cls}">${escHtml(w)}</span>`;
    } else if (m[5]) out += `<span class="tk-op">${escHtml(m[5])}</span>`;
  }
  out += escHtml(src.slice(last));
  return out;
}
function syncEditor() {
  qHl.innerHTML = highlight(qEditor.value);
  qHl.parentNode.scrollTop = qEditor.scrollTop;
  qHl.parentNode.scrollLeft = qEditor.scrollLeft;
}

// --- Autovervollständigung ---
let acItems = [], acSel = 0, acTokenStart = 0;
function currentToken() {
  const caret = qEditor.selectionStart;
  const left = qEditor.value.slice(0, caret);
  const tok = (left.match(/[A-Za-z0-9_.]*$/) || [""])[0];
  return { tok, start: caret - tok.length, caret };
}
function completionsFor(tok) {
  const out = [];
  const add = (text, kind, hint) => out.push({ text, kind, hint });
  const lastDot = tok.lastIndexOf(".");
  const base = lastDot >= 0 ? tok.slice(0, lastDot) : "";
  if (lastDot < 0) {
    if ("event".startsWith(tok) && tok) add("event", "fld", "Event-Variable");
    for (const f of CEL_FUNCS) if (f.name.startsWith(tok)) add(f.name + "()", "fn", f.hint);
    for (const k of CEL_KEYWORDS) if (k.startsWith(tok) && tok) add(k, "kw", "");
  } else if (base === "event") {
    for (const f of EVENT_FIELDS) if (("event." + f.name).startsWith(tok)) add("event." + f.name, "fld", f.hint);
  } else if (base === "event.data" || base.startsWith("event.data.")) {
    for (const p of dataPaths) if (p.startsWith(tok)) add(p, "fld", "data-Feld");
  } else if (STRING_FIELDS.some((s) => base === "event." + s)) {
    for (const mth of STRING_METHODS) add(base + "." + mth.name + "()", "fn", mth.hint);
  } else {
    for (const p of dataPaths) if (p.startsWith(tok)) add(p, "fld", "data-Feld");
  }
  return out.slice(0, 50);
}
function showAc() {
  const { tok, start } = currentToken();
  if (!tok) return hideAc();
  const items = completionsFor(tok);
  if (!items.length) return hideAc();
  acItems = items; acSel = 0; acTokenStart = start;
  qAc.innerHTML = "";
  items.forEach((it, i) => {
    const row = document.createElement("div");
    row.className = "ac-item" + (i === 0 ? " sel" : "");
    row.innerHTML = `<span class="ac-kind ${it.kind}">${it.kind}</span><span class="ac-text"></span><span class="ac-hint"></span>`;
    row.children[1].textContent = it.text;
    row.children[2].textContent = it.hint || "";
    row.addEventListener("mousedown", (e) => { e.preventDefault(); acceptAc(i); });
    qAc.appendChild(row);
  });
  const box = $("q-editor-box");
  qAc.style.top = box.offsetTop + box.offsetHeight + 4 + "px";
  qAc.style.left = "8px";
  qAc.style.display = "block";
}
function hideAc() { qAc.style.display = "none"; acItems = []; }
function moveAc(d) {
  if (!acItems.length) return;
  acSel = (acSel + d + acItems.length) % acItems.length;
  [...qAc.children].forEach((c, i) => c.classList.toggle("sel", i === acSel));
  qAc.children[acSel].scrollIntoView({ block: "nearest" });
}
function acceptAc(i) {
  const it = acItems[i != null ? i : acSel];
  if (!it) return;
  const v = qEditor.value;
  const before = v.slice(0, acTokenStart), after = v.slice(qEditor.selectionStart);
  let insert = it.text, caretInParen = insert.endsWith("()");
  qEditor.value = before + insert + after;
  let pos = acTokenStart + insert.length - (caretInParen ? 1 : 0);
  qEditor.setSelectionRange(pos, pos);
  hideAc(); syncEditor(); qEditor.focus();
}

qEditor.addEventListener("input", () => { syncEditor(); showAc(); persistQuery(); });
qEditor.addEventListener("scroll", syncEditor);
qEditor.addEventListener("blur", () => setTimeout(hideAc, 120));
qEditor.addEventListener("keydown", (e) => {
  const open = qAc.style.display === "block";
  if ((e.ctrlKey || e.metaKey) && e.key === "Enter") { e.preventDefault(); hideAc(); runQuery(); return; }
  if (e.ctrlKey && e.key === " ") { e.preventDefault(); showAc(); return; }
  if (!open) return;
  if (e.key === "ArrowDown") { e.preventDefault(); moveAc(1); }
  else if (e.key === "ArrowUp") { e.preventDefault(); moveAc(-1); }
  else if (e.key === "Enter" || e.key === "Tab") { e.preventDefault(); acceptAc(); }
  else if (e.key === "Escape") { e.preventDefault(); hideAc(); }
});

// --- Feld-Vorschläge aus echten Events lernen ---
function collectPaths(obj, prefix, into) {
  if (obj && typeof obj === "object" && !Array.isArray(obj)) {
    for (const k of Object.keys(obj)) {
      const p = prefix + "." + k;
      into.add(p);
      collectPaths(obj[k], p, into);
    }
  }
}
async function refreshQuerySchema() {
  const token = tokenInput.value.trim();
  if (!token) return;
  qSampleLoaded = true;
  try {
    const body = JSON.stringify({ subject: $("q-subject").value.trim() || "/", recursive: $("q-recursive").checked, limit: 200 });
    const r = await fetch("/api/v1/run-query", {
      method: "POST", headers: { Authorization: "Bearer " + token, "Content-Type": "application/json" },
      body, cache: "no-store",
    });
    if (!r.ok) return;
    const lines = (await r.text()).split("\n").map((s) => s.trim()).filter(Boolean);
    const paths = new Set();
    for (const l of lines) {
      try { const ev = JSON.parse(l); if (ev.data !== undefined) collectPaths(ev.data, "event.data", paths); } catch (_) {}
    }
    dataPaths = [...paths].sort();
  } catch (_) { /* Vorschläge sind optional */ }
}

// --- Ausführen ---
function setQErr(msg) {
  const el = $("q-error");
  if (!msg) { el.style.display = "none"; return; }
  el.textContent = msg; el.style.display = "inline-block";
}
function setQIndexWarn(msg) {
  const el = $("q-index-warn");
  if (!msg) { el.style.display = "none"; return; }
  el.textContent = "⚠ kein Index — voller Scan";
  el.title = msg + "\n\nTipp: ein `event.type == '…'`-Constraint aktiviert den Typ-Index; engerer Subject-Scope und kleineres Limit helfen zusätzlich.";
  el.style.display = "inline-block";
}
function looksLikeEvent(o) { return o && typeof o === "object" && "id" in o && "type" in o && "subject" in o; }
function renderJSON(o) {
  const pre = document.createElement("pre");
  pre.className = "q-json";
  pre.textContent = JSON.stringify(o, null, 2);
  return pre;
}
async function runQuery() {
  const token = tokenInput.value.trim();
  if (!token) { setQErr("Token fehlt — oben Verbinden."); return; }
  sessionStorage.setItem("clioToken", token);
  const subject = $("q-subject").value.trim();
  if (!subject || subject[0] !== "/") { setQErr('subject muss mit "/" beginnen'); return; }
  setQErr(""); setQIndexWarn("");
  const req = { subject, recursive: $("q-recursive").checked };
  // Editor-Prädikat mit dem Zeitraum-Filter (falls gesetzt) zu einem effektiven
  // CEL-Prädikat verbinden. Der Editor-Teil wird geklammert, damit ein internes
  // `||` nicht über das per `&&` angehängte Zeitprädikat hinwegbindet.
  const where = qEditor.value.trim();
  const tp = timePredicate();
  let eff = where;
  if (where && tp) eff = "(" + where + ") && " + tp;
  else if (tp) eff = tp;
  if (eff) req.where = eff;
  const limit = parseInt($("q-limit").value, 10);
  if (!isNaN(limit) && limit >= 0) req.limit = limit;
  const lower = $("q-lower").value.trim(), upper = $("q-upper").value.trim();
  if (lower) req.lowerBound = lower;
  if (upper) req.upperBound = upper;
  const sel = $("q-select").value.split(",").map((s) => s.trim()).filter(Boolean);
  if (sel.length) req.select = sel;

  $("q-status").textContent = "läuft …";
  const t0 = performance.now();
  try {
    const r = await fetch("/api/v1/run-query", {
      method: "POST", headers: { Authorization: "Bearer " + token, "Content-Type": "application/json" },
      body: JSON.stringify(req), cache: "no-store",
    });
    // Hinweis des Servers, wenn die Abfrage keinen Index nutzen kann (voller
    // Scan über den Scope) — z. B. ein Daten-Prädikat ohne event.type-Constraint.
    setQIndexWarn(r.headers.get("X-Clio-Query-Warning") || "");
    if (r.status === 401) { setQErr("Token ungültig (401)."); $("q-status").textContent = "401"; return; }
    if (!r.ok) {
      let detail = "HTTP " + r.status;
      try { const p = await r.json(); if (p.detail) detail = p.detail; } catch (_) {}
      setQErr(detail); $("q-status").textContent = "Fehler";
      return;
    }
    const lines = (await r.text()).split("\n").map((s) => s.trim()).filter(Boolean);
    const ms = Math.round(performance.now() - t0);
    const el = $("q-result");
    el.innerHTML = "";
    qLastRows = [];
    if (!lines.length) { el.innerHTML = '<div class="empty">Keine Treffer.</div>'; }
    else {
      for (const l of lines) {
        let o; try { o = JSON.parse(l); } catch (_) { continue; }
        qLastRows.push(o);
        el.appendChild(looksLikeEvent(o) && !sel.length ? renderEvent(o) : renderJSON(o));
      }
    }
    $("q-status").textContent = lines.length + " Treffer · " + ms + " ms";
    $("q-result-count").textContent = lines.length + (lines.length === 1 ? " Zeile" : " Zeilen");
    $("q-result-bar").style.display = qLastRows.length ? "flex" : "none";
    recordHistory(currentQueryState()); // erfolgreiche Abfrage in den Verlauf
    refreshQuerySchema(); // Vorschläge an den aktuellen Scope anpassen
  } catch (e) {
    setQErr("Verbindung fehlgeschlagen: " + e.message); $("q-status").textContent = "Fehler";
  }
}

function persistQuery() {
  sessionStorage.setItem("clioQuery", qEditor.value);
  sessionStorage.setItem("clioQuerySubject", $("q-subject").value);
  sessionStorage.setItem("clioQueryFrom", $("q-from").value);
  sessionStorage.setItem("clioQueryTo", $("q-to").value);
}
$("q-run").addEventListener("click", runQuery);
$("q-clear").addEventListener("click", () => {
  qEditor.value = ""; setQErr(""); setQIndexWarn(""); syncEditor(); persistQuery();
  qLastRows = []; $("q-result-bar").style.display = "none";
  $("q-result").innerHTML = '<div class="empty">Geleert.</div>';
});
$("q-format").addEventListener("click", () => { qEditor.value = qEditor.value.replace(/\s+/g, " ").trim(); syncEditor(); persistQuery(); });
$("q-subject").addEventListener("change", () => { qSampleLoaded = false; if (tokenInput.value.trim()) refreshQuerySchema(); persistQuery(); });

// ---------- Query: Zustand, Verlauf, Favoriten, Export ----------
let qLastRows = [];
const HIST_MAX = 25;
function currentQueryState() {
  return {
    subject: $("q-subject").value.trim() || "/", recursive: $("q-recursive").checked,
    where: qEditor.value.trim(), select: $("q-select").value.trim(),
    limit: $("q-limit").value.trim(), lower: $("q-lower").value.trim(), upper: $("q-upper").value.trim(),
    from: $("q-from").value, to: $("q-to").value,
  };
}
function applyQueryState(s) {
  $("q-subject").value = s.subject || "/";
  $("q-recursive").checked = s.recursive !== false;
  qEditor.value = s.where || "";
  $("q-select").value = s.select || "";
  $("q-limit").value = (s.limit === undefined || s.limit === "") ? "100" : s.limit;
  $("q-lower").value = s.lower || "";
  $("q-upper").value = s.upper || "";
  $("q-from").value = s.from || "";
  $("q-to").value = s.to || "";
  updatePeriod(null);
  setQErr(""); syncEditor(); persistQuery();
  qSampleLoaded = false;
  switchView("query");
  if (tokenInput.value.trim()) { refreshQuerySchema(); runQuery(); }
}
function loadStore(key) { try { return JSON.parse(localStorage.getItem(key)) || []; } catch (_) { return []; } }
function saveStore(key, v) { try { localStorage.setItem(key, JSON.stringify(v)); } catch (_) {} }
function sameState(a, b) {
  return a.subject === b.subject && a.recursive === b.recursive && a.where === b.where &&
    a.select === b.select && a.limit === b.limit && a.lower === b.lower && a.upper === b.upper &&
    (a.from || "") === (b.from || "") && (a.to || "") === (b.to || "");
}
function scopeLabel(s) {
  let t = s.subject + (s.recursive ? " ·rek" : "");
  if (s.select) t += " · select " + s.select;
  if (s.from || s.to) t += " · ⏱ " + rangeLabel(s.from, s.to);
  return t;
}
function recordHistory(state) {
  let h = loadStore("clioHistory").filter((e) => !sameState(e, state));
  h.unshift(state);
  if (h.length > HIST_MAX) h = h.slice(0, HIST_MAX);
  saveStore("clioHistory", h);
  renderHistory();
}
function renderHistory() {
  const el = $("q-history"), h = loadStore("clioHistory");
  el.innerHTML = "";
  if (!h.length) { el.innerHTML = '<div class="empty">Noch keine ausgeführten Abfragen.</div>'; return; }
  for (const s of h) {
    const row = document.createElement("div");
    row.className = "q-entry";
    row.innerHTML = '<span class="q-where"></span><span class="q-scope"></span>';
    row.children[0].textContent = s.where || "(leeres Prädikat)";
    row.children[1].textContent = scopeLabel(s);
    row.title = (s.where || "(leeres Prädikat)") + "\n" + scopeLabel(s);
    row.addEventListener("click", () => applyQueryState(s));
    el.appendChild(row);
  }
}
function renderFavorites() {
  const el = $("q-favorites"), f = loadStore("clioFavorites");
  el.innerHTML = "";
  if (!f.length) { el.innerHTML = '<div class="empty">Noch keine Favoriten.</div>'; return; }
  f.forEach((fav, i) => {
    const row = document.createElement("div");
    row.className = "q-entry";
    row.innerHTML = '<span class="q-name"></span><span class="q-del" title="Löschen">✕</span>';
    row.children[0].textContent = fav.name;
    row.children[0].title = (fav.state.where || "(leeres Prädikat)") + "\n" + scopeLabel(fav.state);
    row.children[0].addEventListener("click", () => applyQueryState(fav.state));
    row.children[1].addEventListener("click", (e) => {
      e.stopPropagation(); f.splice(i, 1); saveStore("clioFavorites", f); renderFavorites();
    });
    el.appendChild(row);
  });
}
function saveFavorite() {
  const state = currentQueryState();
  const def = state.where ? state.where.slice(0, 40) : state.subject;
  const name = (prompt("Name für den Favoriten:", def) || "").trim();
  if (!name) return;
  const f = loadStore("clioFavorites");
  f.unshift({ name, state });
  saveStore("clioFavorites", f);
  renderFavorites();
  toggleQPanel(true);
}
function toggleQPanel(force) {
  const p = $("q-panel");
  p.style.display = (force != null ? force : p.style.display === "none") ? "grid" : "none";
}
$("q-fav").addEventListener("click", saveFavorite);
$("q-history-toggle").addEventListener("click", () => toggleQPanel());
$("q-history-clear").addEventListener("click", () => { saveStore("clioHistory", []); renderHistory(); });

// ---------- Export der Ergebnisse ----------
function download(name, text, mime) {
  const url = URL.createObjectURL(new Blob([text], { type: mime }));
  const a = document.createElement("a");
  a.href = url; a.download = name;
  document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
function tsName(ext) { return "clio-query-" + new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19) + "." + ext; }
function flatten(obj, prefix, out) {
  if (obj === null || typeof obj !== "object") { out[prefix] = obj; return; }
  if (Array.isArray(obj)) { out[prefix] = JSON.stringify(obj); return; }
  for (const k of Object.keys(obj)) flatten(obj[k], prefix ? prefix + "." + k : k, out);
}
function csvCell(v) {
  if (v == null) return "";
  const s = typeof v === "object" ? JSON.stringify(v) : String(v);
  return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
}
function exportNDJSON() {
  if (!qLastRows.length) return;
  download(tsName("ndjson"), qLastRows.map((o) => JSON.stringify(o)).join("\n") + "\n", "application/x-ndjson");
}
function exportCSV() {
  if (!qLastRows.length) return;
  const flat = qLastRows.map((o) => { const f = {}; flatten(o, "", f); return f; });
  const cols = [], seen = new Set();
  for (const row of flat) for (const k of Object.keys(row)) if (!seen.has(k)) { seen.add(k); cols.push(k); }
  const head = cols.map(csvCell).join(",");
  const body = flat.map((row) => cols.map((c) => csvCell(row[c])).join(",")).join("\n");
  download(tsName("csv"), head + "\n" + body + "\n", "text/csv");
}
$("q-export-ndjson").addEventListener("click", exportNDJSON);
$("q-export-csv").addEventListener("click", exportCSV);

renderHistory(); renderFavorites();

// gespeicherte Query wiederherstellen
qEditor.value = sessionStorage.getItem("clioQuery") || "";
$("q-subject").value = sessionStorage.getItem("clioQuerySubject") || "/";
$("q-from").value = sessionStorage.getItem("clioQueryFrom") || "";
$("q-to").value = sessionStorage.getItem("clioQueryTo") || "";
syncEditor();

// ---------- Query: Zeitraum-Schnellfilter (Ausschnitt aus dem Zeitstrahl) ----------
// Die Presets liefern einen [von, bis)-Bereich relativ zu "jetzt"; bis ist immer
// exklusiv (Beginn der nächsten Periode), damit sich Perioden lückenlos und
// überlappungsfrei aneinanderreihen. Gerechnet wird in lokaler Zeit, ausgegeben
// als UTC-ISO fürs CEL-Prädikat (timestamp(event.time)).
function startOfDay(d) { return new Date(d.getFullYear(), d.getMonth(), d.getDate()); }
function periodRange(key) {
  const now = new Date();
  const y = now.getFullYear(), m = now.getMonth(), day = now.getDate();
  switch (key) {
    case "today":     { const a = startOfDay(now); return [a, new Date(y, m, day + 1)]; }
    case "yesterday": return [new Date(y, m, day - 1), startOfDay(now)];
    case "week":      { const a = startOfDay(now); a.setDate(a.getDate() - ((a.getDay() + 6) % 7)); return [a, new Date(a.getFullYear(), a.getMonth(), a.getDate() + 7)]; }
    case "month":     return [new Date(y, m, 1), new Date(y, m + 1, 1)];
    case "quarter":   { const q = Math.floor(m / 3); return [new Date(y, q * 3, 1), new Date(y, q * 3 + 3, 1)]; }
    case "h1":        return [new Date(y, 0, 1), new Date(y, 6, 1)];
    case "h2":        return [new Date(y, 6, 1), new Date(y + 1, 0, 1)];
    case "year":      return [new Date(y, 0, 1), new Date(y + 1, 0, 1)];
    case "last7":     return [new Date(y, m, day - 6), new Date(y, m, day + 1)];
    case "last30":    return [new Date(y, m, day - 29), new Date(y, m, day + 1)];
  }
  return null;
}
// Date -> "YYYY-MM-DDTHH:mm" in lokaler Zeit für <input type=datetime-local>.
function toLocalInput(d) {
  const p = (n) => String(n).padStart(2, "0");
  return d.getFullYear() + "-" + p(d.getMonth() + 1) + "-" + p(d.getDate()) + "T" + p(d.getHours()) + ":" + p(d.getMinutes());
}
function isoNoMs(d) { return new Date(d).toISOString().replace(/\.\d{3}Z$/, "Z"); }
// timePredicate baut aus den Von/Bis-Feldern das CEL-Zeitprädikat (oder "").
function timePredicate() {
  const f = $("q-from").value, t = $("q-to").value;
  const parts = [];
  if (f) parts.push("timestamp(event.time) >= timestamp('" + isoNoMs(new Date(f)) + "')");
  if (t) parts.push("timestamp(event.time) < timestamp('" + isoNoMs(new Date(t)) + "')");
  return parts.join(" && ");
}
function fmtBound(v) {
  if (!v) return "…";
  const d = new Date(v);
  const hm = d.toLocaleTimeString("de-CH", { hour: "2-digit", minute: "2-digit" });
  const date = d.toLocaleDateString("de-CH");
  return hm === "00:00" ? date : date + " " + hm;
}
function rangeLabel(from, to) { return fmtBound(from) + " – " + fmtBound(to); }
// updatePeriod hebt den passenden Chip hervor (oder keinen, wenn manuell
// justiert wurde) und blendet die lesbare Zeitspanne + das erzeugte CEL ein.
function updatePeriod(activeKey) {
  for (const b of $("q-period-chips").querySelectorAll("[data-period]")) {
    b.classList.toggle("active", b.dataset.period === activeKey);
  }
  const info = $("q-period-info");
  const f = $("q-from").value, t = $("q-to").value;
  if (!f && !t) { info.textContent = ""; info.title = ""; return; }
  info.textContent = "Zeitfilter: " + rangeLabel(f, t);
  info.title = timePredicate();
}
function setPeriod(key) {
  const r = periodRange(key);
  if (!r) return;
  $("q-from").value = toLocalInput(r[0]);
  $("q-to").value = toLocalInput(r[1]);
  updatePeriod(key);
  persistQuery();
}
for (const b of $("q-period-chips").querySelectorAll("[data-period]")) {
  b.addEventListener("click", () => setPeriod(b.dataset.period));
}
// Manuelles Justieren von Von/Bis löst die Chip-Markierung und aktualisiert die Info.
$("q-from").addEventListener("input", () => { updatePeriod(null); persistQuery(); });
$("q-to").addEventListener("input", () => { updatePeriod(null); persistQuery(); });
$("q-period-clear").addEventListener("click", () => {
  $("q-from").value = ""; $("q-to").value = ""; updatePeriod(null); persistQuery();
});
updatePeriod(null);

// ---------- Hilfe: Beispiele (in den Editor ladbar) ----------
const HELP_EXAMPLES = [
  { desc: "Alle Bestellungen über 100", subject: "/orders", recursive: true,
    where: "event.type == 'order-placed' && has(event.data.amount) && event.data.amount > 100" },
  { desc: "Nur ein Feld projizieren (id + Betrag)", subject: "/orders", recursive: true,
    select: "id, data.amount", where: "" },
  { desc: "Textsuche im Titel (case-sensitiv)", subject: "/books", recursive: true,
    where: "has(event.data.title) && event.data.title.contains('Dune')" },
  { desc: "Mehrere Typen via in-Operator", subject: "/orders", recursive: true,
    where: "event.type in ['order-placed', 'order-cancelled']" },
  { desc: "Zeitraum: dieses Quartal (Zeitfilter-Chip)", subject: "/", recursive: true,
    where: "", period: "quarter" },
  { desc: "Zeitraum: ab einem festen Datum (CEL-Zeitprädikat)", subject: "/", recursive: true,
    where: "timestamp(event.time) >= timestamp('2026-01-01T00:00:00Z')" },
  { desc: "Alle Events eines Streams (leeres Prädikat)", subject: "/books/42", recursive: false, where: "" },
];
function loadExample(ex) {
  $("q-subject").value = ex.subject;
  $("q-recursive").checked = !!ex.recursive;
  $("q-select").value = ex.select || "";
  qEditor.value = ex.where || "";
  // Zeitfilter passend zum Beispiel setzen (oder zurücksetzen), damit es
  // vorhersehbar genau das ausführt, was beschrieben ist.
  if (ex.period) { setPeriod(ex.period); }
  else { $("q-from").value = ""; $("q-to").value = ""; updatePeriod(null); }
  setQErr(""); syncEditor(); persistQuery();
  qSampleLoaded = false;
  switchView("query");
  if (tokenInput.value.trim()) { refreshQuerySchema(); runQuery(); }
}
(function renderHelpExamples() {
  const host = $("help-examples");
  for (const ex of HELP_EXAMPLES) {
    const card = document.createElement("div");
    card.className = "ex";
    const head = document.createElement("div");
    head.className = "ex-head";
    const desc = document.createElement("span");
    desc.className = "ex-desc"; desc.textContent = ex.desc;
    const btn = document.createElement("button");
    btn.className = "primary"; btn.textContent = "In Editor laden";
    btn.addEventListener("click", () => loadExample(ex));
    head.append(desc, btn);
    const code = document.createElement("pre");
    code.className = "ex-code";
    code.textContent = (ex.where || "(leeres Prädikat)") +
      "\n→ subject " + ex.subject + (ex.recursive ? " (rekursiv)" : "") + (ex.select ? " · select " + ex.select : "") +
      (ex.period ? " · ⏱ Zeitfilter „" + ex.period + "“" : "");
    card.append(head, code);
    host.appendChild(card);
  }
})();

// ---------- Erzeugen (Event-/Schema-Generator) ----------
let genLoaded = false;

async function authPost(path, body) {
  const token = tokenInput.value.trim();
  if (!token) throw new Error("Token fehlt");
  return fetch(path, {
    method: "POST",
    headers: { Authorization: "Bearer " + token, "Content-Type": "application/json" },
    body: JSON.stringify(body), cache: "no-store",
  });
}
async function problemDetail(r) {
  try { const j = await r.json(); return j.detail || j.title || ("HTTP " + r.status); }
  catch (_) { return "HTTP " + r.status; }
}
function genResult(id, ok, msg) {
  const el = $(id);
  el.className = "gen-result " + (ok ? "ok" : "err");
  el.textContent = msg;
}
function fillGenList(id, values) {
  const dl = $(id); dl.innerHTML = "";
  for (const v of values) { const o = document.createElement("option"); o.value = v; dl.appendChild(o); }
}
async function loadGenSuggestions() {
  if (!tokenInput.value.trim()) return;
  try { fillGenList("dl-gen-types", (await getNDJSON("/api/v1/read-event-types")).map((t) => t.type)); } catch (_) {}
  try { fillGenList("dl-gen-subjects", (await getNDJSON("/api/v1/read-subjects")).map((s) => s.subject).slice(0, 500)); } catch (_) {}
}
// Nach erfolgreichem Write: Vorschläge auffrischen; Dashboard-Zähler holt der
// Auto-Refresh ohnehin nach.
function afterGenWrite() { genLoaded = true; loadGenSuggestions(); }

// writeEvents POSTet eine Event-Liste und meldet Fehler in resultId. Liefert die
// geschriebenen Events oder null.
async function writeEvents(events, resultId) {
  const r = await authPost("/api/v1/write-events", { events });
  if (r.status === 401) { genResult(resultId, false, "Token ungültig (401) — oben Token eingeben und Verbinden."); return null; }
  if (!r.ok) { genResult(resultId, false, "Fehler: " + await problemDetail(r)); return null; }
  return (await r.text()).split("\n").map((s) => s.trim()).filter(Boolean).map((l) => JSON.parse(l));
}

async function genWrite() {
  const subject = $("gen-subject").value.trim();
  const type = $("gen-type").value.trim();
  const source = $("gen-source").value.trim() || "clio-ui";
  if (!subject || subject[0] !== "/") { genResult("gen-result", false, "Subject muss mit / beginnen."); return; }
  if (!type) { genResult("gen-result", false, "Typ ist Pflicht."); return; }
  let data;
  try { const t = $("gen-data").value.trim(); data = t ? JSON.parse(t) : undefined; }
  catch (e) { genResult("gen-result", false, "Daten sind kein gültiges JSON: " + e.message); return; }
  const count = Math.max(1, Math.min(500, parseInt($("gen-count").value, 10) || 1));
  const events = [];
  for (let i = 0; i < count; i++) {
    let d = data;
    if (count > 1) {
      if (data && typeof data === "object" && !Array.isArray(data)) d = { ...data, _n: i + 1 };
      else if (data === undefined) d = { _n: i + 1 };
      else d = { _n: i + 1, value: data };
    }
    const ev = { source, subject, type };
    if (d !== undefined) ev.data = d;
    events.push(ev);
  }
  $("gen-write").disabled = true;
  genResult("gen-result", true, "schreibt …");
  try {
    const written = await writeEvents(events, "gen-result");
    if (written) {
      const ids = written.map((e) => e.id);
      const range = ids.length > 1 ? ids[0] + "–" + ids[ids.length - 1] : ids[0];
      genResult("gen-result", true, "✓ " + written.length + " Event" + (written.length > 1 ? "s" : "") + " geschrieben (ID " + range + ").");
      afterGenWrite();
    }
  } catch (e) { genResult("gen-result", false, "Fehler: " + e.message); }
  finally { $("gen-write").disabled = false; }
}

async function genRegisterSchema() {
  const type = $("gen-schema-type").value.trim();
  if (!type) { genResult("gen-schema-result", false, "Typ ist Pflicht."); return; }
  let schema;
  try { schema = JSON.parse($("gen-schema").value); }
  catch (e) { genResult("gen-schema-result", false, "Schema ist kein gültiges JSON: " + e.message); return; }
  $("gen-schema-register").disabled = true;
  genResult("gen-schema-result", true, "registriert …");
  try {
    const r = await authPost("/api/v1/register-event-schema", { type, schema });
    if (r.status === 401) genResult("gen-schema-result", false, "Token ungültig (401).");
    else if (r.ok) { genResult("gen-schema-result", true, "✓ Schema für '" + type + "' registriert."); loadGenSuggestions(); }
    else if (r.status === 409) genResult("gen-schema-result", false, "Bereits registriert (unveränderlich): " + await problemDetail(r));
    else genResult("gen-schema-result", false, "Fehler: " + await problemDetail(r));
  } catch (e) { genResult("gen-schema-result", false, "Fehler: " + e.message); }
  finally { $("gen-schema-register").disabled = false; }
}

const GEN_TEMPLATES = {
  library: { subject: "/library/book-42", type: "book.borrowed", data: { member: "alice", title: "Dune" } },
  carshare: { subject: "/carshare/car-7", type: "trip.started", data: { driver: "bob", lat: 47.37, lng: 8.54 } },
  order: { subject: "/orders/1001", type: "order.placed", data: { customer: "carol", amount: 250, currency: "CHF" } },
};
function applyTemplate(name) {
  const t = GEN_TEMPLATES[name]; if (!t) return;
  $("gen-subject").value = t.subject;
  $("gen-type").value = t.type;
  $("gen-data").value = JSON.stringify(t.data, null, 2);
  $("gen-count").value = "1";
  genResult("gen-result", true, "Vorlage geladen — anpassen und schreiben.");
}

const GEN_SCENARIOS = {
  library: (p) => [
    { subject: p + "/books/42", type: "book.acquired", data: { title: "Dune", author: "Herbert" } },
    { subject: p + "/books/42", type: "book.borrowed", data: { member: "alice" } },
    { subject: p + "/books/42", type: "book.returned", data: { member: "alice" } },
    { subject: p + "/books/99", type: "book.acquired", data: { title: "1984", author: "Orwell" } },
    { subject: p + "/members/alice", type: "member.registered", data: { name: "Alice" } },
  ],
  carshare: (p) => [
    { subject: p + "/cars/7", type: "car.registered", data: { plate: "ZH-1234", model: "Model 3" } },
    { subject: p + "/cars/7", type: "trip.started", data: { driver: "bob", lat: 47.37, lng: 8.54 } },
    { subject: p + "/cars/7", type: "gps.snapshot", data: { lat: 47.38, lng: 8.55 } },
    { subject: p + "/cars/7", type: "gps.snapshot", data: { lat: 47.39, lng: 8.56 } },
    { subject: p + "/cars/7", type: "trip.ended", data: { driver: "bob", km: 12 } },
  ],
};
async function genScenario(name, btn) {
  let prefix = $("gen-scenario-prefix").value.trim() || "/playground";
  if (prefix[0] !== "/") { genResult("gen-scenario-result", false, "Prefix muss mit / beginnen."); return; }
  prefix = prefix.replace(/\/+$/, "");
  const events = GEN_SCENARIOS[name](prefix).map((e) => ({ source: "clio-ui", ...e }));
  btn.disabled = true;
  genResult("gen-scenario-result", true, "erzeugt …");
  try {
    const written = await writeEvents(events, "gen-scenario-result");
    if (written) {
      genResult("gen-scenario-result", true,
        "✓ " + written.length + " Events unter " + prefix + " erzeugt. Jetzt unter ‚Live-Events' (Subject " +
        prefix + ", rekursiv) oder ‚Explorer' ansehen.");
      afterGenWrite();
    }
  } catch (e) { genResult("gen-scenario-result", false, "Fehler: " + e.message); }
  finally { btn.disabled = false; }
}

$("gen-write").addEventListener("click", genWrite);
$("gen-clear-form").addEventListener("click", () => {
  $("gen-subject").value = "/playground/1"; $("gen-type").value = "demo.created";
  $("gen-source").value = "clio-ui"; $("gen-data").value = ""; $("gen-count").value = "1";
  genResult("gen-result", true, "");
});
$("gen-schema-register").addEventListener("click", genRegisterSchema);
document.querySelectorAll(".gen-templates .tpl").forEach((b) =>
  b.addEventListener("click", () => applyTemplate(b.dataset.tpl)));
$("gen-scenario-library").addEventListener("click", (e) => genScenario("library", e.currentTarget));
$("gen-scenario-carshare").addEventListener("click", (e) => genScenario("carshare", e.currentTarget));

// ---------- Keys (API-Key-Verwaltung, ADR-025) ----------
let keysLoaded = false;
let keysData = null;
let secretHideTimer = null;

function activeAdminCount(keys) {
  return (keys || []).filter((k) => k.status === "active" && (k.scopes || []).includes("admin")).length;
}
function hideKeyTable() { $("key-table").style.display = "none"; }

async function loadKeys() {
  const token = tokenInput.value.trim();
  const status = $("key-list-status");
  if (!token) { status.textContent = "Token fehlt — oben eingeben und Verbinden."; return; }
  status.textContent = "lade …";
  try {
    const r = await fetch("/api/v1/keys", { headers: { Authorization: "Bearer " + token }, cache: "no-store" });
    if (r.status === 401) { status.textContent = "Token ungültig (401)."; hideKeyTable(); return; }
    if (r.status === 403) { status.textContent = "Kein admin-Scope (403) — dieser Key darf keine Keys verwalten."; hideKeyTable(); return; }
    if (!r.ok) { status.textContent = "Fehler: " + await problemDetail(r); hideKeyTable(); return; }
    keysData = await r.json();
    keysLoaded = true;
    renderKeys();
  } catch (e) { status.textContent = "Netzwerkfehler: " + e.message; }
}

function renderKeys() {
  if (!keysData) return;
  const keys = keysData.keys || [];
  const filter = $("key-filter").value;
  const rows = keys.filter((k) =>
    filter === "all" ? true : filter === "active" ? k.status === "active" : k.status === "revoked");

  const warn = $("key-warning");
  if (keysData.warning) { warn.className = "gen-result warn"; warn.textContent = "⚠ " + keysData.warning; warn.style.display = "block"; }
  else { warn.style.display = "none"; }

  const tbody = $("key-tbody");
  tbody.innerHTML = "";
  for (const k of rows) {
    const tr = document.createElement("tr");
    if (k.status !== "active") tr.className = "revoked";
    const scopes = (k.scopes || []).map((s) => '<span class="badge scope">' + escHtml(s) + "</span>").join("");
    const statusBadge = k.status === "active"
      ? '<span class="badge ok">active</span>' : '<span class="badge warn">revoked</span>';
    const created = k.createdAt ? new Date(k.createdAt).toLocaleString("de-CH") : "–";
    const revoked = k.revokedAt ? new Date(k.revokedAt).toLocaleString("de-CH") : "–";
    tr.innerHTML =
      '<td class="kid">' + escHtml(k.kid) + "</td><td>" + escHtml(k.name || "") + "</td><td>" +
      scopes + "</td><td>" + statusBadge + "</td><td>" + created + "</td><td>" + revoked + "</td><td></td>";
    if (k.status === "active") {
      const btn = document.createElement("button");
      btn.className = "link-btn"; btn.textContent = "widerrufen";
      btn.addEventListener("click", () => revokeKey(k.kid, k.name));
      tr.lastElementChild.appendChild(btn);
    }
    tbody.appendChild(tr);
  }
  $("key-table").style.display = rows.length ? "table" : "none";
  let s = keys.length + " Key(s), " + activeAdminCount(keys) + " aktive Admin-Key(s) · aktualisiert " +
    new Date().toLocaleTimeString("de-CH");
  if (!rows.length) s += " — keine Treffer für diesen Filter.";
  $("key-list-status").textContent = s;
}

async function revokeKey(kid, name) {
  const keys = (keysData && keysData.keys) || [];
  const isAdminKey = keys.some((k) => k.kid === kid && (k.scopes || []).includes("admin"));
  const isLastAdmin = isAdminKey && activeAdminCount(keys) <= 1;
  let msg = "Key " + kid + (name ? " (" + name + ")" : "") + " widerrufen?";
  if (isLastAdmin) msg += "\n\n⚠ Das ist der letzte aktive Admin-Key — danach ist keine Schlüsselverwaltung mehr möglich (Self-Lockout).";
  if (!confirm(msg)) return;
  try {
    const r = await authPost("/api/v1/keys/" + encodeURIComponent(kid) + "/revoke", {});
    if (!r.ok) { alert("Widerruf fehlgeschlagen: " + await problemDetail(r)); return; }
    await loadKeys();
  } catch (e) { alert("Netzwerkfehler: " + e.message); }
}

function keyCreateResult(ok, msg) {
  const el = $("key-create-result");
  el.className = "gen-result " + (ok ? "ok" : "err");
  el.textContent = msg;
}

function showNewSecret(j) {
  const el = $("key-create-result");
  if (secretHideTimer) { clearInterval(secretHideTimer); secretHideTimer = null; }
  el.className = "gen-result ok";
  el.innerHTML = "";
  const head = document.createElement("div");
  head.innerHTML = "<strong>Key angelegt:</strong> " + escHtml(j.kid) + " (" + (j.scopes || []).map(escHtml).join(", ") + ")";
  const warnEl = document.createElement("div");
  warnEl.style.margin = "6px 0";
  warnEl.textContent = "⚠ " + (j.warning || "Dieser Wert wird nur einmal angezeigt und ist danach nicht mehr abrufbar.");
  const row = document.createElement("div");
  row.className = "gen-row";
  const code = document.createElement("code");
  code.style.userSelect = "all"; code.style.wordBreak = "break-all";
  code.textContent = j.secret;
  const copy = document.createElement("button");
  copy.className = "link-btn"; copy.textContent = "kopieren";
  copy.addEventListener("click", async () => {
    try { await navigator.clipboard.writeText(j.secret); copy.textContent = "kopiert ✓"; }
    catch (_) { copy.textContent = "(Strg+C)"; }
  });
  const timer = document.createElement("span");
  timer.className = "hint"; timer.style.marginLeft = "8px";
  const hideNow = document.createElement("button");
  hideNow.className = "link-btn"; hideNow.textContent = "ausblenden";
  row.append(code, copy, timer, hideNow);
  el.append(head, warnEl, row);

  // Sicherheits-Auto-Ausblendung: der Klartext verschwindet nach einem Countdown
  // (oder per Klick) aus dem DOM — er ist serverseitig ohnehin nicht erneut
  // abrufbar, soll aber nicht dauerhaft sichtbar herumstehen (Shoulder-Surfing).
  const hide = () => {
    if (secretHideTimer) { clearInterval(secretHideTimer); secretHideTimer = null; }
    el.className = "gen-result";
    el.innerHTML = "";
    const done = document.createElement("div");
    done.innerHTML = "<strong>Key angelegt:</strong> " + escHtml(j.kid) +
      " — Geheimnis ausgeblendet. Falls du es nicht gespeichert hast: neuen Key anlegen und diesen widerrufen.";
    el.append(done);
  };
  let left = 60;
  const tick = () => {
    if (left <= 0) { hide(); return; }
    timer.textContent = "(verschwindet in " + left + " s)";
    left--;
  };
  tick();
  secretHideTimer = setInterval(tick, 1000);
  hideNow.addEventListener("click", hide);
}

async function createKey() {
  const name = $("key-name").value.trim();
  if (!name) { keyCreateResult(false, "Name ist Pflicht."); return; }
  const scopes = [];
  if ($("key-scope-read").checked) scopes.push("read");
  if ($("key-scope-write").checked) scopes.push("write");
  if ($("key-scope-admin").checked) scopes.push("admin");
  if (!scopes.length) { keyCreateResult(false, "Mindestens einen Scope wählen."); return; }
  try {
    const r = await authPost("/api/v1/keys", { name, scopes });
    if (r.status === 401) { keyCreateResult(false, "Token ungültig (401)."); return; }
    if (r.status === 403) { keyCreateResult(false, "Kein admin-Scope (403)."); return; }
    if (r.status !== 201) { keyCreateResult(false, "Fehler: " + await problemDetail(r)); return; }
    showNewSecret(await r.json());
    $("key-name").value = "";
    await loadKeys();
  } catch (e) { keyCreateResult(false, "Netzwerkfehler: " + e.message); }
}

$("key-create").addEventListener("click", createKey);
$("key-refresh").addEventListener("click", loadKeys);
$("key-filter").addEventListener("change", renderKeys);

// Eventstrom-Diagramm + Live-Liste initialisieren (Canvas, Buttons, Collapse).
initEventStream();

// Auto-Start des Dashboards, falls bereits ein Token im Tab liegt.
if (tokenInput.value) connect();
