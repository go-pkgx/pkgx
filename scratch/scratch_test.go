// Package scratch holds one test: that pkgx and pkgs answer about the whole
// dependency tree inside a FROM scratch image with the network cut.
//
// # WHY IT IS HERE AND NOT IN A NOTE
//
// "Works offline, from a cached catalogue, in an image with nothing else in
// it" is the property the catalogue, the name index and the browser were all
// built for. Nothing was checking it. It was measured by hand on 2026-10-20,
// found to hold, and a property measured once is a property that holds once:
// the first `net/http` import on a completion path, or a lookup that falls
// back to the registry when a file is missing, would pass every unit test in
// this repository and break this.
//
// # ⛔ THE CONTROL IS THE POINT, NOT THE CASES
//
// "ls answered offline" is the same sentence whether the network was cut or
// whether somebody dropped the flag that cuts it, and `--network none` is
// one word away from being absent. So the run includes a PLANTED POSITIVE:
// `pkgs catalog update`, the one command that must reach the registry, run
// in the SAME configuration. It has to FAIL, and to fail ABOUT THE NETWORK —
// a binary that crashed for its own reasons is also non-zero and proves
// nothing. If it passes, the whole test fails before judging anything else,
// because every other line would be meaningless.
//
// Skipped unless PKGX_SCRATCH_TEST=1, so `go test ./...` on a laptop without
// docker stays quiet; CI sets it.
package scratch_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFromScratchOffline(t *testing.T) {
	if os.Getenv("PKGX_SCRATCH_TEST") != "1" {
		t.Skip("set PKGX_SCRATCH_TEST=1 to build the image and run it (needs docker)")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("PKGX_SCRATCH_TEST=1 was asked for and docker is not here: %v", err)
	}

	ctx := t.TempDir()
	buildBinaries(t, ctx)
	fetchCatalogue(t, ctx)
	copyFile(t, "Dockerfile", filepath.Join(ctx, "Dockerfile"))

	image := "pkgx-scratch:" + strings.ReplaceAll(t.Name(), "/", "-")
	run(t, "docker", "build", "-t", image, ctx)
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", image).Run() })

	// ⛔ THE CONTROL FIRST. Nothing below means anything until this fails.
	code, out := inImage(t, image, nil, "catalog", "update")
	switch {
	case code == 0:
		t.Fatalf("`catalog update` SUCCEEDED with --network none — the network is not cut, so this test cannot see what it claims to test:\n%s", out)
	case !mentionsNetwork(out):
		t.Fatalf("`catalog update` failed, but not about the network — the sandbox is not what failed it:\n%s", out)
	default:
		t.Logf("control ok: catalog update refused as it must — %s", firstLine(out))
	}

	for _, c := range []struct {
		name    string
		argv    []string
		wantOut string
	}{
		{"the whole tree's roots", []string{"ls"}, "gnu.org"},
		{"one namespace", []string{"ls", "gnu.org"}, "bash"},
		{"a dependency tree", []string{"ls", "--tree", "curl.se"}, "curl.se"},
		{"a search", []string{"search", "json"}, "jsonnet"},
		// Without a TTY the browser prints the roots and exits: a documented
		// degradation, and the only shape a container without -t can take.
		{"the browser, degraded", []string{"browse"}, "not a terminal"},
		{"why one package needs another", []string{"why", "curl.se", "openssl.org"}, "curl.se"},
		{"what catalogue is here", []string{"catalog"}, "project"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out := inImage(t, image, nil, c.argv...)
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if !strings.Contains(out, c.wantOut) {
				t.Errorf("answered without %q:\n%s", c.wantOut, out)
			}
		})
	}

	// ⛔ THIS IS THE NO-NETWORK PROOF FOR COMPLETION, and it is a case and
	// not a stopwatch. A timing gate here would measure the CI runner: on
	// this machine `docker run` of this image costs ~170 ms before the
	// binary's first instruction, which is ten times the 20 ms a completion
	// is allowed, so the first version of this measurement reported docker
	// and called it pkgx. Timings are logged below for a reader; what is
	// ASSERTED is that the answer arrives at all with no network to get it
	// from.
	t.Run("tab completion", func(t *testing.T) {
		start := time.Now()
		code, out := inImageEntrypoint(t, image, "/pkgx",
			[]string{"PKGX_GET_COMPLETIONS=0"}, "gnu.o")
		took := time.Since(start)
		if code != 0 || !strings.Contains(out, "gnu.org") {
			t.Fatalf("exit %d, no completion:\n%s", code, out)
		}
		floorStart := time.Now()
		if code, out := inImageEntrypoint(t, image, "/pkgx", nil, "--version"); code != 0 {
			t.Fatalf("the floor run failed: exit %d:\n%s", code, out)
		}
		floor := time.Since(floorStart)
		t.Logf("completion %v, container floor (--version) %v, difference %v — the difference is the only part that is about pkgx",
			took.Round(time.Millisecond), floor.Round(time.Millisecond), (took - floor).Round(time.Millisecond))

		// ⛔ AND THE REGISTRY IT IS TOLD ABOUT MUST NOT MATTER. A completion
		// that consulted the registry would behave differently when pointed
		// at a blackhole; one that reads a file on disk cannot tell the two
		// apart. Asserted as an ANSWER, not as a time, for the same reason
		// as above.
		code, out = inImageEntrypoint(t, image, "/pkgx",
			[]string{"PKGX_GET_COMPLETIONS=0", "PKGX_DIST=oci://127.0.0.1:1/nothing"}, "gnu.o")
		if code != 0 || !strings.Contains(out, "gnu.org") {
			t.Errorf("a blackhole registry changed the completion: exit %d:\n%s", code, out)
		}
	})
}

