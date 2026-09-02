// Command pkgx is a dependency-free, pure-Go implementation of the pkgx
// runtime: it runs packages on the fly, materialising each one's full
// dependency closure on demand — from the signed OCI registry
// oci://ghcr.io/go-pkgx/packages by default, verifying each bottle's signature
// (fail-closed) — with no runtime deps of its own (a single CGO_ENABLED=0 binary
// that works on a `FROM scratch` image). Point PKGX_DIST at the unsigned upstream
// (https://dist.pkgx.dev) with PKGX_VERIFY=0 for the full pantry.
//
// It shares its whole bottle backend — resolution, download, FROM-scratch
// closure completion, and loader-aware exec — with pkgm via the
// github.com/go-pkgx/bottle package, so there is one source of truth.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-pkgx/bottle"
)

// version is reported by `pkgx --version`. It defaults to "dev" and is
// overridden at release-build time via -ldflags "-X main.version=<tag>".
var version = "dev"

var usage = `pkgx ` + version + ` — pure-Go pkgx runtime

usage:
  pkgx <pkg>[@version] [arg...]      run a package's program ephemerally
                                     e.g. pkgx node@22 --version
  pkgx +<pkg> [+<pkg>...] cmd [...]  bring packages into the environment and
                                     run cmd with them on PATH/LD_LIBRARY_PATH
                                     e.g. pkgx +git +gnu.org/bash -- build.sh
  pkgx +<pkg> [+<pkg>...]            print the environment those packages bring,
                                     for eval "$(pkgx +gnu.org/make +llvm.org)"
  pkgx --json +<pkg>...              the same environment as JSON, for a tool
                                     that composes it (see --modulefile)
  pkgx --modulefile +<pkg>...        the same environment as an Lmod modulefile,
                                     so an HPC site keeps its module command
  pkgx -h, --help                    show this help
  pkgx -v, --version                 show version

env:
  PKGX_DIR     bottle store (default: ~/.pkgx)
  PKGX_DIST    bottle source (default: oci://ghcr.io/go-pkgx/packages, the signed
               registry; set https://dist.pkgx.dev for the full unsigned upstream
               pantry — pair with PKGX_VERIFY=0)
  PKGX_VERIFY  verify bottle signatures, fail-closed (default: on; set
               0/false/no/off to disable)

  ~/.pkgx/config.hcl2 sets defaults for the PKGX_* / OCI_* variables above
  (HCL2 attributes, e.g. PKGX_DIST = "oci://ghcr.io/go-pkgx/packages"). A real
  environment variable always overrides a value set in the file.
`

func main() { os.Exit(run(os.Args[1:])) }

// configError reports a load/parse failure of ~/.pkgx/config.hcl2, if any. It
// is a var so tests can drive the startup-warning path.
var configError = bottle.ConfigError

// setupRootfs poses the pkgx loader at the canonical PT_INTERP path; a var so a
// test can observe the call without writing to the host's /lib.
var setupRootfs = bottle.SetupScratchRootfs

// run is the testable entry point; it returns the process exit code.
func run(argv []string) int {
	// A closure the resolver could not complete is not an error here — it is an
	// error MUCH later, when the program starts and reports "cannot open shared
	// object file". Print those diagnostics; bottle stays silent by default.
	bottle.Warn = func(msg string) { fmt.Fprintln(os.Stderr, "pkgx: "+msg) }
	if err := configError(); err != nil {
		fmt.Fprintln(os.Stderr, "pkgx: warning: ignoring ~/.pkgx/config.hcl2: "+err.Error())
	}
	if len(argv) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch argv[0] {
	case "-h", "--help":
		fmt.Print(usage)
		return 0
	case "-v", "--version":
		fmt.Println("pkgx " + version)
		return 0
	}

	format, argv := splitFormat(argv)
	plus, rest := splitPlus(argv)
	if format != formatShell && (len(plus) == 0 || len(rest) > 0) {
		fmt.Fprintln(os.Stderr, "pkgx: --json and --modulefile describe a package set: give +pkg and no command")
		return 2
	}
	if err := exec(plus, rest, format, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pkgx: "+err.Error())
		return 1
	}
	return 0
}

// outputFormat selects how `pkgx +a +b` renders the environment it composed.
// The three renderings are the SAME data: one resolution, one closure, one set
// of variables. A second code path that recomputed any of it would drift.
type outputFormat int

const (
	formatShell      outputFormat = iota // export lines, for eval "$(pkgx +a)"
	formatJSON                           // the composed environment as data
	formatModulefile                     // an Lmod modulefile (Lua)
)

