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

## ⛔ Two commands: `pkgx` runs, `pkgs` asks

This repository builds **two** binaries, and the split is about grammar
rather than taste.

Upstream pkgx has no verbs at all: the first argument that is not a flag is a
**package**, and pkgx runs it. This fork had added nine verbs to that
position — `ls`, `search`, `why`, `store`, `browse`, `catalog`, `env`,
`compat`, `completion` — and each one took a name away from the registry. Two
took names that exist: measured against the published linux/x86-64 catalogue
on 2026-10-18, **`gnu.org/coreutils` provides both `ls` and `env`**. So `pkgx
ls` listed a catalogue where upstream runs GNU ls, and there was no way to
ask for the other one.

```console
$ pkgx ls --version          # GNU coreutils, as upstream does
ls (GNU coreutils) 9.11
$ pkgs ls gnu.org            # what is available under a node
$ pkgx -Q ls                 # upstream's query mode, which this fork lacked
gnu.org/coreutils
```

**A reserved word could not fix it.** The obvious repair is one word to hang
the verbs off — `pkgx pkg ls`. It fails the same way: `vercel.com/pkg`
provides the command `pkg`, and any bare word can become a package tomorrow,
because the whole point of the first position is that the registry decides
what is in it. Only a token the grammar already reserves is safe: a flag
(upstream's own choice for `-Q`) or a different binary.

`pkgs` reads as a subject followed by a verb — `pkgs search json`, `pkgs why
curl.se openssl.org`. Nothing in either published catalogue provides a
command by that name, while `pkgctl` is a real binary in Arch's devtools and
`pkgtool` one in Slackware.

Both are built from `internal/app`: they share the resolver, the catalogue
reader and the store walker, because a fork of the resolver is a fork of what
a version *means*.

### ⛔ If you had `pkge` in your profile, re-run its init

```sh
eval "$(pkgs env init)"
```

The generated `pkge` function calls `pkgx env load`, and `env` is the one
moved verb that a package also provides — `gnu.org/coreutils`. So an old
profile does not get a pointer, it gets GNU env:

```console
$ pkgx env load foo
env: ‘load’: No such file or directory      # exit 127
```

That is pkgx obeying the grammar, and it names neither binary. `pkgx env`
followed by a `pkge` subcommand therefore prints a transition note and
refuses, instead of running coreutils — one word wide, and only for those
seven words, so `pkgx env FOO=1 printenv` still does what it always did.

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

⛔ **`SHA256SUMS` is a checksum of itself.** It says the binaries match a
manifest published beside them, and whoever replaced the binaries would
replace the manifest. The half that cannot be forged from the release page is
GitHub's **provenance attestation** — signed through Sigstore, saying that
this repository's release workflow built that file from that commit:

```sh
gh attestation verify -R go-pkgx/pkgx pkgx-linux-amd64
```

Every file of a release from **v0.23.2** onward carries one; releases before
it carry none, and `gh attestation verify` answers `HTTP 404` for those rather
than failing closed, so a 404 on an older binary means *unattested*, not
*tampered with*.

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
pkgx --lock <file> [cmd ...]       exactly the versions a bk lock pins
pkgx --graph [--platform os/arch] +<pkg>...
                                   the resolved graph, for any platform
pkgx -Q [command]                  what can be run, and from which project
pkgx -h,--help   pkgx -v,--version

pkgs ls [--tree] [node]            what is available under a node
pkgs ls --dependents <pkg>         who NEEDS this
pkgs why <project> <dependency>    the shortest path between them
pkgs search [-n N] <text>          by name, or by a command it provides
pkgs browse [node]                 walk the tree full-screen
pkgs catalog [update|reindex]      the catalogue; update takes --platform
pkgs store [--by-size] [--root …]  what this disk holds, and what it is for
pkgs compat +<pin>... -- <pkg>...  how much of a set resolves under a base
pkgs env init [--module]           the pkge shell function (and module/ml)
pkgs env load|unload|purge         what that function evaluates
pkgs env avail | show <env>        the declared environments
pkgs env import <modulefile>...    convert Lmod/Environment Modules files
pkgs completion bash|zsh|fish      <TAB> completion, for BOTH commands
pkgs -h,--help   pkgs -v,--version
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
$ pkgs catalog update
1907 project(s) → /home/you/.pkgx/catalog/linux-x86-64.json

$ pkgs ls
pkgx: catalogue /home/you/.pkgx/catalog/linux-x86-64.json, 2 hour(s) old
curl.se                                  8.17.0, 1 under
github.com                               41 under
gnu.org                                  30 under
zlib.net                                 1.3.2

$ pkgs ls gnu.org
gnu.org/bash                             5.3  ✓
gnu.org/gcc                              16.2.0, 1 under
…

$ pkgs ls doxygen.nl
doxygen.nl — no bottle here

$ pkgs ls curl.se
curl.se — 8.17.0
  curl.se/ca-certs
  nghttp2.org
  openssl.org
  zlib.net

also a namespace, 3 under it: pkgs ls curl.se/
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
$ pkgs ls --tree curl.se
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

### ⛔ `--graph --platform`, because the answer is not the same everywhere

`--graph` used to resolve for the host and only the host. `bottle.GraphFor`
always took an os and an arch; one line in `printGraph` pinned them — the
same defect, in the same shape, that `catalog update` had before #106.

It is not cosmetic. A recipe asking for `go.dev ^1.27.1` resolves here and
does not resolve on linux at all, and nothing said so:

```console
$ pkgx --graph +go.dev@^1.27.1
go.dev 1.27.1
└─ openssl.org 3.6.4
   └─ curl.se/ca-certs 2026.09.25

$ pkgx --graph --platform linux/x86-64 +go.dev@^1.27.1
pkgx: no version of go.dev satisfies "^1.27.1" AND is published for
linux/x86-64 (available: 1.27.1 1.26.8 1.26.5 … (11 in all);
satisfy but not published here: 1.27.1); asked for by ^1.27.1 (requested)
```

**"Satisfy but not published here"** is the whole answer a recipe author
needs, and before this the only way to get it was to own the machine.
Measured 2026-10-18 while checking whether go-pkgx/packages#323 could land:
it could not, and the host's confident `1.27.1` said the opposite.

The flag is **refused** on `--json`, `--modulefile` and the default shell
output rather than ignored there. Those compose a real environment on this
machine — real paths on `PATH`, a real loader — so a foreign platform cannot
change what they print, and a flag that is silently without effect is worse
than one that does not exist.

⛔ `PKGX_CATALOG` does **not** redirect `--graph`, and that is why the flag
was needed rather than the variable: `ls`, `search` and `why` read that file,
while `--graph` resolves against the registry and never opens it.

It used to say so by saying nothing. Pointing `PKGX_CATALOG` at another
platform's catalogue and asking `--graph` returned the **host's** answer with
exit 0, which is how this was found — and nearly how go-pkgx/packages#323 was
read as unblocked when it is not. It now refuses:

```console
$ PKGX_CATALOG=/nope/absent.json pkgx --graph +go.dev
pkgx: PKGX_CATALOG names /nope/absent.json, and --graph does not read a
catalogue — it resolves against the registry. To ask about another platform:
--graph --platform os/arch
```

A refusal rather than a warning, because a warning on stderr is missed in
exactly the place this matters — a script reading the tree. And a refusal
rather than resolving *from* the catalogue, because the two answer different
questions: a catalogue records what is **published**, while this resolves
what **would satisfy** a constraint, including versions no bottle carries.
The error message above depends on being able to tell those apart.

### The other direction

Everything above descends, which answers *what does this need* — the question
before **installing**. `--dependents` asks the one before **changing**:

```console
$ pkgs ls --dependents openssl.org
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

`pkgs why` is the third question — not *what is in the closure* but **which
link put it there**:

```console
$ pkgs why git-scm.org zlib.net
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
$ pkgs ls --tree curl.se
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
$ pkgs ls --tree curl.se
curl.se — 8.17.0  ✓
  curl.se/ca-certs  2026.09.25  ✓
  doxygen.nl  (no bottle here)
  openssl.org  3.6.0  ✓ 3.5.1
  zlib.net  1.3.2
```

A catalogue says what **exists**; a reader standing in front of it mostly
wants to know what they **have**. nix and guix show the state of the store
in everything they print; here nothing did, and `pkgs ls curl.se` printed
the same page before and after installing it.

A bare `✓` when what is installed is the version on offer, and
`✓ <version>` when it is **not** — the case worth the extra word, because
it is the one where a command would fetch something new. A repeat in the
tree is not marked twice: the node it points at already carries the mark,
and a second one reads as a second copy in the store.

### And what that has cost you

A `✓` is per project. `pkgs store` is the whole disk:

```console
$ pkgs store --by-size -n 6
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
$ pkgs store --root seed.lock.hcl
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
$ pkgs browse
gnu.org — 54

> gnu.org/bash                             5.3  ✓
  gnu.org/gcc                              no bottle here
 +gnu.org/make                             4.4.1

↑ ↓ / k j move · → / l / enter descend · ← / h back · tab names ⇄ dependencies ⇄ dependents
· / search · space pin for the exit line · q quit, printing what is pinned
```

`pkgs ls` answers one question per command, which is right for a script and
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
`pkgx +$(pkgs browse)` composes instead of putting borders on a command line.

**It reads the cached catalogue and nothing else** — no network, no
resolution, no store walk per keystroke. And **without a terminal it prints
the tree and exits**: `pkgs browse | less`, a CI log and a scratch image are
the same case, and a command that refuses to work when its output is a pipe
is one people stop putting in scripts.

## When it is not here at all

```console
$ pkgs request opendev.org/openstack/python-openstackclient
project   opendev.org/openstack/python-openstackclient
platform  darwin/aarch64
pkgs      0.24.0
state     never published in this registry, for any platform

Nothing was sent. `pkgs request --url …` prints a link that opens this as an issue.
```

The line that prompted it, from a user on 2026-10-22:

```console
$ pkgx openstack user list --domain stratuslab
pkgx: opendev.org/openstack/python-openstackclient is not published in
      ghcr.io/go-pkgx/packages/… : response status code 403: denied
```

Two problems in one line. The **403 is ghcr saying "no such repository"** and
reads as a permissions fault; and the person now has to work out by hand
whether the package is missing everywhere or only for their platform, and
then where to ask. `request` answers both from the registry — *never
published*, *published for other platforms but not yours*, or *already here*,
which it **refuses** to file, since an issue with a false premise spends a
maintainer's trust.

That third answer is also how you check a rollout, because it says **which
version** and **where else**:

```console
$ pkgs request opendev.org/openstack/python-openstackclient
project   opendev.org/openstack/python-openstackclient
platform  darwin/aarch64
pkgs      0.27.0
state     published for darwin/aarch64 at 10.5.0
          also linux/aarch64 linux/x86-64

pkgs: nothing to request: opendev.org/openstack/python-openstackclient 10.5.0 is published for darwin/aarch64
```

⛔ It used to say only `published for this platform` — true, and silent
about the one thing being asked. Measured on myself the day after shipping
it: having rebuilt `vitejs.dev` to close six advisories, I ran this to see
whether the remedy had landed, got that line, and went to the registry by
hand for a fact the command already held. A rollout happens per platform,
so *here, and also there* is the shape of the answer.

⛔ **And look at what else that line contains: `--domain stratuslab`.** A
reporter that attached "the command that failed" would publish an
organisation's infrastructure names into a public tracker, from a machine
whose owner was debugging rather than publishing. So the payload is a fixed
list of fields — project, platform, tool version, state — assembled in one
function that **does not import `os`**, so it cannot reach argv or the
environment even by accident. A test asserts the report and the prefilled
issue body carry no argv, no environment and no paths, and the mutation that
adds the store path to the report is caught.

It sends nothing. `--url` prints a link you read first; there is no flag that
posts, and nothing to configure or forget.

⛔ Its first three live runs each found a defect in **itself**, and all three
had one shape: a rule `bottle` already owned, tested, and did not export, so
this caller wrote a worse one. It counted `sha256-…` attestation tags as
versions (`IsVersionTag`, bottle v0.50.0); and it reprinted ghcr's raw 403
instead of reporting an absence (`IsNotPublished`, bottle v0.51.0 — which
also stopped that predicate panicking on `nil`, a trap only exporting
creates). Chasing the first led to a third: `tagSpellings` offered `26.8.0`
as a spelling of `26.8.1`, which is why the linux/x86-64 catalogue
advertised a `nodejs.org` nobody could install.

### The dead end now names its exit

The command above only helps somebody who already knows it exists. The person
who met the 403 did not, so the failure itself says where to go — and only
that failure:

```console
$ pkgx openstack user list --domain stratuslab
pkgx: opendev.org/openstack/python-openstackclient is not published in …
pkgx: if it should be here: `pkgs request opendev.org/openstack/python-openstackclient`
      prints a report you can open as an issue (it sends nothing)
```

⛔ **It matches a type, not a sentence.** The first version asked
`bottle.IsNotPublished`, whose last resort reads the error's *text*, and its
first live run fired on

```console
pkgx: command not found in the requested packages: true
```

where the package was published and the **command** was missing. A hint that
turns up on unrelated failures teaches people to ignore hints, so the trigger
is a type, and it carries the project: the line names what to ask for instead
of printing a placeholder.

⛔ **And it asks one question, because the first version answered half of
one.** `*bottle.NotPublishedError` means the registry has no such repository
— a brand-new package, and the **rare** case. The ordinary one is a recipe
with no bottle for *your* platform: measured against the published
`darwin/aarch64` catalogue on 2026-10-23, **567 of 1940 projects**, against
which `v0.25.0` shipped a hint that could not fire:

```console
$ pkgx +aerc-mail.org -- echo ok
pkgx: no version of aerc-mail.org is published for darwin/aarch64 (available: 0.22.0; not published here: 0.22.0)
```

Both states are types now, and `bottle.ProjectToRequest` answers both in one
call — two predicates at this call site is exactly how the second came to be
forgotten. `pkgs ls` can name the 567 for a platform
(`go run ./internal/catstat -missing <catalogue.json>`), which is how that
example was chosen: from a list the catalogue derived, not from one already
believed in.

A build that failed, a signature that did not verify and a registry having a
bad day get no hint — none of them is something to ask a maintainer to
publish. A bare registry 404 gets none either: it *is* an absence, but it
names no project, and `pkgs request <project>` is not an instruction. The cost
of that choice is a missed hint, never a wrong one.

## You know the command, not the package

```console
$ pkgs search rg
crates.io/ripgrep                        rg  14.1.1
aardvark.io/rgbds                        rgbasm  0.9.0
```

`rg` is `crates.io/ripgrep`. No guess at the project name reaches it, and
`<TAB>` — which is a **prefix** on the project path — never will either.
Completion and search are different tools, and that is the line between
them.

`nix search`, `guix search` and `spack list -s` all match a package's
**description**, which is right for a collection that has descriptions.
This pantry does not. Measured with `go run ./internal/catstat` over the
published darwin/aarch64 catalogue of **2026-10-12**: of **1915** projects,
**1588** (82.9%) declare the commands they provide — 4511 command names —
and **six** carry a summary. A search over prose would find six packages.

The figures carry their date and their instrument because they move. An
earlier revision of this paragraph said "1907 recipes, 1592 declare" with
neither, and by the time anyone compared, both numbers had changed and
nothing said whether the difference was drift or a defect.

⛔ **And the search says so, even when it finds plenty.** Measured
2026-10-20, `pkgs search json` returns **thirteen** projects and none of
them is `stedolan.github.io/jq` — jq is in that catalogue, with the command
`jq` and a bottle for this platform, and the word "json" is in neither its
path nor its commands. Thirteen answers read as a *complete* answer, so a
reader concludes jq is not packaged. A search that finds nothing already
makes a reader suspicious; one that finds plenty does not. So every search
over a catalogue where fewer than one project in five carries a description
ends with one line on **stderr**:

```console
$ pkgs search json
github.com/zaach/jsonlint                jsonlint  1.6.0
…
pkgs: only 6 of 1938 projects here carry a description, so a word that is in
neither a project's name nor a command it provides cannot reach it
```

On stderr, so `pkgs search x > list` still writes results and nothing else;
and only below that floor, so a catalogue that one day carries descriptions
goes quiet instead of nagging.

The six are not ours to invent. Re-measured the same day over the upstream
corpus, parsed as YAML rather than grepped — `description:` also appears
inside build scripts and URLs there — **1928 recipes read, 259 with a
`display-name`, 6 with a `summary` or `description`**, and the catalogue
those recipes produce carries exactly those six. Nothing is lost between the
recipe and the catalogue: the prose was never written.

### And the command runs, not just searches

```console
$ pkgx rg --version
ripgrep 15.2.0

$ pkgx jq --version
jq-1.8.2
```

`rg` resolved to `crates.io/ripgrep` and `jq` to `stedolan.github.io/jq`,
from an empty store, offline but for the bottles. A constraint rides along —
`pkgx node@22 --version` is `nodejs.org` at 22 — and `+rg` works the same
way, because the two syntaxes are one request.

A bare word is a **command**; a project is always a domain or a
domain/path, so the two cannot be confused — 0 of those 1915 project names
lack both a dot and a slash, and `catstat` prints that count so the day it
stops being true is noticed.

This was reported from the field, and it is why the paragraph above is not
the whole story: the mapping lived in the catalogue and only `search` read
it, so `pkgx hugo version` answered *"no recipe for hugo in …pantry…"* —
which reads as "go-pkgx does not know hugo" rather than "look under
`provides`". Every `pkgx <tool>` habit, Taskfile and doc had to be
rewritten with project names to work at all.

Three things it will not do:

- **Guess between two providers.** `provides` says what a project puts on
  PATH, not who owns the name. Two candidates is an error naming both;
  picking one would run the wrong program under the right name.
- **Blame the pantry when there is no catalogue.** A bare word with no
  catalogue says `run: pkgs catalog update`, because a bare word can never
  be a project and a message about a missing *recipe* would be about the
  wrong thing.
- **Read the catalogue for a project name.** `pkgx +deps` runs inside every
  factory build, in a loop; parsing 467 KB to notice that `gnu.org/bash`
  contains a slash would be paid on every call for nothing.

So the match is on names and on commands, ranked by **how** a thing
matched rather than by how often — a command named exactly what you typed
is what you meant, every time. Each line says why it is there and what you
would get, including `no bottle here` and `✓` where they apply. Offline,
from the same catalogue everything else reads.

A query shorter than three characters matches **commands only**. Two
characters is not a search, it is a sieve: `search rg` used to return 625
projects, 550 of them because `rg` sits inside `.org` — the result anybody
wanted was still first, and 550 lines of noise stood behind it.

Nothing found exits **non-zero**, so `pkgs search x || echo none` works. And
a catalogue that carries no command names says so rather than letting "no
such package" stand for "this catalogue cannot answer that kind of
question".

### Completion asks the binary

```sh
eval "$(pkgs completion bash)"     # or zsh, or: pkgs completion fish | source
```

```console
$ pkgx +gnu.org/ba<TAB>
+gnu.org/bash

$ pkgx gnu.o<TAB>
gnu.org/                                 # with the slash, so the next TAB descends
```

Three projects, three answers — each read from its own source on 2026-10-11
rather than from memory:

| | |
|---|---|
| **Spack** | generates and commits it. `share/spack/spack-completion.bash` says so in its own header — *"auto-generated by: `spack commands --format=bash --update=spack-completion.bash`. Please do not manually modify this file"* — and keeps it level with a test, `test_updated_completion_scripts`, docstring *"Make sure our shell tab completion scripts remain up-to-date."* |
| **Guix** | hand-writes one per shell: `etc/completion/{bash/guix,zsh/_guix,fish/guix.fish}` — 9 KB, 33 KB, 78 KB, each with its own copyright holders and **no generator marker anywhere in any of them**. So there is nothing to compare them against. |
| **Nix** | has the binary answer: `src/libutil/args.cc` reads `NIX_GET_COMPLETIONS` as the **index** of the argument being completed, and the shell function is a few lines that call it. |

An earlier version of this paragraph said *"both then need a check that the
file still matches the program"*. That is true of Spack, which has exactly
such a test, and **wrong about Guix** — a hand-written completion has no
generator to be checked against, so nothing can tell you it has drifted. The
corrected form is the sharper point.

**Nix's is the road this follows**: the
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

### `--platform`, for a question about a machine you do not have

```console
$ pkgs catalog update --platform linux/x86-64
1915 project(s) → …/catalog/linux-x86-64.json
```

It writes to **that platform's** path, so a foreign catalogue cannot be
mistaken for the host's: `pkgs ls` reads `catalog/<host>.json` and will not
see it.

`fetchCatalogFor` always took an os and an arch; only this command pinned
them to the host, and that one line made two measurements impossible from a
darwin machine — whether a darwin/x86-64 bottle starts, and what
`bk vulnfetch -catalog` would report for linux before a CI job is wired to
run it. Both had been written down twice as *"unmeasured, needs another
machine"*, which is a note nobody acts on.

With it, from the same laptop:

```console
$ bk vulnfetch -dir pantry -catalog catalog/linux-x86-64.json
133 of them asked AT the version the catalogue publishes; the rest as a whole package
448 identity(ies) NOT asked: … publishes no version of them for this platform
133 identity(ies) asked, 1090 not answerable by OSV (repository- or CPE-keyed)
0 finding(s) across 0 project(s)
```

### And which platforms are there at all?

```console
$ pkgs catalog platforms
darwin/aarch64
darwin/x86-64
linux/aarch64
linux/s390x
linux/x86-64
windows/x86-64
```

⛔ **This exists because a list copied into a file froze while the registry
grew.** `--platform` could be *guessed at* one value at a time — a platform
that is not published is refused with `platform not in index`, so the index
plainly knows — and nothing could ask it for the set. Measured 2026-10-20:
`go-pkgx/packages`' daily CVE lane carried its platform list in the
workflow, the list said **two**, and the registry publishes **six**. The
four nobody asked about were clean, so the cost was coverage rather than an
exposure; the mechanism guaranteed the seventh would be missed too.

The output is an **input**: one platform per line, nothing else, ready to
hand straight back to `--platform` from a loop. ⛔ And a failure is **not an
empty list** — a loop that reads no lines and sees exit 0 scans nothing and
reports a clean fleet, which is the same sentence as "every platform is
clean" and the opposite fact. An unreadable index, a tag that is a single
manifest, and an index in which nothing names a platform are each an error
with a reason on stderr.

Like `update`, it asks the registry: no local file knows what *exists*, and
this is the only other command in `pkgs` that cannot work offline.

### One command fetches it; nothing else does

`pkgs catalog update` is **the only** thing on the read path that asks the
registry what exists — `catalog platforms` above asks it too, and is the
one other command that does, which is why both live under this verb. `ls`,
`search`, `why`, `browse` and `<TAB>` read the file `update` wrote, and
nothing on that path opens a socket.

That line is not an optimisation, it is the difference between a completion
you leave switched on and one you turn off. A `<TAB>` is a fresh process:
there is no state between two presses, so a fetch on the read path is paid
again on every press, in full — an OCI token and the whole catalogue (470 KB in October 2026), each
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

### And it reads an index, not the catalogue

Moving the fetch off the read path left one cost behind: every `<TAB>` decoded
the whole catalogue. Measured on the real `linux-aarch64` catalogue —
**481 803 bytes, 1908 projects** (the figure this README used to give as
"~200 KB", which the catalogue outgrew when `bk catalog --versions` started
sweeping):

| what a press did | median |
| --- | --- |
| `ReadFile` + `UnmarshalCatalog` | 6.3–9.8 ms |
| `ReadFile` of the JSON alone | 0.31 ms |
| `json.Decoder`, decoding two fields | 5.1 ms |
| **the name index** (38 KB), read + scan | **0.08 ms** |

So the **decode** was all of it, and streaming only the two fields completion
needs is merely twice as fast — not enough to matter. `pkgs catalog update`
therefore also writes `catalog/<os>-<arch>.names`: one line per project, the
name and the newest version, which is everything `<TAB>` shows. `ls`,
`--tree`, `search` and `browse` keep reading the catalogue — they need
dependencies and summaries, and none of them runs on a keystroke.

**A derived file that can disagree with its source is a defect waiting for a
reader**, so three things hold it honest: only `catalog update` and
`catalog reindex` write it; its header records the catalogue's byte length and
project count, both checked on read and both free; and a failed check is not an
error — it falls back to the catalogue, which is the authority. The index can
only make completion faster or make it do exactly what it did before.

`pkgs catalog reindex` rebuilds it from the catalogue already on disk, with no
network — for an image that **ships** a catalogue, which had no way to get an
index and so took the slow path in the one environment that can least afford
it.

Measured in a `FROM scratch` container (one layer, 8.5 MB, nothing but the
binary) with the network **off**, 300 runs per arm, all three back to back
because a figure taken under a different load is not comparable — the same arm
read 25.25 ms at load 89 and 3.78 ms at load 18:

| arm | min | median | above the floor |
| --- | --- | --- | --- |
| the floor: `pkgx --version`, no catalogue read | 2.71 ms | 3.05 ms | — |
| **completion, name index** | 3.07 ms | **3.78 ms** | **+0.73 ms** |
| completion, decoding the catalogue | 5.97 ms | 6.50 ms | +3.45 ms |

The catalogue read went from 3.45 ms to 0.73 ms above the cost of starting the
process at all — which is now the dominant term, and is the Go runtime, not
this program.

re-measures it, building the `before` arm from that commit in a throwaway
worktree rather than quoting a number from a commit message. **The baseline
is named**, because `origin/main` moves: run it against `main` a week later
and both arms have the change, the baseline reports a handsome 5 ms, and
the comparison quietly means something else. `97f08e3` is the commit before
the catalogue became a local file.

The tool has its own tests, because a measuring tool nobody measures is how
a plausible wrong number gets published.

### ⛔ And nothing was keeping it true

That measurement was taken by hand, once. `catbench` times the arms; it
knows nothing about containers, and **no test and no lane had ever built a
`FROM scratch` image or cut the network**. The first `net/http` on a
completion path, or a lookup that falls back to the registry when a file is
missing, passes every unit test in this repository and breaks the one
property the catalogue, the name index and the browser were all built for.

`go test ./scratch/` (with `PKGX_SCRATCH_TEST=1`, and in CI as *FROM
scratch, offline*) builds the image — the two binaries, a real catalogue,
nothing else: no shell, no libc, no CA bundle, no `/etc/resolv.conf` — and
runs `ls`, `ls <namespace>`, `ls --tree`, `search`, `why`, `catalog`, the
browser's no-TTY degradation and a tab completion in it with
`--network none`.

⛔ **Its planted positive is the point.** "Answered offline" and "I forgot
to cut the network" are the same sentence, and `--network none` is one word
away from being absent — so `pkgs catalog update`, the one command that
must reach the registry, runs in the same configuration and has to **fail,
about the network**:

```
control ok: catalog update refused as it must — pkgs: catalog update:
resolve go-pkgx.dev/catalog v1: … dial udp 192.168.65.7:53: connect:
network is unreachable
```

A binary that crashed for its own reasons would also be non-zero and would
prove nothing, so the failure is matched against the ways this stack says
it could not reach the network, and the whole test stops there if the
control passes. Checked by mutation: dropping `--network none` fails the
run at that line.

Nothing in it is a timing gate — `docker run` on this image costs ~170 ms
before the binary's first instruction, ten times what a completion is
allowed, so a stopwatch there measures the runner and not pkgx. (A first
draft of this check did exactly that and reported a completion of 188 ms
against a 20 ms budget.) The timings are logged for a reader; what is
asserted is that the answers arrive with no network to get them from. The
numbers above remain the measurement.

The price is that the index goes stale and nothing tells you by magic, so
every command that reads it says how old it is, and `pkgs catalog` says it
on its own:

```console
$ pkgs catalog
/home/you/.pkgx/catalog/linux-x86-64.json
1907 project(s), 905 with dependencies, 2 hour(s) old
```

On a machine that has never fetched one, that command and `pkgs ls` both
name the one that fixes it. A catalogue that is **there and will not parse**
gets a different sentence, naming the file: "run `pkgs catalog update`" is
no help at all for that, and sending somebody to a command that cannot work
is worse than saying nothing.

`PKGX_CATALOG=<file>` reads a catalogue from somewhere else entirely — an
air-gapped image that ships one beside the store, or one being inspected
before it is published. Set and unreadable is a **refusal**, not a quiet
fall back to the local store: somebody who names a file means that file.

When there is no catalogue at all, `pkgs ls` falls back to **what is
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
$ pkgs env show cfd
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
$ eval "$(pkgs env init)"          # in a profile
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

`pkge load cfd` — and `pkgs env avail` / `pkgs env show cfd` to see what a site
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

`pkgs env init --module` also defines `module` and `ml`, for a container, a
laptop or a cluster built on this toolchain. It **refuses** to install itself
where an Lmod already exists, and says to generate modulefiles instead: two
implementations answering one command is how a support ticket becomes
unanswerable.

`LOADEDMODULES` is maintained, because job scripts read it. `_LMFILES_` is not:
it names modulefiles, there are none, and an invented value is a lie a script
could act on.

### Converting a site's modulefiles

```console
$ pkgs env import /opt/modulefiles/openmpi.lua /opt/modulefiles/hdf5 \
    > $PKGX_DIR/environments/site.hcl2
```

Lmod's Lua and Environment Modules' TCL, into the same HCL2. It is a **parser,
not an evaluator**: an interpreter runs a modulefile and silently skips what it
does not implement, and the result is not an error but an environment that is
subtly wrong, found inside somebody's job. This refuses anything it would have
to guess at, names the line, and writes nothing at all if any file was refused.

```console
$ pkgs env import /opt/modulefiles/openmpi.lua /opt/modulefiles/hdf5
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

### The audit that found a hole in it

The index is a **second reader of the same data**, and the first version of it
skipped a check the catalogue parser applies. Demonstrated rather than
theorised: an index naming `../outside` completed to

```
../outside	1.0  ✓ 9.9.9
```

and that `✓ 9.9.9` is a directory **outside `$PKGX_DIR`**, reported as
installed — because the note beside a completion asks
`bottle.InstalledVersions`, which does `filepath.Join(dir, project)`.
`bottle.UnmarshalCatalog` refuses the name outright (*"has a `..` segment"*);
the index reader built its catalogue by hand and so never asked.

The fix is to call `bottle.ValidateProjectName` — **the same validator**, not a
second one. A fast path that reaches the same data must apply the same checks:
the guard belongs to the data, not to whichever reader got there first. The
regression test is a pair, because a validator that refused everything would
pass the traversal half and break the tool: `../outside` is refused, and
`x.org/protocol/xcb` — a real project whose name contains slashes — is
accepted.
