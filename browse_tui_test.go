package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

func browseCat() bottle.Catalog {
	return bottle.Catalog{
		Generated: "2026-10-06T09:00:00Z",
		Projects: []bottle.CatalogProject{
			{Project: "curl.se", Versions: []string{"8.17.0"}, Provides: []string{"curl"},
				Deps: []string{"openssl.org", "zlib.net"}},
			{Project: "curl.se/ca-certs", Versions: []string{"2026.09.25"}},
			{Project: "openssl.org", Versions: []string{"3.6.0"}, Provides: []string{"openssl"}},
			{Project: "zlib.net", Versions: []string{"1.3.2"}},
			{Project: "gnu.org/bash", Versions: []string{"5.3"}, Provides: []string{"bash"}},
			{Project: "gnu.org/gcc", Provides: []string{"gcc"}}, // no bottle here
		},
	}
}

// Every key, driven without a terminal — which is the point of keeping the
// loop and the terminal apart. A test that needed a tty would not run in
// CI, and a second code path for CI would not be the one a person uses.
func press(t *testing.T, b *browser, keys string) {
	t.Helper()
	in := bufio.NewReader(strings.NewReader(keys))
	var sink bytes.Buffer
	for {
		k, err := readKey(in)
		if err != nil {
			return
		}
		if applyKey(b, k, in, &sink) {
			return
		}
	}
}

func TestBrowseWalksDownAndBack(t *testing.T) {
	withCatalog(t, browseCat(), nil)
	b := &browser{cat: browseCat()}

	// The roots. curl.se is both a package and a namespace.
	if got := strings.Join(b.rows(), " "); got != "curl.se gnu.org openssl.org zlib.net" {
		t.Fatalf("roots = %q", got)
	}
	press(t, b, "j")      // → gnu.org
	press(t, b, "\x1b[C") // right: descend
	if b.at != "gnu.org" {
		t.Fatalf("after descending: at=%q", b.at)
	}
	if got := strings.Join(b.rows(), " "); got != "gnu.org/bash gnu.org/gcc" {
		t.Errorf("under gnu.org = %q", got)
	}
	press(t, b, "h") // back
	if b.at != "" {
		t.Errorf("after back: at=%q", b.at)
	}
}

// A LEAF has no children, so "descend" there means its dependencies.
// Deciding that here rather than making the reader press tab is the whole
// difference between a browser and a pair of commands.
func TestDescendingIntoALeafShowsWhatItNeeds(t *testing.T) {
	b := &browser{cat: browseCat()} // at the roots
	press(t, b, "jj")               // → openssl.org, which has no children
	if b.rows()[b.sel] != "openssl.org" {
		t.Fatalf("selected %q", b.rows()[b.sel])
	}
	press(t, b, "\r")
	if b.at != "openssl.org" {
		t.Fatalf("at=%q", b.at)
	}
	if b.view != viewDeps {
		t.Error("descending into a leaf did not switch to dependencies")
	}
	// And a node that HAS children lists them instead, rather than being
	// dragged into dependency mode by the same key.
	b2 := &browser{cat: browseCat()}
	press(t, b2, "j\r") // → gnu.org, a namespace
	if b2.at != "gnu.org" || b2.view != viewNames {
		t.Errorf("a namespace was switched to dependencies: at=%q view=%v", b2.at, b2.view)
	}
}

// An ARROW is three bytes, and the last of them is a letter that is itself
// a binding. Read byte by byte, an up-arrow would run whatever `A` does —
// so decoding has to happen in readKey or every binding is a hazard.
func TestAnArrowIsNotThreeKeystrokes(t *testing.T) {
	for seq, want := range map[string]key{
		"\x1b[A": keyUp, "\x1b[B": keyDown, "\x1b[C": keyRight, "\x1b[D": keyLeft,
		"k": keyUp, "j": keyDown, "l": keyRight, "h": keyLeft,
		"\t": keyTab, "\r": keyEnter, " ": keySpace, "/": keySlash, "q": keyQuit,
		"\x03": keyQuit, "\x04": keyQuit,
		"Z": keyIgnore,
	} {
		in := bufio.NewReader(strings.NewReader(seq))
		got, err := readKey(in)
		if err != nil || got != want {
			t.Errorf("readKey(%q) = %q, %v; want %q", seq, got, err, want)
		}
		if in.Buffered() != 0 {
			t.Errorf("readKey(%q) left %d byte(s) unread", seq, in.Buffered())
		}
	}

	// ESC ALONE must not block waiting for a second byte that is not
	// coming: a reader that did would hang the browser on the Escape key.
	in := bufio.NewReader(strings.NewReader("\x1b"))
	if got, err := readKey(in); err != nil || got != keyIgnore {
		t.Errorf("a lone ESC = %q, %v", got, err)
	}
}

