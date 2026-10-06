package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgx/bottle"
)

func cfdEnv() environment {
	return environment{
		Name:        "cfd",
		Description: "the solver stack, as validated on 2026-09-01",
		Packages:    []string{"openmpi.org@5", "hdf5.org"},
		File:        filepath.Join("/site", "environments", "site.hcl2"),
	}
}

func cfdLock() bottle.Lock {
	osn, arch := bottle.HostSlug()
	return bottle.Lock{
		Version: bottle.LockfileVersion, Platform: osn + "/" + arch,
		Generated: "2026-10-06T09:00:00Z", BK: "v0.14.1",
		Roots: []string{"openmpi.org", "hdf5.org"},
		Pins: []bottle.LockPin{
			{Project: "hdf5.org", Version: "1.14.6", Spec: "sha256:b"},
			{Project: "openmpi.org", Version: "5.0.6", Spec: "sha256:a"},
			{Project: "zlib.net", Version: "1.3.2", Spec: "sha256:c"}, // NOT in the manifest
		},
	}
}

func withEnvLock(t *testing.T, d bottle.Lock, err error) {
	t.Helper()
	prevR, prevE, prevT := readEnvLock, osIsNotExist, timeNow
	readEnvLock = func(string) (bottle.Lock, error) { return d, err }
	osIsNotExist = func(e error) bool { return e != nil && strings.Contains(e.Error(), "no such") }
	timeNow = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { readEnvLock, osIsNotExist, timeNow = prevR, prevE, prevT })
}

// The pairing is Spack's: the manifest says what you want, the lock says
// what that meant. `spack install` inside an environment installs the lock;
// there is no --lock flag anywhere in it, because the lock belongs to the
// environment.
func TestALockBesideAnEnvironmentWins(t *testing.T) {
	envs := map[string]environment{"cfd": cfdEnv()}

	// Without a lock: the manifest's constraints, unchanged.
	withEnvLock(t, bottle.Lock{}, errors.New("no such file"))
	if got := strings.Join(expandSpecs([]string{"cfd"}, envs), " "); got != "openmpi.org@5 hdf5.org" {
		t.Errorf("unlocked = %q, want the manifest", got)
	}

	// With one: its pins, exactly.
	withEnvLock(t, cfdLock(), nil)
	got := expandSpecs([]string{"cfd"}, envs)
	want := "hdf5.org@=1.14.6 openmpi.org@=5.0.6 zlib.net@=1.3.2"
	if strings.Join(got, " ") != want {
		t.Errorf("locked = %v, want %s", got, want)
	}
	// EVERY pin, including one the manifest never named — which is the
	// difference between a lock and a restatement of the manifest. The
	// fixture has two packages and three pins so this cannot pass by
	// coincidence.
	if len(cfdEnv().Packages) != 2 || len(got) != 3 {
		t.Errorf("%d packages, %d specs — the test proves nothing",
			len(cfdEnv().Packages), len(got))
	}
	// And they go through the SAME spec parser a person's `+pkg@=1.2.3`
	// does, so a locked load and a free one cannot drift apart.
	if project(got[0]) != "hdf5.org" || constraint(got[0]) != "=1.14.6" {
		t.Errorf("the ordinary parser reads %q as %q / %q", got[0], project(got[0]), constraint(got[0]))
	}
}

// A lock from ANOTHER platform is ignored, not refused: a site commits one
// environment directory and loads it everywhere, so a linux lock on a Mac
// is the ordinary case. But it is SAID — silently resolving afresh under a
// file called `cfd.lock.hcl` is how somebody comes to believe a thing is
// pinned when it is not.
func TestALockFromAnotherPlatformIsIgnoredOutLoud(t *testing.T) {
	d := cfdLock()
	d.Platform = "plan9/mips64"
	withEnvLock(t, d, nil)

	var said []string
	_, ok := envLock(cfdEnv(), func(m string) { said = append(said, m) })
	if ok {
		t.Error("a lock for another platform was honoured")
	}
	if len(said) != 1 || !strings.Contains(said[0], "plan9/mips64") {
		t.Errorf("it was ignored in silence: %v", said)
	}
}

