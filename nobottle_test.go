package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

// A catalogue is published PER PLATFORM because what is available differs
// by architecture, and the choice made here is to NAME everything and mark
// what is available — not to hide the rest.
//
// Dropping a name tells a reader the project does not exist, which is
// false. Printing it bare tells them nothing. Marking it tells them it
// exists, that there is no bottle for them, and therefore that building it
// is the thing to do next.
func sweptCatalog() bottle.Catalog {
	return bottle.Catalog{
		Generated: "2026-10-06T09:00:00Z",
		Projects: []bottle.CatalogProject{
			{Project: "gnu.org/bash", Versions: []string{"5.3"}, Platforms: []string{"linux/x86-64"}},
			{Project: "doxygen.nl"}, // named here, no bottle for this platform
			{Project: "curl.se", Versions: []string{"8.17.0"}, Platforms: []string{"linux/x86-64"},
				Deps: []string{"openssl.org", "doxygen.nl"}},
			{Project: "openssl.org", Versions: []string{"3.6.0"}, Platforms: []string{"linux/x86-64"}},
		},
	}
}

func TestLsMarksAProjectWithNoBottleHere(t *testing.T) {
	withCatalog(t, sweptCatalog(), nil)
	var out, errb bytes.Buffer
	if code := runLs(nil, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "doxygen.nl") {
		t.Fatalf("a project with no bottle here was dropped from the listing:\n%s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "doxygen.nl") {
			continue
		}
		if !strings.Contains(line, "no bottle here") {
			t.Errorf("doxygen.nl is listed without its mark: %q", line)
		}
	}
	// And a project that IS carried shows its version, not the mark.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "gnu.org") && strings.Contains(line, "no bottle here") {
			t.Errorf("a carried project is marked unavailable: %q", line)
		}
	}
}

// The mark on a DEPENDENCY is the most useful line on the page: it is the
// reason the thing above it cannot be installed, and without it the node
// prints as a bare name among satisfied ones.
func TestTheTreeMarksADependencyWithNoBottleHere(t *testing.T) {
	withCatalog(t, sweptCatalog(), nil)
	var out, errb bytes.Buffer
	if code := runLs([]string{"--tree", "curl.se"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "doxygen.nl  (no bottle here)") {
		t.Errorf("the unsatisfiable dependency is not marked:\n%s", got)
	}
	if !strings.Contains(got, "openssl.org  3.6.0") {
		t.Errorf("a satisfied dependency lost its version:\n%s", got)
	}
}

// And the project's own line says it, so `pkgx ls doxygen.nl` answers the
// question somebody typed it to ask.
func TestLsOnTheProjectItselfSaysIt(t *testing.T) {
	withCatalog(t, sweptCatalog(), nil)
	var out, errb bytes.Buffer
	if code := runLs([]string{"doxygen.nl"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "doxygen.nl — no bottle here") {
		t.Errorf("out=%q", out.String())
	}
}

// THE CONTROL, and the reason the mark is inferred rather than assumed.
//
// `bk catalog` without --versions names projects and nothing else. On such
// a catalogue EVERY line would read "no bottle here" — a statement about
// the catalogue dressed up as a statement about the registry. There is no
// flag to read, so the only evidence is that a swept catalogue has versions
// somewhere.
func TestAnUnsweptCatalogueClaimsNothingAboutBottles(t *testing.T) {
	withCatalog(t, bottle.Catalog{
		Generated: "2026-10-06T09:00:00Z",
		Projects: []bottle.CatalogProject{
			{Project: "gnu.org/bash"}, {Project: "doxygen.nl"}, {Project: "curl.se"},
		},
	}, nil)
	var out, errb bytes.Buffer
	if code := runLs(nil, &out, &errb); code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	if strings.Contains(out.String(), "no bottle here") {
		t.Errorf("a catalogue that was never asked about bottles reports on them:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "a package") {
		t.Errorf("an unswept catalogue stopped saying what a leaf is:\n%s", out.String())
	}
}

// The mark reaches <TAB> too, where it is the description beside the
// candidate — which is the moment it is most worth knowing, before the
// name is typed rather than after it fails.
func TestCompletionCarriesTheMark(t *testing.T) {
	withCatalog(t, sweptCatalog(), nil)
	found := false
	for _, c := range completionsFor("doxygen") {
		if c.value == "doxygen.nl" {
			found = true
			if !strings.Contains(c.note, "no bottle here") {
				t.Errorf("completion note = %q", c.note)
			}
		}
	}
	if !found {
		t.Error("a project with no bottle here is not even offered")
	}
}
