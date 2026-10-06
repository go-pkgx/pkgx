package main

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
// This pantry does not: measured on its 1907 recipes, 1592 declare the
// commands they provide and SIX carry a summary. A search over prose would
// find six packages.
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
		fmt.Fprintln(stderr, "pkgx: usage: pkgx search [-n N] <text>")
		return 2
	}

	cat, src, err := browseCatalog(bottle.Dir())
	if err != nil {
		fmt.Fprintf(stderr, "pkgx: PKGX_CATALOG names %s and it cannot be read: %v\n", src, err)
		return 2
	}
	hits := cat.Search(query)
	if len(hits) == 0 {
		// Said on STDERR and exiting 1, so `pkgx search x || echo none`
		// works and a shell pipeline is not fed a reassuring silence.
		fmt.Fprintf(stderr, "pkgx: nothing matches %q in %s\n", query, src)
		if !knowsCommands(cat) {
			// The difference between "no such package" and "this
			// catalogue cannot answer that kind of question". A
			// catalogue published before commands were carried matches
			// names only, and a reader deserves to know which of the two
			// they are looking at.
			fmt.Fprintln(stderr, "pkgx: this catalogue carries no command names — only project names were searched")
		}
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
		fmt.Fprintf(stderr, "pkgx: %d more; pkgx search -n 0 %s\n", len(hits)-len(shown), query)
	}
	return 0
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
