package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

// nix and guix show the state of the store in everything they print. Here
// nothing did: a catalogue says what EXISTS, and a reader standing in front
// of it mostly wants to know what they already HAVE. `pkgx ls curl.se`
// before and after `pkgx curl.se --version` printed the same page.
//
// This is the plan's recipe for the phase, run as a test: the two listings
// differ by a ✓.
func TestTheListingDiffersByATickOnceSomethingIsInstalled(t *testing.T) {
	withCatalog(t, sweptCatalog(), nil)

	var before bytes.Buffer
	if code := runLs([]string{"curl.se"}, &before, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if strings.Contains(before.String(), "✓") {
		t.Fatalf("an empty store already shows a tick:\n%s", before.String())
	}

	withInstalled(t, map[string][]string{"curl.se": {"8.17.0"}})
	var after bytes.Buffer
	if code := runLs([]string{"curl.se"}, &after, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(after.String(), "curl.se — 8.17.0  ✓") {
		t.Errorf("installing it changed nothing:\n%s", after.String())
	}
	if strings.ReplaceAll(after.String(), "  ✓", "") != before.String() {
		t.Errorf("the two listings differ by more than a tick:\nbefore:\n%s\nafter:\n%s",
			before.String(), after.String())
	}
}

// A BARE tick when what is installed is the version on offer; the version
// spelled out when it is NOT. That second case is the one worth the extra
// word, because it is the one where a command would fetch something new —
// and showing the version every time would bury it in noise.
func TestTheTickNamesTheVersionOnlyWhenItDiffers(t *testing.T) {
	withCatalog(t, sweptCatalog(), nil)
	withInstalled(t, map[string][]string{
		"gnu.org/bash": {"5.3"},   // the offered one
		"openssl.org":  {"3.5.1"}, // an older one
	})
	var out bytes.Buffer
	if code := runLs([]string{"gnu.org"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "5.3  ✓") || strings.Contains(out.String(), "✓ 5.3") {
		t.Errorf("a matching version is spelled out instead of ticked: %q", out.String())
	}

	out.Reset()
	if code := runLs([]string{"--tree", "curl.se"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "openssl.org  3.6.0  ✓ 3.5.1") {
		t.Errorf("a DIFFERENT installed version is not named:\n%s", out.String())
	}
}

// A project with no bottle for this platform can still be installed — built
// by hand, or carried over from another machine — and that is exactly the
// reader who needs to be told, because the catalogue's own answer is "no".
func TestSomethingInstalledWithNoBottleHereStillShows(t *testing.T) {
	withCatalog(t, sweptCatalog(), nil)
	withInstalled(t, map[string][]string{"doxygen.nl": {"1.14.0"}})
	var out bytes.Buffer
	if code := runLs([]string{"doxygen.nl"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "no bottle here  ✓ 1.14.0") {
		t.Errorf("out=%q", out.String())
	}
}

// A repeat in the tree is a pointer at a node already printed. Marking it
// again reads as a second copy in the store.
func TestARepeatIsNotMarkedTwice(t *testing.T) {
	withCatalog(t, bottle.Catalog{
		Generated: "2026-10-06T09:00:00Z",
		Projects: []bottle.CatalogProject{
			{Project: "curl.se", Versions: []string{"8.17.0"}, Deps: []string{"openssl.org", "zlib.net"}},
			{Project: "openssl.org", Versions: []string{"3.6.0"}, Deps: []string{"zlib.net"}},
			{Project: "zlib.net", Versions: []string{"1.3.2"}},
		},
	}, nil)
	withInstalled(t, map[string][]string{"zlib.net": {"1.3.2"}})
	var out bytes.Buffer
	if code := runLs([]string{"--tree", "curl.se"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if n := strings.Count(out.String(), "✓"); n != 1 {
		t.Errorf("%d tick(s) for one installed package:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "(shown above)") {
		t.Fatalf("the diamond did not repeat, so this proves nothing:\n%s", out.String())
	}
}
