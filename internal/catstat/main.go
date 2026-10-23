// Command catstat says what a published catalogue actually CONTAINS.
//
// # WHY IT EXISTS
//
// `pkgx search` ranks on the project name, on the command names in
// `provides`, and on the summary. Whether each of those does anything at
// all is a property of the published DATA, not of the code, and reading the
// code cannot tell you: a field that is always empty gives a search that
// silently degrades, and every test written against a hand-made catalogue
// passes.
//
// It is also the instrument for the opposite mistake. `pkgx ls` marks a
// project "no bottle here" from the same file, and how many of those there
// are on a given platform decides whether that annotation is a footnote or
// the main thing a reader sees.
//
// # IT USES bottle's OWN TYPE, AND THE FIRST VERSION DID NOT
//
// The first version declared the JSON shape by hand and asked for
// `dependencies`. The field is `deps`, so it found none, and was about to
// report "0% of projects have dependencies" about a catalogue in which 986
// do — a number `pkgx catalog` prints correctly one command away.
//
// A census must count what the client counts. Restating a schema beside the
// schema makes the restatement a dependency that nothing updates, so this
// goes through bottle.UnmarshalCatalog: the same parser, the same
// validation, the same sanitising.
//
//	go run ./internal/catstat catalog/linux-aarch64.json [more.json...]
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

func main() {
	fs := flag.NewFlagSet("catstat", flag.ContinueOnError)
	missing := fs.Bool("missing", false, "name the projects with no bottle on this platform, instead of counting them")
	limit := fs.Int("n", 40, "with -missing, how many names to print (0 prints them all)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: catstat [-missing [-n N]] <catalogue.json>...")
		os.Exit(2)
	}
	status := 0
	for _, path := range fs.Args() {
		var err error
		if *missing {
			err = listMissing(os.Stdout, path, *limit)
		} else {
			err = report(os.Stdout, path)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "catstat:", err)
			status = 1
		}
	}
	os.Exit(status)
}

// listMissing names what the count leaves anonymous.
//
// ⛔ A PERCENTAGE IS NOT A WORK LIST. "with a bottle 1373 (70.8%)" says 567
// projects on darwin/aarch64 have a recipe and nothing to install, and that
// number cannot be acted on: nobody can file, build or triage a percentage.
// The names are already in the file, so this is a read, not a sweep — no
// registry call, no guess at which of 1940 projects to probe.
//
// It is also the only honest way to pick an example of the state. Choosing
// one by hand means choosing one I already believe in, and the last three
// times I did that in this repository the belief was out of date.
func listMissing(w io.Writer, path string, limit int) error {
	c, err := readCatalog(path)
	if err != nil {
		return err
	}
	var names []string
	for _, p := range c.Projects {
		if len(p.Versions) == 0 {
			names = append(names, p.Project)
		}
	}
	sort.Strings(names)
	shown := names
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	fmt.Fprintf(w, "%s\n", path)
	fmt.Fprintf(w, "  %d of %d project(s) have no bottle here\n", len(names), len(c.Projects))
	for _, n := range shown {
		fmt.Fprintf(w, "  %s\n", n)
	}
	if len(shown) < len(names) {
		fmt.Fprintf(w, "  … %d more (-n 0 for all)\n", len(names)-len(shown))
	}
	return nil
}

// readCatalog is the one door both reports come through, so a catalogue that
// is empty or will not parse is refused once rather than in each of them.
func readCatalog(path string) (bottle.Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return bottle.Catalog{}, err
	}
	c, err := bottle.UnmarshalCatalog(b)
	if err != nil {
		return bottle.Catalog{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(c.Projects) == 0 {
		// A catalogue with no projects is not a census of nothing, it is a
		// broken file, and every percentage below would be a division by
		// zero dressed up as a finding — or, for -missing, an empty work
		// list that reads as "nothing to do".
		return bottle.Catalog{}, fmt.Errorf("%s: no projects", path)
	}
	return c, nil
}

func report(w io.Writer, path string) error {
	c, err := readCatalog(path)
	if err != nil {
		return err
	}
	n := len(c.Projects)
	// bareNames are project names with neither a dot nor a slash in them.
	//
	// Counted because a decision rests on it: `pkgx <command>` resolves a
	// bare word through `provides` and must not do that for something that
	// IS a project. Every pantry name is a domain or a domain/path, so the
	// test is exact rather than a guess — but that is a property of the
	// published DATA, and this is the instrument that can say so instead of
	// the claim sitting in a comment nobody can check.
	var summaries, provides, bottled, deps, cmds, bareNames int
	namespaces := map[string]bool{}
	platforms := map[string]bool{}
	for _, p := range c.Projects {
		if strings.TrimSpace(p.Summary) != "" {
			summaries++
		}
		if len(p.Provides) > 0 {
			provides++
			cmds += len(p.Provides)
		}
		if len(p.Versions) > 0 {
			bottled++
		}
		if len(p.Deps) > 0 {
			deps++
		}
		if i := strings.Index(p.Project, "/"); i > 0 {
			namespaces[p.Project[:i]] = true
		}
		if !strings.ContainsAny(p.Project, "./") {
			bareNames++
		}
		for _, pl := range p.Platforms {
			platforms[pl] = true
		}
	}
	fmt.Fprintf(w, "%s\n", path)
	fmt.Fprintf(w, "  generated     %s\n", c.Generated)
	if len(platforms) > 0 {
		fmt.Fprintf(w, "  platforms     %s\n", strings.Join(sorted(platforms), " "))
	}
	fmt.Fprintf(w, "  projects      %d\n", n)
	line := func(label string, k int) {
		fmt.Fprintf(w, "  %-13s %d (%.1f%%)\n", label, k, 100*float64(k)/float64(n))
	}
	// SUMMARIES FIRST: it is the number that decides whether a search over
	// descriptions would find anything, and it is the one that surprises.
	line("summaries", summaries)
	line("provides", provides)
	line("with a bottle", bottled)
	line("dependencies", deps)
	fmt.Fprintf(w, "  commands      %d\n", cmds)
	fmt.Fprintf(w, "  namespaces    %d\n", len(namespaces))
	fmt.Fprintf(w, "  bare names    %d (project names with no dot and no slash)\n", bareNames)
	return nil
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
