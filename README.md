# pkgx

[![pkg.go.dev](https://img.shields.io/badge/pkg.go.dev-pkgx-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/go-pkgx/pkgx)
![Go](https://img.shields.io/badge/Go-1.27%2B-00ADD8?logo=go&logoColor=white)
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
curl -fsSL https://go-pkgx.github.io/install.sh | sh -s -- pkgx v0.1.5
```

**Windows** (PowerShell) — `irm | iex` passes no arguments, so the version goes
in the environment:

```powershell
$env:PKGX_TOOL='pkgx'; $env:PKGX_VERSION='v0.1.5'; irm https://go-pkgx.github.io/install.ps1 | iex
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

## Browsing the tree, and <TAB>

```console
$ pkgx catalog update
1907 project(s) → /home/you/.pkgx/catalog/linux-x86-64.json

$ pkgx ls
pkgx: catalogue /home/you/.pkgx/catalog/linux-x86-64.json, 2 hour(s) old
curl.se                                  8.17.0, 1 under
github.com                               41 under
gnu.org                                  30 under
zlib.net                                 1.3.2

$ pkgx ls gnu.org
gnu.org/bash                             5.3  ✓
gnu.org/gcc                              16.2.0, 1 under
…

$ pkgx ls doxygen.nl
doxygen.nl — no bottle here

$ pkgx ls curl.se
curl.se — 8.17.0
  curl.se/ca-certs
  nghttp2.org
  openssl.org
  zlib.net

also a namespace, 3 under it: pkgx ls curl.se/
```

### A node has two kinds of thing under it

`gnu.org` has `gnu.org/bash` under it because of how it is **named**.
`curl.se` has `openssl.org` under it because of what it **needs**. The same
words — "what is available under this node" — mean containment at a
namespace and dependency at a package, and `ls` answers whichever the node
is. A trailing slash asks for the namespace, which is the only way to reach
`curl.se/ca-certs` from `curl.se`.

A node can be **both**, and `curl.se` is: the listing shows its
dependencies and then says where the namespace half is.

```console
$ pkgx ls --tree curl.se
curl.se — 8.17.0
  curl.se/ca-certs
  nghttp2.org
  openssl.org
    curl.se/ca-certs  (shown above)
  zlib.net
```

`--depth N` bounds the descent. `guix graph --max-depth` exists for the
same reason: the full transitive graph of anything interesting is pages
long, and the first level is what a person reads. A diamond is named once
and expanded once — and the one expanded is the **direct** dependency,
because that is the one a reader came for.

**This is read from the catalogue, so it needs no network.** `pkgx --graph`
answers the same question by resolving against the registry, which is the
better answer when you have one and no answer at all in a scratch image
that has not fetched anything yet.

### The other direction

Everything above descends, which answers *what does this need* — the question
before **installing**. `--dependents` asks the one before **changing**:

```console
$ pkgx ls --dependents openssl.org
openssl.org — 4.0.2  ✓ 3.6.0
  agwa.name/git-crypt  (no bottle here)
  apache.org/arrow  25.0.1
  …

runtime dependents on darwin/aarch64; a build-only user is in no catalogue
```

That count is the blast radius of a bump — and it is **not one number**. On the
catalogues published 2026-10-06, out of 1908 projects each:

| platform | need openssl directly | transitively (`--tree`) |
| --- | ---: | ---: |
| darwin/aarch64 | 153 | 789 |
| linux/aarch64 | 164 | 854 |

A catalogue records what a platform really carries, so a project with no bottle
there has no edges there. That is why the platform is printed under the list
rather than left to be quoted bare.

`pkgx why` is the third question — not *what is in the closure* but **which
link put it there**:

```console
$ pkgx why git-scm.org zlib.net
git-scm.org — 2.55.0  ✓ 2.47.1
  → zlib.net — 1.3.2  ✓
```

A **shortest** path, as [`nix why-depends`][why] answers it: an explanation a
person can hold, not every route. No path is an answer too — it exits 1 and
says so, which is what tells you a bump cannot reach you.

[why]: https://nix.dev/manual/nix/2.35/command-ref/new-cli/nix3-why-depends

**What these are not.** The catalogue holds **runtime** dependencies for **one
platform**, so `--dependents` is a closure and not a rebuild set: a build
dependency is paid once by the factory and is in nobody's installed closure.
[`guix refresh --list-dependent`][gr] is the rebuild question, and its manual
says plainly that it only approximates it. And `why` reads the **declared**
graph; Nix scans built output for the hash parts of store paths, which there is
nothing to do before anything is installed.

[gr]: https://guix.gnu.org/manual/html_node/Invoking-guix-refresh.html

What it shows is what recipes **declare**. It is not the installed closure:
`bottle` also pulls providers by soname that no recipe names, so a real
install can hold more than this tree does.

### Everything is named; what is here is marked

```console
$ pkgx ls --tree curl.se
curl.se — 8.17.0
  curl.se/ca-certs  2026.09.25
  doxygen.nl  (no bottle here)
  openssl.org  3.6.0
```

A catalogue is published **per platform**, because what is available
differs by architecture — the s390x lane has a fraction of what
linux/x86-64 has. The choice here is to name everything and mark the rest,
rather than to hide it: dropping `doxygen.nl` would tell a reader it does
not exist, which is false; printing it bare tells them nothing. Marking it
tells them it exists, that there is no bottle for them, and therefore that
building it is the thing to do next.

In a **tree** that mark is usually the most useful line on the page: it
names the reason the thing above it cannot be installed, which would
otherwise print as a bare name among satisfied ones.

A catalogue built without the registry sweep (`bk catalog` with no
`--versions`) knows nothing about bottles, and says nothing about them —
every line would otherwise read "no bottle here", which is a statement
about the catalogue dressed up as a statement about the registry.

### And what you already have

```console
$ pkgx ls --tree curl.se
curl.se — 8.17.0  ✓
  curl.se/ca-certs  2026.09.25  ✓
  doxygen.nl  (no bottle here)
  openssl.org  3.6.0  ✓ 3.5.1
  zlib.net  1.3.2
```

A catalogue says what **exists**; a reader standing in front of it mostly
wants to know what they **have**. nix and guix show the state of the store
in everything they print; here nothing did, and `pkgx ls curl.se` printed
the same page before and after installing it.

A bare `✓` when what is installed is the version on offer, and
`✓ <version>` when it is **not** — the case worth the extra word, because
it is the one where a command would fetch something new. A repeat in the
tree is not marked twice: the node it points at already carries the mark,
and a second one reads as a second copy in the store.

### And what that has cost you

A `✓` is per project. `pkgx store` is the whole disk:

```console
$ pkgx store --by-size -n 6
~/.pkgx
llvm.org                                   13.8 GiB  3 versions, 8.5 GiB not the newest here
android.com/cmdline-tools                   6.4 GiB
swift.org                                   4.6 GiB
github.com/coqui-ai/TTS                     1.6 GiB
go.dev                                      1.5 GiB  7 versions, 1.3 GiB not the newest here
gohugo.io                                1020.5 MiB  13 versions, 934.6 MiB not the newest here
… 296 more

41.2 GiB over 363 version(s) of 302 project(s)
12.8 GiB is in versions that are not the newest present — which is not the same as unused
```

Every ephemeral run leaves its bottles behind and **nothing removes one**.
`pkgm list` answers a different question — what you asked to have installed
— so a bottle pulled six months ago to run one command is in neither. Those
figures are this developer's machine on 2026-10-08, and **thirteen versions
of gohugo.io** is not a number anybody chose.

**It reports and deletes nothing**, deliberately. nix, guix and spack all
keep listing and collecting apart — `nix path-info -S` against `nix store
gc`, `guix gc --list-live` against `guix gc`, `spack find` against `spack
gc` — and all three make the **roots** explicit first. There is no profile
here, so there is no root set: a version that looks superseded may be
exactly what a lock pins. Hence the wording of the last line.

It walks the tree, so it costs **seconds** rather than milliseconds — eight
over 41 GiB. That is why it is its own command and not a line in `pkgx
catalog`: nothing on the `<TAB>` path may cost that.

Sizes are powers of 1024 and say so with the `i`. `du` counts blocks and
this counts file sizes, so the two disagree twice over; naming the unit is
the least a report can do about it.

### And what any of it is for

A size is not a decision. `--root` says what a lock needs:

```console
$ pkgx store --root seed.lock.hcl
…
41.2 GiB over 363 version(s) of 302 project(s)

against 1 root(s): 12.3 MiB live over 7 version(s), 41.2 GiB in 356 version(s) no root needs
dead here means NOT REACHABLE FROM THE ROOTS YOU NAMED, and nothing is removed
```

[`guix gc`][gc] draws the line this copies: *"any file reachable from a root is
considered live and cannot be deleted; any other file is considered dead"* —
and what makes that safe is that the **roots are explicit**, with `--list-live`
reporting before `gc` removes.

[gc]: https://guix.gnu.org/manual/html_node/Invoking-guix-gc.html

**A lock is already a root set.** `pkgx --lock` pins every project in the
closure, not only what you typed, so membership in it *is* reachability — no
graph walk and no network. An **environment** would not do: it names
constraints, which have to be resolved against the registry, and this works
offline.

Three things worth knowing:

- the **version** counts, not just the project — llvm 16.0.6 is dead while
  22.1.8 is live;
- several `--root` flags **union**, because that is what *reachable from any of
  them* means;
- **without `--root` there is no live/dead line at all.** "Nothing is reachable
  from nothing" is true and would read as a verdict on your disk.

And a mistyped `--root` is refused **before** the store is walked — eight
seconds over 41 GiB is too long to wait to be told the arguments were wrong.

## Walking the tree

```console
$ pkgx browse
gnu.org — 54

> gnu.org/bash                             5.3  ✓
  gnu.org/gcc                              no bottle here
 +gnu.org/make                             4.4.1

↑ ↓ / k j move · → / l / enter descend · ← / h back · tab names ⇄ dependencies ⇄ dependents
· / search · space pin for the exit line · q quit, printing what is pinned
```

`pkgx ls` answers one question per command, which is right for a script and
wrong for a person who does not yet know what they are looking for: reaching
`curl.se/ca-certs` from `curl.se` costs two commands and knowing that the
trailing slash exists. [`nix-tree`](https://github.com/utdemir/nix-tree) is
the precedent — a whole-screen browser over a store, because the same data
read one node at a time is data nobody explores.

**Descending into a leaf shows what it needs**, rather than making you press
`tab`; that decision is the whole difference between a browser and a pair of
commands.

**`tab` cycles three views, not two**: what a node *contains*, what it *needs*,
and **who needs it**. The third is the question asked before changing
something, and in a browser it is the same gesture — stand on a node and look
the other way. The header says which way you are facing, and descending from a
dependents list keeps walking *up*: who needs the thing that needs it.

`q` prints what you pinned on **stdout** while the screen goes to stderr, so
`pkgx +$(pkgx browse)` composes instead of putting borders on a command line.

**It reads the cached catalogue and nothing else** — no network, no
resolution, no store walk per keystroke. And **without a terminal it prints
the tree and exits**: `pkgx browse | less`, a CI log and a scratch image are
the same case, and a command that refuses to work when its output is a pipe
is one people stop putting in scripts.

## You know the command, not the package

```console
$ pkgx search rg
crates.io/ripgrep                        rg  14.1.1
aardvark.io/rgbds                        rgbasm  0.9.0
```

`rg` is `crates.io/ripgrep`. No guess at the project name reaches it, and
`<TAB>` — which is a **prefix** on the project path — never will either.
Completion and search are different tools, and that is the line between
them.

`nix search`, `guix search` and `spack list -s` all match a package's
**description**, which is right for a collection that has descriptions.
This pantry does not: of its 1907 recipes, **1592** declare the commands
they provide and **six** carry a summary. A search over prose would find
six packages.

So the match is on names and on commands, ranked by **how** a thing
matched rather than by how often — a command named exactly what you typed
is what you meant, every time. Each line says why it is there and what you
would get, including `no bottle here` and `✓` where they apply. Offline,
from the same catalogue everything else reads.

A query shorter than three characters matches **commands only**. Two
characters is not a search, it is a sieve: `search rg` used to return 625
projects, 550 of them because `rg` sits inside `.org` — the result anybody
wanted was still first, and 550 lines of noise stood behind it.

Nothing found exits **non-zero**, so `pkgx search x || echo none` works. And
a catalogue that carries no command names says so rather than letting "no
such package" stand for "this catalogue cannot answer that kind of
question".

### Completion asks the binary

```sh
eval "$(pkgx completion bash)"     # or zsh, or: pkgx completion fish | source
```

```console
$ pkgx +gnu.org/ba<TAB>
+gnu.org/bash

$ pkgx gnu.o<TAB>
gnu.org/                                 # with the slash, so the next TAB descends
```

Spack generates `spack-completion.bash` from its command tree and commits it;
Guix hand-writes one per shell. Both then need a check that the file still
matches the program. **Nix took the other road** and this follows it: the
binary answers completion queries itself when `PKGX_GET_COMPLETIONS` names
the argument being completed, and the shell snippet is a few lines that call
it.

The reason to prefer it here is specific: what is being completed is not a
fixed command tree, it is the **registry** — which changes without pkgx
changing, and which a generated file could never be level with. It is also
the only shape that works in a `FROM scratch` image, where there is no
completion framework, no python, and no generator to run.

### Where the list comes from

`gnu.org/<TAB>` needs to know what exists, and **a registry cannot be asked**:

```
GET /v2/_catalog                             403   (ghcr issues no token)
GET /v2/go-pkgx/packages/zlib.net/tags/list  200   ["1.3.2", …]
```

Versions, yes; projects, no. So the registry carries a **catalogue**, which
is an ordinary bottle — signed, attested and cached like any other, fetched
in one pull, per platform because what is available differs by architecture.

### One command fetches it; nothing else does

`pkgx catalog update` is **the only** thing that asks the registry what
exists. `ls` and `<TAB>` read the file it wrote, and nothing on that path
opens a socket.

That line is not an optimisation, it is the difference between a completion
you leave switched on and one you turn off. A `<TAB>` is a fresh process:
there is no state between two presses, so a fetch on the read path is paid
again on every press, in full — an OCI token and ~200 KB of catalogue, each
time. `guix pull` and `nix-channel --update` put the line in the same place.

Measured, one press of `<TAB>` on `gnu.o`, median of five, with `PKGX_DIST`
pointed at a non-routable address so that a connect **hangs** rather than
failing instantly — a closed local port would have come back in
microseconds and looked exactly like not connecting at all:

| arm | median | answer |
| --- | --- | --- |
| before, registry unreachable | 7 326 ms | nothing |
| before, ghcr reachable | 295 ms | nothing |
| **after**, catalogue on disk | **7.6 ms** | `gnu.org/` |
| after, no catalogue yet | 5.2 ms | nothing, in silence |

The second row is the honest status quo and it is the worse news: 295 ms is
a *failed* pull — the token round-trip plus a 404 — because no catalogue is
published for darwin/aarch64. A successful one costs more, not less.

```sh
go run ./internal/catbench . <catalog.json> 5 97f08e3
```

re-measures it, building the `before` arm from that commit in a throwaway
worktree rather than quoting a number from a commit message. **The baseline
is named**, because `origin/main` moves: run it against `main` a week later
and both arms have the change, the baseline reports a handsome 5 ms, and
the comparison quietly means something else. `97f08e3` is the commit before
the catalogue became a local file.

The tool has its own tests, because a measuring tool nobody measures is how
a plausible wrong number gets published.

The price is that the index goes stale and nothing tells you by magic, so
every command that reads it says how old it is, and `pkgx catalog` says it
on its own:

```console
$ pkgx catalog
/home/you/.pkgx/catalog/linux-x86-64.json
1907 project(s), 905 with dependencies, 2 hour(s) old
```

On a machine that has never fetched one, that command and `pkgx ls` both
name the one that fixes it. A catalogue that is **there and will not parse**
gets a different sentence, naming the file: "run `pkgx catalog update`" is
no help at all for that, and sending somebody to a command that cannot work
is worse than saying nothing.

`PKGX_CATALOG=<file>` reads a catalogue from somewhere else entirely — an
air-gapped image that ships one beside the store, or one being inspected
before it is published. Set and unreadable is a **refusal**, not a quiet
fall back to the local store: somebody who names a file means that file.

When there is no catalogue at all, `pkgx ls` falls back to **what is
installed** and says so in its header. That is not a lesser answer offline
or in a fresh scratch image: it is the only true one available, and the two
are never merged, because "available" about a mix of a registry and a local
store is a word with no meaning.

## Running exactly what a lock pins

```console
$ pkgx --lock seed.lock.hcl -- make
pkgx: seed.lock.hcl, 231 pin(s), 3 hour(s) old
```

`bk lock` wrote locks and nothing acted on one. A lock nobody consumes is a
record, not a mechanism: cargo's `--locked` and npm's `ci` exist because the
file only means something once a command refuses to deviate from it.

The defect it answers is not hypothetical here. The **same** pantry commit,
`2df061bd`, resolved tcl to 9.0.4 and then to 9.1.0 four hours apart,
because a recipe's `versions:` asks GitHub at resolution time. Two builds
from one recipe set, two answers, and nothing in between to notice.

**Every pin becomes a root**, not just the lock's own `roots`. That is what
makes it a lock rather than a hint — a transitive dependency the resolver
would otherwise pick afresh is pinned too, and a set that cannot be
satisfied exactly **fails** instead of quietly materialising something near
it. The specs go through the same parser `+pkg@=1.2.3` does, so a locked run
and a free one cannot drift apart.

A lock taken on another platform is refused by name. The platform changes
the answer: measured on our own published catalogues, **537 of 1908**
projects available on `linux/x86-64` have no bottle for `darwin/aarch64`.

`--lock` cannot be mixed with `+pkg`: a lock **is** the set, and adding to
it would resolve one package freshly against pinned ones — a third thing
that is neither locked nor free.

**It is not an environment**, and `environments.go` still says so. An
environment names a *set* and resolves it afresh each time, which is right
for a shell you live in. A lock names *versions*. Two paths, side by side.

### And an environment can have one beside it

```console
$ pkgx env show cfd
env "cfd"
  description  the solver stack, as validated on 2026-09-01
  declared in  /site/environments/site.hcl2
  package      openmpi.org@5
  package      hdf5.org
  lock: /site/environments/cfd.lock.hcl — 231 pin(s), 3 hour(s) old, taken on linux/x86-64
```

Spack keeps `spack.yaml` and `spack.lock` as a **pair**, explicitly modelled
on Gemfile/Gemfile.lock: the manifest says what you want, the lock says what
that meant when it was concretised, and `spack install` inside the
environment installs the lock. There is no `--lock` flag anywhere in it,
because the lock belongs to the environment. Nix and Guix answer the same
problem from the other end — `flake.lock` pins the nixpkgs *input*,
`channels.scm` pins "all of Guix" — so neither has a resolved-version lock
to attach to anything.

So: `<name>.lock.hcl` beside the file that declared it, named for the
**environment** because one `.hcl2` can declare several. When it is there,
`pkge load cfd` uses its pins — *every* pin, including the ones the
manifest never named, which is the difference between a lock and a
restatement of the manifest.

**It reads; it does not write.** `bk lock` writes locks, because
concretising needs a pantry, an overrides set and a version resolver — none
of which belong in a runtime that ships to a login node. Spack splits it
the same way: `spack concretize` writes, `spack install` consumes. A writer
here could only produce versions with no spec hash, and a lock with empty
hashes is worse than none — `bk lock --check` would compare `""` against a
real hash and report every project as drifted.

A lock from **another platform** is ignored rather than refused: a site
commits one environment directory and loads it on every machine it runs. It
says so, because silently resolving afresh under a file called
`cfd.lock.hcl` is how somebody comes to believe a thing is pinned when it
is not.

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

### The loader is posed before the environment is composed

That order is load-bearing, and getting it wrong is how every ELF stopped
running on `FROM scratch`.

glibc's bottle puts `libc.so.6` in `lib/glibc-2.44/`; `lib/` itself holds
nothing. That versioned directory reaches `LD_LIBRARY_PATH` only when the
loader that will read it is **ours** — exporting it to a host loader pairs two
halves of different glibcs and fails on `__pointer_chk_guard`, which once took
the s390x lane down. The test for "is it ours" reads `/lib` and `/lib64`.

So composing the environment *before* posing the loader asks the question
before the answer exists. Measured 2026-10-09 in a container whose only file
was the pkgx binary:

```sh
$ pkgx stedolan.github.io/jq -n '1+1'
…/jq: error while loading shared libraries: libm.so.6: cannot open shared
object file: No such file or directory
```

Every ELF, not one package: `+gnu.org/coreutils -- env` died the same way on
`libc.so.6`, and `pkgx --lock` could not run a lock at all. Supplying the same
path from outside the container made the identical command print `2`, which is
what located it.

The quiet part: `pkgx +jq` **printed** a complete environment the whole time,
because the print path already posed the loader first. Reading the two outputs
side by side showed nothing wrong with either; only running the second one did.

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
