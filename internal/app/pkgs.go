package app

import (
	"fmt"
	"os"
)

// `pkgs` — what the registry has, and what you have.
//
// # WHY A SECOND BINARY RATHER THAN NINE MORE SUBCOMMANDS
//
// Upstream pkgx has one grammar and it has no verbs at all: the first
// argument that is not a flag is a PACKAGE, and pkgx runs it.
//
//	pkgx node@22 --version
//	pkgx ls                  # GNU ls, from gnu.org/coreutils
//
// This fork had added nine verbs to that position — ls, search, why, store,
// browse, catalog, env, compat, completion — and every one of them took a
// name away from the packages. Two did it to packages that exist: measured
// against the published linux/x86-64 catalogue on 2026-10-18, `gnu.org/
// coreutils` provides both `ls` and `env`. So `pkgx ls` ran a catalogue
// listing where upstream runs GNU ls, and there was no way to ask for the
// other one.
//
// ⛔ A RESERVED WORD CANNOT FIX THAT, which is the part worth writing down.
// The obvious repair is a single word to hang the verbs off — `pkgx pkg ls`.
// It fails for the same reason: `vercel.com/pkg` provides the command `pkg`.
// Any bare word can become a package tomorrow, because the whole point of
// the first position is that the registry decides what is in it. Only a
// token the grammar already reserves is safe, and the two kinds are a flag
// (which is what upstream chose for its own query mode, `pkgx -Q`) and a
// different binary.
//
// # WHY `pkgs`
//
// It reads as a subject followed by a verb — `pkgs search json`, `pkgs why
// curl.se openssl.org` — which is the grammar being restored, and it keeps
// the family's shape beside pkgx, pkgm and pkge. Checked before it was
// chosen: nothing in either published catalogue provides a command called
// `pkgs`, while `pkgctl` is a real binary in Arch's devtools and `pkgtool`
// one in Slackware — collisions outside the catalogue that the catalogue
// cannot report.
//
// It is one binary beside pkgx and not one repository beside it, because
// the two share the resolver, the catalogue reader and the store walker. A
// fork of the resolver is a fork of what a version MEANS.
const pkgsUsage = `pkgs dev — what the registry has, and what you have

usage:
  pkgs ls [--tree] [--depth N] [node]
                                     what is available under a node. A
                                     NAMESPACE node lists what it contains,
                                     a PACKAGE node what it needs; a
                                     trailing slash asks for the namespace
                                     where a node is both (curl.se is).
                                     --tree descends, --depth bounds it.
                                     Read from the catalogue, so no network
  pkgs ls --dependents [--tree] <pkg>
                                     the other direction: who NEEDS this.
                                     What you ask before CHANGING something
  pkgs why <project> <dependency>    the shortest path by which one needs the
                                     other, as "nix why-depends" answers it:
                                     an explanation, not every route
  pkgs search [-n N] <text>          find a package by its name or by a COMMAND
                                     it provides: "pkgs search rg" finds
                                     crates.io/ripgrep, which no guess at the
                                     project name reaches. Offline
  pkgs browse [node]                 walk the tree full-screen: arrows to move
                                     and descend, tab cycles names,
                                     dependencies and dependents, / searches,
                                     space pins, q to quit printing what you
                                     pinned — so pkgx +$(pkgs browse) composes
  pkgs catalog                       which catalogue is here, and how old
  pkgs catalog update                fetch the catalogue of what this platform
                                     has, into $PKGX_DIR/catalog. THE ONLY
                                     command that asks the registry what
                                     exists; ls and <TAB> then read the file,
                                     so they are instant and work offline
  pkgs catalog update --platform os/arch
                                     the same, for a platform you are not on:
                                     linux/x86-64 from a mac. Writes to that
                                     platform's own file, so ls and <TAB>
                                     still read the host's
  pkgs catalog reindex               rebuild the name index <TAB> reads from
                                     the catalogue already on disk, with NO
                                     network — for an image that ships one
  pkgs request [--url] <project>     a package is missing: what the registry
                                     actually has, as a report you can read
                                     and then open as an issue. It SENDS
                                     NOTHING — no argv, no environment, no
                                     paths; the fields are fixed and printed
  pkgs catalog platforms             which platforms the registry publishes a
                                     catalogue for, one per line, ready to hand
                                     back to --platform. Asks the registry: no
                                     local file knows what EXISTS
  pkgs store [--by-size] [-n N]      what the bottle store holds: every version
                                     an ephemeral run ever pulled, and its
                                     size. Reports and deletes NOTHING
  pkgs store --root <lock> ...       and what any of it is FOR: a lock pins
                                     a whole closure, so it is a root set.
                                     Live against dead, as guix gc --list-live
                                     does — and like it, removes NOTHING
  pkgs compat +<pin>... -- <pkg>...  how much of a package set resolves under
                                     those base versions, and what each refusal
                                     blames. Projects on stdin when none given
  pkgs env init [--module]           print the pkge shell function, for
                                     eval "$(pkgs env init)" in a profile:
                                     pkge load|unload|purge|list|save|restore
                                     --module also defines module() and ml(),
                                     where no other module system exists
  pkgs env avail                     list the declared environments
  pkgs env show <environment>        what an environment brings, and from where
  pkgs env import <modulefile>...    convert Lmod (.lua) or Environment Modules
                                     (TCL) modulefiles into HCL2 environments,
                                     refusing anything it would have to guess at
  pkgs completion bash|zsh|fish      the shell snippet for <TAB> completion, for
                                     BOTH pkgs and pkgx. It asks the binaries,
                                     so it follows the registry without being
                                     regenerated
  pkgs -h, --help                    show this help
  pkgs -v, --version                 show version

  Running a package is pkgx's job, and it keeps upstream's grammar:
  "pkgx node@22 --version", "pkgx +gnu.org/make -- build.sh", "pkgx -Q git".

env:
  PKGX_DIR     bottle store (default: ~/.pkgx)
  PKGX_CATALOG read the catalogue from this file instead of $PKGX_DIR/catalog —
               for an air-gapped image, or to inspect one before publishing.
               Named and unreadable is an error, not a quiet fallback
  PKGX_DIST    bottle source (default: oci://ghcr.io/go-pkgx/packages)
  PKGX_VERIFY  verify bottle signatures, fail-closed (default: on)

  The variables keep the PKGX_ spelling: they name a store and a registry
  that both binaries share, not a program.
`

