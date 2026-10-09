package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-pkgx/bottle"
)

// glibcFixture lays out a closure holding glibc's VERSIONED sub-libdir, which
// is where libc.so.6 actually is and the whole reason that directory has to
// reach LD_LIBRARY_PATH.
//
// It does NOT try to create a loader file. bottle.LoaderName() is empty on the
// architectures with no canonical ld-linux name — riscv64, ppc64le and loong64
// among them — so a fixture built from it creates nothing there, and the first
// version of this test asserted on a step that never ran. The qemu lanes
// failed with `both steps did not run: [ask]`. The loader lookup is a seam
// instead, so these tests measure the ORDER on every architecture rather than
// on the two where a loader happens to be nameable.
func glibcFixture(t *testing.T) (dir string, versioned string, closure []bottle.Resolved) {
	t.Helper()
	dir = t.TempDir()
	versioned = filepath.Join(dir, bottle.GlibcProject, "v2.44.0", "lib", "glibc-2.44")
	if err := os.MkdirAll(versioned, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, versioned, []bottle.Resolved{{Project: bottle.GlibcProject, Version: bottle.ParseVer("2.44.0")}}
}

// poseSeams makes the platform check and the loader lookup both answer yes, so
// the ordering under test is reached on every architecture.
func poseSeams(t *testing.T) {
	t.Helper()
	oldPoses, oldFind := posesLoader, findLoader
	t.Cleanup(func() { posesLoader, findLoader = oldPoses, oldFind })
	posesLoader = func() bool { return true }
	findLoader = func(string) string { return "/pkgx/gnu.org/glibc/v2.44.0/lib/glibc-2.44/ld-linux" }
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
	poseSeams(t)

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
// the directory libc.so.6 is actually in. A test on the call order alone would
// pass on a composeEnv that had stopped exporting it altogether.
func TestTheComposedEnvironmentCarriesTheDirectoryLibcIsIn(t *testing.T) {
	dir, versioned, closure := glibcFixture(t)
	poseSeams(t)

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
	if !strings.Contains(ld, versioned) {
		t.Errorf("LD_LIBRARY_PATH does not name %s, where libc.so.6 is:\n%s", versioned, ld)
	}
}

// ON A PLATFORM THAT POSES NOTHING, nothing is posed — and the environment is
// still composed. Without this, making posesLoader always true would pass the
// two tests above while breaking darwin and windows.
func TestNothingIsPosedWhereThereIsNoLoaderToPose(t *testing.T) {
	dir, _, closure := glibcFixture(t)

	// ⛔ THE LOADER LOOKUP MUST SUCCEED HERE, so that posesLoader is the ONLY
	// thing that can stop the pose. Without this the real findLoader returns
	// "" for the fixture, nothing is posed whatever posesLoader says, and a
	// mutation replacing `if posesLoader()` with `if true` survives — it did,
	// and that is how this line came to be written.
	poseSeams(t)

	oldPoses, oldSetup := posesLoader, setupRootfs
	t.Cleanup(func() { posesLoader, setupRootfs = oldPoses, oldSetup })
	posesLoader = func() bool { return false }
	posed := false
	setupRootfs = func(string, string) { posed = true }

	env := poseThenCompose(closure, dir, bottle.LibPath(closure, dir))

	if posed {
		t.Error("a loader was posed on a platform that has none")
	}
	if len(env) == 0 {
		t.Error("the environment was not composed at all")
	}
}
