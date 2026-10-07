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
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/go-pkgx/bottle"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: catstat <catalogue.json>...")
		os.Exit(2)
	}
	status := 0
	for _, path := range os.Args[1:] {
		if err := report(os.Stdout, path); err != nil {
			fmt.Fprintln(os.Stderr, "catstat:", err)
			status = 1
		}
	}
	os.Exit(status)
}

func report(w io.Writer, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c, err := bottle.UnmarshalCatalog(b)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	n := len(c.Projects)
	if n == 0 {
		// A catalogue with no projects is not a census of nothing, it is a
		// broken file, and every percentage below would be a division by
		// zero dressed up as a finding.
		return fmt.Errorf("%s: no projects", path)
	}
	var summaries, provides, bottled, deps, cmds int
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
