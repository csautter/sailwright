package gateway

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// The live view lets the human on the host watch the console of a build in a
// browser. It runs on its own loopback listener with its own token in the URL path,
// separate from the MCP endpoint, and only reads what the frame sampler keeps.

const viewPathPrefix = "/view/"

const viewStyle = `
:root { color-scheme: light dark; --bg: #f6f6f4; --fg: #1d1d1b; --muted: #6b6b66; --panel: #ffffff; --line: #d9d9d4; --warn-bg: #fdecc8; --warn-fg: #6b4400; }
@media (prefers-color-scheme: dark) { :root { --bg: #161615; --fg: #ecece8; --muted: #9a9a93; --panel: #1f1f1d; --line: #33332f; --warn-bg: #4a3500; --warn-fg: #ffd98a; } }
* { box-sizing: border-box; }
body { margin: 0; padding: 16px; background: var(--bg); color: var(--fg); font: 14px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif; }
header { display: flex; flex-wrap: wrap; gap: 8px 16px; align-items: baseline; margin-bottom: 12px; }
h1 { font-size: 16px; margin: 0; font-weight: 600; }
dl { display: flex; flex-wrap: wrap; gap: 4px 16px; margin: 0; color: var(--muted); }
dt { display: inline; } dd { display: inline; margin: 0 0 0 4px; color: var(--fg); font-variant-numeric: tabular-nums; }
.badge { padding: 2px 8px; border-radius: 999px; background: var(--warn-bg); color: var(--warn-fg); font-weight: 600; }
.screen { background: var(--panel); border: 1px solid var(--line); border-radius: 6px; padding: 8px; }
.screen img { display: block; max-width: 100%; height: auto; margin: 0 auto; image-rendering: auto; }
.reason { color: var(--muted); padding: 24px 8px; text-align: center; }
`

const viewScript = `
const $ = (id) => document.getElementById(id);
function ago(iso) {
  if (!iso) return "–";
  const s = Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 1000));
  if (s < 90) return s + " s ago";
  if (s < 5400) return Math.round(s / 60) + " min ago";
  return (s / 3600).toFixed(1) + " h ago";
}
async function refresh() {
  try {
    const res = await fetch("status.json", { cache: "no-store" });
    const st = await res.json();
    $("job").textContent = st.job_id || "none";
    $("state").textContent = st.state || "–";
    $("frame").textContent = ago(st.captured_at);
    $("changed").textContent = ago(st.screen_changed_at);
    $("output").textContent = ago(st.last_output_at);
    $("stuck").hidden = !st.stuck_suspected;
    if (st.available) {
      $("img").src = "latest.jpg?job=" + encodeURIComponent(st.job_id) + "&t=" + Date.now();
      $("img").hidden = false;
      $("reason").hidden = true;
    } else {
      $("img").hidden = true;
      $("reason").hidden = false;
      $("reason").textContent = st.reason || "No build job yet.";
    }
  } catch (e) {
    $("reason").hidden = false;
    $("reason").textContent = "Gateway not reachable.";
  }
}
refresh();
setInterval(refresh, 5000);
`

var (
	viewPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Build Live View</title><style>` + viewStyle + `</style></head>
<body>
<header>
<h1>sailwright build console</h1>
<span id="stuck" class="badge" hidden>possibly stuck</span>
<dl>
<div><dt>job</dt><dd id="job">–</dd></div>
<div><dt>state</dt><dd id="state">–</dd></div>
<div><dt>frame</dt><dd id="frame">–</dd></div>
<div><dt>screen changed</dt><dd id="changed">–</dd></div>
<div><dt>last output</dt><dd id="output">–</dd></div>
</dl>
</header>
<div class="screen"><img id="img" alt="VM console" hidden><div id="reason" class="reason">Loading…</div></div>
<script>` + viewScript + `</script>
</body></html>
`
	viewCSP = "default-src 'none'; img-src 'self'; connect-src 'self'; script-src '" + cspHash(viewScript) + "'; style-src '" + cspHash(viewStyle) + "'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

func cspHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

func newViewToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// viewStatus is the live view's status.json.
type viewStatus struct {
	JobID           string    `json:"job_id,omitempty"`
	State           JobState  `json:"state,omitempty"`
	Available       bool      `json:"available"`
	Reason          string    `json:"reason,omitempty"`
	Frames          int       `json:"frames"`
	CapturedAt      time.Time `json:"captured_at,omitzero"`
	ScreenChangedAt time.Time `json:"screen_changed_at,omitzero"`
	LastOutputAt    time.Time `json:"last_output_at,omitzero"`
	StuckSuspected  bool      `json:"stuck_suspected,omitempty"`
}

// viewPath returns the live view path for the gateway's view token.
func (g *Gateway) viewPath() string {
	return viewPathPrefix + g.viewToken + "/"
}

// ViewHandler serves the live view. It is meant for a loopback-only listener.
func (g *Gateway) ViewHandler() http.Handler {
	return http.HandlerFunc(g.serveView)
}

func (g *Gateway) serveView(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, viewPathPrefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	token, sub, hasSlash := strings.Cut(rest, "/")
	if subtle.ConstantTimeCompare([]byte(token), []byte(g.viewToken)) != 1 {
		http.NotFound(w, r)
		return
	}
	if !loopbackHost(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	header := w.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Content-Security-Policy", viewCSP)
	if !hasSlash {
		http.Redirect(w, r, g.viewPath(), http.StatusFound)
		return
	}

	switch sub {
	case "":
		header.Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(viewPage))
	case "status.json":
		header.Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(g.viewStatus(r.URL.Query().Get("job")))
	case "latest.jpg":
		g.serveViewFrame(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (g *Gateway) viewJob(requested string) (string, bool) {
	if requested != "" {
		_, ok := g.jobs.Get(requested)
		return requested, ok
	}
	return g.jobs.LatestScreenJob()
}

func (g *Gateway) viewStatus(requested string) viewStatus {
	id, ok := g.viewJob(requested)
	if !ok {
		return viewStatus{Reason: "No build job yet. Start one with vm_build."}
	}
	shot, err := g.jobs.Screenshot(id, 0)
	if err != nil {
		return viewStatus{JobID: id, Reason: err.Error()}
	}
	return viewStatus{
		JobID:           id,
		State:           shot.View.State,
		Available:       shot.Data != nil,
		Reason:          shot.Reason,
		Frames:          shot.Status.Frames,
		CapturedAt:      shot.Frame.CapturedAt,
		ScreenChangedAt: shot.Status.ChangedAt,
		LastOutputAt:    shot.View.LastOutputAt,
		StuckSuspected:  shot.Status.StuckSuspected,
	}
}

func (g *Gateway) serveViewFrame(w http.ResponseWriter, r *http.Request) {
	id, ok := g.viewJob(r.URL.Query().Get("job"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	shot, err := g.jobs.Screenshot(id, 0)
	if err != nil || shot.Data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(shot.Data)))
	_, _ = w.Write(shot.Data)
}

// loopbackHost accepts Host headers that name this machine's loopback interface, which
// also rejects DNS rebinding.
func loopbackHost(hostHeader string) bool {
	host := hostOnly(hostHeader)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}
