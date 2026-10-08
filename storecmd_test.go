package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A store built the way the installer builds one, including the alias
// symlinks beside a version.
func storeAt(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(path string, n int) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// ⛔ THE BIG ONE SORTS LAST ON PURPOSE. The obvious fixture — llvm.org
	// large and zlib.net small — puts the same project first in BOTH
	// orders, so --by-size and the default agree and the flag can be
	// mutated away unnoticed. That is the trap this file's own comment
	// warns about, and the first version of this fixture fell into it.
	write(filepath.Join(dir, "llvm.org", "v22.1.8", "bin", "clang"), 1024)
	write(filepath.Join(dir, "llvm.org", "v16.0.6", "bin", "clang"), 512)
	write(filepath.Join(dir, "zlib.net", "v1.3.2", "lib", "libz"), 8192)
	if err := os.Symlink("v22.1.8", filepath.Join(dir, "llvm.org", "v*")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PKGX_DIR", dir)
	return dir
}

func TestStoreReportsWhatIsOnDisk(t *testing.T) {
	storeAt(t)
	var out, errb bytes.Buffer
	if code := runStore(nil, &out, &errb); code != 0 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{
		"llvm.org",
		"zlib.net",
		"3 version(s) of 2 project(s)",
		// The one that is easy to misread is worded as a fact, and the
		// wording is part of the answer: a lock may ask for exactly these.
		"not the same as unused",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from:\n%s", want, got)
		}
	}
	// THE ALIAS IS NOT A FOURTH VERSION. `v*` points at v22.1.8, and
	// following it would report llvm.org twice and the store at half again
	// its size.
	if strings.Contains(got, "4 version(s)") {
		t.Errorf("the v* alias was counted as a version:\n%s", got)
	}
}

// --by-size puts the big one first, which is the whole reason to look.
func TestStoreBySizeOrdersByBytes(t *testing.T) {
	storeAt(t)
	var out, errb bytes.Buffer
	if code := runStore([]string{"--by-size"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	got := out.String()
	// zlib.net is the LARGEST and sorts LAST by name, so the two orders
	// disagree and the flag has something to prove.
	llvm, zlib := strings.Index(got, "llvm.org"), strings.Index(got, "zlib.net")
	if llvm < 0 || zlib < 0 || zlib > llvm {
		t.Errorf("the largest project is not first:\n%s", got)
	}
	// AND BY NAME BY DEFAULT, so the two orders are distinguishable — a
	// flag that happens to agree with the default proves nothing.
	out.Reset()
	if code := runStore(nil, &out, &errb); code != 0 {
		t.Fatal(code)
	}
	if l, z := strings.Index(out.String(), "llvm.org"), strings.Index(out.String(), "zlib.net"); l > z {
		t.Errorf("the default order is not by name:\n%s", out.String())
	}
}

func TestStoreTopN(t *testing.T) {
	storeAt(t)
	var out, errb bytes.Buffer
	if code := runStore([]string{"--by-size", "-n", "1"}, &out, &errb); code != 0 {
		t.Fatalf("code=%d err=%s", code, errb.String())
	}
	got := out.String()
	if strings.Contains(got, "llvm.org") {
		t.Errorf("-n 1 printed a second project:\n%s", got)
	}
	// AND SAYS WHAT IT HID. A truncated list that does not admit it is a
	// list somebody will read as complete.
	if !strings.Contains(got, "1 more") {
		t.Errorf("the truncation is silent:\n%s", got)
	}
	// The totals still describe the WHOLE store, not the shown rows.
	if !strings.Contains(got, "3 version(s) of 2 project(s)") {
		t.Errorf("the totals were truncated too:\n%s", got)
	}
}

// AN ABSENT STORE IS NOT AN ERROR: it is the state of every fresh machine,
// and the useful thing to print is where it would be.
func TestStoreWithNoStoreYet(t *testing.T) {
	t.Setenv("PKGX_DIR", filepath.Join(t.TempDir(), "never-used"))
	var out, errb bytes.Buffer
	if code := runStore(nil, &out, &errb); code != 0 {
		t.Fatalf("code=%d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "nothing here yet") {
		t.Errorf("a fresh machine was told something else:\n%s", out.String())
	}
}

func TestStoreTakesNoArguments(t *testing.T) {
	storeAt(t)
	var out, errb bytes.Buffer
	if code := runStore([]string{"llvm.org"}, &out, &errb); code != 2 {
		t.Errorf("code=%d, want 2", code)
	}
}

// POWERS OF 1024, AND IT SAYS SO. `du` reports blocks and this reports file
// sizes, so the two disagree twice over; writing GiB rather than GB at least
// names which of the two this is.
func TestHumanReadsAsAPersonReads(t *testing.T) {
	for n, want := range map[int64]string{
		0:           "0 B",
		999:         "999 B",
		1024:        "1.0 KiB",
		1536:        "1.5 KiB",
		1 << 20:     "1.0 MiB",
		1 << 30:     "1.0 GiB",
		44255092736: "41.2 GiB", // the store this was written for
	} {
		if got := human(n); got != want {
			t.Errorf("human(%d) = %q, want %q", n, got, want)
		}
	}
}
