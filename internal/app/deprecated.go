package app

import "github.com/go-pkgx/bottle"

// ⛔ THIS FILE IS THE ACTIVATION, AND WITHOUT IT THE FEATURE DID NOTHING.
// bottle v0.61.0 taught the resolver to rank an end-of-life release line last,
// and read the list from `PKGX_DEPRECATED` alone. Nobody sets it. So openssl's
// 3.6 line reached its end-of-life date with `^3` still resolving to 3.6.4 on
// every machine in the fleet, while the 3.5 LTS sat in the same registry,
// supported for another three and a half years. A capability nobody switches
// on is a capability we do not have.
//
// bottle v0.62.0 lets a binary carry the list; this is the list both binaries
// carry. `PKGX_DEPRECATED` still overrides it, and a named file that cannot be
// read is an empty list with a warning, never a silent fall back to this —
// see bottle's deprecated.go for why that direction and not the other.
//
// It is a RANK, not a filter. `=3.6.4`, `^3.6.4` and a lock still resolve to
// 3.6.4, because nothing else satisfies them; `^3` and `~3.6` are where the
// answer changes. Reproducibility outranks the preference.

// deprecatedAsOf is the day the lines below were measured. A reader has to be
// able to tell a current judgement from one left behind by a release, and a
// list with no date reads as fresh forever — the same defect as a registry
// index with no generation timestamp (go-pkgx/packages#392).
const deprecatedAsOf = "2026-11-01"

// deprecatedLines is, per project, the release lines upstream has abandoned.
//
// Measured on deprecatedAsOf against the registry and endoflife.date:
//
//	openssl.org publishes 72 tags across nine lines
//	bk eol -product openssl 1.1.1w 3.0.21 3.1.8 3.2.6 3.3.7 3.4.7 3.5.8 3.6.4 4.0.2
//
//	1.1.1  ended 2023-09-11 (LTS)     3.4  ended 2026-10-22
//	3.0    ended 2026-09-07 (LTS)     3.6  ended 2026-11-01
//	3.1    ended 2025-03-14           3.5  supported to 2030-04-08 (LTS)
//	3.2    ended 2025-11-23           4.0  supported to 2027-05-14
//	3.3    ended 2026-04-09
//
// Only openssl so far, and deliberately: this list changes what a constraint
// resolves to across the whole fleet, so each project earns its place by a
// measurement rather than by a sweep. go-pkgx/bottle#179 moves the judgement
// into the signed catalogue, where it is computed rather than committed.
var deprecatedLines = map[string][]string{
	"openssl.org": {"1.1.1", "3.0", "3.1", "3.2", "3.3", "3.4", "3.6"},
}

// supportedLines is the other half of the same measurement: the lines that are
// still supported on deprecatedAsOf, and must therefore NOT appear above.
//
// ⛔ IT EXISTS TO BE CONTRADICTED. The expensive mistake here is listing a
// line that is still supported — that is how `^3` would be pushed off the 3.5
// LTS onto something older, quietly, with no error anywhere. Recording both
// halves of the verdict makes that edit fail a test instead of shipping, the
// same reason the build side and the consumer side of a pin are compared
// rather than trusted.
var supportedLines = map[string][]string{
	"openssl.org": {"3.5", "4.0"},
}

// init activates the list for BOTH binaries, by construction.
//
// Not a call in each main: `pkgx` and `pkgs` are two commands over one
// resolver, and a list activated in one of them would make them disagree
// about what a constraint means — `pkgs compat` resolves too. bottle latches
// the list on first use, so this has to happen before any resolution; package
// initialisation is the only point that is guaranteed to.
func init() { bottle.DefaultDeprecated = deprecatedLines }
