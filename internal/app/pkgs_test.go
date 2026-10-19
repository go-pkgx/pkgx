package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

// ⛔ EVERY DIAGNOSTIC NAMES THE BINARY THE USER TYPED.
//
// The whole suite runs with tool == "pkgx" by default, so it passed
// unchanged through the split — and that means nothing in it would have
// noticed `pkgs catalog` printing "pkgx: usage: pkgx catalog …" and sending
// the reader to a command that no longer has the verb.
func TestDiagnosticsNameTheBinaryTheUserTyped(t *testing.T) {
	var errb bytes.Buffer
	if code := runCatalogCmd([]string{"nonsense"}, &bytes.Buffer{}, &errb); code != 2 {
		t.Fatalf("code = %d", code)
	}
	if got := errb.String(); !strings.HasPrefix(got, "pkgx: ") || !strings.Contains(got, "pkgx catalog") {
		t.Errorf("under pkgx the usage read %q", got)
	}

	asPkgs(t)
	errb.Reset()
	if code := runCatalogCmd([]string{"nonsense"}, &bytes.Buffer{}, &errb); code != 2 {
		t.Fatalf("code = %d", code)
	}
	got := errb.String()
	if !strings.HasPrefix(got, "pkgs: ") {
		t.Errorf("under pkgs the diagnostic is prefixed %q", got)
	}
	if !strings.Contains(got, "pkgs catalog") {
		t.Errorf("under pkgs the usage does not name pkgs: %q", got)
	}
	if strings.Contains(got, "pkgx") {
		t.Errorf("under pkgs the usage still mentions pkgx: %q", got)
	}
}

// A word pkgs does not know is refused, and the refusal names the binary
// that DOES run packages. Guessing between the two is how the grammar got
// muddled in the first place.
func TestPkgsRefusesAPackageAndSaysWhereToRunIt(t *testing.T) {
	asPkgs(t)
	code, _, errb := runSurface(t, "node")
	if code != 2 {
		t.Errorf("pkgs node = %d, want 2", code)
	}
	for _, want := range []string{"is not a pkgs command", "pkgx node"} {
		if !strings.Contains(errb, want) {
			t.Errorf("refusal %q does not mention %q", errb, want)
		}
	}
}

// ⛔ THE CONTROL IN THE OTHER DIRECTION. `pkgx ls` must reach the PACKAGE,
// because that is the entire point of the split: gnu.org/coreutils provides
// ls, and upstream pkgx runs it. unknownVerb must stay silent whenever the
// catalogue has something by that name, or the defect is back with a
// friendlier message.
func TestPkgxDoesNotInterceptAVerbThatIsAlsoAPackage(t *testing.T) {
	// ⛔ BARE COMMAND NAMES, as a CATALOGUE carries them. A recipe writes
	// `provides: [bin/ls]`; `bk catalog` writes `ls`, and ProvidersOf
	// compares with ==. A fixture in the recipe's shape would make this
	// test pass against a reader that strips "bin/" and fail against the
	// one the program actually uses.
	withCatalog(t, bottle.Catalog{Projects: []bottle.CatalogProject{
		{Project: "gnu.org/coreutils", Provides: []string{"ls", "env"}},
	}}, nil)
	for _, word := range []string{"ls", "env"} {
		var errb bytes.Buffer
		if code, said := unknownVerb(word, &errb); said {
			t.Errorf("pkgx %s was intercepted (code=%d, %q) — coreutils provides it", word, code, errb.String())
		}
	}
}

// And a verb that is NOT a package is told where it went, rather than
// failing with "no such package" three layers down.
func TestPkgxPointsAtPkgsForAVerbNoPackageProvides(t *testing.T) {
	withCatalog(t, bottle.Catalog{Projects: []bottle.CatalogProject{
		{Project: "gnu.org/coreutils", Provides: []string{"ls"}},
	}}, nil)
	var errb bytes.Buffer
	code, said := unknownVerb("why", &errb)
	if !said || code != 2 {
		t.Fatalf("pkgx why: said=%v code=%d", said, code)
	}
	for _, want := range []string{"pkgs command", "pkgs why"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("%q does not mention %q", errb.String(), want)
		}
	}
}

// A word that is neither a verb nor anything else is NOT this function's to
// answer: the resolver one line later gives the better error, naming where
// it looked.
func TestUnknownVerbIgnoresWhatIsNotAVerb(t *testing.T) {
	var errb bytes.Buffer
	if _, said := unknownVerb("node", &errb); said {
		t.Errorf("a plain package name was answered by unknownVerb: %q", errb.String())
	}
}

