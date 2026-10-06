package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgx/bottle"
)

func withLock(t *testing.T, d bottle.Lock, err error) {
	t.Helper()
	prevR, prevT := readLockFile, timeNow
	readLockFile = func(string) (bottle.Lock, error) { return d, err }
	timeNow = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { readLockFile, timeNow = prevR, prevT })
}

func hostLock() bottle.Lock {
	osn, arch := bottle.HostSlug()
	return bottle.Lock{
		Version: bottle.LockfileVersion, Platform: osn + "/" + arch,
		Generated: "2026-10-06T09:00:00Z", BK: "v0.12.0",
		Roots: []string{"curl.se"},
		Pins: []bottle.LockPin{
			{Project: "curl.se", Version: "8.17.0", Spec: "sha256:a"},
			{Project: "openssl.org", Version: "3.6.0", Spec: "sha256:b"},
			{Project: "zlib.net", Version: "1.3.2", Spec: "sha256:c"},
		},
	}
}

// EVERY PIN BECOMES A ROOT, not just the lock's own `roots`. That is what
// makes it a lock rather than a hint: a transitive dependency the resolver
// would otherwise pick afresh is pinned too.
func TestEveryPinBecomesAnExactRoot(t *testing.T) {
	got := lockedSpecs(hostLock())
	want := "curl.se@=8.17.0 openssl.org@=3.6.0 zlib.net@=1.3.2"
	if strings.Join(got, " ") != want {
		t.Errorf("lockedSpecs = %v, want %s", got, want)
	}
	// The lock named ONE root, and three projects are pinned. A version
	// that only pinned the roots would let the other two move.
	if len(hostLock().Roots) != 1 || len(got) != 3 {
		t.Errorf("%d roots in the lock, %d specs out — the test proves nothing", len(hostLock().Roots), len(got))
	}
	// And they go through the SAME parser a person's `+pkg@=1.2.3` does,
	// so a locked run and a free one cannot drift apart.
	if project(got[0]) != "curl.se" || constraint(got[0]) != "=8.17.0" {
		t.Errorf("the ordinary spec parser reads %q as %q / %q", got[0], project(got[0]), constraint(got[0]))
	}
}

// A lock records the platform it was taken on because the platform CHANGES
// the answer: 537 of 1908 projects available on linux/x86-64 have no bottle
// for darwin/aarch64. Running one on the other would fail late, inside the
// resolver, with a message about a version rather than about the lock.
func TestALockFromAnotherPlatformIsRefused(t *testing.T) {
	d := hostLock()
	d.Platform = "plan9/mips64"
	withLock(t, d, nil)
	var errb bytes.Buffer
	if _, err := loadLock("seed.lock.hcl", &errb); err == nil {
		t.Fatal("a lock from another platform was accepted")
	} else if !strings.Contains(err.Error(), "plan9/mips64") {
		t.Errorf("the refusal does not name the platform: %v", err)
	}

	// A lock with NO platform is not refused: it predates the field, and
	// refusing it would be a statement about a fact the file never made.
	d.Platform = ""
	withLock(t, d, nil)
	if _, err := loadLock("old.lock.hcl", &errb); err != nil {
		t.Errorf("a lock with no platform was refused: %v", err)
	}
}

// It says which file made the claim and how old it is, because a lock's
// whole claim is that nothing moved.
func TestLoadLockSaysWhatItRead(t *testing.T) {
	withLock(t, hostLock(), nil)
	var errb bytes.Buffer
	if _, err := loadLock("seed.lock.hcl", &errb); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"seed.lock.hcl", "3 pin(s)", "3 hour(s) old"} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the header does not say %q: %q", want, errb.String())
		}
	}
}

func TestSplitLock(t *testing.T) {
	for _, tc := range []struct {
		in      []string
		path    string
		rest    string
		wantErr bool
	}{
		{[]string{"--lock", "f.hcl", "--", "echo"}, "f.hcl", "-- echo", false},
		{[]string{"--lock", "f.hcl"}, "f.hcl", "", false},
		{[]string{"--lock"}, "", "", true},
		{[]string{"+curl.se", "--lock", "f.hcl"}, "", "+curl.se --lock f.hcl", false}, // not leading: untouched
		{nil, "", "", false},
	} {
		p, rest, err := splitLock(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("%v: err=%v", tc.in, err)
			continue
		}
		if p != tc.path || strings.Join(rest, " ") != tc.rest {
			t.Errorf("%v → %q, %v", tc.in, p, rest)
		}
	}
}

// A lock IS the set. Adding a package means resolving that one freshly
// against pinned ones — a third thing that is neither locked nor free.
func TestALockCannotBeMixedWithPlus(t *testing.T) {
	withLock(t, hostLock(), nil)
	code := run([]string{"--lock", "seed.lock.hcl", "+gnu.org/bash"})
	if code != 2 {
		t.Errorf("--lock with +pkg exited %d, want 2", code)
	}
}

// An unreadable lock is an error, not an empty set: materialising nothing
// and reporting success is how a build silently stops being locked.
func TestAnUnreadableLockFails(t *testing.T) {
	withLock(t, bottle.Lock{}, errors.New("no such file"))
	if code := run([]string{"--lock", "absent.hcl"}); code != 1 {
		t.Errorf("an unreadable lock exited %d, want 1", code)
	}
	// And one that reads but pins nothing.
	withLock(t, bottle.Lock{Platform: "", Generated: "2026-10-06T09:00:00Z"}, nil)
	if code := run([]string{"--lock", "empty.hcl"}); code != 1 {
		t.Errorf("a lock pinning nothing exited %d, want 1", code)
	}
}

func TestLockIsInTheUsage(t *testing.T) {
	if !strings.Contains(usage, "pkgx --lock") {
		t.Error("usage does not mention --lock")
	}
}
