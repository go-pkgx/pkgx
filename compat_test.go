package main

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/go-pkgx/bottle"
)

func TestCompatProjectsFromArgs(t *testing.T) {
	got, err := compatProjects([]string{"a.org", "b.org"}, strings.NewReader("ignored\n"))
	if err != nil || len(got) != 2 || got[0] != "a.org" {
		t.Fatalf("compatProjects = %v, %v", got, err)
	}
}

// From stdin when none are named, so bk's recipes.txt or a registry listing
// feeds it without this command needing to know where a pantry lives. Blank
// lines and comments are skipped, and a line with trailing fields keeps only
// the project — recipes.txt carries both shapes.
func TestCompatProjectsFromStdin(t *testing.T) {
	in := strings.NewReader("# a comment\n\na.org\n  b.org/tool  \nc.org 1.2.3\n")
	got, err := compatProjects(nil, in)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.org", "b.org/tool", "c.org"}
	if len(got) != len(want) {
		t.Fatalf("compatProjects = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("compatProjects = %v, want %v", got, want)
		}
	}
}

// The attribution is the point of the command. A list of packages that did not
// resolve tells an operator nothing they can act on; naming the project the
// demands collided on, with a count, names one rebuild that would move all of
// them at once.
func TestPrintCompatAttributesTheRefusals(t *testing.T) {
	r := compatResult{
		outcomes: []outcome{
			{project: "ok.org"},
			{project: "a.org", conflict: &bottle.ConflictError{
				Project: "gnome.org/libxml2", Constraints: []string{">=2.14", "~2.13"},
				AskedBy: []string{"requested", "gnu.org/gettext"}}},
			{project: "b.org", conflict: &bottle.ConflictError{
				Project: "gnome.org/libxml2", Constraints: []string{">=2.14", "2"},
				AskedBy: []string{"requested", "freedesktop.org/fontconfig"}}},
			{project: "c.org", conflict: &bottle.ConflictError{
				Project: "unicode.org", Constraints: []string{"^71", "^73"},
				AskedBy: []string{"qt.io", "nodejs.org"}}},
		},
		resolved: 1, refused: 3,
	}
	var b bytes.Buffer
	printCompat(map[string]string{"gnome.org/libxml2": ">=2.14"}, r, &b)
	out := b.String()

	if !strings.Contains(out, "base: gnome.org/libxml2>=2.14") {
		t.Errorf("the base is not stated:\n%s", out)
	}
	if !strings.Contains(out, "4 recipes") || !strings.Contains(out, "1 resolve") || !strings.Contains(out, "3 refused") {
		t.Errorf("the counts are wrong:\n%s", out)
	}
	// libxml2 excluded two and unicode.org one, so libxml2 comes FIRST: the
	// order is what tells an operator where one rebuild buys the most.
	li, ui := strings.Index(out, "gnome.org/libxml2\n"), strings.Index(out, "unicode.org\n")
	if li < 0 || ui < 0 || li > ui {
		t.Errorf("refusals are not ordered by how much they exclude:\n%s", out)
	}
	if !strings.Contains(out, "   2  gnome.org/libxml2") {
		t.Errorf("the count per project is missing:\n%s", out)
	}
	// Every distinct demand is shown once, whoever made it.
	for _, want := range []string{"~2.13 (gnu.org/gettext)", "2 (freedesktop.org/fontconfig)", ">=2.14 (requested)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing demand %q:\n%s", want, out)
		}
	}
}

// Nothing refused, nothing to attribute: the section is omitted rather than
// printed empty.
func TestPrintCompatSaysNothingWhenAllResolve(t *testing.T) {
	var b bytes.Buffer
	printCompat(nil, compatResult{outcomes: []outcome{{project: "ok.org"}}, resolved: 1}, &b)
	out := b.String()
	if strings.Contains(out, "blame") {
		t.Errorf("an empty attribution was printed:\n%s", out)
	}
	if !strings.Contains(out, "base: (none pinned)") {
		t.Errorf("an unpinned base is not stated:\n%s", out)
	}
}

