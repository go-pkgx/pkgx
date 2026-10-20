package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

func cmdCatalog() bottle.Catalog {
	return bottle.Catalog{
		Generated: "2026-10-06T09:00:00Z",
		Projects: []bottle.CatalogProject{
			{Project: "crates.io/ripgrep", Versions: []string{"14.1.1"}, Provides: []string{"rg"}},
			{Project: "aardvark.io/rgbds", Versions: []string{"0.9.0"}, Provides: []string{"rgbasm"}},
			{Project: "stedolan.github.io/jq", Versions: []string{"1.8.1"}, Provides: []string{"jq"}},
			{Project: "gnu.org/gcc", Provides: []string{"gcc", "g++"}}, // no bottle here
		},
	}
}

// The case the command exists for. `rg` is crates.io/ripgrep: no guess at
// the project name reaches it, and <TAB> — a PREFIX on the project path —
// never will. Completion and search are different tools, and this is the
// line between them.
func TestSearchFindsAPackageByItsCommand(t *testing.T) {
	withCatalog(t, cmdCatalog(), nil)
	var out, errb bytes.Buffer
	if code := runSearch([]string{"rg"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "crates.io/ripgrep") {
		t.Fatalf("first line = %q", out.String())
	}
	// It says WHY, and what you would get. An unexplained list of forty
	// projects is a list nobody reads to the end.
	if !strings.Contains(lines[0], "rg") || !strings.Contains(lines[0], "14.1.1") {
		t.Errorf("the line does not say why or what: %q", lines[0])
	}
	// And completion could NOT have answered this, which is the point.
	for _, c := range completionsFor("rg") {
		if strings.Contains(c.value, "ripgrep") {
			t.Error("completion already finds ripgrep from \"rg\"; the premise is wrong")
		}
	}
}

// A project with nothing for this platform is still a result, marked: the
// reader asked which package provides gcc, and "none" would be false.
func TestSearchMarksWhatHasNoBottleHere(t *testing.T) {
	withCatalog(t, cmdCatalog(), nil)
	var out bytes.Buffer
	if code := runSearch([]string{"gcc"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "gnu.org/gcc") || !strings.Contains(out.String(), noBottle) {
		t.Errorf("out=%q", out.String())
	}
}

// What is installed is marked here too, for the same reason it is in `ls`.
func TestSearchMarksWhatYouHave(t *testing.T) {
	withCatalog(t, cmdCatalog(), nil)
	withInstalled(t, map[string][]string{"stedolan.github.io/jq": {"1.8.1"}})
	var out bytes.Buffer
	if code := runSearch([]string{"jq"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "✓") {
		t.Errorf("an installed hit is not marked: %q", out.String())
	}
}

// Nothing found exits NON-ZERO and says so on stderr, so `pkgx search x ||
// echo none` works and a pipeline is not fed a reassuring silence.
func TestSearchWithNoResults(t *testing.T) {
	withCatalog(t, cmdCatalog(), nil)
	var out, errb bytes.Buffer
	if code := runSearch([]string{"nothing-matches-this"}, &out, &errb); code != 1 {
		t.Errorf("code=%d, want 1", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout got %q", out.String())
	}
	if !strings.Contains(errb.String(), "nothing matches") {
		t.Errorf("stderr=%q", errb.String())
	}
}

// "No such package" and "this catalogue cannot answer that kind of
// question" are different facts. A catalogue published before commands were
// carried matches names only — which is the state of every catalogue on
// ghcr the day this lands — and a reader deserves to know which they are
// looking at.
func TestSearchSaysWhenTheCatalogueCannotAnswer(t *testing.T) {
	withCatalog(t, bottle.Catalog{
		Generated: "2026-10-06T09:00:00Z",
		Projects: []bottle.CatalogProject{
			{Project: "crates.io/ripgrep", Versions: []string{"14.1.1"}}, // no Provides
		},
	}, nil)
	var errb bytes.Buffer
	if code := runSearch([]string{"rg"}, &bytes.Buffer{}, &errb); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errb.String(), "carries no command names") {
		t.Errorf("a reader is not told the catalogue cannot answer: %q", errb.String())
	}

	// And it must NOT say that when the catalogue does carry them: a
	// warning that is always printed is a warning nobody reads.
	withCatalog(t, cmdCatalog(), nil)
	errb.Reset()
	if code := runSearch([]string{"nothing-matches-this"}, &bytes.Buffer{}, &errb); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if strings.Contains(errb.String(), "carries no command names") {
		t.Errorf("a catalogue that DOES carry commands claims it does not: %q", errb.String())
	}
}

// ⛔ THE REAL CASE, AND IT IS THE ONE WITH RESULTS.
//
// Measured 2026-10-20 against the published catalogue: `pkgs search json`
// returns THIRTEEN projects and none of them is `stedolan.github.io/jq`. jq
// is in that catalogue, with the command `jq` and a bottle for this
// platform — the word "json" is simply in neither its path nor its
// commands, and only 6 of 1938 projects carry a description for a search to
// read. Thirteen answers read as a complete answer, so the reader concludes
// jq is not packaged. A search that finds NOTHING already makes a reader
// suspicious; one that finds plenty does not.
func TestASearchWithResultsStillSaysWhatItCouldNotRead(t *testing.T) {
	withCatalog(t, cmdCatalog(), nil)
	var out, errb bytes.Buffer
	if code := runSearch([]string{"rg"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "carry a description") {
		t.Errorf("a search WITH results says nothing about the prose it could not read: %q", errb.String())
	}
	// ⛔ ON STDERR. `pkgs search x > list` must write results and nothing
	// else, or the note breaks every caller to make a point.
	if strings.Contains(out.String(), "carry a description") {
		t.Errorf("the note landed on stdout: %q", out.String())
	}
	// It names both numbers: "most of them" is not a fact a reader can act on.
	for _, want := range []string{"only 0 of 4"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the note does not carry %q: %q", want, errb.String())
		}
	}

	// ⛔ THE CONTROL: a catalogue that DOES describe its packages is silent.
	// A warning printed every time is a warning nobody reads, and this one
	// would otherwise outlive the condition it describes.
	described := cmdCatalog()
	for i := range described.Projects {
		described.Projects[i].Summary = "a description of " + described.Projects[i].Project
	}
	withCatalog(t, described, nil)
	out.Reset()
	errb.Reset()
	if code := runSearch([]string{"rg"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	if strings.Contains(errb.String(), "carry a description") {
		t.Errorf("a catalogue full of descriptions still warns about them: %q", errb.String())
	}

	// And the boundary is a share, not a count: one in five is enough.
	some := cmdCatalog() // 4 projects
	some.Projects[0].Summary = "one of four, which is above one in five"
	withCatalog(t, some, nil)
	errb.Reset()
	if code := runSearch([]string{"rg"}, &bytes.Buffer{}, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	if strings.Contains(errb.String(), "carry a description") {
		t.Errorf("1 of 4 described is above the floor and must be silent: %q", errb.String())
	}
}

// A long result list is cut, and SAYS it was: a reader who sees twenty
// lines and no more has no way to know whether the twenty-first was theirs.
func TestSearchLimitsAndSaysSo(t *testing.T) {
	var projects []bottle.CatalogProject
	for i := 0; i < 30; i++ {
		projects = append(projects, bottle.CatalogProject{
			Project:  "x.org/thing" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Provides: []string{"grep-like"},
		})
	}
	withCatalog(t, bottle.Catalog{Generated: "2026-10-06T09:00:00Z", Projects: projects}, nil)

	var out, errb bytes.Buffer
	if code := runSearch([]string{"grep-like"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if n := len(strings.Split(strings.TrimSpace(out.String()), "\n")); n != 20 {
		t.Errorf("printed %d lines, want the default 20", n)
	}
	if !strings.Contains(errb.String(), "10 more") {
		t.Errorf("the reader is not told the list was cut: %q", errb.String())
	}

	out.Reset()
	if code := runSearch([]string{"-n", "0", "grep-like"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if n := len(strings.Split(strings.TrimSpace(out.String()), "\n")); n != 30 {
		t.Errorf("-n 0 printed %d lines, want all 30", n)
	}
}

// Wired into the dispatch, the usage and the completion — a subcommand
// nobody can reach or discover is not a subcommand.
func TestSearchIsReachable(t *testing.T) {
	withCatalog(t, cmdCatalog(), nil)
	asPkgs(t)
	var code int
	out := captureStdout(t, func() { code = Pkgs([]string{"search", "rg"}) })
	if code != 0 || !strings.Contains(out, "crates.io/ripgrep") {
		t.Errorf("pkgs search rg = %d, %q", code, out)
	}
	if !strings.Contains(pkgsUsage, "pkgs search") {
		t.Error("pkgs usage does not mention search")
	}
	if !strings.Contains(usage, "pkgs search") {
		t.Error("pkgx usage does not point at pkgs search")
	}
	found := false
	for _, c := range completionsFor("sea") {
		if c.value == "search" {
			found = true
		}
	}
	if !found {
		t.Error("`pkgx sea<TAB>` does not offer search")
	}
	// An empty query is a usage error, not a listing of everything.
	if code := runSearch(nil, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Errorf("an empty query = %d, want 2", code)
	}
}
