package scaffold

import (
	"context"
	"errors"
	"fmt"
	"forge/internal/apperror"
	"forge/internal/project"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type genFunc func(context.Context, string, *project.Config) error

func (f genFunc) Generate(ctx context.Context, p string, c *project.Config) error {
	return f(ctx, p, c)
}
func options(t *testing.T) project.Options {
	t.Helper()
	o := project.Defaults()
	o.Name = "orders"
	o.Output = t.TempDir()
	o.Frontend = "none"
	o.Backend = "none"
	o.Python = "uv"
	o.CI = "none"
	o.Docker = false
	return o
}
func TestExistingTargetNeverTouched(t *testing.T) {
	o := options(t)
	target := filepath.Join(o.Output, o.Name)
	os.Mkdir(target, 0755)
	os.WriteFile(filepath.Join(target, "keep"), []byte("user"), 0600)
	called := false
	s := Service{Generator: genFunc(func(context.Context, string, *project.Config) error { called = true; return nil })}
	_, err := s.Create(context.Background(), o)
	var e *apperror.Error
	if !errors.As(err, &e) || e.Code != "PROJECT_ALREADY_EXISTS" || called {
		t.Fatalf("existing target: %v called=%v", err, called)
	}
	b, _ := os.ReadFile(filepath.Join(target, "keep"))
	if string(b) != "user" {
		t.Fatal("modified existing files")
	}
}
func TestFailedGenerationRollsBackOnlyOwnedPaths(t *testing.T) {
	o := options(t)
	keep := filepath.Join(o.Output, "unrelated")
	os.WriteFile(keep, []byte("keep"), 0600)
	s := Service{Generator: genFunc(func(_ context.Context, root string, _ *project.Config) error {
		os.WriteFile(filepath.Join(root, "partial"), []byte("x"), 0600)
		return errors.New("network failed")
	})}
	if _, err := s.Create(context.Background(), o); err == nil {
		t.Fatal("expected failure")
	}
	entries, _ := os.ReadDir(o.Output)
	if len(entries) != 1 || entries[0].Name() != "unrelated" {
		t.Fatalf("left partial project or removed unrelated: %v", entries)
	}
}
func TestConcurrentDestinationCreationIsPreserved(t *testing.T) {
	o := options(t)
	s := Service{Generator: genFunc(func(_ context.Context, root string, c *project.Config) error {
		if err := pythonFixture(root, c); err != nil {
			return err
		}
		target := filepath.Join(o.Output, o.Name)
		os.Mkdir(target, 0755)
		return os.WriteFile(filepath.Join(target, "winner"), []byte("user"), 0600)
	})}
	_, err := s.Create(context.Background(), o)
	if err == nil {
		t.Fatal("overwrote competing destination")
	}
	b, _ := os.ReadFile(filepath.Join(o.Output, o.Name, "winner"))
	if string(b) != "user" {
		t.Fatal("removed competing target")
	}
}
func pythonFixture(root string, c *project.Config) error {
	dir := filepath.Join(root, filepath.FromSlash(c.Python.Path))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for p, b := range map[string]string{"pyproject.toml": "[project]\nname = \"ai\"\nversion = \"0.1.0\"\nrequires-python = \">=3.12\"\n", "uv.lock": "version = 1\n", "main.py": "print('hello')\n"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(b), 0644); err != nil {
			return err
		}
	}
	return nil
}
func TestSuccessfulCreationPublishesValidatedMetadata(t *testing.T) {
	o := options(t)
	s := Service{Generator: genFunc(func(_ context.Context, r string, c *project.Config) error { return pythonFixture(r, c) }), Version: "test"}
	r, err := s.Create(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "success" || r.Validation.Status != "success" {
		t.Fatalf("bad result: %+v", r)
	}
	c, err := project.Load(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if c.ForgeVersion != "test" || len(c.ManagedFiles) == 0 {
		t.Fatal("missing provenance")
	}
	entries, _ := os.ReadDir(o.Output)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".forge-") {
			t.Fatal("staging leak")
		}
	}
}
func TestCanceledGenerationCannotPublish(t *testing.T) {
	o := options(t)
	ctx, cancel := context.WithCancel(context.Background())
	s := Service{Generator: genFunc(func(_ context.Context, r string, c *project.Config) error {
		err := pythonFixture(r, c)
		cancel()
		return err
	})}
	if _, err := s.Create(ctx, o); err == nil {
		t.Fatal("canceled create published")
	}
	if _, err := os.Stat(filepath.Join(o.Output, o.Name)); !os.IsNotExist(err) {
		t.Fatal("target exists after cancel")
	}
}

func TestAllComponentCIAndDockerCombinations(t *testing.T) {
	for mask := 1; mask < 8; mask++ {
		for _, ci := range []string{"none", "gitlab", "github"} {
			for _, docker := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-%s-docker%v", mask, ci, docker), func(t *testing.T) {
					o := options(t)
					o.CI = ci
					o.Docker = docker
					o.Python = "none"
					if mask&1 != 0 {
						o.Frontend = "nextjs"
					}
					if mask&2 != 0 {
						o.Backend = "springboot"
					}
					if mask&4 != 0 {
						o.Python = "uv"
					}
					s := Service{Generator: genFunc(fullFixture), Version: "test"}
					r, err := s.Create(context.Background(), o)
					if err != nil {
						t.Fatalf("create: %v, checks=%+v", err, r.Validation.Checks)
					}
					if r.Validation.Status != "success" {
						t.Fatal(r.Validation)
					}
					for _, cmp := range r.Project.Components() {
						_, err := os.Stat(filepath.Join(r.Path, filepath.FromSlash(cmp.Path), "Dockerfile"))
						if docker && err != nil {
							t.Fatal(err)
						}
						if !docker && !os.IsNotExist(err) {
							t.Fatal("unexpected Dockerfile")
						}
					}
				})
			}
		}
	}
}
func fullFixture(ctx context.Context, root string, c *project.Config) error {
	files := map[string]string{}
	if c.Frontend != nil {
		p := c.Frontend.Path + "/"
		for k, v := range map[string]string{"package.json": `{"name":"frontend","scripts":{"dev":"next dev","build":"next build","start":"next start","lint":"eslint ."},"dependencies":{"next":"16.1.0","react":"19","react-dom":"19"},"engines":{"node":">=22"}}`, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{"dependencies":{"next":"16.1.0","react":"19","react-dom":"19"}},"node_modules/next":{"version":"16.1.0"}}}`, "next.config.ts": "export default {output: 'standalone'};", "tsconfig.json": "{}", "eslint.config.mjs": "export default [];", "src/app/page.tsx": "export default function Page(){return null}", "public/.keep": ""} {
			files[p+k] = v
		}
	}
	if c.Backend != nil {
		p := c.Backend.Path + "/"
		for k, v := range map[string]string{"pom.xml": `<project><parent><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-parent</artifactId><version>3.5.0</version></parent><properties><java.version>21</java.version></properties><build><plugins><plugin><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build></project>`, "mvnw": "wrapper", "mvnw.cmd": "wrapper", ".mvn/wrapper/maven-wrapper.properties": "distributionUrl=https://example.com/maven.zip", "src/main/java/App.java": "class App {}"} {
			files[p+k] = v
		}
	}
	for p, b := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(b), 0644); err != nil {
			return err
		}
	}
	if c.Python != nil {
		return pythonFixture(root, c)
	}
	return nil
}
