package app

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// importEnvSchema is just enough of the generated grammar to count the
// blocks: the question is whether an import can add one, not what is inside.
var importEnvSchema = hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{{Type: "env", LabelNames: []string{"name"}}},
}

// ⛔ A MODULEFILE IS SOMEBODY ELSE'S FILE, AND writeHCL GENERATES CONFIG WE
// LATER EVALUATE.
//
// `pkgs env import` exists to convert an HPC site's Lmod/Environment Modules
// tree — files this project did not write — into an HCL2 env block that
// `pkgx env` then loads. So the generated file is the boundary: anything an
// imported file can put into it, it can put into the environment.
//
// writeHCL escapes every VALUE with %q and wrote every KEY with %s. An
// environment variable name is attacker-chosen (`setenv <name> <value>`), so
// a name carrying a quote, a newline or an `=` closed the attribute it was in
// and opened whatever came next.
//
// The judge is a real HCL parser rather than a string search: "does the
// generated file say what we put in it" is the property, and a hand-written
// check for suspicious characters would be a second, worse implementation of
// the grammar.
func TestWriteHCLCannotBeInjectedThroughAnEnvName(t *testing.T) {
	hostile := []struct {
		what string
		key  string
	}{
		{"a quote and an = close the attribute", `X" = "1`},
		{"a newline opens a second attribute", "X\"\n  EVIL = \"1"},
		{"a space is not an identifier", "A B"},
		{"a brace closes the block", "X\"\n}\nenv \"evil\" {\n  description = \"1"},
		{"empty", ""},
	}
	for _, c := range hostile {
		m := modImport{
			Name:    "site",
			Source:  "modulefile",
			Prepend: map[string][]string{},
			Append:  map[string][]string{},
			Setenv:  map[string]string{c.key: "v"},
		}
		var b strings.Builder
		writeHCL(m, &b)

		f, diags := hclparse.NewParser().ParseHCL([]byte(b.String()), "generated.hcl2")
		if diags.HasErrors() {
			t.Errorf("%s: generated HCL2 does not parse (%s):\n%s", c.what, diags.Error(), b.String())
			continue
		}
		// Parsing is not enough — a file that parses can still have gained a
		// block or an attribute the modulefile never declared. Exactly one
		// top-level `env` block, which is what we wrote.
		body, _, _ := f.Body.PartialContent(&importEnvSchema)
		if n := len(body.Blocks); n != 1 {
			t.Errorf("%s: generated HCL2 holds %d env block(s), want 1:\n%s", c.what, n, b.String())
		}
	}

	// ⛔ AND THE NAME IS REFUSED AT THE DOOR, not merely quoted on the way
	// out. hclKey alone would keep the generated file well formed, so the
	// assertions above would pass with badEnvName deleted — and `env import`
	// would then import a variable no shell can set, silently. The refusal
	// is the behaviour; the quoting is the second line.
	for _, src := range []string{
		"#%Module\nsetenv \"A B\" v\n",
		"setenv(\"X\\\" = \\\"1\", \"v\")\n",
		"#%Module\nprepend-path \"P Q\" /x\n",
	} {
		name := "m"
		if strings.Contains(src, "(") {
			name = "m.lua"
		}
		m, refusals, err := importModulefile(name, strings.NewReader(src))
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if len(refusals) == 0 {
			t.Errorf("%q was converted without a refusal; got %+v", src, m)
		}
		if len(m.Setenv) != 0 || len(m.Prepend) != 0 {
			t.Errorf("%q put a non-name into the conversion: %+v", src, m)
		}
	}

	// The control: an ordinary name must still come through, or a fix that
	// refused every key would pass everything above.
	m := modImport{
		Name: "site", Source: "modulefile",
		Prepend: map[string][]string{"PATH": {"/opt/x/bin"}},
		Append:  map[string][]string{},
		Setenv:  map[string]string{"CC": "clang"},
	}
	var b strings.Builder
	writeHCL(m, &b)
	for _, want := range []string{"CC", "clang", "PATH", "/opt/x/bin"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("an ordinary import lost %q:\n%s", want, b.String())
		}
	}
	if _, diags := hclparse.NewParser().ParseHCL([]byte(b.String()), "generated.hcl2"); diags.HasErrors() {
		t.Errorf("an ordinary import does not parse (%s):\n%s", diags.Error(), b.String())
	}
}

// The same question asked of whole files, because the hostile name above is
// one I thought of and a modulefile is a grammar with corners.
func FuzzImportedModulefileGeneratesParseableHCL(f *testing.F) {
	f.Add("#%Module\nsetenv CC clang\nprepend-path PATH /opt/x/bin\n")
	f.Add("#%Module\nsetenv \"A B\" v\n")
	f.Add("#%Module\nsetenv \"X\" = \"1\" v\n")
	f.Add("setenv(\"CC\", \"clang\")\nprepend_path(\"PATH\", \"/opt/x/bin\")\n")
	f.Add("setenv(\"X\\\" = \\\"1\", \"v\")\n")
	f.Add("help([[a\nb]])\nsetenv(\"A\", \"1\")\n")
	f.Fuzz(func(t *testing.T, body string) {
		for _, name := range []string{"m.lua", "m"} {
			m, _, err := importModulefile(name, strings.NewReader(body))
			if err != nil {
				continue
			}
			var b strings.Builder
			writeHCL(m, &b)
			f, diags := hclparse.NewParser().ParseHCL([]byte(b.String()), "generated.hcl2")
			if diags.HasErrors() {
				t.Fatalf("modulefile %q generated HCL2 that does not parse (%s):\n%s",
					body, diags.Error(), b.String())
			}
			got, _, _ := f.Body.PartialContent(&importEnvSchema)
			if n := len(got.Blocks); n != 1 {
				t.Fatalf("modulefile %q generated %d env block(s), want 1:\n%s", body, n, b.String())
			}
		}
	})
}
