package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-zpkg/bottle"
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

// ⛔⛔ AND LIBRARY_PATH TOO, which is a different variable read by a different
// program, and it did not carry the directory at all.
//
// LD_LIBRARY_PATH is the LOADER's; LIBRARY_PATH is the COMPILER DRIVER's, for
// resolving `-lfoo` at link time. composeEnv put every package's lib and lib64
// on both, and glibc's versioned sub-libdir on LD_LIBRARY_PATH only — so the
// linker was handed a glibc directory holding nothing and could not find libm.
//
// Almost nothing noticed, because almost every build reaches the libc through
// bk's compiler wrapper and its --sysroot. A recipe that drives `cc` itself
// does not. perl is one, and on the linux/s390x generation-1 pilot its
// Configure probed for libm, concluded there was none, and emitted
//
//	cc … -o miniperl … -lcrypt          # no -lm
//	ld.lld: error: undefined symbol: ceil, floor, pow, fmod, atan2, cos, …
//
// A probe that cannot find a library concludes the library is absent, and that
// is indistinguishable from a correct answer until something needs it
// (go-zpkg/packages#316).
func TestTheComposedEnvironmentPutsThatDirectoryOnLibraryPathToo(t *testing.T) {
	dir, versioned, closure := glibcFixture(t)
	poseSeams(t)

	oldSetup, oldOurs := setupRootfs, loaderIsOurs
	t.Cleanup(func() { setupRootfs, loaderIsOurs = oldSetup, oldOurs })
	posed := false
	setupRootfs = func(string, string) { posed = true }
	loaderIsOurs = func(string) bool { return posed }

	env := poseThenCompose(closure, dir, bottle.LibPath(closure, dir))

	var lib string
	for _, e := range env {
		if strings.HasPrefix(e, "LIBRARY_PATH=") {
			lib = strings.TrimPrefix(e, "LIBRARY_PATH=")
		}
	}
	if lib == "" {
		t.Fatalf("no LIBRARY_PATH in the composed environment:\n%s", strings.Join(env, "\n"))
	}
	if !strings.Contains(lib, versioned) {
		t.Errorf("LIBRARY_PATH does not name %s, where libm.so and libc.so are, so `-lm` cannot resolve:\n%s", versioned, lib)
	}
}

// ⛔ AND IT STAYS OUT WHEN THE LOADER IS NOT OURS, for a link-time version of
// the reason LD_LIBRARY_PATH is gated: on a distribution the child's PT_INTERP
// names the host's ld.so, so linking against our libc.so would pair two halves
// of different glibcs. Without this, the fix above would be an unconditional
// widening and would reintroduce that mismatch at the link.
func TestThatDirectoryStaysOffLibraryPathWhenTheLoaderIsNotOurs(t *testing.T) {
	dir, versioned, closure := glibcFixture(t)
	poseSeams(t)

	oldSetup, oldOurs := setupRootfs, loaderIsOurs
	t.Cleanup(func() { setupRootfs, loaderIsOurs = oldSetup, oldOurs })
	setupRootfs = func(string, string) {}
	loaderIsOurs = func(string) bool { return false }

	env := poseThenCompose(closure, dir, bottle.LibPath(closure, dir))

	for _, e := range env {
		if !strings.HasPrefix(e, "LIBRARY_PATH=") && !strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			continue
		}
		if strings.Contains(e, versioned) {
			t.Errorf("a host loader would be paired with our libc: %s", e)
		}
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
