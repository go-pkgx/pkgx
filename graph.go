package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/go-pkgx/bottle"
)

// printGraph renders a resolved closure as a tree, then names the projects
// whose version was decided by a disagreement.
//
// A flat closure answers "which version". The question an operator composing an
// environment has is "why that one", and until now only a FAILING closure ever
// said — the refusal names the constraints and who asked for them. On a closure
// that resolves, the same information was discarded.
// graphFor is a seam: the resolution is the one part of this that needs a
// network, and the rendering is the part worth testing.
var graphFor = bottle.GraphFor

func printGraph(roots map[string]string, stdout io.Writer) error {
	osn, arch := bottle.HostSlug()
	g, err := graphFor(roots, osn, arch)
	if err != nil {
		return err
	}
	printTree(g, stdout)
	printContested(g, stdout)
	return nil
}

// printTree walks from each root. A project already shown is named and not
// descended into: the closure is a DAG, and expanding every path through a
// diamond turns a readable tree into pages of the same subtree.
func printTree(g *bottle.Graph, stdout io.Writer) {
	shown := map[string]bool{}
	var walk func(p, prefix string, last, root bool)
	walk = func(p, prefix string, last, root bool) {
		branch, cont := "└─ ", "   "
		switch {
		case root:
			branch, cont = "", ""
		case !last:
			branch, cont = "├─ ", "│  "
		}
		line := p + " " + versionLabel(g, p)
		// A project already shown is named and not descended into: the closure
		// is a DAG, and expanding every path through a diamond turns a readable
		// tree into pages of the same subtree. A leaf repeats, because ↑ on it
		// would cost a line to say nothing.
		if shown[p] && len(g.Deps[p]) > 0 {
			fmt.Fprintf(stdout, "%s%s%s ↑\n", prefix, branch, line)
			return
		}
		fmt.Fprintf(stdout, "%s%s%s\n", prefix, branch, line)
		shown[p] = true
		deps := g.Deps[p]
		for i, e := range deps {
			walk(e.On, prefix+cont, i == len(deps)-1, false)
		}
	}
	for _, r := range g.Roots {
		walk(r, "", true, true)
	}
}

// printContested names every project asked for under more than one distinct
// constraint, with each demand and who made it.
//
// That set is small and it is the whole answer to "why this version": a single
// demand needs no explanation, and identical demands agree. Only a
// disagreement decided anything — and it is exactly where a set stops being
// coherent, which is what an HPC environment is for.
func printContested(g *bottle.Graph, stdout io.Writer) {
	type row struct {
		project string
		asks    []bottle.Edge
	}
	var rows []row
	for p, asks := range g.Asks {
		seen := map[string]bool{}
		for _, e := range asks {
			seen[e.Constraint] = true
		}
		if len(seen) > 1 {
			rows = append(rows, row{p, asks})
		}
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].project < rows[j].project })
	fmt.Fprintf(stdout, "\ndecided by more than one demand:\n")
	for _, r := range rows {
		fmt.Fprintf(stdout, "  %s %s\n", r.project, versionLabel(g, r.project))
		asks := append([]bottle.Edge(nil), r.asks...)
		sort.Slice(asks, func(i, j int) bool { return asks[i].Of < asks[j].Of })
		for _, e := range asks {
			who := e.Of
			if who == "" {
				who = "(requested)"
			}
			c := e.Constraint
			if c == "" {
				c = "*"
			}
			fmt.Fprintf(stdout, "      %-12s ← %s\n", c, who)
		}
	}
}

// versionLabel is the version a project resolved to, or ALL of them where the
// demands did not intersect but named disjoint sonames.
//
// A graph that printed one version there would be worse than silent: it would
// answer the question "which libxml2 is in this closure" with a half-truth,
// and the whole point of the split is that the answer is two. The leading line
// comes first — it is the one that owns PATH.
func versionLabel(g *bottle.Graph, p string) string {
	lines := g.Lines[p]
	if len(lines) < 2 {
		return g.Versions[p].Raw
	}
	var raws []string
	for _, v := range lines {
		raws = append(raws, v.Raw)
	}
	return strings.Join(raws, " + ") + "  (ABI lines, both installed)"
}
