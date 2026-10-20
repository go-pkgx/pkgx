package app

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/go-pkgx/bottle"
)

// `pkgx search` — for when you know the COMMAND and not the package.
//
// # WHY NOT A DESCRIPTION SEARCH
//
// `nix search`, `guix search` and `spack list -s` all match a package's
// description, and that is right for a collection that has descriptions.
// This pantry does not. Re-measured 2026-10-20 over the upstream corpus,
// parsed as YAML rather than grepped (the word "description:" also appears
// inside build scripts and URLs there):
//
//	1928  recipes read
//	 259  carry a `display-name` — usually the project name recapitalised
//	   6  carry a `summary` or a `description`      (0.3%)
//
// and in the catalogue those recipes produce, 1938 projects, 1608 with
// commands and the SAME six summaries — so nothing is lost between the
// recipe and the catalogue, and a search over prose can only ever find six
// packages. (The earlier note here said 1907 and 1592; the corpus grew, the
// six did not.)
//
// So the match is on names and on commands, and that turns out to be the
// question people actually arrive with. `rg` is `crates.io/ripgrep`: no
// amount of guessing at the project name reaches it, and <TAB> — which is
// a PREFIX on the project path — never will either. Completion and search
// are different tools and this is the line between them.
//
// Offline, from the catalogue `pkgx catalog update` wrote, like everything
// else that reads it.
func runSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("n", 20, "how many results to print; 0 is all")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	query := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(query) == "" {
		fmt.Fprintln(stderr, pfx()+"usage: "+tool+" search [-n N] <text>")
		return 2
	}

	cat, src, err := browseCatalog(bottle.Dir())
	if err != nil {
		fmt.Fprintf(stderr, pfx()+"PKGX_CATALOG names %s and it cannot be read: %v\n", src, err)
		return 2
	}
	hits := cat.Search(query)
	if len(hits) == 0 {
		// Said on STDERR and exiting 1, so `pkgx search x || echo none`
		// works and a shell pipeline is not fed a reassuring silence.
		fmt.Fprintf(stderr, pfx()+"nothing matches %q in %s\n", query, src)
		if !knowsCommands(cat) {
			// The difference between "no such package" and "this
			// catalogue cannot answer that kind of question". A
			// catalogue published before commands were carried matches
			// names only, and a reader deserves to know which of the two
			// they are looking at.
			fmt.Fprintln(stderr, pfx()+"this catalogue carries no command names — only project names were searched")
		}
		sayProseIsThin(cat, stderr)
		return 1
	}

	shown := hits
	if *limit > 0 && len(hits) > *limit {
		shown = hits[:*limit]
	}
	for _, h := range shown {
		fmt.Fprintf(stdout, "%-40s %s\n", h.Project, searchNote(cat, h))
	}
	if len(shown) < len(hits) {
		fmt.Fprintf(stderr, "%s%d more; %s search -n 0 %s\n", pfx(), len(hits)-len(shown), tool, query)
	}
	sayProseIsThin(cat, stderr)
	return 0
}

// proseFloor is the share of projects that must carry a description before
// the catalogue is treated as searchable by prose: one in five.
const proseFloor = 5

// sayProseIsThin tells the reader what the search could NOT look at.
//
// ⛔ IT IS SAID WHEN THERE *ARE* RESULTS, which is the whole point. A search
// that answers nothing already makes the reader suspicious; a search that
// answers THIRTEEN THINGS reads as a complete answer. Measured 2026-10-20 on
// the published catalogue, `search json` returns 13 projects and none of
// them is `stedolan.github.io/jq` — jq IS there, with the command `jq` and a
// bottle, and the word "json" appears in neither its path nor its commands.
// Nothing in that output suggests jq exists, so the reader concludes it is
// not packaged. That is the quietest kind of wrong answer, and one line of
// stderr is the whole fix.
//
// On stderr, so `pkgs search x > list` still writes only results; and only
// when the catalogue really is thin, so a future one with descriptions is
// silent rather than nagging.
func sayProseIsThin(cat bottle.Catalog, stderr io.Writer) {
	with, total := proseCoverage(cat)
	if total == 0 || with*proseFloor >= total {
		return
	}
	fmt.Fprintf(stderr, "%sonly %d of %d projects here carry a description, so a word that is in neither a project's name nor a command it provides cannot reach it\n",
		pfx(), with, total)
}

func proseCoverage(cat bottle.Catalog) (with, total int) {
	for _, p := range cat.Projects {
		total++
		if strings.TrimSpace(p.Summary) != "" {
			with++
		}
	}
	return with, total
}

// searchNote says WHY a result is on the page, and what you would get.
//
// An unexplained list of forty projects is a list nobody reads to the end.
// A command hit shows the command, because that is the word the reader
// typed and the thing they will run.
func searchNote(cat bottle.Catalog, h bottle.SearchHit) string {
	var parts []string
	switch h.Why {
	case "command":
		parts = append(parts, h.Match)
	case "summary":
		parts = append(parts, h.Match)
	}
	if p, ok := cat.Lookup(h.Project); ok {
		switch {
		case len(p.Versions) > 0:
			parts = append(parts, p.Versions[0])
		case knowsVersions(cat):
			parts = append(parts, noBottle)
		}
		parts = append(parts, strings.TrimSpace(haveNote(h.Project, firstVersion(p))))
	}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "  ")
}

func firstVersion(p bottle.CatalogProject) string {
	if len(p.Versions) == 0 {
		return ""
	}
	return p.Versions[0]
}

// knowsCommands reports whether this catalogue carries command names at
// all, the same way knowsVersions reports on versions: there is no flag,
// and the only evidence is that a catalogue which has them has them
// somewhere.
func knowsCommands(cat bottle.Catalog) bool {
	for _, p := range cat.Projects {
		if len(p.Provides) > 0 {
			return true
		}
	}
	return false
}
