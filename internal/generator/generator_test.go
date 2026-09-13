package generator

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"forge/internal/apperror"
	"forge/internal/project"
	"forge/internal/runner"
)

type recordingRunner struct {
	calls []runner.Spec
	run   func(runner.Spec) error
}

func (r *recordingRunner) Run(_ context.Context, spec runner.Spec) error {
	r.calls = append(r.calls, spec)
	if r.run != nil {
		return r.run(spec)
	}
	return nil
}

func TestGenerateNextUsesNonInteractiveOfficialCLIAndAdjustsFreshProject(t *testing.T) {
	root := t.TempDir()
	r := &recordingRunner{run: func(spec runner.Spec) error {
		frontend := filepath.Join(root, "frontend")
		if err := os.MkdirAll(filepath.Join(frontend, ".git"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(frontend, "next.config.ts"), []byte("import type { NextConfig } from \"next\";\n\nconst nextConfig: NextConfig = { reactStrictMode: true };\nexport default nextConfig;\n"), 0o644); err != nil {
			return err
		}
		pkg := `{"scripts":{"dev":"next dev"},"dependencies":{"next":"15.5.2"}}`
		if err := os.WriteFile(filepath.Join(frontend, "package.json"), []byte(pkg), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(frontend, "package-lock.json"), []byte(`{"lockfileVersion":3,"packages":{"":{}}}`), 0o644)
	}}
	c := frontendConfig(true)

	err := (&Service{Runner: r, Stderr: io.Discard}).Generate(context.Background(), root, c)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--yes", "create-next-app@latest", "frontend", "--ts", "--eslint", "--app", "--src-dir", "--use-npm", "--disable-git", "--yes"}
	if len(r.calls) != 1 || r.calls[0].Command != "npx" || !reflect.DeepEqual(r.calls[0].Args, want) || r.calls[0].Dir != root {
		t.Fatalf("unexpected runner calls: %#v", r.calls)
	}
	config, _ := os.ReadFile(filepath.Join(root, "frontend", "next.config.ts"))
	if !strings.HasPrefix(string(config), "import type { NextConfig } from \"next\";") || !strings.Contains(string(config), "const nextConfig: NextConfig = {\n  output: \"standalone\",") {
		t.Fatalf("standalone must be added to the config object, preserving imports: %s", config)
	}
	if !strings.Contains(string(config), `reactStrictMode: true`) || !strings.Contains(string(config), `output: "standalone"`) {
		t.Fatalf("config did not preserve existing fields and add standalone: %s", config)
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	b, _ := os.ReadFile(filepath.Join(root, "frontend", "package.json"))
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Scripts["lint"] != "eslint ." {
		t.Fatalf("lint script = %q", pkg.Scripts["lint"])
	}
	if _, err := os.Stat(filepath.Join(root, "frontend", ".git")); !os.IsNotExist(err) {
		t.Fatalf("nested .git remains: %v", err)
	}
	if c.Frontend.Version != "15.5.2" {
		t.Fatalf("version = %q", c.Frontend.Version)
	}
}

func TestGenerateNextNormalizesLintAndNodeBaselineWithoutDocker(t *testing.T) {
	root := t.TempDir()
	r := &recordingRunner{run: func(runner.Spec) error {
		frontend := filepath.Join(root, "frontend")
		if err := os.MkdirAll(frontend, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(frontend, "next.config.ts"), []byte("const nextConfig = { reactStrictMode: true };\nexport default nextConfig;\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(frontend, "package.json"), []byte(`{"scripts":{"dev":"next dev"},"dependencies":{"next":"15.5.2"}}`), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(frontend, "package-lock.json"), []byte(`{"lockfileVersion":3,"packages":{"":{"name":"frontend"},"node_modules/next":{"version":"15.5.2"}}}`), 0o644)
	}}
	c := frontendConfig(false)

	if err := (&Service{Runner: r}).Generate(context.Background(), root, c); err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
		Engines map[string]string `json:"engines"`
	}
	b, _ := os.ReadFile(filepath.Join(root, "frontend", "package.json"))
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Scripts["lint"] != "eslint ." || pkg.Engines["node"] != ">=22" {
		t.Fatalf("package metadata = %#v", pkg)
	}
	config, _ := os.ReadFile(filepath.Join(root, "frontend", "next.config.ts"))
	if strings.Contains(string(config), "standalone") {
		t.Fatalf("no-Docker config was changed to standalone: %s", config)
	}
	var lock struct {
		Packages map[string]json.RawMessage `json:"packages"`
	}
	b, _ = os.ReadFile(filepath.Join(root, "frontend", "package-lock.json"))
	if err := json.Unmarshal(b, &lock); err != nil {
		t.Fatal(err)
	}
	var rootPackage struct {
		Engines map[string]string `json:"engines"`
		Name    string            `json:"name"`
	}
	if err := json.Unmarshal(lock.Packages[""], &rootPackage); err != nil {
		t.Fatal(err)
	}
	if rootPackage.Engines["node"] != ">=22" || rootPackage.Name != "frontend" || lock.Packages["node_modules/next"] == nil {
		t.Fatalf("lock metadata drifted: %s", b)
	}
}

func TestGenerateValidatesConfigBeforeSideEffects(t *testing.T) {
	r := &recordingRunner{}
	c := frontendConfig(false)
	c.Frontend.Framework = "unsupported"
	root := t.TempDir()
	err := (&Service{Runner: r}).Generate(context.Background(), root, c)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "INVALID_PROJECT" {
		t.Fatalf("error = %#v", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("runner called for invalid config: %#v", r.calls)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid config changed filesystem: %#v", entries)
	}
}

func TestGenerateNextRejectsMissingGeneratedLockfile(t *testing.T) {
	root := t.TempDir()
	r := &recordingRunner{run: func(runner.Spec) error {
		frontend := filepath.Join(root, "frontend")
		if err := os.MkdirAll(frontend, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(frontend, "next.config.ts"), []byte("const nextConfig = {};\nexport default nextConfig;\n"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(frontend, "package.json"), []byte(`{"dependencies":{"next":"15.5.2"}}`), 0o644)
	}}
	err := (&Service{Runner: r}).Generate(context.Background(), root, frontendConfig(false))
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "GENERATION_FAILED" {
		t.Fatalf("error = %#v", err)
	}
}

func TestGeneratePythonUsesStandaloneUvProjectAndLocksIt(t *testing.T) {
	root := t.TempDir()
	r := &recordingRunner{run: func(spec runner.Spec) error {
		if len(spec.Args) > 0 && spec.Args[0] == "init" {
			d := filepath.Join(root, "services", "ai")
			if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(d, "main.py"), []byte("print('hello')\n"), 0o644)
		}
		return nil
	}}
	c := pythonConfig()

	err := (&Service{Runner: r, Stderr: io.Discard}).Generate(context.Background(), root, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("calls = %#v", r.calls)
	}
	wantInit := []string{"init", "--no-workspace", "--vcs", "none", "--no-package", "--python", "3.12", filepath.Join("services", "ai")}
	if r.calls[0].Command != "uv" || !reflect.DeepEqual(r.calls[0].Args, wantInit) || r.calls[0].Dir != root {
		t.Fatalf("init = %#v", r.calls[0])
	}
	if r.calls[1].Command != "uv" || !reflect.DeepEqual(r.calls[1].Args, []string{"lock"}) || r.calls[1].Dir != filepath.Join(root, "services", "ai") {
		t.Fatalf("lock = %#v", r.calls[1])
	}
	if _, err := os.Stat(filepath.Join(root, "services", "ai", "main.py")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "services", "ai", ".git")); !os.IsNotExist(err) {
		t.Fatalf("nested .git remains: %v", err)
	}
	if c.Python.Version != "3.12" {
		t.Fatalf("version = %q", c.Python.Version)
	}
}

func TestGeneratePreservesStructuredRunnerFailure(t *testing.T) {
	want := &apperror.Error{Code: "COMMAND_FAILED", Message: "npx failed", Command: "npx", ExitCode: 17}
	r := &recordingRunner{run: func(runner.Spec) error { return want }}
	c := frontendConfig(false)

	err := (&Service{Runner: r}).Generate(context.Background(), t.TempDir(), c)
	if !errors.Is(err, want) {
		t.Fatalf("Generate() error = %#v, want runner error preserved", err)
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "COMMAND_FAILED" || appErr.ExitCode != 17 {
		t.Fatalf("Generate() structured error = %#v", err)
	}
}

func TestGenerateSpringSelectsStableBootVersionAndExtractsStarter(t *testing.T) {
	archive := zipFixture(t, map[string]zipEntry{
		"pom.xml": {body: "<project/>"}, "mvnw": {body: "#!/bin/sh\n"}, "mvnw.cmd": {body: "@echo off\r\n"}, "src/.git/config": {body: "bad"},
	})
	var starterQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata/client":
			w.Header().Set("Content-Type", "application/vnd.initializr.v2.1+json")
			io.WriteString(w, `{"bootVersion":{"type":"single-select","default":"4.0.0-SNAPSHOT","values":[{"id":"4.0.0-SNAPSHOT","name":"4.0.0 (SNAPSHOT)"},{"id":"3.5.5","name":"3.5.5"}]},"javaVersion":{"type":"single-select","default":"21","values":[{"id":"21","name":"21"}]}}`)
		case "/starter.zip":
			starterQuery = r.URL.RawQuery
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	c := backendConfig("order-system")

	err := (&Service{Runner: &recordingRunner{}, Client: server.Client(), InitializrURL: server.URL, Stderr: io.Discard}).Generate(context.Background(), root, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"type=maven-project", "language=java", "javaVersion=21", "dependencies=web", "bootVersion=3.5.5", "packageName=com.company.ordersystem"} {
		if !strings.Contains(starterQuery, part) {
			t.Errorf("query %q missing %q", starterQuery, part)
		}
	}
	for _, name := range []string{"pom.xml", "mvnw", "mvnw.cmd"} {
		if _, err := os.Stat(filepath.Join(root, "backend", name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "backend", "src", ".git")); !os.IsNotExist(err) {
		t.Fatalf("nested .git remains: %v", err)
	}
	if c.Backend.Version != "3.5.5" {
		t.Fatalf("version = %q", c.Backend.Version)
	}
}

func TestGenerateRejectsUnsafeStarterArchives(t *testing.T) {
	tests := map[string][]byte{
		"traversal":           zipFixture(t, map[string]zipEntry{"../escape": {body: "owned"}}),
		"traversal directory": zipFixture(t, map[string]zipEntry{"../": {mode: os.ModeDir | 0o755}}),
		"windows drive":       zipFixture(t, map[string]zipEntry{"C:/escape": {body: "owned"}}),
		"windows reserved":    zipFixture(t, map[string]zipEntry{"CON/file": {body: "owned"}}),
		"duplicate case path": zipFixture(t, map[string]zipEntry{"README.md": {body: "one"}, "readme.md": {body: "two"}}),
		"symlink":             zipFixture(t, map[string]zipEntry{"link": {body: "target", mode: os.ModeSymlink | 0o777}}),
		"oversized":           oversizedZipFixture(t),
	}
	for name, archive := range tests {
		t.Run(name, func(t *testing.T) {
			server := initializrServer(archive, http.StatusOK)
			defer server.Close()
			root := t.TempDir()
			c := backendConfig("safe")
			err := (&Service{Runner: &recordingRunner{}, Client: server.Client(), InitializrURL: server.URL}).Generate(context.Background(), root, c)
			if err == nil {
				t.Fatal("expected rejection")
			}
			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Code != "GENERATION_FAILED" {
				t.Fatalf("error = %#v", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(statErr) {
				t.Fatalf("archive escaped root: %v", statErr)
			}
		})
	}
}

func TestGenerateReportsInitializrHTTPFailure(t *testing.T) {
	server := initializrServer(nil, http.StatusBadGateway)
	defer server.Close()
	c := backendConfig("safe")
	err := (&Service{Runner: &recordingRunner{}, Client: server.Client(), InitializrURL: server.URL}).Generate(context.Background(), t.TempDir(), c)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "GENERATION_FAILED" || appErr.Dependency != "spring-initializr" {
		t.Fatalf("error = %#v", err)
	}
}

func TestGeneratePreservesHTTPContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) { return nil, req.Context().Err() })}
	err := (&Service{Runner: &recordingRunner{}, Client: client, InitializrURL: "https://initializr.invalid"}).Generate(ctx, t.TempDir(), backendConfig("safe"))
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "CANCELED" || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %#v", err)
	}
}

func TestExtractZIPRejectsPreexistingSymlink(t *testing.T) {
	destination := t.TempDir()
	actual := t.TempDir()
	if err := os.Symlink(actual, filepath.Join(destination, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := extractZIP(zipFixture(t, map[string]zipEntry{"linked/escape": {body: "owned"}}), destination)
	if err == nil {
		t.Fatal("expected preexisting symlink rejection")
	}
	if _, statErr := os.Stat(filepath.Join(actual, "escape")); !os.IsNotExist(statErr) {
		t.Fatalf("extractor followed symlink: %v", statErr)
	}
}

type zipEntry struct {
	body string
	mode os.FileMode
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func zipFixture(t *testing.T, entries map[string]zipEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, entry := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		if entry.mode != 0 {
			h.SetMode(entry.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func oversizedZipFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "huge", Method: zip.Deflate}
	h.UncompressedSize64 = maxArchiveFileSize + 1
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("small")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	// archive/zip recalculates the actual size, so patch the central-directory
	// uncompressed-size field to exercise rejection before extraction.
	raw := b.Bytes()
	idx := bytes.Index(raw, []byte{'P', 'K', 1, 2})
	if idx < 0 {
		t.Fatal("missing central directory")
	}
	sz := uint32(maxArchiveFileSize + 1)
	raw[idx+24], raw[idx+25], raw[idx+26], raw[idx+27] = byte(sz), byte(sz>>8), byte(sz>>16), byte(sz>>24)
	return raw
}

func initializrServer(archive []byte, starterStatus int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metadata/client" {
			io.WriteString(w, `{"bootVersion":{"default":"3.5.5","values":[{"id":"3.5.5","name":"3.5.5"}]},"javaVersion":{"default":"21","values":[{"id":"21","name":"21"}]}}`)
			return
		}
		w.WriteHeader(starterStatus)
		w.Write(archive)
	}))
}

func frontendConfig(docker bool) *project.Config {
	o := project.Defaults()
	o.Name, o.Backend, o.Docker = "safe", "none", docker
	return project.New(o)
}

func backendConfig(name string) *project.Config {
	o := project.Defaults()
	o.Name, o.Frontend = name, "none"
	return project.New(o)
}

func pythonConfig() *project.Config {
	o := project.Defaults()
	o.Name, o.Frontend, o.Backend, o.Python = "safe", "none", "none", "uv"
	return project.New(o)
}

func TestStandaloneRejectsUnsupportedConfigWithoutWriting(t *testing.T) {
	for _, input := range []string{
		`import type { NextConfig } from "next"; export default makeConfig({});`,
		`const nextConfig = makeConfig({}); export default nextConfig;`,
		`const nextConfig = { output: "export" }; export default nextConfig;`,
	} {
		t.Run(input, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "next.config.ts")
			if err := os.WriteFile(p, []byte(input), 0644); err != nil {
				t.Fatal(err)
			}
			if err := ensureStandaloneConfig(dir); err == nil {
				t.Fatal("accepted unsupported config")
			}
			got, err := os.ReadFile(p)
			if err != nil || string(got) != input {
				t.Fatalf("modified unsupported config: %s (%v)", got, err)
			}
		})
	}
}

