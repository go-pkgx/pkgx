package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/go-pkgx/bottle"
)

// `pkgx store` — what is on this disk, which nothing could show.
//
// # WHY
//
// Every ephemeral `pkgx <pkg>` leaves its bottles in
// $PKGX_DIR/<project>/v<version>, and nothing ever removes one. `pkgm list`
// answers a different question — what did I ask to have installed — and a
// bottle pulled to run one command six months ago is in neither.
//
// Measured on this developer's machine, before this command existed:
// 41.2 GiB across 363 versions and 302 projects, 12.8 GiB of it in versions
// that are not the newest present. Nobody chose that, and nothing could
// have told them.
//
// # IT REPORTS AND DELETES NOTHING, DELIBERATELY
//
// nix, guix and spack all keep the two apart — `nix path-info -S` against
// `nix store gc`, `guix gc --list-live` against `guix gc`, `spack find`
// against `spack gc` — and all three make the ROOTS explicit before
// removing anything. There is no profile here, so there is no root set: a
// version that looks superseded may be exactly what a lock pins or what an
// environment asks for. "Older than another version here" is a fact about
// the disk; "garbage" would be a claim about intent.
//
// # IT WALKS, SO IT IS SECONDS
//
// Eight seconds over 41 GiB on an SSD. That is why it is its own command
// and not a line in `pkgx catalog`: nothing on the completion path may cost
// that, and a figure worth having is worth waiting for once.
func runStore(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("store", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bySize := fs.Bool("by-size", false, "largest first, instead of by name")
	top := fs.Int("n", 0, "show only the first N projects (0 = all)")
	// ROOTS THE CALLER NAMES, because there are none to find.
	//
	// guix's rule is the one copied here: what is reachable from a root is
	// live, everything else is dead — and the roots are explicit. There is
	// no profile in this design, so the only honest root set is one handed
	// in, and a LOCK is already exactly that: it pins the whole closure,
	// not only what somebody typed, so membership needs no graph walk and
	// no network.
	//
	// An environment would not do. It names constraints, which have to be
	// resolved against the registry, and this command works offline.
	var roots stringList
	fs.Var(&roots, "root", "a `bk lock` file whose pins are live; repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "pkgx: usage: pkgx store [--by-size] [-n N]")
		return 2
	}
	dir := bottle.Dir()
	// THE ROOTS ARE READ BEFORE THE STORE IS WALKED. A mistyped lock path
	// should cost a moment, not the eight seconds it takes to scan 41 GiB
	// and then be told the arguments were wrong.
	var locks []bottle.Lock
	for _, p := range roots {
		l, err := bottle.ReadLock(p)
		if err != nil {
			fmt.Fprintf(stderr, "pkgx: --root %s: %v\n", p, err)
			return 2
		}
		locks = append(locks, l)
	}
	entries, err := bottle.ScanStore(dir)
	// EMPTY AND ABSENT GET THE SAME SENTENCE, because the difference is the
	// code's business and not the reader's.
	//
	// A FROM scratch image ships a catalogue and no bottles, so its store
	// directory EXISTS and holds nothing. The first version printed
	//
	//	/pkgx
	//
	//	0 B over 0 version(s) of 0 project(s)
	//
	// which is correct and tells nobody anything. Found by running it in
	// the image rather than by reading the function.
	if err == nil && len(entries) == 0 {
		fmt.Fprintf(stdout, "%s\nnothing here yet — a store fills up the first time pkgx runs something\n", dir)
		return 0
	}
	if err != nil {
		if os.IsNotExist(err) {
			// An absent store is the state of every fresh machine, and the
			// useful thing to print is where it would be.
			fmt.Fprintf(stdout, "%s\nnothing here yet — a store fills up the first time pkgx runs something\n", dir)
			return 0
		}
		fmt.Fprintf(stderr, "pkgx: %s: %v\n", dir, err)
		return 2
	}

	// Per project, because a version is not what a person decides about.
	type row struct {
		project  string
		bytes    int64
		versions int
		older    int64
	}
	byProject := map[string]*row{}
	for _, e := range entries {
		r := byProject[e.Project]
		if r == nil {
			r = &row{project: e.Project}
			byProject[e.Project] = r
		}
		r.bytes += e.Bytes
		r.versions++
		if !e.Newest {
			r.older += e.Bytes
		}
	}
	rows := make([]*row, 0, len(byProject))
	for _, r := range byProject {
		rows = append(rows, r)
	}
	// By name by default; by size when asked, with the name breaking ties so
	// two runs over one store print the same thing. A report that reorders
	// itself between runs cannot be diffed, which is most of what a report
	// about a growing disk is for.
	sort.Slice(rows, func(i, j int) bool {
		if *bySize && rows[i].bytes != rows[j].bytes {
			return rows[i].bytes > rows[j].bytes
		}
		return rows[i].project < rows[j].project
	})

	total, versions, projects, older := bottle.StoreTotals(entries)
	fmt.Fprintf(stdout, "%s\n", dir)
	shown := rows
	if *top > 0 && *top < len(rows) {
		shown = rows[:*top]
	}
	for _, r := range shown {
		note := ""
		if r.versions > 1 {
			note = fmt.Sprintf("  %d versions, %s not the newest here", r.versions, human(r.older))
		}
		fmt.Fprintf(stdout, "%-40s %10s%s\n", r.project, human(r.bytes), note)
	}
	if len(shown) < len(rows) {
		fmt.Fprintf(stdout, "… %d more\n", len(rows)-len(shown))
	}
	fmt.Fprintf(stdout, "\n%s over %d version(s) of %d project(s)\n", human(total), versions, projects)
	if older > 0 {
		// SAID AS A FACT, NOT AS ADVICE. A lock or an environment may ask
		// for exactly these, and this command has no way to know.
		fmt.Fprintf(stdout, "%s is in versions that are not the newest present — which is not the same as unused\n", human(older))
	}
	if len(roots) > 0 {
		live, dead := bottle.LiveFromLocks(entries, locks)
		lb, lv, _, _ := bottle.StoreTotals(live)
		db, dv, _, _ := bottle.StoreTotals(dead)
		fmt.Fprintf(stdout, "\nagainst %d root(s): %s live over %d version(s), %s in %d version(s) no root needs\n",
			len(roots), human(lb), lv, human(db), dv)
		// WHOSE ROOTS, SAID EVERY TIME. Name a different lock and a
		// different half of the store is dead; the judgement is the
		// caller's and the report has to hand it back rather than imply
		// the store has an opinion. guix says the same thing by making
		// `--list-dead` a listing and `gc` a separate command.
		fmt.Fprintln(stdout, "dead here means NOT REACHABLE FROM THE ROOTS YOU NAMED, and nothing is removed")
	}
	return 0
}

// human prints bytes the way a person reads them, in powers of 1024, and
// says so with the i: 41.2 GiB is not 42 GB, and `du` and this command
// disagree for that reason and for counting blocks rather than sizes.
func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// stringList is a repeatable flag. Repeatable rather than
// comma-separated, because these are PATHS and a comma is a legal
// character in one.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }
