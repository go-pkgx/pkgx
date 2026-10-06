package main

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/go-pkgx/bottle"
)

// `pkgx --lock <file>` — run exactly what a lock pins.
//
// # THE OTHER END OF `bk lock`
//
// `bk lock` wrote locks and nothing acted on one. A lock nobody consumes is
// a record, not a mechanism: cargo's `--locked` and npm's `ci` exist
// because the file only means something once a command refuses to deviate
// from it.
//
// The defect it answers is not hypothetical here. The SAME pantry commit,
// 2df061bd, resolved tcl to 9.0.4 and then to 9.1.0 four hours apart,
// because a recipe's `versions:` asks GitHub at resolution time. Two builds
// from one recipe set, two answers, and nothing in between to notice.
//
// # IT IS NOT AN ENVIRONMENT
//
// environments.go says "An environment is NOT a lockfile", and that stays
// true. An environment names a SET — what you want present — and resolves
// it afresh each time, which is right for a shell you live in. A lock names
// VERSIONS, and the point is that nothing moves. Two paths, side by side;
// this is the second one.
//
// # EVERY PIN IS A ROOT
//
// Not just the lock's own `roots`: every project it pins becomes an exact
// constraint. That is what makes it a lock rather than a hint — a
// transitive dependency the resolver would otherwise pick afresh is pinned
// too, and a set that cannot be satisfied exactly FAILS instead of quietly
// materialising something near it. `--locked` is a refusal, and the refusal
// is the whole of its value.

// lockedSpecs turns a lock into the `+pkg@=version` list the ordinary path
// already takes, so a locked run and a free one go through ONE resolver.
// A second code path that honoured pins its own way would drift from the
// first, and the drift would be invisible until a build differed.
func lockedSpecs(d bottle.Lock) []string {
	out := make([]string, 0, len(d.Pins))
	for _, p := range d.Pins {
		if p.Project == "" || p.Version == "" {
			continue
		}
		// `=` is the exact-version form the resolver already understands,
		// the one a person writes as `pkgx node@=22.1.0`.
		out = append(out, p.Project+"@="+p.Version)
	}
	sort.Strings(out)
	return out
}

// splitLock pulls a leading `--lock <file>` off the argument list.
func splitLock(argv []string) (path string, rest []string, err error) {
	if len(argv) == 0 || argv[0] != "--lock" {
		return "", argv, nil
	}
	if len(argv) < 2 {
		return "", nil, fmt.Errorf("--lock wants a file")
	}
	return argv[1], argv[2:], nil
}

// loadLock reads a lock, checks it is about THIS machine, and says what it
// is reading.
//
// The platform check is not ceremony. A lock records the platform it was
// taken on because the platform CHANGES the answer: measured on our own
// published catalogues, 537 of 1908 projects available on linux/x86-64 have
// no bottle for darwin/aarch64. Running one lock on the other machine would
// fail late, inside the resolver, with a message about a version rather
// than about the lock.
func loadLock(path string, stderr io.Writer) (bottle.Lock, error) {
	d, err := readLockFile(path)
	if err != nil {
		return bottle.Lock{}, err
	}
	osn, arch := bottle.HostSlug()
	here := osn + "/" + arch
	if d.Platform != "" && d.Platform != here {
		return bottle.Lock{}, fmt.Errorf(
			"%s was locked on %s and this is %s — what a platform has is not what another has, "+
				"so this lock cannot be honoured here", path, d.Platform, here)
	}
	// Said, because a lock's whole claim is that nothing moved, and a
	// reader should be able to see WHICH file made that claim and how old
	// it is without opening it.
	fmt.Fprintf(stderr, "pkgx: %s, %d pin(s), %s\n",
		path, len(d.Pins), bottle.LockAge(d.Generated, timeNow()))
	return d, nil
}

// Seams: a test needs neither a file nor a clock.
var (
	readLockFile = bottle.ReadLock
	timeNow      = time.Now
)