// runVerb dispatches the verbs `pkgs` owns, and reports whether argv[0] was
// one of them.
//
// ⛔ ONE DISPATCH, NOT TWO. It lives here rather than inline in Pkgs because
// `pkgx` has to be able to say something useful about these words without
// running them — see unknownVerb in main.go, which answers `pkgx ls` by
// pointing at `pkgs ls` ONLY when the registry does not offer a package by
// that name. Two lists of nine words would disagree the first time one
// gained a tenth.
func runVerb(argv []string) (int, bool) {
	switch argv[0] {
	case "ls":
		return runLs(argv[1:], os.Stdout, os.Stderr), true
	case "catalog":
		return runCatalogCmd(argv[1:], os.Stdout, os.Stderr), true
	case "store":
		return runStore(argv[1:], os.Stdout, os.Stderr), true
	case "why":
		return runWhy(argv[1:], os.Stdout, os.Stderr), true
	case "search":
		return runSearch(argv[1:], os.Stdout, os.Stderr), true
	case "request":
		return runRequest(argv[1:], os.Stdout, os.Stderr), true
	case "browse":
		return runBrowse(argv[1:], os.Stdin, os.Stdout, os.Stderr), true
	case "completion":
		if len(argv) < 2 {
			fmt.Fprintln(os.Stderr, pfx()+"usage: "+tool+" completion bash|zsh|fish")
			return 2, true
		}
		return runCompletionSnippet(argv[1], os.Stdout, os.Stderr), true
	case "compat":
		// How far does a chosen base reach? See runCompat.
		plus, rest := splitPlus(argv[1:])
		if err := runCompat(plus, rest, os.Stdin, os.Stdout); err != nil {
			if err != errSilent {
				fmt.Fprintln(os.Stderr, pfx()+err.Error())
			}
			return 1, true
		}
		return 0, true
	case "env":
		return runEnvCmd(argv), true
	}
	return 0, false
}

