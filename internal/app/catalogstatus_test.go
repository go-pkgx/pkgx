package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ TestCatalogStatusNamesTheCatalogueItWouldRead is the whole point of
// `pkgs catalog`. It answers "what am I looking at", and it used to answer
// about the host's file while `ls`, `search` and <TAB> — which go through
// catalogFor — had just read the one PKGX_CATALOG names. That is the defect
// go-pkgx/pkgx#110 describes, in the one place where a wrong answer cannot be
// caught by comparing it with anything else on screen.
//
// Observed before the fix: `PKGX_CATALOG=…/linux-x86-64.json pkgs catalog`
// printed `…/darwin-aarch64.json` and that file's project count.
func TestCatalogStatusNamesTheCatalogueItWouldRead(t *testing.T) {
	dir := t.TempDir()
	named := filepath.Join(dir, "linux-x86-64.json")
	const catalogue = `{"generated":"2026-11-01T09:00:00Z","projects":[` +
		`{"project":"openssl.org","versions":["3.5.8"]},` +
		`{"project":"zlib.net","versions":["1.3.2"],"deps":["gnu.org/make"]}]}`
	if err := os.WriteFile(named, []byte(catalogue), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PKGX_CATALOG", named)

	var out, errOut bytes.Buffer
	if code := catalogStatus(&out, &errOut); code != 0 {
		t.Fatalf("catalogStatus = %d, stderr %q", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, named) {
		t.Errorf("it does not name the catalogue it would read.\nwant %q in:\n%s", named, got)
	}
	// And the FIGURES have to come from that file too — naming one catalogue
	// while counting another would be the same defect with a better label.
	if !strings.Contains(got, "2 project(s), 1 with dependencies") {
		t.Errorf("the counts are not the named file's:\n%s", got)
	}
}

// ⛔ TestCatalogStatusRefusesANamedFileItCannotRead: named and unreadable is a
// user's mistake, and browseCatalog already refuses to paper over it. Printing
// "run catalog update" here would point at a file that is not the one they
// named, and exit 0 would call a mistake a fresh machine.
func TestCatalogStatusRefusesANamedFileItCannotRead(t *testing.T) {
	t.Setenv("PKGX_CATALOG", filepath.Join(t.TempDir(), "absent.json"))

	var out, errOut bytes.Buffer
	if code := catalogStatus(&out, &errOut); code == 0 {
		t.Errorf("catalogStatus = 0 for a named file that is absent; stdout was %q", out.String())
	}
	if !strings.Contains(errOut.String(), "PKGX_CATALOG") {
		t.Errorf("the refusal does not name the variable: %q", errOut.String())
	}
	if strings.Contains(out.String(), "catalog update") {
		t.Errorf("it suggested updating a catalogue the user did not name:\n%s", out.String())
	}
}

// TestCatalogStatusStillGuidesAFreshMachine: with no variable and no file, the
// useful output is the path and the command, not an error. Every fresh machine
// is in this state, so it must not be a failure.
func TestCatalogStatusStillGuidesAFreshMachine(t *testing.T) {
	t.Setenv("PKGX_CATALOG", "")
	t.Setenv("PKGX_DIR", t.TempDir())

	var out, errOut bytes.Buffer
	if code := catalogStatus(&out, &errOut); code != 0 {
		t.Fatalf("catalogStatus = %d on a fresh machine, stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "catalog update") {
		t.Errorf("it does not say how to fix it:\n%s", out.String())
	}
}
