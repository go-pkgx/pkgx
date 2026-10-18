package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

func commandCatalog() bottle.Catalog {
	return bottle.Catalog{
		Generated: "2026-10-12T09:00:00Z",
		Projects: []bottle.CatalogProject{
			{Project: "gohugo.io", Versions: []string{"0.167.0"}, Provides: []string{"hugo"}},
			{Project: "nodejs.org", Versions: []string{"26.8.1"}, Provides: []string{"node", "npm", "npx"}},
			{Project: "zoo.example/hugo-mirror", Provides: []string{"hugo-mirror", "hugo"}},
			{Project: "gnu.org/bash", Versions: []string{"5.3.0"}, Provides: []string{"bash"}},
		},
	}
}

// The reported command: `pkgx node --version` must reach nodejs.org. One
// provider, so it resolves, and with no constraint the matcher's "any" is
// what comes back — not an empty string, and not "*" glued to the name.
func TestResolveMapsACommandToItsProject(t *testing.T) {
	withCatalog(t, commandCatalog(), nil)
	m := &commandMapper{dir: t.TempDir()}
	proj, cons, err := m.resolve("node")
	if err != nil {
		t.Fatalf("resolve(node): %v", err)
	}
	if proj != "nodejs.org" || cons != "*" {
		t.Errorf("resolve(node) = %q %q, want nodejs.org *", proj, cons)
	}
}

// ⛔ The constraint survives, in both spellings. A first draft returned the
// REJOINED spec, which cannot work: splitSpec reports "*" when there is no
// constraint and drops the "@" of the "@" form, so `hugo` rejoined as
// `gohugo.io*` and `hugo@0.167.0` as `gohugo.io0.167.0`. Returning the pair
// is what makes that unexpressible.
func TestResolveKeepsTheConstraintInBothForms(t *testing.T) {
	withCatalog(t, commandCatalog(), nil)
	m := &commandMapper{dir: t.TempDir()}
	for _, tc := range []struct{ in, proj, cons string }{
		{"node@26.8.1", "nodejs.org", "26.8.1"}, // the @ form: operator dropped
		{"node^26", "nodejs.org", "^26"},        // the operator form: kept whole
		{"node>=24", "nodejs.org", ">=24"},
		{"node", "nodejs.org", "*"},
	} {
		proj, cons, err := m.resolve(tc.in)
		if err != nil {
			t.Errorf("resolve(%q): %v", tc.in, err)
			continue
		}
		if proj != tc.proj || cons != tc.cons {
			t.Errorf("resolve(%q) = %q %q, want %q %q", tc.in, proj, cons, tc.proj, tc.cons)
		}
	}
}

// A project name is answered WITHOUT the catalogue being read. That is the
// whole reason bareWord exists: `pkgx +deps` runs inside every factory build,
// in a loop, and parsing a 467 KB catalogue to discover that `gnu.org/bash`
// contains a slash would be paid on every call for nothing.
func TestAProjectNameNeverReadsTheCatalogue(t *testing.T) {
	reads := 0
	prev := catalogFor
	catalogFor = func() (bottle.Catalog, error) { reads++; return commandCatalog(), nil }
	t.Cleanup(func() { catalogFor = prev })

	m := &commandMapper{dir: t.TempDir()}
	for _, s := range []string{"gnu.org/bash", "curl.se", "go.dev@1.27", "cmake.org^3"} {
		proj, _, err := m.resolve(s)
		if err != nil {
			t.Errorf("resolve(%q): %v", s, err)
		}
		if proj != project(s) {
			t.Errorf("resolve(%q) changed the project to %q", s, proj)
		}
	}
	if reads != 0 {
		t.Errorf("the catalogue was read %d time(s) for project names alone", reads)
	}
}

