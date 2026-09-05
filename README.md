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

## Install

**Linux / macOS** — one line, naming the release you want:

```sh
curl -fsSL https://go-pkgx.github.io/install.sh | sh -s -- pkgx v0.1.4
```

**Windows** (PowerShell) — `irm | iex` passes no arguments, so the version goes
in the environment:

```powershell
$env:PKGX_TOOL='pkgx'; $env:PKGX_VERSION='v0.1.4'; irm https://go-pkgx.github.io/install.ps1 | iex
```

The installer downloads the static binary for your os/arch from that
[release](https://github.com/go-pkgx/pkgx/releases), verifies it against the
release `SHA256SUMS`, and drops `pkgx` on your `PATH` (`$HOME/.local/bin`, or
`%LOCALAPPDATA%\Programs\go-pkgx` on Windows; `PKGX_INSTALL` overrides the
directory on Unix).

The version is named on purpose: this line copied today and the same line
copied in six months install the same bytes, and a bad release does not reach
everyone who happens to install that hour. To track releases instead, say so —
`sh -s -- pkgx latest`, or `PKGX_VERSION=latest`. Re-running is the updater; it
skips the download when the target version is already installed.

**Go users**:

```sh
go install github.com/go-pkgx/pkgx@latest
```

## Usage

```
pkgx <pkg>[@version] [arg...]      run a package's program ephemerally
                                   e.g. pkgx node@22 --version
pkgx +<pkg> [+<pkg>...] [cmd ...]  bring packages into the environment and run
                                   cmd with them on PATH + LD_LIBRARY_PATH
                                   e.g. pkgx +git +gnu.org/bash -- ./build.sh
pkgx --json +<pkg>...              the composed environment as data
pkgx --modulefile +<pkg>...        the same, as an Lmod modulefile
pkgx env init [--module]           the pkge shell function (and module/ml)
pkgx env load|unload|purge         what that function evaluates
pkgx env avail | show <env>        the declared environments
pkgx env import <modulefile>...    convert Lmod/Environment Modules files
pkgx -h,--help   pkgx -v,--version
```

```console
# on a FROM scratch image whose only file is the pkgx binary:
$ pkgx node@22 --version
v22.x.x
$ pkgx +git +gnu.org/bash -- sh -c 'git --version'
git version 2.x.x
```

## Environments, and HPC

A module system does two things: it resolves what a package needs, and it edits
your shell. `pkgx +a +b` already did the first. `pkge` does the second.

```console
$ eval "$(pkgx env init)"          # in a profile
$ pkge load gnu.org/sed jq
$ pkge list
gnu.org/sed
stedolan.github.io/jq
$ pkge unload jq                   # /usr/bin/jq is back, exactly as it was
$ pkge save mine ; pkge purge ; pkge restore mine
```

Loading never edits incrementally: it restores the environment it first saw and
recomposes from the whole set. So `unload b` after `load a b c` leaves precisely
what `load a c` would have — independent of the order things were loaded in.

### Named environments

```hcl
# $PKGX_DIR/environments/site.hcl2
env "cfd" {
  description = "the solver stack, as validated on 2026-09-01"
  packages    = ["openmpi.org@5", "hdf5.org", "python.org@3.12"]
}
```

`pkge load cfd` — and `pkgx env avail` / `pkgx env show cfd` to see what a site
declares and from which file. An environment is not a lockfile: it names
constraints, and the closure is resolved at load time from the signed registry,
so a login node and a container agree.

### With an existing Lmod

Generate modulefiles and let the site's own Lmod load them. Nothing about
`module` changes, and conflicts, hierarchies and `spider` keep working because
Lmod is still the one doing the work.

```console
$ pkgx --modulefile +openmpi.org@5 > $MODULEPATH_DIR/openmpi/5.0.8.lua
```

### Without one

`pkgx env init --module` also defines `module` and `ml`, for a container, a
laptop or a cluster built on this toolchain. It **refuses** to install itself
where an Lmod already exists, and says to generate modulefiles instead: two
implementations answering one command is how a support ticket becomes
unanswerable.

`LOADEDMODULES` is maintained, because job scripts read it. `_LMFILES_` is not:
it names modulefiles, there are none, and an invented value is a lie a script
could act on.

### Converting a site's modulefiles

```console
$ pkgx env import /opt/modulefiles/openmpi.lua /opt/modulefiles/hdf5 \
    > $PKGX_DIR/environments/site.hcl2
```

Lmod's Lua and Environment Modules' TCL, into the same HCL2. It is a **parser,
not an evaluator**: an interpreter runs a modulefile and silently skips what it
does not implement, and the result is not an error but an environment that is
subtly wrong, found inside somebody's job. This refuses anything it would have
to guess at, names the line, and writes nothing at all if any file was refused.

```console
$ pkgx env import /opt/modulefiles/openmpi.lua /opt/modulefiles/hdf5
/opt/modulefiles/openmpi.lua: 3 statement(s) this converter will not guess at:
  line 3: not a plain call: this converter reads statements, it does not run Lua
    local root = "/opt/openmpi/5.0.8"
  line 4: argument is not a string literal (pathJoin(root, "bin")): its value depends on something we are not running
    prepend_path("PATH", pathJoin(root, "bin"))
  line 5: depends_on states a RELATIONSHIP between modules, not an environment change: decide it in the environment that replaces this one
    depends_on("hwloc")
pkgx: 1 of 2 modulefile(s) not converted — nothing written, because a PARTIAL conversion is the one outcome nobody can check
```

### Two things no code here removes

- **MPI and the interconnect come from the host.** libfabric/UCX, Slurm's PMI
  and a vendor libmpi tied to a kernel driver cannot live in a hermetic tree;
  bind-mount them, as Spack and Apptainer do.
- **Metadata storms.** Ten thousand ranks walking a shared `$PKGX_DIR` will melt
  a Lustre MDS. Materialise once into a squashfs or SIF and mount it read-only.

## Environment

- `PKGX_DIR` — bottle store (default `~/.pkgx`)
- `PKGX_DIST` — bottle source (default `oci://ghcr.io/go-pkgx/packages`, the signed
  registry; set `https://dist.pkgx.dev` for the full unsigned upstream pantry —
  pair with `PKGX_VERIFY=0`)
- `PKGX_VERIFY` — verify bottle signatures, fail-closed (default on; set
  `0`/`false`/`no`/`off` to disable)

By default `pkgx <pkg>` fetches from the signed registry and verifies each
bottle's signature before running it — no env needed.

### `~/.pkgx/config.hcl2`

Rather than exporting the `PKGX_*` (and OCI auth) variables every time, set
their defaults declaratively in `~/.pkgx/config.hcl2`. It is a small
[HCL2](https://github.com/hashicorp/hcl) file of top-level attributes; a real
environment variable always overrides a value set here:

```hcl2
# ~/.pkgx/config.hcl2 — defaults for the go-pkgx tools.
# A real environment variable always overrides a value set here.
PKGX_DIST   = "oci://ghcr.io/go-pkgx/packages"  # signed registry (default)
PKGX_VERIFY = true                               # fail-closed signature check
# PKGX_DIR    = "/opt/pkgx"
# PKGX_PANTRY = "https://raw.githubusercontent.com/pkgxdev/pantry/main/projects"
# OCI_TOKEN   = "..."                            # private-registry credentials
```

Values may be strings, booleans, or numbers. A missing file is ignored; a
malformed one is reported once on stderr and otherwise ignored (the tools fall
back to environment variables and built-in defaults).

## Design

Pure Go, cgo disabled. On `FROM scratch` it reads each bottle's ELF `DT_NEEDED`
to auto-complete the implicit libc/gcc closure the pantry graph omits, then
execs through the pkgx glibc loader so the program and its children resolve.
BSD-3-Clause.

### Where it is proven to work

CI builds ten targets — linux, darwin and windows on amd64 and arm64, plus
linux on riscv64, ppc64le, s390x and loong64 — and then **runs the suite** on
five of them under `qemu-user`:

```
test (arm64, qemu)  test (riscv64, qemu)  test (ppc64le, qemu)
test (s390x, qemu)  test (loong64, qemu)
```

Cross-compiling proves the code builds for an architecture. It says nothing
about whether it works there, and the bugs a compiler cannot see are the
interesting ones: a byte order read from the host rather than from the format,
an assumption about int width, a struct laid out differently. s390x is in that
list because it is big-endian and nothing else here is, and this program reads
formats it did not write — ELF headers, OCI manifests, tar and xz streams.

Windows gets its own lane rather than a build check: `windows-run` builds
`pkgx.exe` on a real Windows runner, fabricates a Windows bottle, serves it,
and asserts that `pkgx.exe` fetches it, execs the `.exe`, and propagates both
its output and its exit code.