// `pkgx -Q`, upstream's query mode, which this fork never had — and whose
// absence is why nine verbs were put in the position upstream reserves for
// a package.
func TestQueryAnswersWhatProvidesACommand(t *testing.T) {
	withCatalog(t, bottle.Catalog{Projects: []bottle.CatalogProject{
		{Project: "gnu.org/coreutils", Provides: []string{"ls", "env"}},
		{Project: "busybox.net", Provides: []string{"ls"}},
		{Project: "crates.io/ripgrep", Provides: []string{"rg"}},
	}}, nil)

	var out, errb bytes.Buffer
	if code := runQuery([]string{"rg"}, &out, &errb); code != 0 {
		t.Fatalf("-Q rg = %d (%s)", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "crates.io/ripgrep" {
		t.Errorf("-Q rg printed %q", out.String())
	}

	// ⛔ TWO PROVIDERS, BOTH PRINTED. Choosing one silently is how a caller
	// runs something it did not ask for; the catalogue is the authority on
	// the ambiguity and the answer is to report it.
	out.Reset()
	if code := runQuery([]string{"ls"}, &out, &errb); code != 0 {
		t.Fatalf("-Q ls = %d", code)
	}
	lines := strings.Fields(strings.TrimSpace(out.String()))
	if len(lines) != 2 || lines[0] != "busybox.net" || lines[1] != "gnu.org/coreutils" {
		t.Errorf("-Q ls printed %q, want both providers sorted", out.String())
	}

	// Nothing provides it: exit 1, which is the question a script asks.
	out.Reset()
	errb.Reset()
	if code := runQuery([]string{"nosuchthing"}, &out, &errb); code != 1 {
		t.Errorf("-Q nosuchthing = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Errorf("-Q printed %q on a miss", out.String())
	}

	// With no argument, every command, with the project beside it.
	out.Reset()
	if code := runQuery(nil, &out, &errb); code != 0 {
		t.Fatalf("-Q = %d", code)
	}
	for _, want := range []string{"rg", "crates.io/ripgrep", "ls", "env"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("-Q does not list %q:\n%s", want, out.String())
		}
	}
}

// The completion snippet covers BOTH binaries, or installing it leaves one
// of the two without <TAB> and no sign of which.
func TestCompletionSnippetCoversBothBinaries(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out bytes.Buffer
		if code := runCompletionSnippet(shell, &out, &bytes.Buffer{}); code != 0 {
			t.Fatalf("%s = %d", shell, code)
		}
		s := out.String()
		for _, want := range []string{"pkgx", "pkgs"} {
			if !strings.Contains(s, want) {
				t.Errorf("the %s snippet never mentions %s:\n%s", shell, want, s)
			}
		}
		// ⛔ NO PLACEHOLDER SURVIVES. A template emitted raw would install a
		// completion for a command called @TOOL@, which fails silently —
		// the shell simply never completes anything.
		if strings.Contains(s, toolToken) {
			t.Errorf("the %s snippet still carries %s:\n%s", shell, toolToken, s)
		}
		// ⛔ AND THE zsh PARAMETER EXPANSION SURVIVES INTACT. `${l%%\t*}`
		// is why the templates are filled with ReplaceAll and not Sprintf:
		// %% would come out as a single % and the completion would return
		// nothing, with no error anywhere.
		if shell == "zsh" && !strings.Contains(s, "%%") {
			t.Errorf("the zsh snippet lost its %%%% expansion:\n%s", s)
		}
	}
}

// pkgx's own <TAB> must NOT offer the verbs: they are pkgs's, and a
// completion shows a word with no provenance — the one place a reader
// cannot tell which of the two they are being offered.
func TestPkgxCompletionDoesNotOfferPkgsVerbs(t *testing.T) {
	withCatalog(t, browseCat(), nil)
	for _, c := range completionsFor("l") {
		if c.value == "ls" {
			t.Errorf("pkgx l<TAB> offers the pkgs verb ls")
		}
	}
	asPkgs(t)
	found := false
	for _, c := range completionsFor("l") {
		if c.value == "ls" {
			found = true
		}
	}
	if !found {
		t.Error("pkgs l<TAB> does not offer ls")
	}
}

// ⛔ THE WORD THE USER TYPED IS THE PROGRAM THAT RUNS.
//
// Found by running the thing: `pkgx ls --version` printed
//
//	[ (GNU coreutils) 9.11
//	Written by Kevin Braunsdorf and Matthew Bradburn
//
// — GNU coreutils' real banner, from `[` rather than from `ls`, because the
// bare word was resolved to a PROJECT and then discarded, and the project's
// primary program is its first `provides` entry. It looked plausible enough
// that only the authors' names gave it away.
//
// Nobody had met it because `ls` was a pkgx subcommand until this change and
// never reached the package path at all. Giving the word back to the
// registry uncovered it.
func TestABareCommandRunsThatCommandNotTheProjectsFirstBinary(t *testing.T) {
	dir := t.TempDir()
	const proj, ver = "gnu.org/coreutils", "9.11"
	binDir := filepath.Join(dir, filepath.FromSlash(proj), "v"+ver, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Both exist, and `[` sorts first exactly as it does in the real recipe.
	for _, name := range []string{"[", "ls"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	closure := []bottle.Resolved{{Project: proj, Version: bottle.ParseVer(ver)}}

	got, err := resolveBin(proj, "ls", nil, closure, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "ls" {
		t.Errorf("resolveBin ran %q, want ls", got)
	}

	// ⛔ THE CONTROL. A PROJECT name carries no command, so the primary
	// program must still win — `pkgx gnu.org/bash` has to run bash, and a
	// fix that always preferred the typed word would have nothing to prefer
	// here and must not break.
	got, err = resolveBin(proj, "", nil, closure, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) == "ls" {
		t.Errorf("with no command named, resolveBin picked %q instead of the primary program", got)
	}
	// ⛔ AND IT IS A FILE. Dropping the `program != ""` guard makes the new
	// branch join an EMPTY name onto the bin directory, os.Stat finds the
	// directory, and resolveBin hands back a path to exec that is not a
	// program at all. The first version of this control only compared the
	// base name — "bin" is not "ls", so it passed, and the mutation
	// survived. A name is not a file.
	if st, err := os.Stat(got); err != nil || st.IsDir() {
		t.Errorf("resolveBin returned %q, which is not an executable file (err=%v)", got, err)
	}

	// A command the project does not install falls back rather than
	// refusing: the mapper said this project provides it, and the next step
	// reports what it could not run.
	if _, err := resolveBin(proj, "nosuchbin", nil, closure, dir); err != nil {
		t.Errorf("an unknown command refused instead of falling back: %v", err)
	}
}

// ⛔ THE WIRING, NOT ONLY THE FUNCTION.
//
// The test above covers resolveBin. It did NOT cover the line that decides
// what to hand it, and a mutation deleting that line survived the whole
// suite — the exact failure surface_test.go's own comment warns about: "an
// earlier test in this repo tested a function and not the wiring, and the
// wiring was what was broken."
func TestRunTargetKeepsTheTypedCommand(t *testing.T) {
	withCatalog(t, bottle.Catalog{Projects: []bottle.CatalogProject{
		{Project: "gnu.org/coreutils", Provides: []string{"[", "ls"}},
		{Project: "gnu.org/bash", Provides: []string{"bash"}},
	}}, nil)
	cmds := &commandMapper{dir: t.TempDir()}

	for _, c := range []struct{ word, project, program string }{
		// A bare COMMAND: the word is the program.
		{"ls", "gnu.org/coreutils", "ls"},
		// With a constraint, the constraint is not part of the program.
		{"ls@9", "gnu.org/coreutils", "ls"},
		// A PROJECT names no command, so the primary program wins.
		{"gnu.org/bash", "gnu.org/bash", ""},
	} {
		proj, _, prog, err := runTarget(c.word, cmds)
		if err != nil {
			t.Fatalf("%s: %v", c.word, err)
		}
		if proj != c.project {
			t.Errorf("%s resolved to %q, want %q", c.word, proj, c.project)
		}
		if prog != c.program {
			t.Errorf("%s would run %q, want %q", c.word, prog, c.program)
		}
	}
}

// ⛔ THE RELEASE STAMPS THE SYMBOL THAT EXISTS.
//
// `version` lived in package main until the two binaries were split, and
// release.yml went on stamping `-X main.version`. A `-X` naming a symbol
// that does not exist is SILENT: the linker neither warns nor fails. v0.22.0
// therefore built twelve binaries, every one stamped v0.22.0 in its build
// info and every one printing "dev", and the only thing that noticed was the
// workflow's own comparison of the PRINTED version against the tag — which
// runs after six cross-compilations, on a tag that cannot be rebuilt.
//
// This is the same check, one second into `go test`. It reads the workflow
// rather than trusting a comment, because the comment was right and the
// command below it was not.
func TestTheReleaseStampsThisPackagesVersion(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Skipf("no release workflow to read here: %v", err)
	}
	wf := string(b)
	const want = "-X github.com/go-pkgx/pkgx/internal/app.version="
	if !strings.Contains(wf, want) {
		t.Errorf("release.yml does not stamp %q — the binaries would print %q", want, version)
	}
	// And not the old one, which is the shape that fails silently.
	if strings.Contains(wf, "-X main.version=") {
		t.Error("release.yml still stamps main.version, a symbol neither main declares")
	}
	// Both binaries are built, or a release ships half of itself.
	for _, pkg := range []string{`-o "$DIST/${out}" "$pkg"`, "./cmd/pkgs"} {
		if !strings.Contains(wf, pkg) {
			t.Errorf("release.yml does not look like it builds both binaries: %q is absent", pkg)
		}
	}
}
