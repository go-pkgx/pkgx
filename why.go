package main

import (
	"fmt"
	"io"
	"time"

	"github.com/go-pkgx/bottle"
)

// `pkgx why <project> <dependency>` — WHICH LINK put it there.
//
// # A THIRD QUESTION, NOT A VIEW OF THE OTHER TWO
//
// `pkgx ls --tree` says what is in a closure. `pkgx ls --dependents` says
// who is affected by a change. Neither says WHY a particular thing is in a
// particular closure, and that is the question somebody has when they look
// at a closure and find something they did not ask for.
//
// `nix why-depends A B` exists for exactly this and makes the same choice
// this does: a SHORTEST path, because the point is an explanation a person
// can hold, not an enumeration of every route.
//
// # IT READS THE CATALOGUE, SO IT IS OFFLINE AND IT IS RUNTIME-ONLY
//
// Nix answers from the store, by scanning built output for hashes, so it
// can show the file fragment that carries the reference. There is nothing
// to scan here before anything is installed, and the honest answer is the
// DECLARED graph: this is what the recipes say, not what the bytes do.
// Saying so is better than implying a scan nobody ran.
func runWhy(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "pkgx: usage: pkgx why <project> <dependency>")
		return 2
	}
	from, to := args[0], args[1]
	cat, src, err := browseCatalog(bottle.Dir())
	if err != nil {
		fmt.Fprintf(stderr, "pkgx: PKGX_CATALOG names %s and it cannot be read: %v\n", src, err)
		return 2
	}
	// BOTH ENDS ARE CHECKED FIRST, and separately, because "no such
	// project" and "no path between these two" call for different actions
	// and an answer that merges them sends the reader to look for a
	// dependency that was never spelt right.
	for _, p := range []string{from, to} {
		if _, ok := cat.Lookup(p); !ok {
			fmt.Fprintf(stderr, "pkgx: %s is not in this catalogue (%s)\n", p, cat.Age(time.Now()))
			if catalogAbsent() {
				fmt.Fprintln(stderr, "pkgx: there is no catalogue here; run: pkgx catalog update")
			}
			return 1
		}
	}
	path := cat.WhyDepends(from, to)
	if path == nil {
		// A negative is an ANSWER here, and worth as much as a path: it is
		// what tells somebody a bump cannot reach them.
		fmt.Fprintf(stdout, "%s does not need %s, directly or through anything else\n", from, to)
		return 1
	}
	fmt.Fprintf(stderr, "pkgx: catalogue %s, %s\n", src, cat.Age(time.Now()))
	swept := knowsVersions(cat)
	for i, p := range path {
		note := ""
		if e, ok := cat.Lookup(p); ok && swept {
			note = versionNote(cat, e)
		}
		switch i {
		case 0:
			fmt.Fprintf(stdout, "%s%s\n", p, note)
		default:
			fmt.Fprintf(stdout, "%s→ %s%s\n", indent(i), p, note)
		}
	}
	if len(path) > 2 {
		fmt.Fprintf(stdout, "\nshortest of possibly several routes, %d hops; declared dependencies, not scanned bytes\n", len(path)-1)
	}
	return 0
}

func indent(n int) string {
	s := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		s = append(s, ' ', ' ')
	}
	return string(s)
}
