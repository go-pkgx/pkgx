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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-pkgx/bottle"
)

const version = "0.1.0"

const usage = `pkgx ` + version + ` — pure-Go pkgx runtime

usage:
  pkgx <pkg>[@version] [arg...]      run a package's program ephemerally
                                     e.g. pkgx node@22 --version
  pkgx +<pkg> [+<pkg>...] [cmd ...]  bring packages into the environment and
                                     run cmd with them on PATH/LD_LIBRARY_PATH
                                     e.g. pkgx +git +gnu.org/bash -- build.sh
  pkgx -h, --help                    show this help
  pkgx -v, --version                 show version

env:
  PKGX_DIR     bottle store (default: ~/.pkgx)
  PKGX_DIST    bottle source (default: oci://ghcr.io/go-pkgx/packages, the signed
               registry; set https://dist.pkgx.dev for the full unsigned upstream
               pantry — pair with PKGX_VERIFY=0)
  PKGX_VERIFY  verify bottle signatures, fail-closed (default: on; set
               0/false/no/off to disable)
`

func main() { os.Exit(run(os.Args[1:])) }

// run is the testable entry point; it returns the process exit code.
func run(argv []string) int {
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

	plus, rest := splitPlus(argv)
	if err := exec(plus, rest); err != nil {
		fmt.Fprintln(os.Stderr, "pkgx: "+err.Error())
		return 1
	}
	return 0
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
func exec(plus, rest []string) error {
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
		} else {
			// no explicit command: run the last +pkg's primary program.
			runProject = project(plus[len(plus)-1])
		}
	}

	// Materialise every requested package's complete FROM-scratch closure.
	closure, err := bottle.CompleteClosure(roots, dir)
	if err != nil {
		return err
	}
	libPath := bottle.LibPath(closure, dir)

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

// --- pkgspec parsing (project[@constraint]) --------------------------------

func project(spec string) string {
	if i := strings.LastIndex(spec, "@"); i > 0 {
		return spec[:i]
	}
	return spec
}

func constraint(spec string) string {
	if i := strings.LastIndex(spec, "@"); i > 0 {
		return spec[i+1:]
	}
	return "*"
}

// spec returns the project part of a run target (alias of project, named for
// readability at call sites that mean "the project to run").
func spec(s string) string { return project(s) }
