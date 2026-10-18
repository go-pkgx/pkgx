package app

import (
	"fmt"
	"strings"

	"github.com/go-pkgx/bottle"
)

// Resolving the COMMAND a person knows to the project that provides it.
//
// # The report
//
// From a colleague on 2026-10-12, replacing pkgx 2.10.3 with go-pkgx on a
// macOS arm64 workstation for a Slurm-on-OpenStack deployment. Seven tools
// tried — go, terraform, tofu, hugo, gh, task, openstack — and the short
// program name failed for every one of them:
//
//	$ pkgx hugo version
//	pkgx: no recipe for hugo in …pantry-overlay… or …pkgxdev/pantry…
//
// while in the same store, the same minute:
//
//	$ pkgx search hugo
//	gohugo.io                                hugo  no bottle here
//
// So the mapping was already in the catalogue and only the SEARCH path
// consulted it. Their conclusion, and it is the right one: without this,
// every Taskfile, every doc and every habit (`pkgx <tool>`) has to be
// rewritten with project names, which is the first obstacle to replacing
// pkgx at all. The README's own example is `pkgx node@22 --version`.
//
// # Why a bare word is never a project
//
// Every project in the pantry is a domain or a domain/path. Measured rather
// than assumed, with `go run ./internal/catstat` over the published
// darwin/aarch64 catalogue of 2026-10-12: of **1915** project names, **0**
// lack both a dot and a slash. So "has neither" is an exact test for "this is
// a command, not a project", and catstat is the instrument that can say so
// again when the data moves.
//
// That matters for more than tidiness: it is what lets the catalogue stay
// unread for an ordinary project name. `pkgx +deps` runs inside every build
// the factory does, in a loop, and making it parse a 467 KB catalogue to
// discover that `gnu.org/bash` contains a slash would be a cost paid on every
// call for nothing.

// bareWord reports whether a spec names a command rather than a project.
func bareWord(spec string) bool {
	return !strings.ContainsAny(project(spec), "./")
}

// commandMapper rewrites bare commands to projects, reading the catalogue at
// most once and only if it is asked something it needs the catalogue for.
type commandMapper struct {
	dir    string
	cat    bottle.Catalog
	src    string
	err    error
	loaded bool
}

func (m *commandMapper) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	m.cat, m.src, m.err = browseCatalog(m.dir)
}

// resolve returns the PROJECT and the CONSTRAINT a spec names, mapping a bare
// command to the project that provides it: `hugo` gives `gohugo.io` and `*`,
// `hugo@0.167.0` gives `gohugo.io` and `0.167.0`.
//
// The two come back separately rather than as a rewritten spec, because a spec
// cannot be rebuilt by concatenation: splitSpec reports `*` when there is no
// constraint and DROPS the `@` of the `@` form, so `hugo` would rejoin as
// `gohugo.io*` and `hugo@0.167.0` as `gohugo.io0.167.0`. Both callers want the
// pair anyway — one for `roots`, one for the project to run.
//
// A spec that is already a project name is answered without the catalogue
// being read at all.
func (m *commandMapper) resolve(s string) (string, string, error) {
	proj, cons := splitSpec(s)
	if !bareWord(s) {
		return proj, cons, nil
	}
	cmd := proj
	m.load()
	if m.err != nil || len(m.cat.Projects) == 0 {
		// ⛔ NOT the pantry's "no recipe for hugo" message. A bare word is
		// never a project, so going on to look for a project called `hugo`
		// can only produce an error about the wrong thing — which is exactly
		// what the report ran into, and why it reads as "go-pkgx does not
		// know hugo" rather than "this store has no catalogue yet".
		return "", "", fmt.Errorf("%q is a command, not a project, and there is no catalogue to look it up in — run: pkgx catalog update", cmd)
	}
	switch hits := m.cat.ProvidersOf(cmd); len(hits) {
	case 1:
		return hits[0], cons, nil
	case 0:
		// `provides` is carried by 1588 of 1915 projects (82.9%, same
		// measurement), so a miss is not proof that nothing provides the
		// command — hence "declares" rather than "provides", and a pointer
		// at the search that looks in names too.
		return "", "", fmt.Errorf("no project in %s declares the command %q — try: pkgx search %s", m.src, cmd, cmd)
	default:
		// ⛔ NEVER pick one. `provides` says what a project puts on PATH,
		// not who owns the name, so there is no basis here for choosing —
		// and the cost of guessing is running the wrong program under the
		// right name, which is worse than stopping.
		return "", "", fmt.Errorf("%d projects declare the command %q: %s — name the one you mean",
			len(hits), cmd, strings.Join(hits, ", "))
	}
}
