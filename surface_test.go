package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// THREE LISTS DESCRIBE THE SAME COMMAND SURFACE and nothing made them
// agree: the dispatch in run(), the `usage` text, and
// subcommandCompletions. A subcommand can be added to one and missed in
// the others, and each omission fails differently —
//
//	missing from the dispatch    it does not run
//	missing from the usage       nobody finds it
//	missing from the completion  nobody finds it either, more quietly
//
// — and none of them fails a test, because each list is correct about
// itself. This repository has already shipped the third kind.
//
// So: every advertised subcommand must be REACHABLE, and every reachable
// one must be ADVERTISED, both ways round.
func TestTheCommandSurfaceAgreesWithItself(t *testing.T) {
	withCatalog(t, browseCat(), nil)

	// What completion offers. The source of truth for "advertised".
	var offered []string
	for _, c := range subcommandCompletions {
		offered = append(offered, c.value)
	}
	if len(offered) < 5 {
		t.Fatalf("only %d subcommands offered — this test is looking at the wrong list", len(offered))
	}

	for _, name := range offered {
		// ADVERTISED ⇒ IN THE USAGE, on a LINE OF ITS OWN. A reader's
		// first question is `--help`, and a command absent from it may
		// as well not exist.
		//
		// The line, not a substring anywhere: `strings.Contains(usage,
		// "pkgx browse")` passes on a usage with no browse entry at all,
		// because browse's own help text says `pkgx +$(pkgx browse)` and
		// the grep matches that mention. Found by mutating the usage and
		// watching the suite stay green.
		if !usageListsCommand(name) {
			t.Errorf("%q is offered at <TAB> but has no line in the usage", name)
		}

		// ADVERTISED ⇒ REACHABLE. Run it with no arguments and require
		// that it is NOT treated as a package to install: the dispatch
		// either handles it or falls through to "run this package",
		// which is the failure this catches.
		code, _, errb := runSurface(t, name)
		if strings.Contains(errb, "no recipe for "+name) ||
			strings.Contains(errb, "cannot read "+name) {
			t.Errorf("%q fell through to the package path — it is not dispatched (code=%d)", name, code)
		}
	}

	// And the other direction, for the subcommands this file knows about:
	// reachable ⇒ advertised. Named explicitly rather than parsed out of
	// the source, because a test that greps its own program for `case`
	// statements is a test that breaks on an `if`, which is exactly how
	// `env` is dispatched here.
	for _, name := range []string{"ls", "catalog", "search", "browse", "completion", "compat", "env"} {
		found := false
		for _, o := range offered {
			if o == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is dispatched but never offered at <TAB>", name)
		}
	}
}

// runSurface calls a subcommand with no arguments and captures BOTH
// streams. Most answer with a usage error, which is fine — the question is
// only whether the dispatch SAW it, and the evidence for "it did not" is
// on stderr.
func runSurface(t *testing.T, name string) (int, string, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()

	var code int
	out := captureStdout(t, func() { code = run([]string{name}) })

	os.Stderr = old
	_ = w.Close()
	errb := <-done
	_ = r.Close()
	return code, out, errb
}

// usageListsCommand reports whether the usage has an ENTRY for a command —
// a line whose first words are `pkgx <name>` — rather than a mention of it
// somewhere in another entry's prose.
func usageListsCommand(name string) bool {
	for _, line := range strings.Split(usage, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "pkgx" && f[1] == name {
			return true
		}
	}
	return false
}
