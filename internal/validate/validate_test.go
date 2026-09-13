package validate

import (
	"forge/internal/project"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, root, p, s string) {
	t.Helper()
	p = filepath.Join(root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0644); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (string, *project.Config) {
	t.Helper()
	root := t.TempDir()
	o := project.Defaults()
	o.Name = "orders"
	o.Backend = "none"
	o.Docker = false
	o.CI = "none"
	c := project.New(o)
	for _, p := range []string{"README.md", "AGENTS.md", ".gitignore", "frontend/public/.keep", "frontend/src/app/page.tsx", "frontend/tsconfig.json", "frontend/eslint.config.mjs"} {
		put(t, root, p, "fixture")
	}
	for _, p := range []string{"docs", "scripts", "deploy"} {
		if err := os.Mkdir(filepath.Join(root, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	put(t, root, "frontend/package.json", `{"name":"frontend","scripts":{"dev":"next dev","build":"next build","start":"next start","lint":"eslint ."},"dependencies":{"next":"16.1.0","react":"19.0.0","react-dom":"19.0.0"},"devDependencies":{"typescript":"5","eslint":"9"},"engines":{"node":">=22"}}`)
	put(t, root, "frontend/package-lock.json", `{"lockfileVersion":3,"packages":{"":{"dependencies":{"next":"16.1.0","react":"19.0.0","react-dom":"19.0.0"},"devDependencies":{"typescript":"5","eslint":"9"}},"node_modules/next":{"version":"16.1.0"}}}`)
	put(t, root, "frontend/next.config.ts", "export default {};")
	if err := project.Save(root, c); err != nil {
		t.Fatal(err)
	}
	return root, c
}
func TestValidationFindsMissingFileAndPackageDrift(t *testing.T) {
	root, c := fixture(t)
	if r := Run(root, c); r.Status != "success" {
		t.Fatalf("valid fixture: %+v", r)
	}
	if err := os.Remove(filepath.Join(root, "frontend", "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	r := Run(root, c)
	if r.Status != "error" {
		t.Fatal("missing lockfile passed")
	}
	found := false
	for _, ch := range r.Checks {
		if ch.Status == "error" && strings.Contains(ch.Path, "package-lock.json") {
			found = true
		}
	}
	if !found {
		t.Fatal("missing actionable lockfile check")
	}
}
func TestValidationRejectsInvalidJSONAndMissingStandalone(t *testing.T) {
	root, c := fixture(t)
	put(t, root, "frontend/package.json", `{"scripts":{}}`)
	if Run(root, c).Status != "error" {
		t.Fatal("invalid package passed")
	}
	root, c = fixture(t)
	c.Container.Enabled = true
	put(t, root, "frontend/Dockerfile", "FROM node:22-alpine")
	put(t, root, "frontend/.dockerignore", "node_modules")
	if Run(root, c).Status != "error" {
		t.Fatal("missing standalone passed")
	}
}
func TestValidationRejectsSymlinkEscapes(t *testing.T) {
	root, c := fixture(t)
	outside := t.TempDir()
	if err := os.RemoveAll(filepath.Join(root, "frontend")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "frontend")); err != nil {
		t.Skip(err)
	}
	if Run(root, c).Status != "error" {
		t.Fatal("escaped symlink passed")
	}
}

func TestValidationRejectsDevelopmentAndRemovedDependencyDrift(t *testing.T) {
	root, c := fixture(t)
	put(t, root, "frontend/package-lock.json", `{"lockfileVersion":3,"packages":{"":{"dependencies":{"next":"16.1.0","react":"19.0.0","react-dom":"19.0.0"},"devDependencies":{"typescript":"4","eslint":"9"}},"node_modules/next":{"version":"16.1.0"}}}`)
	if Run(root, c).Status != "error" {
		t.Fatal("development dependency drift accepted")
	}
	root, c = fixture(t)
	put(t, root, "frontend/package-lock.json", `{"lockfileVersion":3,"packages":{"":{"dependencies":{"next":"16.1.0","react":"19.0.0","react-dom":"19.0.0","removed":"1.0.0"},"devDependencies":{"typescript":"5","eslint":"9"}},"node_modules/next":{"version":"16.1.0"}}}`)
	if Run(root, c).Status != "error" {
		t.Fatal("removed dependency drift accepted")
	}
}