// A project that could not be read at all is counted apart from one whose
// closure has no solution: the first is a broken lookup, the second is an
// answer.
func TestPrintCompatSeparatesErrorsFromRefusals(t *testing.T) {
	var b bytes.Buffer
	printCompat(nil, compatResult{
		outcomes: []outcome{{project: "x.org", err: bytes.ErrTooLarge}},
		errored:  1,
	}, &b)
	if !strings.Contains(b.String(), "x.org") {
		t.Errorf("the unreadable project was not named:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "1 could not be read at all") {
		t.Errorf("an unreadable project was not distinguished:\n%s", b.String())
	}
}

// solveAll classifies each project into the three outcomes, and the pins it
// adds are indistinguishable to the resolver from a demand a recipe made —
// which is the whole reason a base can be expressed as extra roots.
func TestSolveAll(t *testing.T) {
	old := graphFor
	defer func() { graphFor = old }()
	var mu sync.Mutex
	var sawPin int
	graphFor = func(roots map[string]string, _, _ string) (*bottle.Graph, error) {
		mu.Lock()
		if roots["gnome.org/libxml2"] == ">=2.14" {
			sawPin++
		}
		mu.Unlock()
		switch {
		case roots["bad.org"] != "":
			return nil, &bottle.ConflictError{Project: "gnome.org/libxml2",
				Constraints: []string{">=2.14", "~2.13"}, AskedBy: []string{"requested", "gnu.org/gettext"}}
		case roots["gone.org"] != "":
			return nil, errors.New("no such project")
		}
		return &bottle.Graph{}, nil
	}

	r := solveAll(map[string]string{"gnome.org/libxml2": ">=2.14"},
		[]string{"ok.org", "bad.org", "gone.org"})

	if r.resolved != 1 || r.refused != 1 || r.errored != 1 {
		t.Fatalf("resolved=%d refused=%d errored=%d", r.resolved, r.refused, r.errored)
	}
	if sawPin != 3 {
		t.Errorf("the pin reached %d of 3 resolutions", sawPin)
	}
	// The order of the outcomes follows the order asked for, although the work
	// is concurrent: a report whose rows move between runs cannot be diffed.
	if r.outcomes[0].project != "ok.org" || r.outcomes[2].project != "gone.org" {
		t.Errorf("outcomes were reordered: %v", r.outcomes)
	}
}

// runCompat end to end: it names the base, counts, attributes, and returns a
// non-zero status WITHOUT printing a second line — the report already said
// everything, and a trailing error in a pipe is noise.
func TestRunCompat(t *testing.T) {
	old := graphFor
	defer func() { graphFor = old }()
	graphFor = func(roots map[string]string, _, _ string) (*bottle.Graph, error) {
		if roots["bad.org"] != "" {
			return nil, &bottle.ConflictError{Project: "gnome.org/libxml2",
				Constraints: []string{">=2.14", "~2.13"}, AskedBy: []string{"requested", "gnu.org/gettext"}}
		}
		return &bottle.Graph{}, nil
	}
	var b bytes.Buffer
	err := runCompat([]string{"gnome.org/libxml2>=2.14"}, nil,
		strings.NewReader("ok.org\nbad.org\n"), &b)
	if err != errSilent {
		t.Fatalf("err = %v, want the silent sentinel", err)
	}
	out := b.String()
	if !strings.Contains(out, "1 resolve") || !strings.Contains(out, "1 refused") ||
		!strings.Contains(out, "gnome.org/libxml2") {
		t.Errorf("report:\n%s", out)
	}
}

// All green is a zero status.
func TestRunCompatAllResolve(t *testing.T) {
	old := graphFor
	defer func() { graphFor = old }()
	graphFor = func(map[string]string, string, string) (*bottle.Graph, error) { return &bottle.Graph{}, nil }
	var b bytes.Buffer
	if err := runCompat(nil, []string{"ok.org"}, strings.NewReader(""), &b); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

// Nothing to solve is a usage error, not an empty success: a pipe that yielded
// no lines would otherwise report "0 recipes, 0 refused" and exit 0.
func TestRunCompatWithNothingToSolve(t *testing.T) {
	var b bytes.Buffer
	err := runCompat(nil, nil, strings.NewReader("\n# only a comment\n"), &b)
	if err == nil || err == errSilent {
		t.Fatalf("err = %v, want a usage error", err)
	}
}
