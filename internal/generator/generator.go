// Package generator invokes the official ecosystem project generators.
package generator

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"forge/internal/apperror"
	"forge/internal/project"
	"forge/internal/runner"
)

const (
	defaultInitializrURL = "https://start.spring.io"
	maxHTTPArchiveSize   = 64 << 20
	maxArchiveFileSize   = 32 << 20
	maxArchiveTotalSize  = 128 << 20
	maxArchiveFiles      = 10_000
)

type Service struct {
	Runner        runner.Runner
	Client        *http.Client
	InitializrURL string
	Stderr        io.Writer
}

func (s *Service) Generate(ctx context.Context, root string, c *project.Config) error {
	if c == nil {
		return generationError("", root, "project configuration is required", nil)
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if s.Runner == nil {
		return generationError("", root, "process runner is required", nil)
	}
	if c.Frontend != nil {
		if err := s.generateNext(ctx, root, c.Frontend, c.Container.Enabled); err != nil {
			return err
		}
	}
	if c.Backend != nil {
		if err := s.generateSpring(ctx, root, c.Project, c.Backend); err != nil {
			return err
		}
	}
	if c.Python != nil {
		if err := s.generatePython(ctx, root, c.Python); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) generateNext(ctx context.Context, root string, component *project.Component, docker bool) error {
	dir, err := componentDir(root, component.Path)
	if err != nil {
		return err
	}
	spec := runner.Spec{
		Command: "npx",
		Args:    []string{"--yes", "create-next-app@latest", filepath.FromSlash(component.Path), "--ts", "--eslint", "--app", "--src-dir", "--use-npm", "--disable-git", "--yes"},
		Dir:     root, Stdin: nil, Stdout: s.stderr(), Stderr: s.stderr(),
		Env: []string{"CI=1", "NEXT_TELEMETRY_DISABLED=1", "npm_config_yes=true"},
	}
	if err := s.Runner.Run(ctx, spec); err != nil {
		return err
	}
	if err := removeNestedGit(dir); err != nil {
		return generationError("nextjs", component.Path, "cannot remove nested Git metadata", err)
	}
	if docker {
		if err := ensureStandaloneConfig(dir); err != nil {
			return generationError("nextjs", component.Path, "cannot configure standalone output", err)
		}
	}
	if err := ensurePackageMetadata(dir); err != nil {
		return generationError("nextjs", component.Path, "cannot configure package metadata", err)
	}
	version, err := nextVersion(dir)
	if err != nil {
		return generationError("nextjs", component.Path, "cannot read generated Next.js version", err)
	}
	component.Version = version
	return nil
}

func (s *Service) generatePython(ctx context.Context, root string, component *project.Component) error {
	dir, err := componentDir(root, component.Path)
	if err != nil {
		return err
	}
	init := runner.Spec{Command: "uv", Args: []string{"init", "--no-workspace", "--vcs", "none", "--no-package", "--python", "3.12", filepath.FromSlash(component.Path)}, Dir: root, Stdout: s.stderr(), Stderr: s.stderr(), Env: []string{"UV_NO_PROGRESS=1"}}
	if err := s.Runner.Run(ctx, init); err != nil {
		return err
	}
	if err := removeNestedGit(dir); err != nil {
		return generationError("uv", component.Path, "cannot remove nested Git metadata", err)
	}
	lock := runner.Spec{Command: "uv", Args: []string{"lock"}, Dir: dir, Stdout: s.stderr(), Stderr: s.stderr(), Env: []string{"UV_NO_PROGRESS=1"}}
	if err := s.Runner.Run(ctx, lock); err != nil {
		return err
	}
	component.Version = "3.12"
	return nil
}

type initializrMetadata struct {
	BootVersion selection `json:"bootVersion"`
	JavaVersion selection `json:"javaVersion"`
}
type selection struct {
	Default string `json:"default"`
	Values  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"values"`
}

func (s *Service) generateSpring(ctx context.Context, root, projectName string, component *project.Component) error {
	dir, err := componentDir(root, component.Path)
	if err != nil {
		return err
	}
	base := strings.TrimRight(s.InitializrURL, "/")
	if base == "" {
		base = defaultInitializrURL
	}
	// v2.3 preserves modern Spring Boot versions. Older media types can adapt
	// 4.x versions into legacy .RELEASE coordinates that Maven cannot resolve.
	metadataBody, err := s.get(ctx, base+"/metadata/client", "application/vnd.initializr.v2.3+json", 2<<20)
	if err != nil {
		if canceled := cancellationError(err); canceled != nil {
			return canceled
		}
		return generationError("spring-initializr", component.Path, "cannot load Spring Initializr metadata", err)
	}
	var metadata initializrMetadata
	if err := json.Unmarshal(metadataBody, &metadata); err != nil {
		return generationError("spring-initializr", component.Path, "invalid Spring Initializr metadata", err)
	}
	if !selectionHas(metadata.JavaVersion, "21") {
		return generationError("spring-initializr", component.Path, "Spring Initializr does not offer Java 21", nil)
	}
	bootVersion := stableVersion(metadata.BootVersion)
	if bootVersion == "" {
		return generationError("spring-initializr", component.Path, "Spring Initializr offers no stable Spring Boot version", nil)
	}
	values := url.Values{
		"type": {"maven-project"}, "language": {"java"}, "javaVersion": {"21"},
		"dependencies": {"web"}, "bootVersion": {bootVersion}, "packaging": {"jar"},
		"groupId": {"com.company"}, "artifactId": {projectName}, "name": {projectName},
		"packageName": {javaPackage(projectName)},
	}
	archive, err := s.get(ctx, base+"/starter.zip?"+values.Encode(), "application/zip", maxHTTPArchiveSize)
	if err != nil {
		if canceled := cancellationError(err); canceled != nil {
			return canceled
		}
		return generationError("spring-initializr", component.Path, "cannot download Spring Initializr project", err)
	}
	if err := extractZIP(archive, dir); err != nil {
		return generationError("spring-initializr", component.Path, "unsafe or invalid Spring Initializr archive", err)
	}
	if err := removeNestedGit(dir); err != nil {
		return generationError("spring-initializr", component.Path, "cannot remove nested Git metadata", err)
	}
	component.Version = bootVersion
	return nil
}

func (s *Service) get(parent context.Context, rawURL, accept string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	client := s.Client
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP status %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return b, nil
}

func extractZIP(data []byte, destination string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if len(zr.File) > maxArchiveFiles {
		return fmt.Errorf("archive contains too many files")
	}
	var total uint64
	seen := make(map[string]struct{}, len(zr.File))
	for _, f := range zr.File {
		if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() && !f.FileInfo().IsDir() {
			return fmt.Errorf("archive entry %q has unsupported type", f.Name)
		}
		if !safeArchiveName(f.Name) {
			return fmt.Errorf("archive entry %q has unsafe path", f.Name)
		}
		key := strings.ToLower(strings.TrimSuffix(f.Name, "/"))
		if _, exists := seen[key]; exists {
			return fmt.Errorf("archive contains duplicate path %q", f.Name)
		}
		seen[key] = struct{}{}
		if f.UncompressedSize64 > maxArchiveFileSize {
			return fmt.Errorf("archive entry %q is too large", f.Name)
		}
		total += f.UncompressedSize64
		if total > maxArchiveTotalSize {
			return fmt.Errorf("archive expands beyond size limit")
		}
	}
	if info, err := os.Lstat(destination); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("archive destination is a symbolic link")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, f := range zr.File {
		name := filepath.FromSlash(strings.TrimSuffix(f.Name, "/"))
		if f.FileInfo().IsDir() {
			if err := rootMkdirAll(root, name); err != nil {
				return err
			}
			continue
		}
		if err := rootMkdirAll(root, filepath.Dir(name)); err != nil {
			return err
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			r.Close()
			return err
		}
		written, copyErr := io.Copy(out, io.LimitReader(r, maxArchiveFileSize+1))
		closeErr, readCloseErr := out.Close(), r.Close()
		if copyErr != nil {
			return copyErr
		}
		if written > maxArchiveFileSize {
			return fmt.Errorf("archive entry %q exceeded its declared size limit", f.Name)
		}
		if closeErr != nil {
			return closeErr
		}
		if readCloseErr != nil {
			return readCloseErr
		}
	}
	return nil
}

func safeArchiveName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	trimmed := strings.TrimSuffix(name, "/")
	if !project.SafePath(trimmed) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(trimmed)))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../") && clean == trimmed
}

func componentDir(root, path string) (string, error) {
	if !project.SafePath(path) {
		return "", generationError("", path, "unsafe component path", nil)
	}
	return filepath.Join(root, filepath.FromSlash(path)), nil
}

func removeNestedGit(dir string) error {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() == ".git" {
			paths = append(paths, path)
			if entry.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func ensureStandaloneConfig(dir string) error {
	for _, name := range []string{"next.config.ts", "next.config.mjs", "next.config.js"} {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		// Accept the official generator's object initializer only. In particular,
		// the first brace in its TypeScript template belongs to the type import.
		// Fail closed if a future template changes to a function or wrapper.
		shape := regexp.MustCompile(`(?s)^\s*(?:import\s+type\s*\{\s*NextConfig\s*\}\s+from\s+["']next["'];\s*)?const\s+nextConfig(?:\s*:\s*NextConfig)?\s*=\s*\{(.*)\};\s*export\s+default\s+nextConfig;\s*$`)
		match := shape.FindSubmatchIndex(b)
		if match == nil {
			return fmt.Errorf("%s has an unsupported configuration shape; expected a nextConfig object initializer", name)
		}
		body := b[match[2]:match[3]]
		if regexp.MustCompile(`\boutput\s*:`).Match(body) {
			return fmt.Errorf("%s already defines output; refusing to replace generated configuration", name)
		}
		marker := match[2] - 1
		updated := append([]byte{}, b[:marker+1]...)
		updated = append(updated, []byte("\n  output: \"standalone\",")...)
		updated = append(updated, b[marker+1:]...)
		return os.WriteFile(path, updated, 0o644)
	}
	return fmt.Errorf("Next.js configuration file was not generated")
}

func ensurePackageMetadata(dir string) error {
	path := filepath.Join(dir, "package.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return err
	}
	scripts, ok := doc["scripts"].(map[string]any)
	if !ok {
		scripts = map[string]any{}
		doc["scripts"] = scripts
	}
	scripts["lint"] = "eslint ."
	engines, ok := doc["engines"].(map[string]any)
	if !ok {
		engines = map[string]any{}
		doc["engines"] = engines
	}
	engines["node"] = ">=22"
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, "package-lock.json")
	lockBytes, err := os.ReadFile(lockPath)
	if err != nil {
		return err
	}
	var lock map[string]any
	if err := json.Unmarshal(lockBytes, &lock); err != nil {
		return err
	}
	packages, ok := lock["packages"].(map[string]any)
	if !ok {
		return fmt.Errorf("package-lock.json has no packages object")
	}
	rootPackage, ok := packages[""].(map[string]any)
	if !ok {
		return fmt.Errorf("package-lock.json has no root package")
	}
	lockEngines, ok := rootPackage["engines"].(map[string]any)
	if !ok {
		lockEngines = map[string]any{}
		rootPackage["engines"] = lockEngines
	}
	lockEngines["node"] = ">=22"
	updatedLock, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(lockPath, append(updatedLock, '\n'), 0o644)
}

func nextVersion(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", err
	}
	var doc struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", err
	}
	v := doc.Dependencies["next"]
	if v == "" {
		v = doc.DevDependencies["next"]
	}
	if v == "" {
		return "", fmt.Errorf("package.json has no next dependency")
	}
	return strings.TrimLeft(v, "^~>= "), nil
}

