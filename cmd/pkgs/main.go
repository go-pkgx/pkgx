// Command pkgs answers about packages: what the registry has, what this
// disk holds, what needs what, and where a catalogue comes from.
//
// It is the other half of `pkgx`, which RUNS packages and keeps upstream
// pkgx's grammar — the first argument that is not a flag is a package. The
// nine verbs this binary owns used to sit in that position, and two of them
// shadowed commands `gnu.org/coreutils` provides: `pkgx ls` listed a
// catalogue where upstream runs GNU ls, and there was no way to ask for the
// other one.
//
// Both binaries are built from internal/app, because they share the
// resolver, the catalogue reader and the store walker. See internal/app/
// pkgs.go for why a second binary rather than a reserved word.
package main

import (
	"os"

	"github.com/go-pkgx/pkgx/internal/app"
)

func main() { os.Exit(app.Pkgs(os.Args[1:])) }