// And the other direction, so the test above cannot pass by the mapper never
// reading anything at all.
func TestABareWordDoesReadTheCatalogue(t *testing.T) {
	reads := 0
	prev := catalogFor
	catalogFor = func() (bottle.Catalog, error) { reads++; return commandCatalog(), nil }
	t.Cleanup(func() { catalogFor = prev })
	withInstalled(t, nil)

	m := &commandMapper{dir: t.TempDir()}
	if _, _, err := m.resolve("node"); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Errorf("the catalogue was read %d time(s), want exactly 1", reads)
	}
	// And only once, however many bare words follow.
	if _, _, err := m.resolve("bash"); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Errorf("a second bare word read the catalogue again: %d reads", reads)
	}
}

// ⛔ Two providers is an ERROR, never a choice. `provides` says what a project
// puts on PATH, not who owns the name, so there is no basis for picking — and
// guessing runs the wrong program under the right name.
func TestTwoProvidersIsRefusedAndBothAreNamed(t *testing.T) {
	withCatalog(t, commandCatalog(), nil)
	m := &commandMapper{dir: t.TempDir()}
	_, _, err := m.resolve("hugo")
	if err == nil {
		t.Fatal("an ambiguous command resolved instead of failing")
	}
	for _, want := range []string{"gohugo.io", "zoo.example/hugo-mirror"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
}

// A command nothing declares says so, and points at the search that looks in
// names too. It must NOT claim nothing provides it: `provides` is carried by
// 1588 of 1915 projects, so a miss is not proof.
func TestAnUnknownCommandPointsAtSearch(t *testing.T) {
	withCatalog(t, commandCatalog(), nil)
	m := &commandMapper{dir: t.TempDir()}
	_, _, err := m.resolve("nosuchtool")
	if err == nil {
		t.Fatal("an unknown command resolved")
	}
	if !strings.Contains(err.Error(), "declares") {
		t.Errorf("the error overclaims — it should say DECLARES, not provides: %v", err)
	}
	if !strings.Contains(err.Error(), "pkgx search nosuchtool") {
		t.Errorf("the error does not point at the search: %v", err)
	}
}

// ⛔ No catalogue is its OWN message, not the pantry's. This is the defect in
// the field report: `pkgx hugo version` answered "no recipe for hugo in
// …pantry…", which reads as "go-pkgx does not know hugo" when the truth is
// "this store has no catalogue yet".
func TestNoCatalogueSaysToUpdateItRatherThanBlamingThePantry(t *testing.T) {
	withCatalog(t, bottle.Catalog{}, errors.New("open …/catalog: no such file or directory"))
	m := &commandMapper{dir: t.TempDir()}
	_, _, err := m.resolve("hugo")
	if err == nil {
		t.Fatal("a bare word with no catalogue resolved")
	}
	if !strings.Contains(err.Error(), "pkgx catalog update") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
	if strings.Contains(err.Error(), "no recipe for") {
		t.Errorf("it still blames the pantry: %v", err)
	}
}

// An empty catalogue is the same situation as none: a file that parsed and
// holds nothing cannot answer, and falling through to the pantry would ask
// about a project called `hugo` that cannot exist.
func TestAnEmptyCatalogueIsTreatedAsNone(t *testing.T) {
	withCatalog(t, bottle.Catalog{Generated: "2026-10-12T09:00:00Z"}, nil)
	m := &commandMapper{dir: t.TempDir()}
	if _, _, err := m.resolve("hugo"); err == nil || !strings.Contains(err.Error(), "pkgx catalog update") {
		t.Errorf("empty catalogue: err = %v", err)
	}
}

func TestBareWord(t *testing.T) {
	for _, s := range []string{"hugo", "node", "rg", "node@22"} {
		if !bareWord(s) {
			t.Errorf("bareWord(%q) = false, want true", s)
		}
	}
	// Measured, not assumed: 0 of 1915 published project names lack both a
	// dot and a slash (`go run ./internal/catstat`, darwin/aarch64,
	// 2026-10-12). These are the shapes the real data has.
	for _, s := range []string{"gnu.org/bash", "curl.se", "go.dev@1.27", "cmake.org^3", "x.org/protocol/xcb"} {
		if bareWord(s) {
			t.Errorf("bareWord(%q) = true, want false", s)
		}
	}
}
