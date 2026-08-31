//go:build test

package fileserve_test

import (
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"windshift/internal/fileserve"
)

// writeFile creates path (and parents) with the given content under t's control.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readAllAndClose(t *testing.T, f *os.File) string {
	t.Helper()
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

func TestOpenUnderRoot_NormalRelativePath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "items", "1", "file.txt"), "hello")

	f, err := fileserve.OpenUnderRoot(root, "items/1/file.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readAllAndClose(t, f); got != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
}

func TestOpenUnderRoot_LegacyAbsolutePathUnderRoot(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "items", "2", "file.txt")
	writeFile(t, abs, "legacy")

	// Older rows stored the full absolute path; it must still resolve as long
	// as it lives inside root.
	f, err := fileserve.OpenUnderRoot(root, abs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readAllAndClose(t, f); got != "legacy" {
		t.Fatalf("content = %q, want %q", got, "legacy")
	}
}

func TestOpenUnderRoot_RelativeRootResolvesAgainstCWD(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, filepath.Join(dir, "uploads", "a.txt"), "rel")

	// Default/e2e setup: root is itself a relative path and rows were stored
	// as "uploads/a.txt" relative to the working directory.
	f, err := fileserve.OpenUnderRoot("uploads", "uploads/a.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := readAllAndClose(t, f); got != "rel" {
		t.Fatalf("content = %q, want %q", got, "rel")
	}
}

func TestOpenUnderRoot_RejectsDotDotTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	writeFile(t, filepath.Join(root, "ok.txt"), "ok")
	// A secret sibling of root that "../" would reach.
	writeFile(t, filepath.Join(filepath.Dir(root), "secret.txt"), "secret")

	if _, err := fileserve.OpenUnderRoot(root, "../secret.txt"); !errors.Is(err, fileserve.ErrOutsideRoot) {
		t.Fatalf("err = %v, want ErrOutsideRoot", err)
	}
}

func TestOpenUnderRoot_RejectsAbsolutePathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "nope")

	if _, err := fileserve.OpenUnderRoot(root, outside); !errors.Is(err, fileserve.ErrOutsideRoot) {
		t.Fatalf("err = %v, want ErrOutsideRoot", err)
	}
}

func TestOpenUnderRoot_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	secretDir := t.TempDir()
	writeFile(t, filepath.Join(secretDir, "passwd"), "root:x:0:0")

	// Plant a symlink inside root that points outside it.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(secretDir, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	f, err := fileserve.OpenUnderRoot(root, "escape/passwd")
	if err == nil {
		content := readAllAndClose(t, f)
		t.Fatalf("expected symlink escape to be rejected; got content %q", content)
	}
}

func TestOpenUnderRoot_MissingFileIsNotExist(t *testing.T) {
	root := t.TempDir()
	_, err := fileserve.OpenUnderRoot(root, "items/9/missing.txt")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestOpenUnderRoot_EmptyRoot(t *testing.T) {
	if _, err := fileserve.OpenUnderRoot("", "anything.txt"); !errors.Is(err, fileserve.ErrOutsideRoot) {
		t.Fatalf("err = %v, want ErrOutsideRoot", err)
	}
}

func TestRemoveUnderRoot_RemovesFileUnderRoot(t *testing.T) {
	root := t.TempDir()
	stored := filepath.Join(root, "items", "1", "file.txt")
	writeFile(t, stored, "bye")

	if err := fileserve.RemoveUnderRoot(root, "items/1/file.txt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(stored); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file still present after remove: stat err = %v", err)
	}
}

func TestRemoveUnderRoot_LegacyAbsolutePathUnderRoot(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "items", "2", "file.txt")
	writeFile(t, abs, "legacy")

	if err := fileserve.RemoveUnderRoot(root, abs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(abs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file still present after remove: stat err = %v", err)
	}
}

func TestRemoveUnderRoot_RejectsDotDotTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	writeFile(t, filepath.Join(root, "ok.txt"), "ok")
	secret := filepath.Join(filepath.Dir(root), "secret.txt")
	writeFile(t, secret, "secret")

	if err := fileserve.RemoveUnderRoot(root, "../secret.txt"); !errors.Is(err, fileserve.ErrOutsideRoot) {
		t.Fatalf("err = %v, want ErrOutsideRoot", err)
	}
	// The escape target must survive a refused removal.
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("secret file should be untouched: %v", err)
	}
}

