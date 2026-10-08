package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "pkgx: usage: pkgx store [--by-size] [-n N]")
		return 2
	}
	dir := bottle.Dir()
	entries, err := bottle.ScanStore(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// NOT AN ERROR. An absent store is the state of every fresh
			// machine, and the useful thing to print is where it would be.
			fmt.Fprintf(stdout, "%s\nnothing here yet — a store appears the first time pkgx runs something\n", dir)
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
