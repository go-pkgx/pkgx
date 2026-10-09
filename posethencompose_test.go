package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

// glibcFixture lays out a closure that FindLoader can find a loader in: the
// loader lives in the VERSIONED sub-libdir, which is also where libc.so.6 is
// and the whole reason that directory has to reach LD_LIBRARY_PATH.
func glibcFixture(t *testing.T) (dir string, versioned string, closure []bottle.Resolved) {
	t.Helper()
	dir = t.TempDir()
	versioned = filepath.Join(dir, bottle.GlibcProject, "v2.44.0", "lib", "glibc-2.44")
	if err := os.MkdirAll(versioned, 0o755); err != nil {
		t.Fatal(err)
	}
	if name := bottle.LoaderName(); name != "" {
		if err := os.WriteFile(filepath.Join(versioned, name), []byte{0x7f, 'E', 'L', 'F'}, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, versioned, []bottle.Resolved{{Project: bottle.GlibcProject, Version: bottle.ParseVer("2.44.0")}}
}

// ⛔⛔ THE ORDER IS THE DEFECT. composeEnv exports glibc's versioned lib dir
// only `if loaderIsOurs(dir)`, and that reads /lib and /lib64 — so the
// environment must be composed AFTER the loader is posed there, never before.
//
// Measured 2026-10-09 in a FROM-scratch container: every ELF died with
// "error while loading shared libraries", jq on libm.so.6 and coreutils' env
// on libc.so.6, and `pkgx --lock` could not run a lock at all.
func TestTheLoaderIsPosedBeforeTheEnvironmentIsComposed(t *testing.T) {
	dir, _, closure := glibcFixture(t)

	// The platform check is a seam precisely so this does not skip: the
	// first version of this test skipped on darwin, which is the machine it
	// was written on, so the assertion ran nowhere its author could see it.
	oldPoses := posesLoader
	posesLoader = func() bool { return true }
	t.Cleanup(func() { posesLoader = oldPoses })

	var order []string
	oldSetup, oldOurs := setupRootfs, loaderIsOurs
	t.Cleanup(func() { setupRootfs, loaderIsOurs = oldSetup, oldOurs })
	setupRootfs = func(string, string) { order = append(order, "pose") }
	loaderIsOurs = func(string) bool {
		order = append(order, "ask")
		// Answer as the real one would AFTER the loader has been posed, so a
		// wrong order shows up as a wrong order and not as a different answer.
		return true
	}

	poseThenCompose(closure, dir, bottle.LibPath(closure, dir))

	if len(order) < 2 {
		t.Fatalf("both steps did not run: %v", order)
	}
	if order[0] != "pose" {
		t.Errorf("the environment was composed before the loader was posed: %v", order)
	}
}

// AND THE CONSEQUENCE, not just the order: the composed environment carries
// the directory libc.so.6 is actually in. A test on the call order alone
// would pass on a composeEnv that had stopped exporting it alltogether.
func TestTheComposedEnvironmentCarriesTheDirectoryLibcIsIn(t *testing.T) {
	dir, versioned, closure := glibcFixture(t)

	oldSetup, oldOurs := setupRootfs, loaderIsOurs
	t.Cleanup(func() { setupRootfs, loaderIsOurs = oldSetup, oldOurs })
	posed := false
	setupRootfs = func(string, string) { posed = true }
	loaderIsOurs = func(string) bool { return posed }

	env := poseThenCompose(closure, dir, bottle.LibPath(closure, dir))

	var ld string
	for _, e := range env {
		if strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			ld = strings.TrimPrefix(e, "LD_LIBRARY_PATH=")
		}
	}
	if ld == "" {
		t.Fatalf("no LD_LIBRARY_PATH in the composed environment:\n%s", strings.Join(env, "\n"))
	}
	if bottle.GOOS() == "linux" && !strings.Contains(ld, versioned) {
		t.Errorf("LD_LIBRARY_PATH does not name %s, where libc.so.6 is:\n%s", versioned, ld)
	}
}
