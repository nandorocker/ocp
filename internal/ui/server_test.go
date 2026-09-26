package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nando/ocp/internal/ocp"
)

func handlerFixture(t *testing.T) (http.Handler, Store, ocp.Paths) {
	t.Helper()
	store := source(t, "version: 1\n")
	root := t.TempDir()
	paths := ocp.Paths{OpenCode: filepath.Join(root, "config", "opencode"), Current: filepath.Join(root, "data", "current"), LockFile: filepath.Join(root, "config", ".lock")}
	handler := newHandler(handlerOptions{Store: store, Paths: paths, Token: "secret", Origin: "http://127.0.0.1:4321", Host: "127.0.0.1:4321", Apply: func() (ApplyResult, error) { return ApplyResult{Profiles: []string{"default"}}, nil }})
	return handler, store, paths
}

func request(t *testing.T, handler http.Handler, method, path string, body any, secure bool) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	r := httptest.NewRequest(method, path, reader)
	r.Host = "127.0.0.1:4321"
	if secure {
		r.Header.Set("Origin", "http://127.0.0.1:4321")
		r.Header.Set("X-OCP-Token", "secret")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestHandlerSecurityAndProfileFlow(t *testing.T) {
	handler, store, _ := handlerFixture(t)
	if w := request(t, handler, http.MethodGet, "/api/state", nil, false); w.Code != http.StatusForbidden {
		t.Fatalf("unprotected state status = %d: %s", w.Code, w.Body.String())
	}
	if w := request(t, handler, http.MethodGet, "/api/state", nil, true); w.Code != http.StatusOK {
		t.Fatalf("state status = %d: %s", w.Code, w.Body.String())
	}
	badHost := httptest.NewRequest(http.MethodGet, "/", nil)
	badHost.Host = "example.com"
	badHostResponse := httptest.NewRecorder()
	handler.ServeHTTP(badHostResponse, badHost)
	if badHostResponse.Code != http.StatusForbidden {
		t.Fatalf("bad host status = %d", badHostResponse.Code)
	}
	snap, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]string{"revision": snap.Revision, "name": "deep", "extends": "default"}
	if w := request(t, handler, http.MethodPost, "/api/profiles", body, false); w.Code != http.StatusForbidden {
		t.Fatalf("unprotected mutation status = %d", w.Code)
	}
	if w := request(t, handler, http.MethodPost, "/api/profiles", body, true); w.Code != http.StatusOK {
		t.Fatalf("create status = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(store.Source, "profiles", "deep.yaml")); err != nil {
		t.Fatal(err)
	}
	if w := request(t, handler, http.MethodGet, "/api/profiles", nil, true); w.Code == http.StatusOK {
		t.Fatalf("mutation GET status = %d", w.Code)
	}
}

func TestHandlerConflictAndAssets(t *testing.T) {
	handler, store, _ := handlerFixture(t)
	snap, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAgent(snap.Revision, "one.md", "body"); err != nil {
		t.Fatal(err)
	}
	w := request(t, handler, http.MethodPost, "/api/agents", map[string]string{"revision": snap.Revision, "name": "two.md", "content": "body"}, true)
	if w.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d: %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		w = request(t, handler, http.MethodGet, path, nil, false)
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src") {
			t.Fatalf("asset %s status=%d headers=%v", path, w.Code, w.Header())
		}
	}
	if !strings.Contains(request(t, handler, http.MethodGet, "/", nil, false).Body.String(), "secret") {
		t.Fatal("session token not embedded")
	}
}