func selectionHas(s selection, value string) bool {
	for _, v := range s.Values {
		if v.ID == value {
			return true
		}
	}
	return false
}
func stableVersion(s selection) string {
	if isStable(s.Default) && selectionHas(s, s.Default) {
		return s.Default
	}
	for _, v := range s.Values {
		if isStable(v.ID) {
			return v.ID
		}
	}
	return ""
}
func isStable(v string) bool {
	return regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:\.RELEASE)?$`).MatchString(v)
}
func javaPackage(name string) string {
	clean := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(name, "")
	clean = strings.ToLower(clean)
	// Include keywords, literals and restricted identifiers so all accepted
	// project slugs yield portable Java package segments after removing hyphens.
	reserved := " abstract assert boolean break byte case catch char class const continue default do double else enum extends final finally float for goto if implements import instanceof int interface long native new package private protected public return short static strictfp super switch synchronized this throw throws transient try void volatile while true false null exports module open opens permits provides record requires sealed to transitive uses var when with yield "
	if clean == "" || clean[0] >= '0' && clean[0] <= '9' || strings.Contains(reserved, " "+clean+" ") {
		clean = "app" + clean
	}
	return "com.company." + clean
}
func (s *Service) stderr() io.Writer {
	if s.Stderr != nil {
		return s.Stderr
	}
	return io.Discard
}
func generationError(dependency, path, message string, cause error) error {
	return &apperror.Error{Code: "GENERATION_FAILED", Message: message, Dependency: dependency, Path: path, Cause: cause}
}

func cancellationError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &apperror.Error{Code: "CANCELED", Message: "generation was canceled", Cause: err}
	}
	return nil
}

func rootMkdirAll(root *os.Root, name string) error {
	if name == "." || name == "" {
		return nil
	}
	current := ""
	for _, part := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		info, err := root.Lstat(current)
		if os.IsNotExist(err) {
			if err := root.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("archive path %q contains a non-directory or symbolic link", current)
		}
	}
	return nil
}