// splitFormat pulls a leading --json / --modulefile off the argument list.
func splitFormat(argv []string) (outputFormat, []string) {
	if len(argv) == 0 {
		return formatShell, argv
	}
	switch argv[0] {
	case "--json":
		return formatJSON, argv[1:]
	case "--modulefile":
		return formatModulefile, argv[1:]
	}
	return formatShell, argv
}

// splitPlus separates leading "+pkg" tokens from the remainder. A lone "--"
// ends the package list and is dropped; everything after it is the command.
// Without any "+pkg", the first remainder token is itself the package to run.
func splitPlus(argv []string) (plus, rest []string) {
	i := 0
	for ; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			i++
			break
		}
		if strings.HasPrefix(a, "+") {
			plus = append(plus, strings.TrimPrefix(a, "+"))
			continue
		}
		break
	}
	rest = argv[i:]
	return plus, rest
}

// exec materialises the requested packages and runs the target program with
// every package's bin dir on PATH and the combined closure on LD_LIBRARY_PATH.
//
//   - `pkgx <pkg> args`      -> plus empty, rest=[pkg, args...]; run pkg's
//     primary program (its declared bin matching the project leaf).
//   - `pkgx +a +b [cmd...]`  -> materialise a and b; run cmd resolved from
//     their bin dirs (or, with no cmd, the primary program of the last +pkg).
func exec(plus, rest []string, format outputFormat, stdout io.Writer) error {
	dir := bottle.Dir()

	// The set of packages to materialise, and the program to run.
	roots := map[string]string{}
	var runProject, program string
	var args []string

	if len(plus) == 0 {
		if len(rest) == 0 {
			return fmt.Errorf("nothing to run (try --help)")
		}
		runProject, args = spec(rest[0]), rest[1:]
		roots[project(rest[0])] = constraint(rest[0])
	} else {
		for _, p := range plus {
			roots[project(p)] = constraint(p)
		}
		if len(rest) > 0 {
			program, args = rest[0], rest[1:]
		}
		// No command: print the environment these packages bring, the
		// `eval "$(pkgx +a +b)"` contract (see envMode below).
	}

	// Materialise every requested package's complete FROM-scratch closure.
	closure, err := bottle.CompleteClosure(roots, dir)
	if err != nil {
		return err
	}
	libPath := bottle.LibPath(closure, dir)

	// `pkgx +a +b` with no command prints the environment those packages bring,
	// for `eval "$(pkgx +a +b)"` — the contract bk's generated build script
	// depends on, and the reason a build can source its toolchain without a
	// system package manager.
	if len(plus) > 0 && runProject == "" && program == "" {
		// The caller is about to run the packages' own binaries (a build script
		// after `eval "$(pkgx +deps)"`), so the loader has to be in place first:
		// on a FROM-scratch image there is no /lib/ld-linux-* until we pose the
		// pkgx glibc one. Best-effort — on a normal host it is already there.
		if bottle.GOOS() == "linux" {
			if loader := bottle.FindLoader(dir); loader != "" {
				setupRootfs(loader, "")
			}
		}
		return envMode(closure, dir, format, stdout)
	}

	// Every requested root's bin dir joins PATH so `program` resolves across
	// all the packages the user brought in with +pkg.
	var binDirs []string
	for p := range roots {
		binDirs = append(binDirs, filepath.Join(bottle.PrefixOf(p, closure, dir), "bin"))
	}

	binPath, err := resolveBin(runProject, program, binDirs, closure, dir)
	if err != nil {
		return err
	}

	env := runEnv(binDirs, closure, dir, libPath)
	return runELF(binPath, args, env, libPath, dir)
}

// runEnv builds the child environment. On linux/darwin the closure's shared
// libraries are found via $LD_LIBRARY_PATH (and PATH carries the bin dirs). On
// Windows there is no LD_LIBRARY_PATH: the loader resolves DLLs from the exe's
// own directory and from PATH, so PATH must carry BOTH the bin and lib dirs,
// joined with the OS-native list separator (";", not ":").
func runEnv(binDirs []string, closure []bottle.Resolved, dir, libPath string) []string {
	env := os.Environ()
	if bottle.GOOS() == "windows" {
		sep := string(os.PathListSeparator)
		parts := append(append([]string{}, binDirs...), bottle.LibDirs(closure, dir)...)
		return append(env, "PATH="+strings.Join(parts, sep)+sep+os.Getenv("PATH"))
	}
	return append(env,
		"LD_LIBRARY_PATH="+libPath,
		"PATH="+strings.Join(binDirs, ":")+":"+os.Getenv("PATH"),
	)
}

