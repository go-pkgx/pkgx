package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/go-pkgx/bottle"
)

// compatWorkers bounds the resolutions in flight. Each one is network-bound —
// a metadata fetch per project in the closure — so the useful number is well
// above the core count and well below anything a registry would call abuse.
const compatWorkers = 8

// runCompat answers the question a distribution asks before it ships: given
// THESE versions of the base libraries, how much of the pantry still resolves,
// and what does each exclusion blame?
//
// A single coherent set over everything is not on offer and never was. The 29
// packages this repository audits cannot form one environment:
//
//	no version of gnome.org/libxml2 satisfies "2" AND ">=2.14" AND "~2.13"
//
// which is the ordinary state of a package collection, not a defect. Spack
// lives with it by building trees in parallel — one per base — and presenting
// them through a module hierarchy; this is the measurement that says how many
// trees a site needs and what each one would cover.
//
// Pins are given as ordinary `+spec` roots because that is what they are: an
// extra demand on the closure, indistinguishable to the resolver from one a
// recipe made. Projects come after `--`, or on stdin when none are named.
func runCompat(plus, rest []string, in io.Reader, stdout io.Writer) error {
	pins := map[string]string{}
	for _, p := range plus {
		pins[project(p)] = constraint(p)
	}
	projects, err := compatProjects(rest, in)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return fmt.Errorf("no projects to solve (name them after -- , or pipe them in)")
	}
	res := solveAll(pins, projects)
	printCompat(pins, res, stdout)
	if res.refused > 0 || res.errored > 0 {
		return errSilent
	}
	return nil
}

// errSilent makes the exit status non-zero without printing twice: the report
// above already said everything, and a second line would be noise in a pipe.
var errSilent = errors.New("")

// compatProjects reads the project list from the arguments, or from stdin when
// there are none — so `bk`'s recipes.txt, a registry listing or a hand-written
// subset all feed it without the command needing to know where a pantry lives.
func compatProjects(rest []string, in io.Reader) ([]string, error) {
	if len(rest) > 0 {
		return rest, nil
	}
	var out []string
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Fields(line)[0])
	}
	return out, sc.Err()
}

// outcome is one project's answer.
type outcome struct {
	project  string
	conflict *bottle.ConflictError // set when the closure has no solution
	err      error                 // set when it failed for any other reason
}

type compatResult struct {
	outcomes []outcome
	resolved int
	refused  int
	errored  int
}

func solveAll(pins map[string]string, projects []string) compatResult {
	osn, arch := bottle.HostSlug()
	out := make([]outcome, len(projects))
	var wg sync.WaitGroup
	sem := make(chan struct{}, compatWorkers)
	for i, p := range projects {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			roots := map[string]string{p: "*"}
			for k, v := range pins {
				roots[k] = v
			}
			o := outcome{project: p}
			if _, err := graphFor(roots, osn, arch); err != nil {
				var ce *bottle.ConflictError
				if errors.As(err, &ce) {
					o.conflict = ce
				} else {
					o.err = err
				}
			}
			out[i] = o
		}(i, p)
	}
	wg.Wait()

	r := compatResult{outcomes: out}
	for _, o := range out {
		switch {
		case o.conflict != nil:
			r.refused++
		case o.err != nil:
			r.errored++
		default:
			r.resolved++
		}
	}
	return r
}

// printCompat reports the coverage, then attributes every refusal.
//
// The attribution is the point. A list of packages that did not resolve tells
// an operator nothing they can act on; "libxml2 ~2.13 excluded 68 of them"
// names one rebuild that would move all 68 at once.
func printCompat(pins map[string]string, r compatResult, stdout io.Writer) {
	if len(pins) == 0 {
		fmt.Fprintf(stdout, "base: (none pinned)\n")
	} else {
		var ps []string
		for k, v := range pins {
			ps = append(ps, k+v)
		}
		sort.Strings(ps)
		fmt.Fprintf(stdout, "base: %s\n", strings.Join(ps, "  "))
	}
	total := len(r.outcomes)
	fmt.Fprintf(stdout, "  %d recipes\n  %d resolve\n  %d refused\n", total, r.resolved, r.refused)
	if r.errored > 0 {
		// Named, not counted. A project that failed for a reason OTHER than a
		// conflict is the one an operator has to go and look at, and "1 could
		// not be read at all" sends them to find out which — measured while
		// chasing a regression, where the unnamed one turned out to be a
		// pre-existing failure and not the one being hunted.
		fmt.Fprintf(stdout, "  %d could not be read at all:\n", r.errored)
		for _, o := range r.outcomes {
			if o.err != nil {
				fmt.Fprintf(stdout, "      %s: %v\n", o.project, o.err)
			}
		}
	}

	if r.refused > 0 {
		// Named, like the unreadable ones above, and for the same reason. The
		// blame below says which PROJECT the demands collided on; it does not
		// say which recipes were excluded, and "2 refused" sends an operator
		// to work that out by hand. Measured while checking whether a resolver
		// change had fixed the refusals it was written for: the counts moved
		// and there was no way to tell which two recipes had moved with them.
		fmt.Fprintf(stdout, "  refused:\n")
		for _, o := range r.outcomes {
			if o.conflict != nil {
				fmt.Fprintf(stdout, "      %s: %s\n", o.project, o.conflict.Project)
			}
		}
	}

	// project -> how many recipes it excluded, and the demands seen on it
	type blame struct {
		n       int
		demands map[string]bool
	}
	byProject := map[string]*blame{}
	for _, o := range r.outcomes {
		if o.conflict == nil {
			continue
		}
		b := byProject[o.conflict.Project]
		if b == nil {
			b = &blame{demands: map[string]bool{}}
			byProject[o.conflict.Project] = b
		}
		b.n++
		for i := range o.conflict.Constraints {
			b.demands[fmt.Sprintf("%s (%s)", o.conflict.Constraints[i], o.conflict.AskedBy[i])] = true
		}
	}
	if len(byProject) == 0 {
		return
	}
	keys := make([]string, 0, len(byProject))
	for k := range byProject {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if byProject[keys[i]].n != byProject[keys[j]].n {
			return byProject[keys[i]].n > byProject[keys[j]].n
		}
		return keys[i] < keys[j]
	})
	fmt.Fprintf(stdout, "\nwhat the refusals blame:\n")
	for _, k := range keys {
		b := byProject[k]
		ds := make([]string, 0, len(b.demands))
		for d := range b.demands {
			ds = append(ds, d)
		}
		sort.Strings(ds)
		fmt.Fprintf(stdout, "  %4d  %s\n", b.n, k)
		for _, d := range ds {
			fmt.Fprintf(stdout, "          %s\n", d)
		}
	}
}