// isVerb reports whether a word is one of pkgs's verbs.
//
// ⛔ IT READS subcommandCompletions, the list completion already offers,
// rather than a second slice of the same nine words. I wrote that second
// slice first, two functions after a comment saying two lists would
// disagree the first time one gained a tenth entry. The one that exists is
// the one <TAB> shows, so it is the one that cannot be forgotten.
func isVerb(s string) bool {
	for _, c := range subcommandCompletions {
		if c.value == s {
			return true
		}
	}
	return false
}

// runEnvCmd is `pkgs env …`, the environment front-end's one entry point. A
// verb rather than four top-level flags: the flags are not options on
// anything, they are a different job.
func runEnvCmd(argv []string) int {
	if len(argv) == 1 {
		fmt.Fprintln(os.Stderr, pfx()+"usage: "+tool+" env init|load|unload|purge|avail|show|import")
		return 2
	}
	var err error
	switch argv[1] {
	case "init":
		err = modeShellInit(len(argv) > 2 && argv[2] == "--module", os.Stdout)
	case "load", "unload", "purge":
		err = modeShell(argv[1], argv[2:], composeSpecs, osLookupEnv, os.Stdout)
	case "avail":
		err = modeAvail(os.Stdout)
	case "import":
		if len(argv) < 3 {
			fmt.Fprintln(os.Stderr, pfx()+"usage: "+tool+" env import <modulefile>...")
			return 2
		}
		err = modeImport(argv[2:], os.Stdout, os.Stderr)
	case "show":
		if len(argv) < 3 {
			fmt.Fprintln(os.Stderr, pfx()+"usage: "+tool+" env show <environment>")
			return 2
		}
		err = modeShow(argv[2], os.Stdout)
	default:
		fmt.Fprintln(os.Stderr, pfx()+"unknown env command "+argv[1]+" (init|load|unload|purge|avail|show|import)")
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, pfx()+err.Error())
		return 1
	}
	return 0
}

// Pkgs is the `pkgs` command.
//
// It sets `tool` first and before anything can print, because every
// diagnostic in this package names the binary the user typed and there is
// exactly one moment at which that is known.
func Pkgs(argv []string) int {
	tool = "pkgs"
	bottleWarn()
	if err := configError(); err != nil {
		fmt.Fprintln(os.Stderr, pfx()+"warning: ignoring ~/.pkgx/config.hcl2: "+err.Error())
	}
	if len(argv) == 0 {
		fmt.Fprint(os.Stderr, pkgsUsage)
		return 2
	}
	// Before anything else, for the same reason pkgx does it: a completion
	// must not install, fetch or run a thing.
	if maybeComplete(argv, os.Getenv, os.Stdout) {
		return 0
	}
	if code, handled := runVerb(argv); handled {
		return code
	}
	switch argv[0] {
	case "-h", "--help":
		fmt.Print(pkgsUsage)
		return 0
	case "-v", "--version":
		fmt.Println("pkgs " + version)
		return 0
	}
	// ⛔ IT DOES NOT FALL BACK TO RUNNING THE PACKAGE. `pkgs node` is a
	// question nobody asked — pkgs answers ABOUT packages and pkgx runs
	// them — and guessing which was meant is how the grammar got muddled in
	// the first place. The refusal names the other binary.
	fmt.Fprintf(os.Stderr, "%s%q is not a pkgs command. To RUN a package: pkgx %s\n", pfx(), argv[0], argv[0])
	fmt.Fprint(os.Stderr, pkgsUsage)
	return 2
}
