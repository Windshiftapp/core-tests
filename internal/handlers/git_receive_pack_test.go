package handlers

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
)

// pktLine encodes a git pkt-line (4-hex length prefix + payload).
func pktLine(payload string) string {
	return fmt.Sprintf("%04x%s", len(payload)+4, payload)
}

const flushPkt = "0000"

// fakePack is a stand-in for the packfile that follows the command list; the
// broker must forward it untouched without parsing it.
var fakePack = []byte("PACK\x00\x00\x00\x02fakepackdata")

func receivePackBody(commands ...string) []byte {
	var b bytes.Buffer
	for _, c := range commands {
		b.WriteString(pktLine(c))
	}
	b.WriteString(flushPkt)
	b.Write(fakePack)
	return b.Bytes()
}

func TestParseReceivePackCommands(t *testing.T) {
	body := receivePackBody(
		"0000000000000000000000000000000000000000 1111111111111111111111111111111111111111 refs/heads/run-42\x00report-status side-band-64k",
		"2222222222222222222222222222222222222222 3333333333333333333333333333333333333333 refs/heads/other",
	)
	cmds, err := parseReceivePackCommands(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("want 2 commands, got %d", len(cmds))
	}
	if cmds[0].Ref != "refs/heads/run-42" {
		t.Errorf("cmd0 ref: got %q (capabilities not stripped?)", cmds[0].Ref)
	}
	if cmds[1].Ref != "refs/heads/other" {
		t.Errorf("cmd1 ref: got %q", cmds[1].Ref)
	}
}

func TestParseReceivePackCommands_Malformed(t *testing.T) {
	// Length header claims more bytes than are present → must error (fail closed).
	cmds, err := parseReceivePackCommands(bytes.NewReader([]byte("0050 short")))
	if err == nil {
		t.Fatalf("expected error on malformed pkt-line, got %v", cmds)
	}
}

func TestAuthorizeGitPush_AllowsGrantedRefAndReplaysBody(t *testing.T) {
	body := receivePackBody(
		"0000000000000000000000000000000000000000 1111111111111111111111111111111111111111 refs/heads/run-42\x00report-status",
	)
	r := httptest.NewRequest("POST", "/git-proxy/1/acme/widgets/git-receive-pack", bytes.NewReader(body))
	grants := &models.RunGrants{Git: &models.GitGrant{Repo: "acme/widgets", Ref: "refs/heads/run-42"}}

	h := &RunnerBrokerHandler{}
	replay, err := h.authorizeGitPush(r, grants, "acme/widgets")
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	got, _ := io.ReadAll(replay)
	if !bytes.Equal(got, body) {
		t.Fatalf("replayed body must equal original (packfile preserved)\n got %q\nwant %q", got, body)
	}
}

func TestAuthorizeGitPush_RejectsUngrantedRef(t *testing.T) {
	body := receivePackBody(
		"0000000000000000000000000000000000000000 1111111111111111111111111111111111111111 refs/heads/main\x00report-status",
	)
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	grants := &models.RunGrants{Git: &models.GitGrant{Repo: "acme/widgets", Ref: "refs/heads/run-42"}}

	h := &RunnerBrokerHandler{}
	if _, err := h.authorizeGitPush(r, grants, "acme/widgets"); err == nil {
		t.Fatal("push to an un-granted ref must be rejected")
	}
}

func TestAuthorizeGitPush_GzipBodyReplaysExactBytes(t *testing.T) {
	body := receivePackBody(
		"0000000000000000000000000000000000000000 1111111111111111111111111111111111111111 refs/heads/run-42\x00report-status",
	)
	var gzbuf bytes.Buffer
	gw := gzip.NewWriter(&gzbuf)
	if _, err := gw.Write(body); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	compressed := gzbuf.Bytes()

	r := httptest.NewRequest("POST", "/", bytes.NewReader(compressed))
	r.Header.Set("Content-Encoding", "gzip")
	grants := &models.RunGrants{Git: &models.GitGrant{Repo: "acme/widgets", Ref: "refs/heads/run-42"}}

	h := &RunnerBrokerHandler{}
	replay, err := h.authorizeGitPush(r, grants, "acme/widgets")
	if err != nil {
		t.Fatalf("authorize gzip: %v", err)
	}
	got, _ := io.ReadAll(replay)
	if !bytes.Equal(got, compressed) {
		t.Fatal("replayed gzip body must equal original compressed bytes")
	}
}