func TestHandlerPreviewAndModels(t *testing.T) {
	store := source(t, "version: 1\n")
	if err := os.MkdirAll(filepath.Join(store.Source, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Source, "profiles", "default.yaml"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	paths := ocp.Paths{OpenCode: filepath.Join(root, "config", "opencode"), Current: filepath.Join(root, "data", "current"), LockFile: filepath.Join(root, "config", ".lock")}
	handler := newHandler(handlerOptions{
		Store: store, Paths: paths, Token: "secret", Origin: "http://127.0.0.1:4321", Host: "127.0.0.1:4321",
		Models: func(context.Context) ([]ModelOption, error) {
			return []ModelOption{{ID: "openai/test", Provider: "openai"}}, nil
		},
	})
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, handler, http.MethodPost, "/api/profiles/default/preview", map[string]any{"revision": snapshot.Revision, "model": "manual/model", "agents": []any{}}, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"model":"manual/model"`) {
		t.Fatalf("preview status=%d body=%s", w.Code, w.Body.String())
	}
	w = request(t, handler, http.MethodGet, "/api/models", nil, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "openai/test") {
		t.Fatalf("models status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestTrustedOriginValidation(t *testing.T) {
	tests := map[string]string{
		"https://windy.tail1fe933.ts.net:4100/": "https://windy.tail1fe933.ts.net:4100",
		"https://EXAMPLE.com":                   "https://example.com",
		"https://example.com:04100":             "https://example.com:4100",
		"https://[0:0:0:0:0:0:0:1]:4100":        "https://[::1]:4100",
	}
	for input, want := range tests {
		got, err := trustedOrigin(input)
		if err != nil || got != want {
			t.Errorf("trustedOrigin(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"http://example.com",
		"https://user@example.com",
		"https://example.com/path",
		"https://example.com?query=yes",
		"https://example.com#fragment",
		"https://*.example.com",
		"https://example.com:443",
		"https://example.com:0443",
		"https://example.com:",
		"https://example.com:70000",
		"example.com",
	} {
		if _, err := trustedOrigin(input); err == nil {
			t.Errorf("trustedOrigin(%q) accepted invalid origin", input)
		}
	}
}

type failingListener struct{ addr net.Addr }

func (l failingListener) Accept() (net.Conn, error) { return nil, errors.New("stop") }
func (l failingListener) Close() error              { return nil }
func (l failingListener) Addr() net.Addr            { return l.addr }

func TestRunUsesLoopbackListenerAndBrowserOrigin(t *testing.T) {
	for _, test := range []struct {
		name, trustedOrigin, wantURL string
	}{
		{name: "local", wantURL: "http://127.0.0.1:4321/"},
		{name: "trusted", trustedOrigin: "https://windy.tail1fe933.ts.net:4100/", wantURL: "https://windy.tail1fe933.ts.net:4100/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var browserURL string
			var out bytes.Buffer
			err := Run(Options{
				Source:        t.TempDir(),
				Out:           &out,
				Open:          true,
				TrustedOrigin: test.trustedOrigin,
				Listen: func(network, address string) (net.Listener, error) {
					if network != "tcp" || address != "127.0.0.1:0" {
						t.Fatalf("Listen(%q, %q)", network, address)
					}
					return failingListener{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4321}}, nil
				},
				OpenBrowser: func(url string) error {
					browserURL = url
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "stop") {
				t.Fatalf("Run error = %v", err)
			}
			if browserURL != test.wantURL || !strings.Contains(out.String(), test.wantURL) {
				t.Fatalf("browser URL = %q, output = %q; want %q", browserURL, out.String(), test.wantURL)
			}
		})
	}
}

func TestExternalOriginSecurity(t *testing.T) {
	store := source(t, "version: 1\n")
	root := t.TempDir()
	paths := ocp.Paths{OpenCode: filepath.Join(root, "config", "opencode"), Current: filepath.Join(root, "data", "current"), LockFile: filepath.Join(root, "config", ".lock")}
	handler := newHandler(handlerOptions{Store: store, Paths: paths, Token: "secret", Origin: "https://windy.tail1fe933.ts.net:4100", Host: "windy.tail1fe933.ts.net:4100"})

	request := func(host, origin, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/profiles", strings.NewReader(`{}`))
		r.Host = host
		r.Header.Set("Origin", origin)
		r.Header.Set("X-OCP-Token", token)
		r.Header.Set("X-Forwarded-Host", "windy.tail1fe933.ts.net:4100")
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if w := request("windy.tail1fe933.ts.net:4100", "https://windy.tail1fe933.ts.net:4100", "secret"); w.Code == http.StatusForbidden {
		t.Fatalf("trusted request rejected: %s", w.Body.String())
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"local host":      request("127.0.0.1:4321", "https://windy.tail1fe933.ts.net:4100", "secret"),
		"wrong origin":    request("windy.tail1fe933.ts.net:4100", "https://example.com", "secret"),
		"missing token":   request("windy.tail1fe933.ts.net:4100", "https://windy.tail1fe933.ts.net:4100", ""),
		"forwarded spoof": request("example.com", "https://windy.tail1fe933.ts.net:4100", "secret"),
	} {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", name, w.Code)
		}
	}
}
