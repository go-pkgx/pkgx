// Command pkgx runs a package's program without installing it.
//
// ⛔ THE WHOLE PROGRAM MOVED TO internal/app AND THIS FILE IS WHAT IS LEFT.
// Two binaries ship from this module — `pkgx`, which runs things, and `pkgs`
// (./cmd/pkgs), which asks about them — and they share one resolver, one
// catalogue reader and one store walker. A second repository would have
// forked all three, and a fork of the resolver is a fork of what a version
// means.
//
// The module path is unchanged, so `go install github.com/go-pkgx/pkgx@latest`
// still produces this binary and nothing a user types moves.
package main

import (
	"os"

	"github.com/go-pkgx/pkgx/internal/app"
)

func main() { os.Exit(app.Pkgx(os.Args[1:])) }
