package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/csautter/dev-alchemy/internal/gateway"
	"github.com/csautter/dev-alchemy/internal/gateway/gatewaytest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestJobScreenshotReturnsBuildConsole(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	session := h.Connect(t)

	var view gateway.JobView
	mustCall(t, session, "vm_build", map[string]any{"os": "ubuntu", "type": "server", "arch": "arm64"}, &view)
	final := waitJob(t, session, view.ID)
	if final.State != gateway.JobSucceeded {
		t.Fatalf("build failed: %+v", final)
	}
	if !final.ScreenshotAvailable || final.ScreenChangedAt.IsZero() || final.LastOutputAt.IsZero() || final.StuckSuspected {
		t.Fatalf("job status lacks screen hints: %+v", final)
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "job_screenshot", Arguments: map[string]any{"job_id": view.ID}})
	if err != nil || result.IsError {
		t.Fatalf("job_screenshot: %v %+v", err, result)
	}
	if len(result.Content) != 2 {
		t.Fatalf("expected image and text content, got %d items", len(result.Content))
	}
	image, ok := result.Content[0].(*mcp.ImageContent)
	if !ok || image.MIMEType != "image/jpeg" {
		t.Fatalf("first content is not a JPEG image: %#v", result.Content[0])
	}
	decoded, err := jpeg.Decode(bytes.NewReader(image.Data))
	if err != nil {
		t.Fatalf("image does not decode: %v", err)
	}
	// The fake CLI's newest frame shows a bright dialog in the middle.
	if gray := decoded.At(160, 120); !isBright(gray) {
		t.Fatalf("expected the newest frame (bright dialog), got %v", gray)
	}
	text, ok := result.Content[1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("second content is not text: %#v", result.Content[1])
	}
	var info gateway.ScreenshotInfo
	if err := json.Unmarshal([]byte(text.Text), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.FramesAvailable != 1 || info.CapturedAt.IsZero() || info.LiveView == "" {
		t.Fatalf("unexpected screenshot info %+v", info)
	}
	if strings.Contains(text.Text, "/view/") {
		t.Fatal("the live view address must not be sent to the agent")
	}

	if err := call(t, session, "job_screenshot", map[string]any{"job_id": view.ID, "index": 1}, nil); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("expected an out of range error, got %v", err)
	}
	if err := call(t, session, "job_screenshot", map[string]any{"job_id": "nope"}, nil); err == nil {
		t.Fatal("expected an error for an unknown job")
	}
}

func TestJobScreenshotExplainsMissingFrames(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	session := h.Connect(t)

	var view gateway.JobView
	mustCall(t, session, "vm_create", map[string]any{"os": "ubuntu"}, &view)
	waitJob(t, session, view.ID)

	var info gateway.ScreenshotInfo
	mustCall(t, session, "job_screenshot", map[string]any{"job_id": view.ID}, &info)
	if info.Available || !strings.Contains(info.Reason, "not a build job") {
		t.Fatalf("unexpected info for a create job: %+v", info)
	}
}

func isBright(c interface{ RGBA() (r, g, b, a uint32) }) bool {
	r, _, _, _ := c.RGBA()
	return r>>8 > 200
}

func TestLiveViewServesBuildConsoleOnlyWithToken(t *testing.T) {
	h := gatewaytest.Start(t, gatewaytest.Options{})
	if h.ViewURL == "" || !strings.HasPrefix(h.ViewURL, "http://127.0.0.1:") || !strings.HasSuffix(h.ViewURL, "/") {
		t.Fatalf("unexpected live view URL %q", h.ViewURL)
	}
	session := h.Connect(t)

	get := func(method string, url string, host string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res, body
	}

	res, body := get(http.MethodGet, h.ViewURL+"status.json", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "No build job yet") {
		t.Fatalf("status before a build: %d %s", res.StatusCode, body)
	}

	var view gateway.JobView
	mustCall(t, session, "vm_build", map[string]any{"os": "ubuntu"}, &view)
	waitJob(t, session, view.ID)

	res, body = get(http.MethodGet, h.ViewURL, "")
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>Build Live View</title>") {
		t.Fatalf("page: %d", res.StatusCode)
	}
	for name, want := range map[string]string{
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := res.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("weak CSP %q", csp)
	}

	res, body = get(http.MethodGet, h.ViewURL+"status.json", "")
	var status map[string]any
	if err := json.Unmarshal(body, &status); err != nil || status["job_id"] != view.ID || status["available"] != true {
		t.Fatalf("status after a build: %d %s", res.StatusCode, body)
	}

	res, body = get(http.MethodGet, h.ViewURL+"latest.jpg", "")
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("latest.jpg: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if _, err := jpeg.Decode(bytes.NewReader(body)); err != nil {
		t.Fatalf("latest.jpg does not decode: %v", err)
	}

	base := strings.TrimSuffix(h.ViewURL, "/")
	wrongToken := base[:strings.LastIndex(base, "/")+1] + strings.Repeat("0", 64) + "/"
	if res, _ = get(http.MethodGet, wrongToken+"latest.jpg", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("wrong token: %d, want 404", res.StatusCode)
	}
	if res, _ = get(http.MethodPost, h.ViewURL+"status.json", ""); res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", res.StatusCode)
	}
	if res, _ = get(http.MethodGet, h.ViewURL, "evil.example"); res.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host: %d, want 403", res.StatusCode)
	}
	if res, _ = get(http.MethodGet, h.ViewURL, "host.docker.internal"); res.StatusCode != http.StatusForbidden {
		t.Errorf("container Host: %d, want 403", res.StatusCode)
	}
	if res, _ = get(http.MethodGet, base, ""); res.StatusCode != http.StatusFound {
		t.Errorf("missing slash: %d, want 302", res.StatusCode)
	}
	if res, _ = get(http.MethodGet, h.ViewURL+"../token", ""); res.StatusCode == http.StatusOK {
		t.Errorf("unexpected path served")
	}

	// The MCP endpoint does not serve the live view, and the view does not serve MCP.
	mcpBase := strings.TrimSuffix(h.URL, "/mcp")
	viewPath := h.ViewURL[strings.Index(h.ViewURL[len("http://"):], "/")+len("http://"):]
	if res, _ = get(http.MethodGet, mcpBase+viewPath, ""); res.StatusCode == http.StatusOK {
		t.Errorf("MCP listener served the live view: %d", res.StatusCode)
	}

	if !strings.Contains(h.Console.String(), "live view:") {
		t.Errorf("console does not show the live view on build start:\n%s", h.Console.String())
	}
}