func TestRemoveUnderRoot_RejectsAbsolutePathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "nope")

	if err := fileserve.RemoveUnderRoot(root, outside); !errors.Is(err, fileserve.ErrOutsideRoot) {
		t.Fatalf("err = %v, want ErrOutsideRoot", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file should be untouched: %v", err)
	}
}

func TestRemoveUnderRoot_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	secretDir := t.TempDir()
	target := filepath.Join(secretDir, "passwd")
	writeFile(t, target, "root:x:0:0")

	link := filepath.Join(root, "escape")
	if err := os.Symlink(secretDir, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	if err := fileserve.RemoveUnderRoot(root, "escape/passwd"); err == nil {
		t.Fatalf("expected symlink escape to be rejected")
	}
	// The file behind the symlink must not have been deleted.
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target should be untouched: %v", err)
	}
}

func TestRemoveUnderRoot_MissingFileIsNotExist(t *testing.T) {
	root := t.TempDir()
	if err := fileserve.RemoveUnderRoot(root, "items/9/missing.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestRemoveUnderRoot_EmptyRoot(t *testing.T) {
	if err := fileserve.RemoveUnderRoot("", "anything.txt"); !errors.Is(err, fileserve.ErrOutsideRoot) {
		t.Fatalf("err = %v, want ErrOutsideRoot", err)
	}
}

// parsedFilename runs the header value back through mime.ParseMediaType (the
// same primitive a browser/HTTP client uses) and returns the decoded filename
// parameter plus the parameter count, so tests assert on what a client sees.
func parsedFilename(t *testing.T, header string) (string, map[string]string) {
	t.Helper()
	mediatype, params, err := mime.ParseMediaType(header)
	if err != nil {
		t.Fatalf("ParseMediaType(%q): %v", header, err)
	}
	if mediatype != "attachment" && mediatype != "inline" {
		t.Fatalf("unexpected disposition %q in %q", mediatype, header)
	}
	return params["filename"], params
}

func TestContentDisposition_PlainASCII(t *testing.T) {
	got := fileserve.ContentDisposition("attachment", "report.pdf")
	name, _ := parsedFilename(t, got)
	if name != "report.pdf" {
		t.Fatalf("filename = %q, want %q (header %q)", name, "report.pdf", got)
	}
}

func TestContentDisposition_QuotesDoNotBreakHeader(t *testing.T) {
	got := fileserve.ContentDisposition("attachment", `evil".pdf`)
	name, _ := parsedFilename(t, got)
	if name != `evil".pdf` {
		t.Fatalf("filename = %q, want %q (header %q)", name, `evil".pdf`, got)
	}
}

func TestContentDisposition_SemicolonCannotInjectParameter(t *testing.T) {
	// A naive `filename="..."` would let this inject a second parameter.
	got := fileserve.ContentDisposition("attachment", `a.pdf"; filename="b.exe`)
	name, params := parsedFilename(t, got)
	if len(params) != 1 {
		t.Fatalf("expected exactly one parameter, got %v (header %q)", params, got)
	}
	if name != `a.pdf"; filename="b.exe` {
		t.Fatalf("filename = %q, want the whole hostile string (header %q)", name, got)
	}
}

func TestContentDisposition_StripsCRLF(t *testing.T) {
	got := fileserve.ContentDisposition("inline", "a\r\nSet-Cookie: x=1.txt")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("header contains raw CR/LF: %q", got)
	}
	name, _ := parsedFilename(t, got)
	if name != "aSet-Cookie: x=1.txt" {
		t.Fatalf("filename = %q, want CR/LF stripped (header %q)", name, got)
	}
}

func TestContentDisposition_NonASCII(t *testing.T) {
	got := fileserve.ContentDisposition("attachment", "résumé.pdf")
	name, _ := parsedFilename(t, got)
	if name != "résumé.pdf" {
		t.Fatalf("filename = %q, want %q (header %q)", name, "résumé.pdf", got)
	}
}
