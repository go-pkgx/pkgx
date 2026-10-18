package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

// fixture is the diamond that makes the point: two packages need z, and only
// one of them has an opinion about which z.
func fixture() *bottle.Graph {
	v := func(s string) bottle.Ver { return bottle.Ver{Raw: s} }
	return &bottle.Graph{
		Roots: []string{"app.org/main"},
		Versions: map[string]bottle.Ver{
			"app.org/main": v("1.0.0"), "lib.org/a": v("1.4.0"),
			"lib.org/b": v("2.0.0"), "lib.org/z": v("2.1.0"),
		},
		Deps: map[string][]bottle.Edge{
			"app.org/main": {{Of: "app.org/main", On: "lib.org/a", Constraint: "^1"}, {Of: "app.org/main", On: "lib.org/b", Constraint: "*"}},
			"lib.org/a":    {{Of: "lib.org/a", On: "lib.org/z", Constraint: "^2"}},
			"lib.org/b":    {{Of: "lib.org/b", On: "lib.org/z", Constraint: "*"}},
		},
		Asks: map[string][]bottle.Edge{
			"app.org/main": {{Of: "", On: "app.org/main", Constraint: "*"}},
			"lib.org/a":    {{Of: "app.org/main", On: "lib.org/a", Constraint: "^1"}},
			"lib.org/b":    {{Of: "app.org/main", On: "lib.org/b", Constraint: "*"}},
			"lib.org/z":    {{Of: "lib.org/a", On: "lib.org/z", Constraint: "^2"}, {Of: "lib.org/b", On: "lib.org/z", Constraint: "*"}},
		},
	}
}

func TestPrintTree(t *testing.T) {
	var b bytes.Buffer
	printTree(fixture(), &b)
	want := strings.TrimSpace(`
app.org/main 1.0.0
├─ lib.org/a 1.4.0
│  └─ lib.org/z 2.1.0
└─ lib.org/b 2.0.0
   └─ lib.org/z 2.1.0
`)
	if got := strings.TrimSpace(b.String()); got != want {
		t.Errorf("tree =\n%s\nwant\n%s", got, want)
	}
}

// A project with dependencies that is reached twice is named once and marked,
// because expanding every path through a diamond prints the same subtree over
// and over.
func TestPrintTreeElidesARepeatedSubtree(t *testing.T) {
	g := fixture()
	g.Deps["lib.org/z"] = []bottle.Edge{{Of: "lib.org/z", On: "lib.org/deep", Constraint: "*"}}
	g.Versions["lib.org/deep"] = bottle.Ver{Raw: "9.0.0"}
	var b bytes.Buffer
	printTree(g, &b)
	out := b.String()
	if strings.Count(out, "lib.org/deep") != 1 {
		t.Errorf("the shared subtree was expanded twice:\n%s", out)
	}
	if !strings.Contains(out, "lib.org/z 2.1.0 ↑") {
		t.Errorf("the second visit was not marked:\n%s", out)
	}
}