// resolveBin finds the absolute path of the binary to exec: a project's primary
// program (runProject set), or a named program looked up across binDirs. The
// result is passed through bottle.ResolveBinPath so a Windows lookup lands on
// the ".exe" image.
func resolveBin(runProject, program string, binDirs []string, closure []bottle.Resolved, dir string) (string, error) {
	if runProject != "" {
		_, provides, err := bottle.FetchMeta(runProject)
		if err != nil {
			return "", err
		}
		prefix := bottle.PrefixOf(runProject, closure, dir)
		return bottle.ResolveBinPath(filepath.Join(prefix, "bin", bottle.PrimaryBin(runProject, provides))), nil
	}
	// An explicit path runs as itself — `pkgx +gnu.org/glibc -- /usr/local/bin/bk`
	// brings the packages into the environment for a program that is NOT one of
	// them, which is how a tool gets a runtime on a FROM-scratch image.
	if strings.ContainsRune(program, os.PathSeparator) {
		cand := bottle.ResolveBinPath(program)
		if _, err := os.Stat(cand); err != nil {
			return "", fmt.Errorf("not executable: %s", program)
		}
		return cand, nil
	}
	for _, d := range binDirs {
		cand := bottle.ResolveBinPath(filepath.Join(d, program))
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("command not found in the requested packages: %s", program)
}

// runELF execs the target binary. On linux it arranges the pkgx glibc loader so
// the binary (and any children) resolve on a scratch image — the same mechanism
// pkgm's `run` uses. On darwin and windows there is no such loader dance: the
// binary is exec'd directly (Windows resolves its DLLs from the exe dir + PATH,
// which runEnv has already populated).
func runELF(binPath string, args, env []string, libPath, dir string) error {
	if bottle.GOOS() == "linux" {
		if loader := bottle.FindLoader(dir); loader != "" {
			bottle.SetupScratchRootfs(loader, "")
			if !bottle.CanonicalLoaderExists() && bottle.IsELF(binPath) {
				argv := append([]string{loader, "--library-path", libPath, binPath}, args...)
				return bottle.Exec(loader, argv, env)
			}
		}
	}
	return bottle.Exec(binPath, append([]string{binPath}, args...), env)
}

// --- pkgspec parsing (project[@constraint] | project<op>version) -----------

// verDelims are the characters that end a project name and begin its version
// constraint. A pkgspec pins a version either with "@" (`gnu.org/gawk@5.3`) or
// by appending a range operator directly to the project name (`cmake.org^3`,
// `gnu.org/gmp>=6`) — both forms are pkgx syntax, and a pantry dependency map
// such as `invisible-island.net/ncurses: ^6` renders as the second one. No
// project name contains any of these characters, so the FIRST occurrence marks
// the boundary.
const verDelims = "@^~<>="

// splitSpec cuts a pkgspec into its project and its constraint. The constraint
// keeps its operator ("^6", ">=6") because that is what bottle's version
// matcher consumes; the "@" form carries no operator, so it is dropped. A spec
// with no constraint matches any version ("*").
func splitSpec(s string) (project, constraint string) {
	i := strings.IndexAny(s, verDelims)
	if i <= 0 {
		return s, "*"
	}
	if s[i] == '@' {
		return s[:i], s[i+1:]
	}
	return s[:i], s[i:]
}

func project(spec string) string {
	p, _ := splitSpec(spec)
	return p
}

func constraint(spec string) string {
	_, c := splitSpec(spec)
	return c
}

// spec returns the project part of a run target (alias of project, named for
// readability at call sites that mean "the project to run").
func spec(s string) string { return project(s) }

// composed is the environment a package set brings, as DATA: one resolution,
// one closure, one set of variables, rendered three ways (shell, JSON, Lmod).
// A second code path that recomputed any of it would drift from this one, and
// the drift would show up as a module that works under eval and not under
// `module load`.
type composed struct {
	Closure []pkgRef `json:"closure"`
	Env     []envVar `json:"env"`
}

// pkgRef names one member of the closure and where it was materialised.
type pkgRef struct {
	Project string `json:"project"`
	Version string `json:"version"`
	Prefix  string `json:"prefix"`
}

// envVar is one variable the package set contributes. Exactly one of Prepend
// and Value is set: Prepend joins the existing value (PATH and friends), Value
// replaces it (what a recipe's `runtime: env:` block declares).
type envVar struct {
	Name    string   `json:"name"`
	Prepend []string `json:"prepend,omitempty"`
	Value   string   `json:"value,omitempty"`
}

// envMode prints the environment a package set brings, in the requested
// rendering — what `eval "$(pkgx +a +b)"` consumes, what a module generator
// reads, or a modulefile an HPC site can install. It covers the WHOLE closure
// (dependencies included), so a compiler invoked afterwards finds every
// dependency's headers, libraries and pkg-config files.
func envMode(closure []bottle.Resolved, dir string, format outputFormat, stdout io.Writer) error {
	c := composeEnv(closure, dir)
	switch format {
	case formatJSON:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(c)
	case formatModulefile:
		return writeModulefile(c, stdout)
	}
	for _, e := range c.Env {
		if e.Value != "" {
			fmt.Fprintf(stdout, "export %s=%q\n", e.Name, e.Value)
			continue
		}
		fmt.Fprintf(stdout, "export %s=\"%s${%s:+:$%s}\"\n", e.Name, strings.Join(e.Prepend, ":"), e.Name, e.Name)
	}
	return nil
}

// composeEnv computes what the closure contributes.
//
// Note lib64: a bottle built on x86-64 may install its libraries and .pc files
// under lib64 rather than lib. Upstream pkgx only exports lib/pkgconfig, which
// is why a linux/x86-64 build of wget could not find openssl through
// pkg-config. Both are exported here.
func composeEnv(closure []bottle.Resolved, dir string) composed {
	var c composed
	var bin, ld, lib, cpath, pc, xdg, aclocal, cmake []string
	for _, r := range closure {
		p := bottle.PrefixOf(r.Project, closure, dir)
		c.Closure = append(c.Closure, pkgRef{Project: r.Project, Version: r.Version.Raw, Prefix: p})
		// CMake's find_package searches <prefix>/{include,lib,…} for every entry
		// of CMAKE_PREFIX_PATH. CPATH and LIBRARY_PATH mean nothing to it, so a
		// cmake recipe's find_package(ZLIB) failed with "Could NOT find ZLIB
		// (missing: ZLIB_LIBRARY ZLIB_INCLUDE_DIR)" even though zlib was right
		// there in the closure — measured on facebook.com/zstd and libzip.org in
		// a FROM-scratch builder, where there is no system zlib to fall back on.
		addDir(&cmake, p)
		addDir(&bin, filepath.Join(p, "bin"))
		// sbin too: a bottle puts its system tools there, and a recipe that calls
		// one gets a bare "command not found" (exit 127) with nothing naming the
		// missing binary. gnu.org/glibc ships sbin/ldconfig, which nlnetlabs.nl/ldns
		// invokes at install time — measured, in a FROM-scratch builder where there
		// is no system /sbin to fall back on.
		addDir(&bin, filepath.Join(p, "sbin"))
		for _, l := range []string{"lib", "lib64"} {
			addDir(&ld, filepath.Join(p, l))
			addDir(&lib, filepath.Join(p, l))
			addDir(&pc, filepath.Join(p, l, "pkgconfig"))
		}
		// A libc's headers must NEVER travel on CPATH. CPATH applies to every
		// compiler invocation, so a bottle glibc's headers reach compilers that
		// are using the HOST's libc — and two glibc header sets in one
		// translation unit conflict outright:
		//   glibc/v2.44.0/include/bits/stdint-uintn.h:27: error: conflicting
		//   types for 'uint64_t'
		// which is how the kernel's host tools failed. A compiler that should use
		// a bottle libc is told so with --sysroot and -isystem, at the right
		// priority; the library path is unaffected and stays below.
		if r.Project != bottle.GlibcProject {
			addDir(&cpath, filepath.Join(p, "include"))
		}
		addDir(&xdg, filepath.Join(p, "share"))
		addDir(&aclocal, filepath.Join(p, "share", "aclocal"))
	}
	for _, e := range []struct {
		name string
		dirs []string
	}{
		{"PATH", bin},
		{"LD_LIBRARY_PATH", ld},
		{"LIBRARY_PATH", lib},
		{"CPATH", cpath},
		{"PKG_CONFIG_PATH", pc},
		{"XDG_DATA_DIRS", xdg},
		{"ACLOCAL_PATH", aclocal},
		{"CMAKE_PREFIX_PATH", cmake},
	} {
		if len(e.dirs) > 0 {
			c.Env = append(c.Env, envVar{Name: e.name, Prepend: e.dirs})
		}
	}
	// Then each package's OWN declarations. A `runtime: env:` block is how a
	// package states what its consumers need: gnu.org/help2man bundles the perl
	// module Locale::gettext into its prefix and publishes PERL5LIB so it can be
	// found. Emitted after the computed paths, in closure order, and verbatim —
	// each value carries its own `$VAR` chain, so two packages contributing to
	// the same variable compose instead of overwriting.
	//
	// A recipe we cannot fetch is not fatal: the package is installed and its
	// paths are already exported, so the build keeps its chance. It is said out
	// loud, though — silence would look exactly like a package declaring nothing.
	for _, r := range closure {
		// FetchRuntimeEnvIn, not FetchRuntimeEnv: a recipe may point at a
		// DEPENDENCY's prefix, and only the closure can resolve that.
		// rust-lang.org/cargo sets
		//   CARGO_HTTP_CAINFO: ${{deps.curl.se/ca-certs.prefix}}/ssl/cert.pem
		// and without the closure cargo built with no CA bundle at all.
		env, err := bottle.FetchRuntimeEnvIn(r.Project, closure, dir)
		if err != nil {
			if bottle.Warn != nil {
				bottle.Warn(fmt.Sprintf("cannot read %s's runtime env: %v", r.Project, err))
			}
			continue
		}
		for _, k := range sortedKeys(env) {
			c.Env = append(c.Env, envVar{Name: k, Value: env[k]})
		}
	}
	return c
}

// sortedKeys returns a map's keys in a deterministic order, so the emitted
// environment is byte-identical from one run to the next.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// addDir appends a directory to a list if it exists and is not already there.
func addDir(list *[]string, dir string) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	for _, d := range *list {
		if d == dir {
			return
		}
	}
	*list = append(*list, dir)
}

