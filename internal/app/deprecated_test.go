package app

import (
	"testing"

	"github.com/go-zpkg/bottle"
)

// ⛔⛔ THE RANKING IS STILL ON, now that nothing here switches it on.
//
// This is the one assertion worth keeping in zpkgx. The list, and the tests
// that guard its CONTENT — both halves recorded, no supported line marked
// dead, lines rather than releases, a date that parses — moved to bottle with
// the data (go-zpkg/bottle#186), because that is where the data is and two
// copies are two answers waiting to disagree.
//
// What zpkgx still has to answer is narrower and specific to this binary: when
// `zpkgx` resolves a constraint, is the ranking in force? It was inert twice
// before — behind an environment variable nobody set, then behind an
// activation that reached this module and no other — and both times every
// test passed while no answer changed. A test that sets the list measures the
// mechanism; this one measures the state a user's resolution actually sees.
func TestTheRankingIsInForceWithoutAnyActivationHere(t *testing.T) {
	if len(bottle.DefaultDeprecated) == 0 {
		t.Fatal("bottle.DefaultDeprecated is empty, so the EOL ranking is inert again")
	}
	if got := bottle.DefaultDeprecated["openssl.org"]; len(got) == 0 {
		t.Error("no openssl.org lines are deprecated, which is what this is for")
	}
}