// The point of the command: which demand decided the version.
func TestPrintContested(t *testing.T) {
	var b bytes.Buffer
	printContested(fixture(), &b)
	out := b.String()
	for _, want := range []string{"lib.org/z 2.1.0", "^2", "← lib.org/a", "← lib.org/b"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Projects everyone agrees about are not listed: a single demand explains
	// itself, and printing it would bury the disagreements.
	if strings.Contains(out, "lib.org/a") && strings.Contains(out, "1.4.0") {
		t.Errorf("an uncontested project was listed:\n%s", out)
	}
}

// A closure nobody disagrees about prints no section at all, rather than an
// empty heading.
func TestPrintContestedSaysNothingWhenThereIsNothing(t *testing.T) {
	g := fixture()
	g.Asks["lib.org/z"] = []bottle.Edge{{Of: "lib.org/a", On: "lib.org/z", Constraint: "^2"}}
	var b bytes.Buffer
	printContested(g, &b)
	if b.Len() != 0 {
		t.Errorf("printed a section for an undisputed closure: %q", b.String())
	}
}

// A root is asked for by nobody, and saying so beats an empty arrow.
func TestPrintContestedNamesARequestedRoot(t *testing.T) {
	g := fixture()
	g.Asks["lib.org/z"] = append(g.Asks["lib.org/z"], bottle.Edge{Of: "", On: "lib.org/z", Constraint: "=2.1.0"})
	var b bytes.Buffer
	printContested(g, &b)
	if !strings.Contains(b.String(), "← (requested)") {
		t.Errorf("a directly requested demand was not named:\n%s", b.String())
	}
}

func TestSplitFormatGraph(t *testing.T) {
	f, rest := splitFormat([]string{"--graph", "+a"})
	if f != formatGraph || len(rest) != 1 || rest[0] != "+a" {
		t.Errorf("splitFormat = %v, %v", f, rest)
	}
}

func TestPrintGraph(t *testing.T) {
	old := graphFor
	defer func() { graphFor = old }()
	graphFor = func(map[string]string, string, string) (*bottle.Graph, error) { return fixture(), nil }

	var b bytes.Buffer
	if err := printGraph(map[string]string{"app.org/main": "*"}, "linux", "x86-64", &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "app.org/main 1.0.0") || !strings.Contains(out, "decided by more than one demand") {
		t.Errorf("printGraph output:\n%s", out)
	}
}

// A closure that cannot resolve is reported, not rendered half-way: a partial
// tree of a set that does not exist would read as a set that does.
func TestPrintGraphPropagatesTheRefusal(t *testing.T) {
	old := graphFor
	defer func() { graphFor = old }()
	graphFor = func(map[string]string, string, string) (*bottle.Graph, error) {
		return nil, errors.New("no version of unicode.org satisfies \"^71\" AND \"^73\"")
	}
	var b bytes.Buffer
	err := printGraph(map[string]string{"qt.io": "*"}, "linux", "x86-64", &b)
	if err == nil {
		t.Fatal("an unsatisfiable closure rendered a graph")
	}
	if b.Len() != 0 {
		t.Errorf("printed something anyway: %q", b.String())
	}
}

// A dependency declared with no constraint at all shows as *, rather than as a
// blank where a demand should be.
func TestPrintContestedShowsAnEmptyConstraintAsStar(t *testing.T) {
	g := fixture()
	g.Asks["lib.org/z"] = []bottle.Edge{
		{Of: "lib.org/a", On: "lib.org/z", Constraint: "^2"},
		{Of: "lib.org/b", On: "lib.org/z", Constraint: ""},
	}
	var b bytes.Buffer
	printContested(g, &b)
	if !strings.Contains(b.String(), "*            ← lib.org/b") {
		t.Errorf("an empty constraint was not shown as *:\n%s", b.String())
	}
}

// A project the resolver split onto two ABI lines must be printed as two. One
// version there would answer "which libxml2 is in this closure" with a
// half-truth, and the whole point of the split is that the answer is two.
func TestVersionLabelNamesBothABILines(t *testing.T) {
	g := &bottle.Graph{
		Versions: map[string]bottle.Ver{
			"gnome.org/libxml2": bottle.ParseVer("2.15.4"),
			"zlib.net":          bottle.ParseVer("1.3.2"),
		},
		Lines: map[string][]bottle.Ver{
			"gnome.org/libxml2": {bottle.ParseVer("2.15.4"), bottle.ParseVer("2.13.9")},
		},
	}
	got := versionLabel(g, "gnome.org/libxml2")
	if !strings.Contains(got, "2.15.4") || !strings.Contains(got, "2.13.9") {
		t.Errorf("label = %q, want both lines", got)
	}
	// The leading line comes first: it is the one that owns PATH.
	if strings.Index(got, "2.15.4") > strings.Index(got, "2.13.9") {
		t.Errorf("label = %q, want the newest line first", got)
	}
	// A project with one version is unchanged — no parenthetical noise on the
	// overwhelming majority of rows.
	if got := versionLabel(g, "zlib.net"); got != "1.3.2" {
		t.Errorf("label = %q, want a bare version", got)
	}
}

// --- `--graph --platform`: the question about a machine you do not have ---

// ⛔ THE PLATFORM REACHES THE RESOLVER. Asserting only that the flag parses
// would pass just as well against the old code, which took the host's and
// threw the answer away — the defect was never in the parsing.
func TestPrintGraphResolvesForThePlatformItWasGiven(t *testing.T) {
	old := graphFor
	defer func() { graphFor = old }()
	var gotOS, gotArch string
	graphFor = func(_ map[string]string, osn, arch string) (*bottle.Graph, error) {
		gotOS, gotArch = osn, arch
		return fixture(), nil
	}
	var b bytes.Buffer
	if err := printGraph(map[string]string{"app.org/main": "*"}, "linux", "s390x", &b); err != nil {
		t.Fatal(err)
	}
	if gotOS != "linux" || gotArch != "s390x" {
		t.Errorf("resolved for %s/%s, want linux/s390x", gotOS, gotArch)
	}
}

func TestSplitPlatform(t *testing.T) {
	for _, c := range []struct {
		name     string
		format   outputFormat
		argv     []string
		os, arch string
		rest     []string
		wantErr  string
	}{
		{
			name:   "absent leaves the host to the caller",
			format: formatGraph,
			argv:   []string{"+go.dev"},
			rest:   []string{"+go.dev"},
		},
		{
			name:   "taken off the front",
			format: formatGraph,
			argv:   []string{"--platform", "linux/x86-64", "+go.dev"},
			os:     "linux", arch: "x86-64",
			rest: []string{"+go.dev"},
		},
		{
			// Every other format composes an environment for THIS machine, so
			// the flag could only be decoration — and a flag without effect is
			// worse than one that is absent.
			name:    "refused on a format that builds something",
			format:  formatJSON,
			argv:    []string{"--platform", "linux/x86-64", "+go.dev"},
			wantErr: "only means something with --graph",
		},
		{
			name:    "a value that is not os/arch",
			format:  formatGraph,
			argv:    []string{"--platform", "linux", "+go.dev"},
			wantErr: "is not os/arch",
		},
		{
			name:    "an empty half is not a platform",
			format:  formatGraph,
			argv:    []string{"--platform", "/x86-64", "+go.dev"},
			wantErr: "is not os/arch",
		},
		{
			name:    "nothing after the flag",
			format:  formatGraph,
			argv:    []string{"--platform"},
			wantErr: "wants an os/arch",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			osn, arch, rest, err := splitPlatform(c.format, c.argv)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("accepted %v", c.argv)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("error %q does not mention %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if osn != c.os || arch != c.arch {
				t.Errorf("platform = %q/%q, want %q/%q", osn, arch, c.os, c.arch)
			}
			if strings.Join(rest, " ") != strings.Join(c.rest, " ") {
				t.Errorf("rest = %v, want %v", rest, c.rest)
			}
		})
	}
}
