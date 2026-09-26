package ui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nando/ocp/internal/ocp"
)

//go:embed web/*
var webFiles embed.FS

type ApplyResult struct {
	Profiles []string `json:"profiles"`
	Warnings []string `json:"warnings,omitempty"`
	Active   string   `json:"active,omitempty"`
	FellBack bool     `json:"fellBack,omitempty"`
	NoActive bool     `json:"noActive,omitempty"`
}

type Options struct {
	Paths         ocp.Paths
	Source        string
	Out           io.Writer
	Err           io.Writer
	Open          bool
	TrustedOrigin string
	OpenCode      string
	Apply         func() (ApplyResult, error)
	Models        func(context.Context) ([]ModelOption, error)
	OpenBrowser   func(string) error
	Listen        func(string, string) (net.Listener, error)
}

type handlerOptions struct {
	Store  Store
	Paths  ocp.Paths
	Token  string
	Origin string
	Host   string
	Apply  func() (ApplyResult, error)
	Models func(context.Context) ([]ModelOption, error)
}

func Run(o Options) error {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.Err == nil {
		o.Err = io.Discard
	}
	if o.Listen == nil {
		o.Listen = net.Listen
	}
	if o.OpenBrowser == nil {
		o.OpenBrowser = openBrowser
	}
	if o.Models == nil {
		o.Models = func(ctx context.Context) ([]ModelOption, error) { return discoverModels(ctx, o.OpenCode) }
	}
	listener, err := o.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	origin := "http://" + listener.Addr().String()
	if o.TrustedOrigin != "" {
		origin, err = trustedOrigin(o.TrustedOrigin)
		if err != nil {
			return err
		}
	}
	host := strings.TrimPrefix(origin, "http://")
	host = strings.TrimPrefix(host, "https://")
	token, err := sessionToken()
	if err != nil {
		return err
	}
	handler := newHandler(handlerOptions{Store: Store{Source: o.Source}, Paths: o.Paths, Token: token, Origin: origin, Host: host, Apply: o.Apply, Models: o.Models})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	browserURL := origin + "/"
	fmt.Fprintf(o.Out, "OCP UI running at:\n%s\n", browserURL)
	if o.Open {
		if err := o.OpenBrowser(browserURL); err != nil {
			fmt.Fprintf(o.Err, "Warning: could not open browser: %v\n", err)
		}
	}
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func newHandler(o handlerOptions) http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(webFiles, "web")
	static := http.FileServer(http.FS(assets))
	var modelCache struct {
		sync.Mutex
		models  []ModelOption
		expires time.Time
	}

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, err := webFiles.ReadFile("web/index.html")
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, strings.ReplaceAll(string(b), "__OCP_TOKEN__", o.Token))
	})
	mux.Handle("GET /app.js", static)
	mux.Handle("GET /style.css", static)

	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-OCP-Token") != o.Token {
			http.Error(w, "request rejected", http.StatusForbidden)
			return
		}
		state, err := loadState(o.Store, o.Paths)
		writeJSON(w, state, err)
	})
	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-OCP-Token") != o.Token {
			http.Error(w, "request rejected", http.StatusForbidden)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		modelCache.Lock()
		models := modelCache.models
		var err error
		if time.Now().After(modelCache.expires) {
			models, err = o.Models(ctx)
			if err == nil {
				modelCache.models = models
				modelCache.expires = time.Now().Add(5 * time.Minute)
			}
		}
		modelCache.Unlock()
		if err == nil {
			var snapshot Snapshot
			snapshot, err = o.Store.Snapshot()
			if err == nil {
				models = mergeModels(models, snapshot)
			}
		}
		writeJSON(w, models, err)
	})
	mux.HandleFunc("POST /api/profiles", secure(o, func(w http.ResponseWriter, r *http.Request) (any, error) {
		var req struct{ Revision, Name, Extends, Duplicate string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return o.Store.CreateProfile(req.Revision, req.Name, req.Extends, req.Duplicate)
	}))
	mux.HandleFunc("PUT /api/profiles/{name}", secure(o, func(w http.ResponseWriter, r *http.Request) (any, error) {
		var req struct {
			Revision string        `json:"revision"`
			Extends  string        `json:"extends"`
			Model    string        `json:"model"`
			Agents   []DirectAgent `json:"agents"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return o.Store.UpdateProfile(req.Revision, r.PathValue("name"), ProfileUpdate{Extends: req.Extends, Model: req.Model, Agents: req.Agents})
	}))
	mux.HandleFunc("POST /api/profiles/{name}/preview", secure(o, func(w http.ResponseWriter, r *http.Request) (any, error) {
		var req struct {
			Revision string        `json:"revision"`
			Extends  string        `json:"extends"`
			Model    string        `json:"model"`
			Agents   []DirectAgent `json:"agents"`
		}
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return o.Store.PreviewProfile(req.Revision, r.PathValue("name"), ProfileUpdate{Extends: req.Extends, Model: req.Model, Agents: req.Agents})
	}))
	mux.HandleFunc("DELETE /api/profiles/{name}", secure(o, func(w http.ResponseWriter, r *http.Request) (any, error) {
		var req struct{ Revision string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return o.Store.DeleteProfile(req.Revision, r.PathValue("name"))
	}))
	mux.HandleFunc("POST /api/agents", secure(o, func(w http.ResponseWriter, r *http.Request) (any, error) {
		var req struct{ Revision, Name, Content string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return o.Store.CreateAgent(req.Revision, req.Name, req.Content)
	}))
	mux.HandleFunc("PUT /api/agents/{name}", secure(o, func(w http.ResponseWriter, r *http.Request) (any, error) {
		var req struct{ Revision, Content string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return o.Store.UpdateAgent(req.Revision, r.PathValue("name"), req.Content)
	}))
	mux.HandleFunc("DELETE /api/agents/{name}", secure(o, func(w http.ResponseWriter, r *http.Request) (any, error) {
		var req struct{ Revision string }
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		return o.Store.DeleteAgent(req.Revision, r.PathValue("name"))
	}))
	mux.HandleFunc("POST /api/apply", secure(o, func(w http.ResponseWriter, r *http.Request) (any, error) {
		if o.Apply == nil {
			return nil, errors.New("apply is unavailable")
		}
		return o.Apply()
	}))
	mux.HandleFunc("POST /api/activate/{name}", secure(o, func(w http.ResponseWriter, r *http.Request) (any, error) {
		if err := ocp.Activate(o.Paths, r.PathValue("name")); err != nil {
			return nil, err
		}
		return loadState(o.Store, o.Paths)
	}))
	return securityHeaders(o.Host, mux)
}

func secure(o handlerOptions, fn func(http.ResponseWriter, *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Host != o.Host || r.Header.Get("Origin") != o.Origin || r.Header.Get("X-OCP-Token") != o.Token {
			http.Error(w, "request origin rejected", http.StatusForbidden)
			return
		}
		release, err := ocp.AcquireLock(o.Paths)
		if err != nil {
			http.Error(w, err.Error(), http.StatusLocked)
			return
		}
		defer release()
		value, err := fn(w, r)
		writeJSON(w, value, err)
	}
}

func securityHeaders(host string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host {
			http.Error(w, "request host rejected", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func trustedOrigin(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", errors.New("trusted origin must be an HTTPS origin")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("trusted origin must not contain a path, query, or fragment")
	}
	hostname := strings.ToLower(u.Hostname())
	authority := hostname
	if ip := net.ParseIP(hostname); ip != nil {
		hostname = ip.String()
		authority = "[" + hostname + "]"
	} else if !validHostname(hostname) {
		return "", errors.New("trusted origin must use an exact host and omit the default HTTPS port")
	}

	explicitPort := strings.HasPrefix(u.Host, "[") && !strings.HasSuffix(u.Host, "]") || !strings.HasPrefix(u.Host, "[") && strings.Contains(u.Host, ":")
	if explicitPort {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 || port == 443 {
			return "", errors.New("trusted origin must use a valid non-default HTTPS port")
		}
		authority += ":" + strconv.Itoa(port)
	}
	return "https://" + authority, nil
}

func validHostname(host string) bool {
	if len(host) > 253 || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

func loadState(store Store, paths ocp.Paths) (any, error) {
	snapshot, err := store.Snapshot()
	if err != nil {
		return nil, err
	}
	active, _ := ocp.ActiveProfile(paths)
	return struct {
		Snapshot
		Active string `json:"active,omitempty"`
	}{Snapshot: snapshot, Active: active}, nil
}

func decode(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid request: expected one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		status = http.StatusConflict
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func sessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, args = "xdg-open", []string{url}
	}
	return exec.Command(name, args...).Start()
}
