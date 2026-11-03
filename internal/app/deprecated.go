package app

// The end-of-life ranking needs no activation here any more.
//
// This file used to hold the list of abandoned release lines and an `init()`
// that assigned it to `bottle.DefaultDeprecated`. That was written because the
// feature had shipped behind `PKGX_DEPRECATED` alone and nobody sets it, so it
// changed no answer for anybody — a capability nobody switches on is a
// capability the fleet does not have.
//
// ⛔⛔ AND THAT REMEDY HAD THE SAME DEFECT ONE LAYER UP. It moved the opt-in
// from an environment variable to a line of Go in ONE module. This package is
// `internal`, so `pkgm`, `bk` and `mirror` could not reach the list even if
// someone had thought to: measured 2026-11-03, `pkgm install 'openssl.org@^3'`
// — the INSTALLER — put the end-of-life 3.6.4 on a clean machine while `pkgx`
// gave 3.5.8 from the same registry.
//
// bottle v0.63.0 carries the list itself (go-pkgx/bottle#186), so every
// binary that resolves inherits the same ranking without an activation step,
// and there is ONE list rather than one per module. Two lists are two answers
// waiting to disagree, which is the defect this was meant to end.
//
// The data and the tests that guard it now live in bottle:
// `KnownDeprecated`, `KnownSupported`, `KnownDeprecatedAsOf`.
//
// `PKGX_DEPRECATED` is unchanged and still overrides, and a file that is named
// but unreadable is still an empty list with a warning rather than a silent
// fall back to the default.
//
// The file is kept, empty of behaviour, because the next person to ask "where
// does pkgx turn this on?" should find this answer rather than silence.
