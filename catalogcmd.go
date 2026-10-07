package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/go-pkgx/bottle"
)

// `pkgx catalog` — the ONE path that talks to the registry about what exists.
//
// # WHY A FETCH IS A COMMAND AND NOT A SIDE EFFECT
//
// Completion has one hard requirement: it must be instant and it must not
// fail. A shell that pauses on TAB is a shell the user stops pressing TAB
// in.
//
// Before this, every TAB spawned a `pkgx` that fetched an OCI token and
// pulled the ~200 KB catalogue bottle — once per press, with no reuse
// between them, because each press is a fresh process. Measured at ~0.4 s
// on the fast path, and that was the path where no catalogue existed and
// the pull failed immediately.
//
// So the fetch moved out of the read path entirely. `guix pull` and
// `nix-channel --update` are the same shape and for the same reason: the
// index is a thing you refresh, not a thing every command re-derives.
//
// The cost is that a catalogue goes stale and nobody is told by magic.
// That is paid for by saying the age on every `pkgx ls`, and by this
// command printing it too.
func runCatalogCmd(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 0:
		return catalogStatus(stdout, stderr)
	case len(args) == 1 && args[0] == "update":
		return catalogUpdate(stdout, stderr)
	}
	fmt.Fprintln(stderr, "pkgx: usage: pkgx catalog [update]")
	return 2
}

// catalogPath is where the fetched catalogue lives, per platform.
//
// Under $PKGX_DIR rather than a cache directory, because it is not a cache:
// nothing re-derives it on a miss, and losing it changes what `pkgx ls`
// can answer. It belongs with the store it describes — and a scratch image
// that ships one just drops the file in beside the packages.
func catalogPath(osn, arch string) string {
	return filepath.Join(bottle.Dir(), "catalog", osn+"-"+arch+".json")
}

// catalogAbsent says whether there is simply NO catalogue here, as opposed
// to one that exists and could not be read. The caller prints a different
// sentence for each, because "run pkgx catalog update" is the fix for the
// first and no help at all for the second.
func catalogAbsent() bool {
	osn, arch := bottle.HostSlug()
	_, err := osStat(catalogPath(osn, arch))
	return err != nil
}

func catalogUpdate(stdout, stderr io.Writer) int {
	osn, arch := bottle.HostSlug()
	c, err := fetchCatalogFor(osn, arch)
	if err != nil {
		fmt.Fprintf(stderr, "pkgx: catalog update: %v\n", err)
		return 1
	}
	body, err := bottle.MarshalCatalog(c)
	if err != nil {
		fmt.Fprintln(stderr, "pkgx: catalog update:", err)
		return 1
	}
	p := catalogPath(osn, arch)
	if err := osMkdirAll(filepath.Dir(p), 0o755); err != nil {
		fmt.Fprintln(stderr, "pkgx: catalog update:", err)
		return 1
	}
	// Written whole, then renamed. A reader that caught this half-written
	// would see a truncated JSON document and report the registry as
	// empty — and the reader here is tab-completion, which would do it
	// silently.
	tmp := p + ".new"
	if err := osWriteFile(tmp, body, 0o644); err != nil {
		fmt.Fprintln(stderr, "pkgx: catalog update:", err)
		return 1
	}
	if err := osRename(tmp, p); err != nil {
		fmt.Fprintln(stderr, "pkgx: catalog update:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%d project(s) → %s\n", len(c.Projects), p)
	return 0
}

func catalogStatus(stdout, stderr io.Writer) int {
	osn, arch := bottle.HostSlug()
	p := catalogPath(osn, arch)
	b, err := osReadFile(p)
	if err != nil {
		// Not an error: no catalogue yet is the state of every fresh
		// machine, and the useful thing to print is the command that
		// fixes it — and WHERE it would land, because an image that
		// ships one has to know the path to drop the file at, and
		// reading it out of this program beats restating it by hand.
		fmt.Fprintf(stdout, "%s\n", p)
		fmt.Fprintf(stdout, "no catalogue for %s/%s — run: pkgx catalog update\n", osn, arch)
		return 0
	}
	c, err := bottle.UnmarshalCatalog(b)
	if err != nil {
		fmt.Fprintf(stderr, "pkgx: %s is not a catalogue: %v\n", p, err)
		return 1
	}
	withDeps := 0
	for _, pr := range c.Projects {
		if len(pr.Deps) > 0 {
			withDeps++
		}
	}
	fmt.Fprintf(stdout, "%s\n", p)
	fmt.Fprintf(stdout, "%d project(s), %d with dependencies, %s\n",
		len(c.Projects), withDeps, c.Age(time.Now()))
	// SAY IT WHEN SOMETHING WAS DROPPED. bottle removes version strings it
	// cannot read rather than refusing the file — a version reaches a
	// catalogue from an upstream recipe, and refusing would hand a recipe
	// author a switch that turns off every <TAB> for everybody. But a
	// guard that quietly removes things leaves a reader comparing a short
	// list against their memory, and on a catalogue our own factory
	// published this count should never be anything but zero.
	if c.Dropped > 0 {
		fmt.Fprintf(stdout, "%d version string(s) were unreadable and are not listed\n", c.Dropped)
	}
	return 0
}

// seams, so a test needs neither a registry nor a writable home.
var (
	osMkdirAll  = os.MkdirAll
	osWriteFile = os.WriteFile
	osRename    = os.Rename
	osReadFile  = os.ReadFile
	osStat      = os.Stat

	fetchCatalogFor = func(osn, arch string) (bottle.Catalog, error) {
		c, err := bottle.NewOCIClient(bottle.DistBase)
		if err != nil {
			return bottle.Catalog{}, err
		}
		return bottle.FetchCatalog(c, osn, arch)
	}
)