// q prints the pins on STDOUT and the screen on stderr, so
// `pkgx +$(pkgx browse)` composes instead of putting borders on a command
// line.
func TestPinsAreTheResultAndGoToStdout(t *testing.T) {
	b := &browser{cat: browseCat()}
	press(t, b, " ")   // pin the first row
	press(t, b, "jj ") // move down twice, pin
	if strings.Join(b.pinned, " ") != "curl.se openssl.org" {
		t.Fatalf("pinned = %v", b.pinned)
	}
	// Pinning twice unpins: a toggle, because the alternative is a list
	// you cannot correct without starting again.
	b2 := &browser{cat: browseCat()}
	press(t, b2, "  ")
	if len(b2.pinned) != 0 {
		t.Errorf("space twice left %v pinned", b2.pinned)
	}
}

// / searches the whole catalogue, not the current node: somebody who knows
// what they want is not where it is.
func TestSearchFromAnywhere(t *testing.T) {
	b := &browser{cat: browseCat(), at: "gnu.org"}
	press(t, b, "/zlib\r")
	if b.filter != "zlib" {
		t.Fatalf("filter = %q", b.filter)
	}
	if got := strings.Join(b.rows(), " "); got != "zlib.net" {
		t.Errorf("search rows = %q", got)
	}
	// Back clears the search before leaving the node — otherwise the one
	// key does two things in an order nobody can predict.
	press(t, b, "h")
	if b.filter != "" {
		t.Error("back did not clear the search first")
	}
	if b.at != "gnu.org" {
		t.Errorf("back left the node as well: at=%q", b.at)
	}
}

// The screen is a WINDOW around the cursor. A node with 452 children on a
// 24-line terminal must not print 452 lines per keystroke.
func TestRenderShowsAWindow(t *testing.T) {
	var projects []bottle.CatalogProject
	for i := 0; i < 400; i++ {
		projects = append(projects, bottle.CatalogProject{
			Project:  "x.org/p" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Versions: []string{"1.0.0"},
		})
	}
	b := &browser{cat: bottle.Catalog{Generated: "2026-10-06T09:00:00Z", Projects: projects}, at: "x.org"}
	withInstalled(t, nil)
	var out bytes.Buffer
	b.render(&out, 24)
	if n := strings.Count(out.String(), "\r\n"); n > 24 {
		t.Errorf("a 24-line terminal got %d lines", n)
	}
	// And the cursor is on the page it drew.
	b.sel = 399
	out.Reset()
	b.render(&out, 24)
	if !strings.Contains(out.String(), "> ") {
		t.Error("the cursor is off the drawn window")
	}
}

