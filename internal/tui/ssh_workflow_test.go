//go:build test

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/wish/v2"
	wishbubbletea "charm.land/wish/v2/bubbletea"
	"charm.land/wish/v2/testsession"
	gossh "golang.org/x/crypto/ssh"
)

func TestSSHWorkflowOpensWorkspaceBoardAndQuits(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(serveTUIWorkflowAPI))
	t.Cleanup(api.Close)

	server, err := wish.NewServer(
		wish.WithHostKeyPath(t.TempDir()+"/id_ed25519"),
		wish.WithMiddleware(wishbubbletea.Middleware(NewTUIHandler(api.URL, nil))),
	)
	if err != nil {
		t.Fatalf("create SSH server: %v", err)
	}

	session := testsession.New(t, server, nil)
	if err := session.RequestPty("xterm-256color", 30, 120, gossh.TerminalModes{gossh.ECHO: 0}); err != nil {
		t.Fatalf("request PTY: %v", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("open SSH stdin: %v", err)
	}
	output := newSessionOutput()
	session.Stdout = output
	session.Stderr = output
	if err := session.Shell(); err != nil {
		t.Fatalf("start SSH shell: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := output.waitFor(ctx, "DEMO · Delivery Workspace"); err != nil {
		t.Fatalf("workspace picker did not render: %v\noutput:\n%s", err, output.String())
	}
	if _, err := io.WriteString(stdin, "\r"); err != nil {
		t.Fatalf("select workspace: %v", err)
	}
	if err := output.waitFor(ctx, "WI-42"); err != nil {
		t.Fatalf("board did not render the canonical item key: %v\noutput:\n%s", err, output.String())
	}
	if _, err := stdin.Write([]byte{3}); err != nil {
		t.Fatalf("send Ctrl+C: %v", err)
	}

	waited := make(chan error, 1)
	go func() { waited <- session.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("SSH session did not exit cleanly: %v\noutput:\n%s", err, output.String())
		}
	case <-ctx.Done():
		t.Fatalf("SSH session did not exit after Ctrl+C: %v\noutput:\n%s", ctx.Err(), output.String())
	}
}

func serveTUIWorkflowAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/users/me/preferences":
		_, _ = io.WriteString(w, `{"data":{"color_mode":"system","theme_id":null,"theme":null,"tui":{}}}`)
	case r.Method == http.MethodPatch && r.URL.Path == "/rest/api/v2/users/me/preferences":
		_, _ = io.WriteString(w, `{"data":{"color_mode":"system","theme_id":null,"theme":null,"tui":{}}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/users/me":
		_, _ = io.WriteString(w, `{"data":{"timezone":"UTC"}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/workspaces":
		_, _ = io.WriteString(w, `{"data":[{"id":7,"name":"Delivery Workspace","key":"DEMO","description":"","active":true}],"pagination":{"page":1,"page_size":100,"total_items":1,"total_pages":1}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/items":
		_, _ = io.WriteString(w, `{"data":[{"id":900,"workspace_id":7,"workspace_key":"DEMO","key":"WI-42","workspace_item_number":42,"title":"SSH workflow item","description":"Rendered over SSH","status":{"id":3,"name":"In Progress","category_id":2,"category_name":"Active","category_color":"#3b82f6"},"priority":{"id":2,"name":"Medium"},"created_at":"2026-09-02T08:00:00Z","updated_at":"2026-09-02T08:00:00Z"}],"pagination":{"page":1,"page_size":100,"total_items":1,"total_pages":1}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/workspaces/7/statuses":
		_, _ = io.WriteString(w, `{"data":[{"id":3,"name":"In Progress","category":{"id":2,"name":"Active","color":"#3b82f6"}}]}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/priorities":
		_, _ = io.WriteString(w, `{"data":[{"id":2,"name":"Medium"}]}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/time/projects":
		_, _ = io.WriteString(w, `{"data":[]}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/workspaces/7/assignable-users":
		_, _ = io.WriteString(w, `{"data":[]}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/items/900":
		_, _ = io.WriteString(w, `{"data":{"id":900,"workspace_id":7,"workspace_key":"DEMO","key":"WI-42","workspace_item_number":42,"title":"SSH workflow item","description":"Rendered over SSH","status":{"id":3,"name":"In Progress","category_id":2,"category_name":"Active","category_color":"#3b82f6"},"priority":{"id":2,"name":"Medium"},"transitions":[],"created_at":"2026-09-02T08:00:00Z","updated_at":"2026-09-02T08:00:00Z"}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/items/900/comments":
		_, _ = io.WriteString(w, `{"data":{"comments":[],"has_more":false}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/items/900/agent-runs":
		_, _ = io.WriteString(w, `{"data":{"runs":[],"has_more":false}}`)
	default:
		http.Error(w, fmt.Sprintf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery), http.StatusNotFound)
	}
}

type sessionOutput struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	changed chan struct{}
}

func newSessionOutput() *sessionOutput {
	return &sessionOutput{changed: make(chan struct{}, 1)}
}

func (o *sessionOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	n, err := o.buffer.Write(p)
	o.mu.Unlock()
	select {
	case o.changed <- struct{}{}:
	default:
	}
	return n, err
}

func (o *sessionOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.String()
}

func (o *sessionOutput) waitFor(ctx context.Context, text string) error {
	for {
		if strings.Contains(o.String(), text) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-o.changed:
		}
	}
}
