package main

// Named environments, declared in HCL2 — the unit a site actually wants to hand
// people, and the reason `module load cfd` can mean something without a
// hand-written modulefile behind it.
//
//	# ~/.pkgx/environments/site.hcl2      (or $PKGX_DIR/environments/*.hcl2)
//	env "cfd" {
//	  description = "the solver stack, as validated on 2026-09-01"
//	  packages    = ["openmpi.org@5", "hdf5.org", "python.org@3.12"]
//	}
//
// Why HCL2 rather than a list of lines: an environment is a thing a site
// REVIEWS. It wants a description, it wants to live in a pull request, and it
// wants to say more later (a default, a conflict) without inventing a syntax
// each time. ~/.pkgx/config.hcl2 already made that choice for this toolchain;
// this follows it rather than adding a second dialect.
//
// An environment is NOT a lockfile. It names constraints, and the closure is
// resolved at load time from the signed registry — so `module load cfd` on a
// login node and inside a container give the same answer for the same registry
// state, and a stale hand-edited modulefile cannot disagree with what is
// installed.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

// environment is one named package set.
type environment struct {
	Name        string
	Description string
	Packages    []string
	File        string // where it was declared, for `show`
}

// envSchema is the block shape: env "<name>" { … }.
var envSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{{Type: "env", LabelNames: []string{"name"}}},
}

var envBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "packages", Required: true},
		{Name: "description"},
	},
}

// environmentDirs lists the directories scanned for *.hcl2, nearest last so a
// user's own file wins over a site's.
func environmentDirs(pkgxDir, home string) []string {
	var dirs []string
	if v := os.Getenv("PKGX_ENVIRONMENTS"); v != "" {
		dirs = append(dirs, filepath.SplitList(v)...)
	}
	if pkgxDir != "" {
		dirs = append(dirs, filepath.Join(pkgxDir, "environments"))
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".pkgx", "environments"))
	}
	return dirs
}

// loadEnvironments reads every environment declared under dirs. A file that
// does not parse is REPORTED and skipped: one broken site file must not make
// every other environment unavailable, and silence would look like an
// environment that was never declared.
func loadEnvironments(dirs []string, warn func(string)) map[string]environment {
	out := map[string]environment{}
	for _, d := range dirs {
		files, err := filepath.Glob(filepath.Join(d, "*.hcl2"))
		if err != nil {
			continue
		}
		sort.Strings(files)
		for _, f := range files {
			for name, e := range parseEnvironmentFile(f, warn) {
				out[name] = e
			}
		}
	}
	return out
}

// parseEnvironmentFile reads one HCL2 file.
func parseEnvironmentFile(path string, warn func(string)) map[string]environment {
	out := map[string]environment{}
	p := hclparse.NewParser()
	file, diags := p.ParseHCLFile(path)
	if diags.HasErrors() {
		warn(fmt.Sprintf("ignoring %s: %s", path, diags.Error()))
		return out
	}
	content, diags := file.Body.Content(envSchema)
	if diags.HasErrors() {
		warn(fmt.Sprintf("ignoring %s: %s", path, diags.Error()))
		return out
	}
	for _, blk := range content.Blocks {
		name := blk.Labels[0]
		body, diags := blk.Body.Content(envBodySchema)
		if diags.HasErrors() {
			warn(fmt.Sprintf("ignoring env %q in %s: %s", name, path, diags.Error()))
			continue
		}
		e := environment{Name: name, File: path}
		if attr, ok := body.Attributes["packages"]; ok {
			v, diags := attr.Expr.Value(nil)
			if diags.HasErrors() {
				warn(fmt.Sprintf("ignoring env %q in %s: %s", name, path, diags.Error()))
				continue
			}
			// Iterate rather than gocty.FromCtyValue: HCL types a literal
			// ["a","b"] as a TUPLE, not a list, so asking for []string fails with
			// "list or set value is required" on a file that is perfectly correct.
			pkgs, err := stringsOf(v)
			if err != nil {
				warn(fmt.Sprintf("ignoring env %q in %s: packages must be a list of strings: %v", name, path, err))
				continue
			}
			e.Packages = pkgs
		}
		if attr, ok := body.Attributes["description"]; ok {
			if v, diags := attr.Expr.Value(nil); !diags.HasErrors() && v.Type() == cty.String {
				e.Description = v.AsString()
			}
		}
		if len(e.Packages) == 0 {
			warn(fmt.Sprintf("ignoring env %q in %s: it names no package", name, path))
			continue
		}
		out[name] = e
	}
	return out
}

// expandSpecs turns each spec that names an ENVIRONMENT into its packages, and
// leaves everything else alone.
//
// The spec list keeps the environment's NAME, not its expansion: `module list`
// after `module load cfd` must say cfd, and `module unload cfd` must remove
// exactly what cfd brought — which it does, because the whole set is recomposed
// from the remaining specs rather than subtracted.
func expandSpecs(specs []string, envs map[string]environment) []string {
	var out []string
	for _, s := range specs {
		if e, ok := envs[s]; ok {
			out = append(out, e.Packages...)
			continue
		}
		out = append(out, s)
	}
	return out
}

// sortedEnvironments returns the environments by name, for `avail`.
func sortedEnvironments(envs map[string]environment) []environment {
	out := make([]environment, 0, len(envs))
	for _, e := range envs {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// stringsOf reads a cty tuple/list/set of strings.
func stringsOf(v cty.Value) ([]string, error) {
	if !v.CanIterateElements() {
		return nil, fmt.Errorf("%s is not a list", v.Type().FriendlyName())
	}
	var out []string
	for it := v.ElementIterator(); it.Next(); {
		_, ev := it.Element()
		if ev.Type() != cty.String {
			return nil, fmt.Errorf("element is %s, not a string", ev.Type().FriendlyName())
		}
		out = append(out, ev.AsString())
	}
	return out, nil
}