// modulePrepend matches a runtime-env value that COMPOSES with the variable it
// sets — `<path>:$NAME`, which is how a recipe says "put mine in front of what
// is already there". PYTHONPATH is the case that matters: a dozen recipes in
// the pantry write `{{prefix}}/lib/python3.12/site-packages:$PYTHONPATH`.
//
// In shell that composes on its own. Lmod has no shell to expand it, so the
// value has to be recognised and turned into prepend_path — otherwise the
// modulefile would literally set PYTHONPATH to a string containing `$PYTHONPATH`
// and every python underneath would see a path that does not exist.
var modulePrepend = regexp.MustCompile(`^(.*):\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?$`)

// writeModulefile renders the composed environment as an Lmod modulefile (Lua),
// which is also a valid Environment Modules 4.x modulefile — both read Lua.
//
// Why generate rather than replace: a site's users know `module load`, their job
// scripts call it, and their documentation says so. The part worth replacing is
// what sits UNDER the modulefile — a hand-built tree on a shared filesystem —
// not the command people type. This gives them signed, content-addressed,
// closure-complete prefixes with the front-end they already have.
func writeModulefile(c composed, stdout io.Writer) error {
	fmt.Fprintln(stdout, "-- Generated by pkgx --modulefile. Do not edit: regenerate.")
	fmt.Fprintln(stdout, "--")
	fmt.Fprintln(stdout, "-- The closure this module puts on PATH, with the exact version of each")
	fmt.Fprintln(stdout, "-- member. Every prefix is content-addressed and immutable, so a module")
	fmt.Fprintln(stdout, "-- that loaded once loads the same tree forever.")
	for _, p := range c.Closure {
		fmt.Fprintf(stdout, "--   %s %s\n", p.Project, p.Version)
	}
	fmt.Fprintln(stdout)
	if len(c.Closure) > 0 {
		fmt.Fprintf(stdout, "whatis(%q)\n\n", "pkgx closure rooted at "+c.Closure[0].Project+" "+c.Closure[0].Version)
	}
	for _, e := range c.Env {
		if len(e.Prepend) > 0 {
			// Reverse order: prepend_path pushes each entry to the FRONT, so
			// emitting them in order would end with the list back to front.
			for i := len(e.Prepend) - 1; i >= 0; i-- {
				fmt.Fprintf(stdout, "prepend_path(%q, %q)\n", e.Name, e.Prepend[i])
			}
			continue
		}
		if m := modulePrepend.FindStringSubmatch(e.Value); m != nil && m[2] == e.Name {
			fmt.Fprintf(stdout, "prepend_path(%q, %q)\n", e.Name, m[1])
			continue
		}
		if strings.Contains(e.Value, "$") {
			// Said out loud rather than mangled: a value referring to some OTHER
			// variable cannot be expressed as a prepend, and setenv would install
			// a literal dollar sign. Nothing in the pantry does this today; if
			// something starts to, this line is how it will be found.
			fmt.Fprintf(stdout, "-- SKIPPED %s: value refers to another variable and Lmod cannot expand it: %q\n", e.Name, e.Value)
			continue
		}
		fmt.Fprintf(stdout, "setenv(%q, %q)\n", e.Name, e.Value)
	}
	return nil
}
