package main

import (
	"reflect"
	"testing"
)

func TestSplitPlus(t *testing.T) {
	cases := []struct {
		argv       []string
		plus, rest []string
	}{
		// no +pkg: the first token is the package to run.
		{[]string{"node@22", "--version"}, nil, []string{"node@22", "--version"}},
		// +pkgs then a command.
		{[]string{"+git", "+gnu.org/bash", "sh", "-c", "x"}, []string{"git", "gnu.org/bash"}, []string{"sh", "-c", "x"}},
		// "--" ends the package list and is dropped.
		{[]string{"+git", "--", "git", "--version"}, []string{"git"}, []string{"git", "--version"}},
		// +pkgs with no command.
		{[]string{"+wget"}, []string{"wget"}, nil},
		// lone "--" then a bare command.
		{[]string{"--", "env"}, nil, []string{"env"}},
	}
	for _, c := range cases {
		plus, rest := splitPlus(c.argv)
		if !eqStrs(plus, c.plus) || !eqStrs(rest, c.rest) {
			t.Errorf("splitPlus(%v) = %v,%v want %v,%v", c.argv, plus, rest, c.plus, c.rest)
		}
	}
}

func TestSpecParsing(t *testing.T) {
	cases := []struct{ in, proj, con string }{
		{"nodejs.org", "nodejs.org", "*"},
		{"node@22", "node", "22"},
		{"gnu.org/wget@^1.21", "gnu.org/wget", "^1.21"},
		{"openssl.org@=3.0.0", "openssl.org", "=3.0.0"},
	}
	for _, c := range cases {
		if p := project(c.in); p != c.proj {
			t.Errorf("project(%q)=%q want %q", c.in, p, c.proj)
		}
		if con := constraint(c.in); con != c.con {
			t.Errorf("constraint(%q)=%q want %q", c.in, con, c.con)
		}
	}
}

func TestRunHelpVersion(t *testing.T) {
	if rc := run([]string{"--help"}); rc != 0 {
		t.Errorf("--help rc=%d", rc)
	}
	if rc := run([]string{"-v"}); rc != 0 {
		t.Errorf("-v rc=%d", rc)
	}
	if rc := run(nil); rc != 2 {
		t.Errorf("no-args rc=%d want 2", rc)
	}
}

func eqStrs(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