// Without a terminal it prints and exits — `pkgx browse | less`, a CI log,
// a scratch image. A command that refuses to work when its output is a pipe
// is a command people stop putting in scripts.
func TestBrowseWithoutATerminalPrintsTheTree(t *testing.T) {
	withCatalog(t, browseCat(), nil)
	prev := termIsTerminal
	termIsTerminal = func(int) bool { return false }
	t.Cleanup(func() { termIsTerminal = prev })

	var out, errb bytes.Buffer
	code := runBrowse([]string{"gnu.org"}, bytes.NewReader(nil), &out, &errb)
	if code != 0 {
		t.Fatalf("code=%d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "gnu.org/bash") {
		t.Errorf("it printed nothing useful: %q", out.String())
	}
	if !strings.Contains(errb.String(), "not a terminal") {
		t.Errorf("it did not say why: %q", errb.String())
	}
}

// An empty catalogue is not a browser with nothing in it: it is a machine
// that has not fetched one, and the useful thing to print is the command.
func TestBrowseWithNoCatalogue(t *testing.T) {
	// An EMPTY STORE too: browseCatalog falls back to what is installed
	// when the catalogue has no projects, and without this the test read
	// the developer's own ~/.pkgx and found plenty.
	t.Setenv("PKGX_DIR", t.TempDir())
	withCatalog(t, bottle.Catalog{Generated: "2026-10-06T09:00:00Z"}, nil)
	var errb bytes.Buffer
	if code := runBrowse(nil, bytes.NewReader(nil), &bytes.Buffer{}, &errb); code != 1 {
		t.Errorf("code=%d, want 1", code)
	}
	if !strings.Contains(errb.String(), "pkgx catalog update") {
		t.Errorf("stderr=%q", errb.String())
	}
}

// The help line is generated from the key table, so it cannot describe a
// binding that does not exist — or miss one that does.
func TestTheHelpLineMatchesTheBindings(t *testing.T) {
	b := &browser{cat: browseCat()}
	status := b.status()
	for _, k := range browseKeys {
		if !strings.Contains(status, k.key) {
			t.Errorf("the help line omits %q", k.key)
		}
	}
	// Every key the table advertises is one applyKey acts on. `q` is the
	// one that stops, so it is checked apart.
	for _, probe := range []struct {
		press string
		is    string
	}{{"\x1b[A", "↑ ↓ / k j"}, {"\x1b[C", "→ / l / enter"}, {"\t", "tab"}, {" ", "space"}} {
		// From the SECOND row, so that up-arrow has somewhere to go: at the
		// top it correctly does nothing, and probing there would have
		// measured the clamp instead of the binding.
		b.sel = 1
		before := *b
		press(t, b, probe.press)
		if b.sel == before.sel && b.at == before.at && b.view == before.view && len(b.pinned) == len(before.pinned) {
			t.Errorf("%s (%q) did nothing", probe.is, probe.press)
		}
		*b = before
	}
}

func TestBrowseIsReachable(t *testing.T) {
	if !strings.Contains(usage, "pkgx browse") {
		t.Error("usage does not mention browse")
	}
	found := false
	for _, c := range completionsFor("bro") {
		if c.value == "browse" {
			found = true
		}
	}
	if !found {
		t.Error("`pkgx bro<TAB>` does not offer browse")
	}
	if code := runBrowse([]string{"a", "b"}, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Error("two nodes is not a usage error")
	}
}

// TAB CYCLES THREE VIEWS, not two. A node has what it CONTAINS, what it
// NEEDS, and who NEEDS IT — and the third is the question asked before
// changing something, which the browser could not ask at all.
//
// The cycle must come back round: a key that reaches a state with no way
// out of it is worse than no key.
func TestTabCyclesThreeViews(t *testing.T) {
	b := &browser{cat: browseCat(), at: "curl.se"}
	if got := strings.Join(b.rows(), " "); got != "curl.se/ca-certs" {
		t.Fatalf("names under curl.se = %q", got)
	}
	press(t, b, "\t")
	if got := strings.Join(b.rows(), " "); got != "openssl.org zlib.net" {
		t.Errorf("dependencies of curl.se = %q", got)
	}
	press(t, b, "\t")
	// Nothing in the fixture needs curl.se, and an empty third view is the
	// honest answer rather than a reason to skip the state.
	if got := strings.Join(b.rows(), " "); got != "" {
		t.Errorf("dependents of curl.se = %q, want none", got)
	}
	press(t, b, "\t")
	if got := strings.Join(b.rows(), " "); got != "curl.se/ca-certs" {
		t.Errorf("tab did not come back round: %q", got)
	}
}

// AND THE THIRD VIEW HAS CONTENT WHERE THERE IS SOME — an empty list is
// also what a view that silently fell back to names would NOT show, so the
// cycle is checked on a node that really has dependents.
func TestTabReachesTheDependentsOfANodeThatHasSome(t *testing.T) {
	b := &browser{cat: browseCat(), at: "openssl.org"}
	press(t, b, "\t\t") // names → dependencies → dependents
	if got := strings.Join(b.rows(), " "); got != "curl.se" {
		t.Errorf("dependents of openssl.org = %q, want curl.se", got)
	}
	// THE HEADER SAYS WHICH WAY YOU ARE FACING. A cycle whose state is
	// invisible is a guess on every keystroke.
	//
	// ⛔ ON THE HEADER LINE, not anywhere on the screen. The help line at
	// the bottom reads "tab names ⇄ dependencies ⇄ dependents", so a
	// Contains over the whole screen passes with an EMPTY header label —
	// it matches the help. Mutating the label to "" and watching this stay
	// green is how that was found.
	var screen bytes.Buffer
	b.render(&screen, 24)
	head, _, _ := strings.Cut(screen.String(), "\r\n")
	if !strings.Contains(head, "dependents") {
		t.Errorf("the header does not say the view: %q", head)
	}
}

// WALKING UP STAYS WALKING UP. Descending from a dependents list follows
// the graph further that way — who needs the thing that needs it — and
// resetting to names would make the second step undo the first.
//
// ⛔ THE DEPENDENT MUST BE A LEAF. Descending onto a node that HAS children
// leaves the view alone anyway, so the first version of this test — which
// landed on curl.se — could not tell the early return from its absence.
// Its own catalogue, so the shape is part of the test rather than a
// property of a shared fixture somebody may change.
func TestDescendingFromDependentsKeepsWalkingUp(t *testing.T) {
	cat := bottle.Catalog{Projects: []bottle.CatalogProject{
		{Project: "leaf.org", Versions: []string{"1.0"}, Deps: []string{"zlib.net"}},
		{Project: "zlib.net", Versions: []string{"1.3.2"}},
	}}
	withCatalog(t, cat, nil)
	b := &browser{cat: cat, at: "zlib.net"}
	press(t, b, "\t\t") // → dependents of zlib.net
	if got := strings.Join(b.rows(), " "); got != "leaf.org" {
		t.Fatalf("dependents of zlib.net = %q", got)
	}
	if kids := cat.Children("leaf.org"); len(kids) != 0 {
		t.Fatalf("the fixture's dependent has %d children — this test cannot see the defect", len(kids))
	}
	press(t, b, "\r")
	if b.at != "leaf.org" {
		t.Fatalf("at=%q", b.at)
	}
	if b.view != viewDependents {
		t.Errorf("descending from dependents switched to %v", b.view)
	}
}
