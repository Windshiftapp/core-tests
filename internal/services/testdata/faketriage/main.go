// faketriage is a test double for the windshift-triage binary. It accepts the
// same prepare/push flags TriageRunner passes and emits the same JSON shapes,
// without touching real git. prepare creates a throwaway checkout dir holding a
// PREPARED marker; push drops a "<dest>.pushed" sibling marker (a sibling, so
// it survives TriageRunner's post-run RemoveAll of the checkout).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "faketriage: missing subcommand")
		os.Exit(2)
	}
	sub := os.Args[1]
	fs := flag.NewFlagSet(sub, flag.ContinueOnError)
	// union of flags both subcommands may receive
	fs.String("root", "", "")
	fs.Int("workspace-id", 0, "")
	fs.String("repo", "", "")
	fs.String("remote-url", "", "")
	fs.String("base-ref", "", "")
	runID := fs.Int("run-id", 0, "")
	fs.String("token-file", "", "")
	dest := fs.String("dest", "", "")
	fs.String("branch", "", "")
	fs.String("git-transport", "askpass", "")
	fs.String("proxy-url", "", "")
	fs.Bool("allow-file-url", false, "")
	skipIfHead := fs.String("skip-if-head", "", "")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	switch sub {
	case "prepare":
		d, err := os.MkdirTemp("", "faketriage-checkout-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, "faketriage:", err)
			os.Exit(1)
		}
		_ = os.WriteFile(d+"/PREPARED", []byte("x"), 0o644)
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"checkout_path": d,
			"branch":        fmt.Sprintf("agent-runs/run-%d", *runID),
			"base_commit":   "base123",
		})
	case "push":
		if *dest == "" {
			fmt.Fprintln(os.Stderr, "faketriage: push needs --dest")
			os.Exit(2)
		}
		// FAKETRIAGE_NO_COMMITS simulates a commit-less run: the branch head
		// still equals the base SHA passed via --skip-if-head, so the real
		// triage push would short-circuit without pushing.
		if os.Getenv("FAKETRIAGE_NO_COMMITS") == "1" && *skipIfHead != "" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"head_sha": "", "skipped": true})
			return
		}
		_ = os.WriteFile(*dest+".pushed", []byte("x"), 0o644)
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"head_sha": "deadbeef", "skipped": false})
	default:
		fmt.Fprintln(os.Stderr, "faketriage: unknown subcommand", sub)
		os.Exit(2)
	}
}
