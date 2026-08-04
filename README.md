# pkgx

[![pkg.go.dev](https://img.shields.io/badge/pkg.go.dev-pkgx-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/go-pkgx/pkgx)
![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)

The **pkgx runtime** in pure Go — one static binary that runs any pkgx package
on the fly, and works on a literally-empty `FROM scratch` image.

`pkgx` is the *runtime* half of the pure-Go pkgx family; its sibling
[pkgm](https://github.com/go-pkgx/pkgm) is the *installer*. Both share one
backend — bottle resolution, download, `FROM scratch` closure completion, and
loader-aware exec — via the [`github.com/go-pkgx/bottle`](https://github.com/go-pkgx/bottle)
package, so there is a single source of truth and no duplication.

Like pkgm it is `CGO_ENABLED=0` with no runtime dependencies of its own: no
Deno, no curl, no shell — a single ~9 MB binary that materialises each package's
full dependency closure on demand and execs it.

## Usage

```
pkgx <pkg>[@version] [arg...]      run a package's program ephemerally
                                   e.g. pkgx node@22 --version
pkgx +<pkg> [+<pkg>...] [cmd ...]  bring packages into the environment and run
                                   cmd with them on PATH + LD_LIBRARY_PATH
                                   e.g. pkgx +git +gnu.org/bash -- ./build.sh
pkgx -h,--help   pkgx -v,--version
```

```console
# on a FROM scratch image whose only file is the pkgx binary:
$ pkgx node@22 --version
v22.x.x
$ pkgx +git +gnu.org/bash -- sh -c 'git --version'
git version 2.x.x
```

## Environment

- `PKGX_DIR` — bottle store (default `~/.pkgx`)
- `PKGX_DIST` — bottle source (default `oci://ghcr.io/go-pkgx/packages`, the signed
  registry; set `https://dist.pkgx.dev` for the full unsigned upstream pantry —
  pair with `PKGX_VERIFY=0`)
- `PKGX_VERIFY` — verify bottle signatures, fail-closed (default on; set
  `0`/`false`/`no`/`off` to disable)

By default `pkgx <pkg>` fetches from the signed registry and verifies each
bottle's signature before running it — no env needed.

## Design

Pure Go, cgo disabled — cross-compiles to six 64-bit targets (linux & darwin,
amd64/arm64, plus riscv64 & ppc64le). On `FROM scratch` it reads each bottle's
ELF `DT_NEEDED` to auto-complete the implicit libc/gcc closure the pantry graph
omits, then execs through the pkgx glibc loader so the program and its children
resolve. BSD-3-Clause.