func TestJavaPackageAvoidsReservedIdentifiers(t *testing.T) {
	for _, name := range []string{"class", "int", "cl-ass", "true", "null", "record"} {
		if got := javaPackage(name); got != "com.company.app"+strings.ReplaceAll(name, "-", "") {
			t.Errorf("javaPackage(%q) = %q; expected prefixed safe identifier", name, got)
		}
	}
}

func TestInitializrModernMediaTypePreservesResolvableBootVersion(t *testing.T) {
	modern := zipFixture(t, map[string]zipEntry{"pom.xml": {body: "<project><parent><version>4.1.1</version></parent></project>"}})
	legacy := zipFixture(t, map[string]zipEntry{"pom.xml": {body: "<project><parent><version>4.1.1.RELEASE</version></parent></project>"}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metadata/client" {
			version := "4.1.1.RELEASE"
			if r.Header.Get("Accept") == "application/vnd.initializr.v2.3+json" {
				version = "4.1.1"
			}
			io.WriteString(w, `{"bootVersion":{"default":"`+version+`","values":[{"id":"`+version+`"}]},"javaVersion":{"values":[{"id":"21"}]}}`)
			return
		}
		if r.URL.Path == "/starter.zip" {
			if r.URL.Query().Get("bootVersion") == "4.1.1" {
				w.Write(modern)
			} else {
				w.Write(legacy)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	root := t.TempDir()
	c := backendConfig("orders")
	if err := (&Service{Runner: &recordingRunner{}, Client: server.Client(), InitializrURL: server.URL}).Generate(context.Background(), root, c); err != nil {
		t.Fatal(err)
	}
	pom, err := os.ReadFile(filepath.Join(root, "backend", "pom.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend.Version != "4.1.1" || string(pom) != "<project><parent><version>4.1.1</version></parent></project>" {
		t.Fatalf("legacy metadata generated unresolvable version: recorded=%s pom=%s", c.Backend.Version, pom)
	}
}

func TestStableBootVersionRequiresReleaseNumber(t *testing.T) {
	for _, version := range []string{"alpha", "4.1.1-alpha", "4.1.1-beta.1", "4.1.1-M1", "4.1.1-RC1", "4.1.1-SNAPSHOT", "4.1", "", "nonsense"} {
		if isStable(version) {
			t.Errorf("accepted unstable or malformed version %q", version)
		}
	}
	for _, version := range []string{"4.1.1", "3.5.5", "2.1.0.RELEASE"} {
		if !isStable(version) {
			t.Errorf("rejected stable version %q", version)
		}
	}
}
