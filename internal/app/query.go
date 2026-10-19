package app

import (
	"fmt"
	"io"
	"sort"

	"github.com/go-pkgx/bottle"
)

// `pkgx -Q` — upstream's query mode, which this fork had never implemented.
//
// ⛔ ITS ABSENCE IS WHY THE GRAMMAR BROKE. Upstream pkgx answers "what can
// you run?" with a FLAG, precisely because the first positional is always a
// package:
//
//	pkgx -Q git     # can you run git? prints the project that provides it
//	pkgx -Q         # everything that could be run
//
// Not having it, this fork reached for the one position upstream reserves
// and put nine verbs in it — two of which, `ls` and `env`, shadowed commands
// gnu.org/coreutils provides. The verbs are in `pkgs` now; this is the half
// that should have been here all along.
//
// It reads the CATALOGUE, so it needs no network and works in a scratch
// image, and it answers from the same file `pkgs ls` and <TAB> read.
func runQuery(args []string, stdout, stderr io.Writer) int {
	cat, src, err := browseCatalog(bottle.Dir())
	if err != nil {
		// Same treatment the other readers give it: a named and unreadable
		// PKGX_CATALOG is an error, not a quiet fallback.
		fmt.Fprintf(stderr, pfx()+"PKGX_CATALOG names %s and it cannot be read: %v\n", src, err)
		return 2
	}
	if len(cat.Projects) == 0 {
		fmt.Fprintln(stderr, pfx()+"no catalogue here; run: pkgs catalog update")
		return 1
	}
	switch len(args) {
	case 0:
		return queryAll(cat, stdout)
	case 1:
		return queryOne(cat, args[0], stdout, stderr)
	}
	fmt.Fprintln(stderr, pfx()+"usage: "+tool+" -Q [command]")
	return 2
}

// queryAll lists every command the catalogue says can be run, one per line,
// with the project that provides it.
//
// Upstream prints project names; this prints the COMMAND and its project,
// because the question "-Q with no argument" answers is "what could I type",
// and a project name is not always something you can type — gnu.org/coreutils
// is not a command.
func queryAll(cat bottle.Catalog, stdout io.Writer) int {
	type pair struct{ cmd, project string }
	var out []pair
	for _, p := range cat.Projects {
		for _, c := range p.Provides {
			out = append(out, pair{c, p.Project})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].cmd != out[j].cmd {
			return out[i].cmd < out[j].cmd
		}
		return out[i].project < out[j].project
	})
	for _, p := range out {
		fmt.Fprintf(stdout, "%-30s %s\n", p.cmd, p.project)
	}
	return 0
}

// queryOne answers whether one command can be run, and by which project.
//
// ⛔ IT PRINTS EVERY PROVIDER AND EXITS 0, rather than picking one. Two
// projects can provide the same command — the catalogue is the authority on
// that, not this function — and choosing silently is how a caller ends up
// running something it did not ask for. Exit 1 means NOTHING provides it,
// which is the question upstream's -Q is actually used to answer in a
// script.
//
// ⛔ IT ASKS bottle.ProvidersOf RATHER THAN WALKING Provides ITSELF. The
// first draft of this file carried a `commandLeaf` helper that stripped a
// "bin/" prefix, on the assumption that a catalogue stores `provides` the
// way a RECIPE writes it. It does not — `bk catalog` writes the bare
// command, which is why `ProvidersOf` compares with `==`. Two readers of
// one field, disagreeing about its shape, is how a question gets two
// answers; this is the reader the rest of the program already uses.
func queryOne(cat bottle.Catalog, cmd string, stdout, stderr io.Writer) int {
	hits := cat.ProvidersOf(cmd)
	if len(hits) == 0 {
		fmt.Fprintf(stderr, pfx()+"nothing in this catalogue provides %q\n", cmd)
		return 1
	}
	sort.Strings(hits)
	for _, h := range hits {
		fmt.Fprintln(stdout, h)
	}
	return 0
}

// unknownVerb answers a bare word that used to be a `pkgx` verb and is now
// one of `pkgs`'s, and reports whether it said anything.
//
// ⛔ ONLY WHEN THE REGISTRY HAS NOTHING BY THAT NAME. `pkgx ls` must run
// gnu.org/coreutils' ls — that is the whole point of moving the verbs — so
// this must not intercept a word a package provides. It checks the
// catalogue first and stays silent when there is a package to run, which
// means `pkgx ls` runs ls and `pkgx why` (no package named why) is told
// where `why` went instead of failing with "no such package".
//
// A catalogue that cannot be read is a silence, not a guess: the resolver
// one line later gives the better error.
// pkgeSubcommands are the words the generated pkge shell function passes to
// `env`. They are the whole reason staleEnvCall exists; see it.
var pkgeSubcommands = []string{"init", "load", "unload", "purge", "avail", "show", "import"}

// staleEnvCall answers `pkgx env <pkge subcommand>`, and reports whether it
// said anything.
//
// ⛔ ONE WORD WIDE, AND IT IS THE ONE THAT BITES. Of the nine verbs that
// moved to pkgs, `env` is the only one that is BOTH provided by a package —
// gnu.org/coreutils — AND baked into something users already have: the pkge
// shell function, which a profile sources and which runs
//
//	command pkgx env "$_pkge_cmd" "$@"
//
// Every other moved word reaches unknownVerb and is told where it went.
// `env` does not: a package provides it, so pkgx correctly runs GNU env, and
// a profile written last month answers `pkge load foo` with
//
//	env: 'load': No such file or directory      (exit 127)
//
// which names neither pkgx nor pkgs and looks like the user's fault. That is
// the same shape as the `pkgx ls` → `[` defect the split already uncovered:
// a plausible error from the wrong program.
//
// ⛔ IT REFUSES; IT DOES NOT DO THE WORK. This is a transition note, not a
// verb coming back — pkgx never reads an environment again. The cost is that
// `pkgx env show` can no longer mean "run GNU env, then a program called
// show"; the message says exactly what happened, and `env` with any other
// argument still runs coreutils as it should.
func staleEnvCall(argv []string, stderr io.Writer) (int, bool) {
	if len(argv) < 2 || argv[0] != "env" {
		return 0, false
	}
	for _, s := range pkgeSubcommands {
		if argv[1] != s {
			continue
		}
		fmt.Fprintf(stderr, "%senv %s is pkgs's: pkgs env %s\n", pfx(), argv[1], argv[1])
		fmt.Fprintf(stderr, "%sif a profile is still calling this, its pkge function is from before the split — re-run: eval \"$(pkgs env init)\"\n", pfx())
		fmt.Fprintf(stderr, "%s(to run gnu.org/coreutils' env instead, give it something that is not a pkge subcommand)\n", pfx())
		return 2, true
	}
	return 0, false
}

func unknownVerb(word string, stderr io.Writer) (int, bool) {
	if !isVerb(word) {
		return 0, false
	}
	cat, _, err := browseCatalog(bottle.Dir())
	if err == nil {
		if len(cat.ProvidersOf(word)) > 0 {
			return 0, false
		}
		for _, p := range cat.Projects {
			if p.Project == word {
				return 0, false
			}
		}
	}
	fmt.Fprintf(stderr, "%s%q is a pkgs command, not a pkgx one: pkgs %s\n", pfx(), word, word)
	fmt.Fprintf(stderr, "%spkgx runs packages; pkgs answers about them. `pkgs -h` lists its verbs.\n", pfx())
	return 2, true
}
