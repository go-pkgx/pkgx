package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// An Lmod modulefile of the shape a site actually writes.
const luaModule = `-- -*- lua -*-
help([[ nothing here changes the environment ]])
whatis("Name: openmpi 5.0.8")
prepend_path("PATH", "/opt/openmpi/5.0.8/bin")
prepend_path("LD_LIBRARY_PATH", "/opt/openmpi/5.0.8/lib")
append_path("MANPATH", "/opt/openmpi/5.0.8/share/man")
setenv("MPI_HOME", "/opt/openmpi/5.0.8")
unsetenv("MPI_DEBUG")
`

// The same thing as Environment Modules writes it.
const tclModule = `#%Module1.0
module-whatis "openmpi 5.0.8"
prepend-path PATH /opt/openmpi/5.0.8/bin
prepend-path LD_LIBRARY_PATH /opt/openmpi/5.0.8/lib
append-path MANPATH /opt/openmpi/5.0.8/share/man
setenv MPI_HOME /opt/openmpi/5.0.8
`

func TestImportLua(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "openmpi.lua", luaModule)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, refusals, err := importModulefile(p, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(refusals) != 0 {
		t.Fatalf("refused a file it should read: %v", refusals)
	}
	if m.Name != "openmpi" {
		t.Errorf("name = %q", m.Name)
	}
	if m.Description != "Name: openmpi 5.0.8" {
		t.Errorf("description = %q", m.Description)
	}
	if got := m.Prepend["PATH"]; len(got) != 1 || got[0] != "/opt/openmpi/5.0.8/bin" {
		t.Errorf("PATH = %v", got)
	}
	if got := m.Append["MANPATH"]; len(got) != 1 {
		t.Errorf("MANPATH = %v", got)
	}
	if m.Setenv["MPI_HOME"] != "/opt/openmpi/5.0.8" {
		t.Errorf("setenv = %v", m.Setenv)
	}
	if len(m.Unsetenv) != 1 || m.Unsetenv[0] != "MPI_DEBUG" {
		t.Errorf("unsetenv = %v", m.Unsetenv)
	}
}

// The TCL dialect must produce the SAME environment as the Lua one: a site
// migrating from Environment Modules to Lmod, or the reverse, wrote the same
// intent twice, and a converter that disagreed with itself would be worse than
// none.
func TestImportTclAgreesWithLua(t *testing.T) {
	dir := t.TempDir()
	lua := write(t, dir, "openmpi.lua", luaModule)
	tcl := write(t, dir, "openmpi", tclModule)
	read := func(p string) modImport {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		m, refusals, err := importModulefile(p, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(refusals) != 0 {
			t.Fatalf("%s refused: %v", p, refusals)
		}
		return m
	}
	a, b := read(lua), read(tcl)
	if a.Prepend["PATH"][0] != b.Prepend["PATH"][0] {
		t.Errorf("PATH differs: %v vs %v", a.Prepend["PATH"], b.Prepend["PATH"])
	}
	if a.Setenv["MPI_HOME"] != b.Setenv["MPI_HOME"] {
		t.Errorf("MPI_HOME differs: %q vs %q", a.Setenv["MPI_HOME"], b.Setenv["MPI_HOME"])
	}
	if b.Description != "openmpi 5.0.8" {
		t.Errorf("TCL description = %q", b.Description)
	}
	if len(b.Append["MANPATH"]) != 1 {
		t.Errorf("TCL MANPATH = %v", b.Append["MANPATH"])
	}
}

// Everything the converter must REFUSE, and the reason it gives. This list is
// the point of the tool: each of these would be silently mishandled by a
// partial interpreter, and the mishandling would surface inside a job.
func TestRefusals(t *testing.T) {
	for _, tc := range []struct{ name, file, body, want string }{
		{"a computed path", "x.lua", `prepend_path("PATH", pathJoin(root, "bin"))`, "not a string literal"},
		{"a variable", "x.lua", `setenv("CC", cc)`, "not a string literal"},
		{"a conditional", "x.lua", `if isloaded("gcc") then load("openmpi") end`, "not a plain call"},
		{"an assignment", "x.lua", `local root = "/opt/x"`, "not a plain call"},
		{"a relationship", "x.lua", `conflict("openmpi")`, "RELATIONSHIP"},
		{"a dependency", "x.lua", `depends_on("gcc")`, "RELATIONSHIP"},
		{"a family", "x.lua", `family("mpi")`, "RELATIONSHIP"},
		{"an unknown call", "x.lua", `LmodMessage("hello")`, "unknown statement"},
		{"a TCL variable", "x", "prepend-path PATH $root/bin", "not a literal"},
		{"a TCL command substitution", "x", "setenv HOST [exec hostname]", "not a literal"},
		{"a TCL conditional", "x", "if { [ module-info mode load ] } {", "not a literal"},
		{"a TCL relationship", "x", "conflict openmpi", "RELATIONSHIP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := write(t, dir, tc.file, tc.body+"\n")
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			_, refusals, err := importModulefile(p, f)
			if err != nil {
				t.Fatal(err)
			}
			if len(refusals) != 1 {
				t.Fatalf("want exactly one refusal, got %d: %v", len(refusals), refusals)
			}
			if !strings.Contains(refusals[0].Why, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", refusals[0].Why, tc.want)
			}
			if refusals[0].Line != 1 || refusals[0].Text == "" {
				t.Errorf("a refusal must name the line and quote it: %+v", refusals[0])
			}
		})
	}
}

// One refused file poisons the whole run: nothing is written. A partial
// conversion is the one outcome nobody can check.
func TestPartialConversionIsNeverEmitted(t *testing.T) {
	dir := t.TempDir()
	good := write(t, dir, "openmpi.lua", luaModule)
	bad := write(t, dir, "bad.lua", `prepend_path("PATH", pathJoin(root, "bin"))`+"\n")
	var out, errs strings.Builder
	err := modeImport([]string{good, bad}, &out, &errs)
	if err == nil {
		t.Fatal("want an error when a file was refused")
	}
	if out.Len() != 0 {
		t.Errorf("wrote a partial conversion:\n%s", out.String())
	}
	if !strings.Contains(errs.String(), "will not guess at") {
		t.Errorf("stderr must name what was refused:\n%s", errs.String())
	}
	// And on its own, the good file converts.
	out.Reset()
	errs.Reset()
	if err := modeImport([]string{good}, &out, &errs); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`env "openmpi"`,
		`description = "Name: openmpi 5.0.8"`,
		`PATH = ["/opt/openmpi/5.0.8/bin"]`,
		`MPI_HOME = "/opt/openmpi/5.0.8"`,
		`unsetenv = ["MPI_DEBUG"]`,
		"# imported from",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

// The emitted HCL2 must be readable by the loader that will consume it —
// otherwise the converter produces something only it can understand.
func TestImportedHCLLoadsBack(t *testing.T) {
	dir := t.TempDir()
	src := write(t, dir, "openmpi.lua", luaModule)
	var out, errs strings.Builder
	if err := modeImport([]string{src}, &out, &errs); err != nil {
		t.Fatal(err)
	}
	envDir := t.TempDir()
	write(t, envDir, "imported.hcl2", out.String())
	var warns []string
	envs := loadEnvironments([]string{envDir}, func(m string) { warns = append(warns, m) })
	if len(warns) != 0 {
		t.Fatalf("the loader rejected what the converter wrote: %v\n%s", warns, out.String())
	}
	if _, ok := envs["openmpi"]; !ok {
		t.Errorf("openmpi did not load back: %v", envs)
	}
}
