package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

func testCatalog() bottle.Catalog {
	return bottle.Catalog{
		Generated: "2026-10-05T09:00:00Z",
		Projects: []bottle.CatalogProject{
			{Project: "zlib.net", Versions: []string{"1.3.2"}},
			{Project: "curl.se", Versions: []string{"8.17.0"}},
			{Project: "curl.se/ca-certs", Versions: []string{"2026.09.25"}},
			{Project: "gnu.org/bash", Versions: []string{"5.3"}},
			{Project: "gnu.org/gcc", Versions: []string{"16.2.0"}},
		},
	}
}

func withCatalog(t *testing.T, c bottle.Catalog, err error) {
	t.Helper()
	prev := catalogFor
	catalogFor = func() (bottle.Catalog, error) { return c, err }
	t.Cleanup(func() { catalogFor = prev })
}

func TestLsShowsWhatIsUnderANode(t *testing.T) {
	withCatalog(t, testCatalog(), nil)
	var out, errb bytes.Buffer
	if code := runLs(nil, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"curl.se", "gnu.org", "zlib.net"} {
		if !strings.Contains(got, want) {
			t.Errorf("the roots do not include %s:\n%s", want, got)
		}
	}
	// curl.se is a package AND a namespace, and the line has to say both.
	if !strings.Contains(got, "8.17.0, 1 under") {
		t.Errorf("curl.se's line does not say it is both:\n%s", got)
	}
	// The header says which catalogue, and how old.
	if !strings.Contains(errb.String(), "registry catalogue") {
		t.Errorf("no provenance header: %q", errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := runLs([]string{"gnu.org"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "gnu.org/bash") || strings.Contains(out.String(), "zlib.net") {
		t.Errorf("ls gnu.org = %q", out.String())
	}
}

// A leaf and an absent node must not print alike: one is an answer, the
// other is a mistake the user can fix.
func TestLsDistinguishesALeafFromNothing(t *testing.T) {
	withCatalog(t, testCatalog(), nil)
	var out, errb bytes.Buffer
	if code := runLs([]string{"zlib.net"}, &out, &errb); code != 0 {
		t.Fatalf("a leaf exited %d", code)
	}
	if !strings.Contains(out.String(), "is a package, not a namespace") || !strings.Contains(out.String(), "1.3.2") {
		t.Errorf("leaf = %q", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := runLs([]string{"nope.invalid"}, &out, &errb); code != 1 {
		t.Errorf("an absent node exited %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "nothing under") {
		t.Errorf("absent = %q", errb.String())
	}

	if code := runLs([]string{"a", "b"}, &out, &errb); code != 2 {
		t.Errorf("two nodes exited %d, want 2", code)
	}
}

// With no registry, the store is the answer — and the header SAYS so, so a
// user is never told "available" about a list that is only what they have.
func TestLsFallsBackToTheStoreAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"gnu.org/bash/v5.3", "gnu.org/make/v4.4.1", "zlib.net/v1.3.2", ".local/tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PKGX_DIR", dir)
	withCatalog(t, bottle.Catalog{}, os.ErrDeadlineExceeded)

	var out, errb bytes.Buffer
	if code := runLs(nil, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "showing what is INSTALLED") {
		t.Errorf("the fallback did not say what it is showing: %q", errb.String())
	}
	if !strings.Contains(out.String(), "gnu.org") || !strings.Contains(out.String(), "zlib.net") {
		t.Errorf("the store was not read:\n%s", out.String())
	}
	// A dot-directory is not a namespace. $PKGX_DIR holds .local/tmp, and
	// listing it told the user there is a package family called `.local`.
	if strings.Contains(out.String(), ".local") {
		t.Errorf("a dot-directory was listed as a namespace:\n%s", out.String())
	}
}

// The off-by-one that the first real run exposed: with the index wrong,
// `pkgx +gnu.org/ba<TAB>` reads past the arguments, finds "", and offers the
// subcommand list.
func TestCompletionIndexesTheArgumentsPkgxReceives(t *testing.T) {
	withCatalog(t, testCatalog(), nil)
	run := func(n string, argv ...string) string {
		var out bytes.Buffer
		env := func(k string) string {
			if k == completionsEnv {
				return n
			}
			return ""
		}
		if !maybeComplete(argv, env, &out) {
			t.Fatal("the completion request was not answered")
		}
		return out.String()
	}
	if got := run("0", "+gnu.org/ba"); !strings.HasPrefix(got, "+gnu.org/bash\t") {
		t.Errorf("index 0 should complete the FIRST argument; got %q", got)
	}
	// The plus is kept: the shell replaces the whole word, and a candidate
	// without it would turn `pkgx +x` into `pkgx x` — a different command.
	if strings.Contains(run("0", "+gnu.org/ba"), "\ngnu.org/bash") {
		t.Error("a +word completed to a bare project")
	}
	// A namespace completes WITH its slash, so the next TAB descends.
	if got := run("0", "gnu.o"); !strings.HasPrefix(got, "gnu.org/\t") {
		t.Errorf("a namespace did not complete with its slash: %q", got)
	}
	// Second argument.
	if got := run("1", "+zlib.net", "+curl"); !strings.Contains(got, "+curl.se") {
		t.Errorf("index 1 = %q", got)
	}
}

func TestCompletionIsSilentUnlessAsked(t *testing.T) {
	var out bytes.Buffer
	if maybeComplete([]string{"ls"}, func(string) string { return "" }, &out) {
		t.Error("answered a completion nobody asked for")
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q", out.String())
	}
	// A malformed request is answered with NOTHING rather than a
	// diagnostic: the other end is a shell's completion buffer, and text it
	// did not ask for lands in the user's prompt.
	for _, bad := range []string{"x", "-1"} {
		out.Reset()
		if !maybeComplete([]string{"ls"}, func(string) string { return bad }, &out) {
			t.Errorf("%q was not treated as a completion request", bad)
		}
		if out.Len() != 0 {
			t.Errorf("%q produced %q", bad, out.String())
		}
	}
	// An index past the end is the empty word, not a crash.
	out.Reset()
	withCatalog(t, testCatalog(), nil)
	if !maybeComplete([]string{"a"}, func(string) string { return "99" }, &out) {
		t.Error("not answered")
	}
}

// The snippets exist for the three shells and each one calls the binary
// with an index one less than its own word counter.
func TestCompletionSnippets(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		var out, errb bytes.Buffer
		if code := runCompletionSnippet(sh, &out, &errb); code != 0 {
			t.Fatalf("%s: code=%d %s", sh, code, errb.String())
		}
		s := out.String()
		if !strings.Contains(s, completionsEnv+"=") {
			t.Errorf("%s snippet does not set %s:\n%s", sh, completionsEnv, s)
		}
		// Every shell counts its words WITH the command; pkgx does not see
		// that word. A snippet that forgot to subtract is the defect this
		// asserts away.
		if !strings.Contains(s, "- 1)") && !strings.Contains(s, "- 2)") {
			t.Errorf("%s snippet does not adjust the word index:\n%s", sh, s)
		}
	}
	var out, errb bytes.Buffer
	if code := runCompletionSnippet("tcsh", &out, &errb); code != 2 {
		t.Errorf("an unknown shell exited %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "bash|zsh|fish") {
		t.Errorf("it does not say which shells: %q", errb.String())
	}
}