// buildBinaries cross-builds both commands for the image's platform.
func buildBinaries(t *testing.T, dir string) {
	t.Helper()
	for _, b := range []struct{ out, pkg string }{{"pkgx", ".."}, {"pkgs", "../cmd/pkgs"}} {
		cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, b.out), b.pkg)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building %s: %v\n%s", b.out, err, out)
		}
	}
}

// fetchCatalogue puts a real catalogue in the build context.
//
// It runs the HOST build of pkgs against a store of its own, so the test
// never reads or writes the developer's ~/.pkgx — and it asks for the
// platform the image will be, which is not necessarily this one.
func fetchCatalogue(t *testing.T, dir string) {
	t.Helper()
	store := t.TempDir()
	host := filepath.Join(t.TempDir(), "pkgs")
	build := exec.Command("go", "build", "-o", host, "../cmd/pkgs")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the host pkgs: %v\n%s", err, out)
	}
	cmd := exec.Command(host, "catalog", "update", "--platform", "linux/"+pkgxArch())
	cmd.Env = append(os.Environ(), "PKGX_DIR="+store, "PKGX_CATALOG=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fetching the catalogue (this step, and only this step, needs the network): %v\n%s", err, out)
	}
	src := filepath.Join(store, "catalog")
	dst := filepath.Join(dir, "catalog")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	// ⛔ An empty copy would build an image whose catalogue is absent, and
	// `ls` would then answer "no catalogue" — a clean failure that reads as
	// a defect in pkgx rather than in this test.
	if len(entries) == 0 {
		t.Fatalf("catalog update wrote nothing into %s", src)
	}
	for _, e := range entries {
		copyFile(t, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()))
	}
}

// pkgxArch is pkgx's spelling of this machine's architecture, which is not
// Go's: a catalogue is published as linux-x86-64, not linux-amd64.
func pkgxArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86-64"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}

func inImage(t *testing.T, image string, env []string, args ...string) (int, string) {
	t.Helper()
	return dockerRun(t, image, "", env, args)
}

func inImageEntrypoint(t *testing.T, image, entrypoint string, env []string, args ...string) (int, string) {
	t.Helper()
	return dockerRun(t, image, entrypoint, env, args)
}

func dockerRun(t *testing.T, image, entrypoint string, env, args []string) (int, string) {
	t.Helper()
	argv := []string{"run", "--rm", "--network", "none"}
	for _, e := range env {
		argv = append(argv, "-e", e)
	}
	if entrypoint != "" {
		argv = append(argv, "--entrypoint", entrypoint)
	}
	argv = append(argv, image)
	argv = append(argv, args...)
	cmd := exec.Command("docker", argv...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), buf.String()
	}
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, buf.String())
	}
	return 0, buf.String()
}

// mentionsNetwork is a list of the ways this stack says it could not reach
// the network, rather than a substring of one of them: the message comes
// from the Go runtime and from the registry client, and a check matching
// only today's wording would quietly accept a future failure of another
// kind as proof of isolation.
func mentionsNetwork(s string) bool {
	for _, w := range []string{
		"network is unreachable", "no such host", "connection refused",
		"dial tcp", "i/o timeout", "lookup ", "server misbehaving",
	} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
