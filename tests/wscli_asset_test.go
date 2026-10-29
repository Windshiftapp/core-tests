// wscli_asset_test exercises the ws asset / asset-set / asset-type
// subcommands by driving wscli.Run in-process against an isolated test
// server. Asserts the CLI wiring (cobra subcommand registration, client
// REST helpers, JSON output) is intact end-to-end. Functional v1
// coverage lives in v1_assets_test.go — this file is the CLI canary.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"windshift/internal/wscli"
)

func TestWSCLI_Asset_SetListAndCreateGetEdit(t *testing.T) {
	ts, _ := StartTestServer(t, GetDBType())
	_ = CreateBearerToken(t, ts)
	// Default admin bearer carries assets:read+write via legacy 'admin'
	// expansion, so ts.BearerToken works for everything the CLI exposes
	// (delete is no longer a CLI verb — see internal/wscli/asset.go).
	token := ts.BearerToken
	setID, assetTypeID := seedAssetSetAndType(t, ts, "wscli")

	env := map[string]string{"WS_URL": ts.BaseURL, "WS_TOKEN": token}

	// `ws asset-set ls` should surface the seeded set.
	{
		var stdout, stderr bytes.Buffer
		code := wscli.Run(
			context.Background(),
			[]string{"asset-set", "ls", "-o", "json"},
			nil, &stdout, &stderr, env,
		)
		if code != 0 {
			t.Fatalf("asset-set ls: %d, stderr=%s", code, stderr.String())
		}
		var sets []map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &sets); err != nil {
			t.Fatalf("decode asset-set ls: %v\nraw=%s", err, stdout.String())
		}
		var found bool
		for _, s := range sets {
			if id, ok := s["id"].(float64); ok && int(id) == setID {
				found = true
			}
		}
		if !found {
			t.Fatalf("seeded set %d missing from ws asset-set ls output: %s", setID, stdout.String())
		}
	}

	// `ws asset-type ls --set <id>` should list types in the set.
	{
		var stdout, stderr bytes.Buffer
		code := wscli.Run(
			context.Background(),
			[]string{"asset-type", "ls", "--set", itoa(setID), "-o", "json"},
			nil, &stdout, &stderr, env,
		)
		if code != 0 {
			t.Fatalf("asset-type ls: %d, stderr=%s", code, stderr.String())
		}
		var types []map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &types); err != nil {
			t.Fatalf("decode asset-type ls: %v\nraw=%s", err, stdout.String())
		}
		if len(types) == 0 {
			t.Fatalf("expected at least one type in set %d, got 0", setID)
		}
	}

	// `ws asset create` returns the created asset as JSON.
	var createdID int
	{
		var stdout, stderr bytes.Buffer
		code := wscli.Run(
			context.Background(),
			[]string{
				"asset", "create",
				"--set", itoa(setID),
				"--type", itoa(assetTypeID),
				"-t", "Macbook Pro 16",
				"-d", "Apple M3 Max",
				"--tag", "MBP-001",
				"-o", "json",
			},
			nil, &stdout, &stderr, env,
		)
		if code != 0 {
			t.Fatalf("asset create: %d, stderr=%s", code, stderr.String())
		}
		var asset map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &asset); err != nil {
			t.Fatalf("decode asset create: %v\nraw=%s", err, stdout.String())
		}
		id, ok := asset["id"].(float64)
		if !ok {
			t.Fatalf("asset create response missing id: %s", stdout.String())
		}
		createdID = int(id)
		if title := asset["title"]; title != "Macbook Pro 16" {
			t.Fatalf("title mismatch: got %v", title)
		}
	}

	// `ws asset get <id>` returns the same row.
	{
		var stdout, stderr bytes.Buffer
		code := wscli.Run(
			context.Background(),
			[]string{"asset", "get", itoa(createdID), "-o", "json"},
			nil, &stdout, &stderr, env,
		)
		if code != 0 {
			t.Fatalf("asset get: %d, stderr=%s", code, stderr.String())
		}
		var asset map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &asset); err != nil {
			t.Fatalf("decode asset get: %v\nraw=%s", err, stdout.String())
		}
		if id := int(asset["id"].(float64)); id != createdID {
			t.Fatalf("id mismatch: got %d, want %d", id, createdID)
		}
	}

	// `ws asset edit <id>` partial-updates title; description must survive.
	{
		var stdout, stderr bytes.Buffer
		code := wscli.Run(
			context.Background(),
			[]string{"asset", "edit", itoa(createdID), "-t", "Macbook Pro 16-inch", "-o", "json"},
			nil, &stdout, &stderr, env,
		)
		if code != 0 {
			t.Fatalf("asset edit: %d, stderr=%s", code, stderr.String())
		}
		var asset map[string]interface{}
		if err := json.Unmarshal(stdout.Bytes(), &asset); err != nil {
			t.Fatalf("decode asset edit: %v\nraw=%s", err, stdout.String())
		}
		if title := asset["title"]; title != "Macbook Pro 16-inch" {
			t.Fatalf("edit didn't take: got %v", title)
		}
		if desc := asset["description"]; desc != "Apple M3 Max" {
			t.Fatalf("description was clobbered by partial edit: got %v", desc)
		}
	}

	// `ws asset ls --set <id>` should now show 1 row.
	{
		var stdout, stderr bytes.Buffer
		code := wscli.Run(
			context.Background(),
			[]string{"asset", "ls", "--set", itoa(setID), "-o", "json"},
			nil, &stdout, &stderr, env,
		)
		if code != 0 {
			t.Fatalf("asset ls: %d, stderr=%s", code, stderr.String())
		}
		var resp struct {
			Data []map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
			t.Fatalf("decode asset ls: %v\nraw=%s", err, stdout.String())
		}
		if len(resp.Data) != 1 {
			t.Fatalf("expected 1 row, got %d", len(resp.Data))
		}
	}

	// Asset delete is not exposed on the CLI — assets:delete isn't in
	// the default scope set, so 'ws asset rm' would 403 out of the box.
	// Operators with the explicit scope use curl. Cleanup happens via
	// the cookie-auth admin surface (test teardown drops the DB).
	_ = createdID
}

// TestWSCLI_Asset_HelpRegistration is a fast canary that just runs --help
// against the three new commands. If a flag definition or init() call is
// broken, this fails before the heavier tests even seed.
func TestWSCLI_Asset_HelpRegistration(t *testing.T) {
	for _, cmd := range [][]string{
		{"asset", "--help"},
		{"asset-set", "--help"},
		{"asset-type", "--help"},
	} {
		var stdout, stderr bytes.Buffer
		code := wscli.Run(context.Background(), cmd, nil, &stdout, &stderr, nil)
		if code != 0 {
			t.Fatalf("ws %s: exit %d, stderr=%s", strings.Join(cmd, " "), code, stderr.String())
		}
		if !strings.Contains(stdout.String()+stderr.String(), cmd[0]) {
			t.Fatalf("ws %s help output didn't reference %q: stdout=%q stderr=%q",
				strings.Join(cmd, " "), cmd[0], stdout.String(), stderr.String())
		}
	}
}
