package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-pkgx/bottle"
)

// An environment can have a LOCK beside it.
//
// # THE SHAPE IS SPACK'S, AND THAT IS NOT A COINCIDENCE
//
// Spack keeps `spack.yaml` and `spack.lock` as a pair, explicitly modelled
// on Gemfile/Gemfile.lock: the manifest says what you want, the lock says
// what that meant when it was concretised, and `spack install` inside the
// environment installs the lock. There is no `--lock` flag anywhere in it,
// because the lock belongs to the environment.
//
// Nix and Guix solve the same problem from the other end — `flake.lock`
// pins the nixpkgs INPUT, `channels.scm` pins "all of Guix" — so neither
// has a resolved-version lock to attach to anything. Three systems, two
// answers, and only one of them is about versions.
//
// environments.go says "An environment is NOT a lockfile", and that stays
// exactly true: an environment names CONSTRAINTS and resolves them at load
// time. That is the manifest half. This is the other half, beside it, not
// instead of it.
//
// # IT READS, IT DOES NOT WRITE
//
// `bk lock` writes locks, because concretising needs a pantry, an overrides
// set and a version resolver — none of which belong in a runtime that ships
// to a login node. Spack splits it the same way: `spack concretize` writes
// the lock, `spack install` consumes it.
//
// A writer here could only produce versions with no spec hash, and a lock
// with empty hashes is worse than none: `bk lock --check` would compare ""
// against a real hash and report every project as drifted. A tool that
// cries wolf is a tool nobody runs.

// envLockPath is where an environment's lock lives: beside the file that
// declared it, named for the ENVIRONMENT and not for the file, because one
// `.hcl2` can declare several.
func envLockPath(e environment) string {
	if e.File == "" || !safeEnvName(e.Name) {
		return ""
	}
	return filepath.Join(filepath.Dir(e.File), e.Name+".lock.hcl")
}

// safeEnvName guards the name BECAUSE THIS FILE MADE IT A PATH.
//
// An environment's name is an HCL block label — `env "cfd"` — and until
// now it was only ever a map key, where any string is harmless. Joining it
// to a directory changes that: filepath.Join CLEANS its result, so
//
//	env "../../../../tmp/evil"
//
// would read /tmp/evil.lock.hcl and install exactly what it pins. The pins
// themselves can no longer traverse (bottle validates project names), but
// they can still name a real project at an old version, which is a
// downgrade somebody else chose.
//
// One segment, of the characters a name is actually written in. A site
// calls these `cfd` and `site`; nothing legitimate needs a slash.
func safeEnvName(n string) bool {
	if n == "" || n == "." || n == ".." || len(n) > 64 {
		return false
	}
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// envLock reads the lock for an environment, if there is one for THIS
// platform.
//
// A lock from another platform is ignored rather than refused: a site
// commits one environment directory and loads it on every machine it runs,
// so finding a linux lock on a Mac is the ordinary case and not a mistake.
// It is SAID, because silently resolving afresh under a file named
// `cfd.lock.hcl` is how somebody comes to believe a thing is pinned when it
// is not.
func envLock(e environment, warn func(string)) (bottle.Lock, bool) {
	p := envLockPath(e)
	if p == "" {
		return bottle.Lock{}, false
	}
	d, err := readEnvLock(p)
	if err != nil {
		// Absent is the ordinary case and says nothing. Present and
		// unreadable is a broken pin, and must not read as "no lock".
		if !osIsNotExist(err) {
			warn(fmt.Sprintf("%s: %v — resolving %s afresh", p, err, e.Name))
		}
		return bottle.Lock{}, false
	}
	osn, arch := bottle.HostSlug()
	if here := osn + "/" + arch; d.Platform != "" && d.Platform != here {
		warn(fmt.Sprintf("%s was locked on %s and this is %s — resolving %s afresh",
			p, d.Platform, here, e.Name))
		return bottle.Lock{}, false
	}
	if len(d.Pins) == 0 {
		warn(fmt.Sprintf("%s pins nothing — resolving %s afresh", p, e.Name))
		return bottle.Lock{}, false
	}
	return d, true
}

// lockedSpecsFor turns an environment's lock into the exact specs the
// ordinary resolver already takes.
//
// EVERY PIN, not just the ones the manifest names: that is what makes it a
// lock. The manifest asks for `openmpi.org@5`; the lock says which 5, and
// which hdf5, and which zlib underneath them.
func lockedSpecsFor(d bottle.Lock) []string {
	out := make([]string, 0, len(d.Pins))
	for _, p := range d.Pins {
		if p.Project != "" && p.Version != "" {
			out = append(out, p.Project+"@="+p.Version)
		}
	}
	sort.Strings(out)
	return out
}

// describeEnvLock is the line `pkgx env show` prints about the pairing.
func describeEnvLock(e environment, w io.Writer) {
	p := envLockPath(e)
	if p == "" {
		return
	}
	d, ok := envLock(e, func(msg string) { fmt.Fprintln(w, "  lock:", msg) })
	if !ok {
		fmt.Fprintf(w, "  lock: none (%s) — resolved afresh at load\n", p)
		return
	}
	fmt.Fprintf(w, "  lock: %s — %d pin(s), %s, taken on %s\n",
		p, len(d.Pins), bottle.LockAge(d.Generated, timeNow()), d.Platform)
}

// Seams: a test must not need files on disk, and the "present but broken"
// branch must be reachable from one that is not.
var (
	readEnvLock  = bottle.ReadLock
	osIsNotExist = os.IsNotExist
)