// Absent says nothing — that is every environment that was never locked.
// Present and UNREADABLE is a broken pin and must not read as "no lock".
func TestAnAbsentLockIsSilentAndABrokenOneIsNot(t *testing.T) {
	withEnvLock(t, bottle.Lock{}, errors.New("open x: no such file or directory"))
	var said []string
	if _, ok := envLock(cfdEnv(), func(m string) { said = append(said, m) }); ok || len(said) != 0 {
		t.Errorf("an absent lock said %v", said)
	}

	withEnvLock(t, bottle.Lock{}, errors.New("not a lock"))
	said = nil
	if _, ok := envLock(cfdEnv(), func(m string) { said = append(said, m) }); ok {
		t.Error("an unreadable lock was honoured")
	}
	if len(said) != 1 || !strings.Contains(said[0], "not a lock") {
		t.Errorf("a broken lock was swallowed: %v", said)
	}

	// And one that parses but pins nothing.
	withEnvLock(t, bottle.Lock{Generated: "2026-10-06T09:00:00Z"}, nil)
	said = nil
	if _, ok := envLock(cfdEnv(), func(m string) { said = append(said, m) }); ok {
		t.Error("a lock pinning nothing was honoured")
	}
	if len(said) != 1 || !strings.Contains(said[0], "pins nothing") {
		t.Errorf("said %v", said)
	}
}

// The lock is named for the ENVIRONMENT, not for the file: one .hcl2 can
// declare several, and naming it after the file would give them one lock
// between them.
func TestTheLockIsNamedForTheEnvironment(t *testing.T) {
	a := environment{Name: "cfd", File: "/site/environments/site.hcl2"}
	b := environment{Name: "chem", File: "/site/environments/site.hcl2"}
	if envLockPath(a) == envLockPath(b) {
		t.Fatalf("two environments in one file share a lock: %s", envLockPath(a))
	}
	if filepath.Base(envLockPath(a)) != "cfd.lock.hcl" {
		t.Errorf("path = %s", envLockPath(a))
	}
	if filepath.Dir(envLockPath(a)) != "/site/environments" {
		t.Errorf("the lock is not beside its declaration: %s", envLockPath(a))
	}
	// An environment with no file (imported, synthesised) has no lock
	// rather than a lock at a guessed path.
	if p := envLockPath(environment{Name: "x"}); p != "" {
		t.Errorf("a fileless environment got %q", p)
	}
}

// `env show` says whether a lock is in force. An environment that resolves
// afresh and one that is pinned look identical from the outside, and behave
// differently the day a version moves.
func TestEnvShowSaysWhetherALockIsInForce(t *testing.T) {
	withEnvLock(t, cfdLock(), nil)
	var out bytes.Buffer
	describeEnvLock(cfdEnv(), &out)
	for _, want := range []string{"cfd.lock.hcl", "3 pin(s)", "3 hour(s) old"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("show does not say %q: %q", want, out.String())
		}
	}

	out.Reset()
	withEnvLock(t, bottle.Lock{}, errors.New("no such file"))
	describeEnvLock(cfdEnv(), &out)
	if !strings.Contains(out.String(), "none") || !strings.Contains(out.String(), "resolved afresh") {
		t.Errorf("show does not say there is no lock: %q", out.String())
	}
}

// And the real path, once, so the seams are not the only thing tested.
func TestEnvLockReadsARealFile(t *testing.T) {
	dir := t.TempDir()
	e := environment{Name: "cfd", File: filepath.Join(dir, "site.hcl2")}
	if err := os.WriteFile(envLockPath(e), []byte(bottle.RenderLock(cfdLock())), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := timeNow
	timeNow = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { timeNow = prev })

	d, ok := envLock(e, func(m string) { t.Errorf("warned: %s", m) })
	if !ok {
		t.Fatal("a real lock on disk was not read")
	}
	if len(d.Pins) != 3 {
		t.Errorf("%d pins", len(d.Pins))
	}
}

// I MADE THE NAME A PATH, so I have to guard it.
//
// An environment's name is an HCL block label and was only ever a map key,
// where any string is harmless. filepath.Join CLEANS its result, so a
// crafted label escapes the directory — and the lock it then reads is
// installed. The pins cannot traverse any more (bottle validates project
// names), but they can name a real project at an old version, which is a
// downgrade somebody else chose.
func TestACraftedEnvironmentNameIsNotAPath(t *testing.T) {
	const decl = "/site/environments/site.hcl2"
	for _, bad := range []string{
		"../../../../tmp/evil",
		"../cfd",
		"..",
		".",
		"",
		"cfd/../../etc/x",
		"cfd\x00",
		"cfd name",
		"cfd/sub",
		"cfd\\sub",
		strings.Repeat("a", 65),
	} {
		if p := envLockPath(environment{Name: bad, File: decl}); p != "" {
			t.Errorf("name %q yielded a path: %s", bad, p)
		}
	}
	// THE POSITIVE CONTROL: the names a site actually writes still work,
	// or the guard has eaten the feature.
	for _, good := range []string{"cfd", "site", "chem-2026", "openfoam_11", "v2.1"} {
		p := envLockPath(environment{Name: good, File: decl})
		if p == "" {
			t.Errorf("a real name was refused: %q", good)
			continue
		}
		if filepath.Dir(p) != "/site/environments" {
			t.Errorf("%q escaped: %s", good, p)
		}
	}
}
