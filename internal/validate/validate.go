// Package validate performs offline, read-only structural checks.
package validate

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"forge/internal/project"
	"gopkg.in/yaml.v3"
)

type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}
type Report struct {
	Status string  `json:"status"`
	Checks []Check `json:"checks"`
}
type checker struct {
	root string
	r    Report
}

func (x *checker) add(name, p string, err error) {
	c := Check{Name: name, Path: p, Status: "ok"}
	if err != nil {
		c.Status = "error"
		c.Message = err.Error()
		x.r.Status = "error"
	}
	x.r.Checks = append(x.r.Checks, c)
}
func safeStat(root, p string) (os.FileInfo, error) {
	if !project.SafePath(p) {
		return nil, fmt.Errorf("unsafe relative path")
	}
	cur := root
	var info os.FileInfo
	for _, part := range strings.Split(p, "/") {
		cur = filepath.Join(cur, part)
		var err error
		info, err = os.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlinks are not allowed in structural project paths")
		}
	}
	return info, nil
}
func (x *checker) file(p string) []byte {
	info, err := safeStat(x.root, p)
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("expected a regular file")
	}
	if err == nil && info.Size() > 8<<20 {
		err = fmt.Errorf("file exceeds 8 MiB validation limit")
	}
	var b []byte
	if err == nil {
		b, err = os.ReadFile(filepath.Join(x.root, filepath.FromSlash(p)))
	}
	x.add("file", p, err)
	return b
}
func (x *checker) dir(p string) {
	info, err := safeStat(x.root, p)
	if err == nil && !info.IsDir() {
		err = fmt.Errorf("expected directory")
	}
	x.add("directory", p, err)
}
func Run(root string, c *project.Config) Report {
	x := checker{root: root, r: Report{Status: "success", Checks: []Check{}}}
	if err := c.Validate(); err != nil {
		x.add("metadata", project.MetadataFile, err)
		return x.r
	}
	_, err := project.Load(root)
	x.add("metadata", project.MetadataFile, err)
	for _, p := range []string{"README.md", "AGENTS.md", ".gitignore"} {
		x.file(p)
	}
	for _, p := range []string{"docs", "scripts", "deploy"} {
		x.dir(p)
	}
	for _, v := range c.Components() {
		x.dir(v.Path)
		if c.Container.Enabled {
			x.docker(v)
		}
	}
	if c.Frontend != nil {
		x.frontend(*c.Frontend, c.Container.Enabled)
	}
	if c.Backend != nil {
		x.backend(*c.Backend)
	}
	if c.Python != nil {
		x.python(*c.Python)
	}
	if c.CI.Provider == "gitlab" {
		x.yaml(".gitlab-ci.yml")
		for _, v := range componentNames(c) {
			x.yaml(".gitlab/" + v + "-ci.yml")
		}
	}
	if c.CI.Provider == "github" {
		for _, v := range componentNames(c) {
			x.yaml(".github/workflows/" + v + "-ci.yml")
		}
	}
	for _, p := range c.ManagedFiles {
		x.file(p)
	}
	return x.r
}
func componentNames(c *project.Config) []string {
	var a []string
	if c.Frontend != nil {
		a = append(a, "frontend")
	}
	if c.Backend != nil {
		a = append(a, "backend")
	}
	if c.Python != nil {
		a = append(a, "python")
	}
	return a
}
func (x *checker) yaml(p string) {
	b := x.file(p)
	if b == nil {
		return
	}
	var n map[string]any
	err := yaml.Unmarshal(b, &n)
	if err == nil && len(n) == 0 {
		err = fmt.Errorf("CI configuration must contain a YAML mapping")
	}
	x.add("ci-config", p, err)
}
func (x *checker) docker(c project.Component) {
	b := x.file(c.Path + "/Dockerfile")
	x.file(c.Path + "/.dockerignore")
	if b != nil {
		var err error
		if !regexp.MustCompile(`(?m)^FROM\s+\S+`).Match(b) || !regexp.MustCompile(`(?m)^USER\s+\S+`).Match(b) {
			err = fmt.Errorf("Dockerfile must define a base image and non-root USER")
		}
		if regexp.MustCompile(`(?im)^USER\s+(root|0)(\s|$)`).Match(b) {
			err = fmt.Errorf("runtime USER must be non-root")
		}
		x.add("docker-config", c.Path+"/Dockerfile", err)
	}
}
func (x *checker) frontend(c project.Component, docker bool) {
	p := c.Path
	for _, d := range []string{"src", "public"} {
		x.dir(p + "/" + d)
	}
	x.file(p + "/tsconfig.json")
	x.file(p + "/eslint.config.mjs")
	b := x.file(p + "/package.json")
	var pkg struct{ Scripts, Dependencies, DevDependencies, Engines map[string]string }
	if b != nil {
		err := json.Unmarshal(b, &pkg)
		if err == nil {
			for _, s := range []string{"dev", "build", "start", "lint"} {
				if strings.TrimSpace(pkg.Scripts[s]) == "" {
					err = fmt.Errorf("missing npm %s script", s)
					break
				}
			}
			if err == nil && pkg.Dependencies["next"] == "" {
				err = fmt.Errorf("missing next dependency")
			}
			if err == nil && (pkg.Dependencies["react"] == "" || pkg.Dependencies["react-dom"] == "") {
				err = fmt.Errorf("missing React dependencies")
			}
		}
		x.add("node-config", p+"/package.json", err)
	}
	b = x.file(p + "/package-lock.json")
	if b != nil {
		var lock struct {
			LockfileVersion int
			Packages        map[string]struct {
				Version         string
				Dependencies    map[string]string
				DevDependencies map[string]string
			}
		}
		err := json.Unmarshal(b, &lock)
		if err == nil {
			if lock.LockfileVersion < 2 || lock.Packages["node_modules/next"].Version == "" {
				err = fmt.Errorf("expected npm lockfile with resolved Next.js dependency")
			}
			if !maps.Equal(lock.Packages[""].Dependencies, pkg.Dependencies) || !maps.Equal(lock.Packages[""].DevDependencies, pkg.DevDependencies) {
				err = fmt.Errorf("package-lock.json differs from package.json dependencies; run npm install")
			}
		}
		x.add("npm-lock", p+"/package-lock.json", err)
	}
	b = x.file(p + "/next.config.ts")
	if b != nil && docker {
		var err error
		if !regexp.MustCompile(`\boutput\s*:\s*["']standalone["']`).Match(b) {
			err = fmt.Errorf("Docker requires output: 'standalone' in Next.js configuration")
		}
		x.add("next-standalone", p+"/next.config.ts", err)
	}
}
func (x *checker) backend(c project.Component) {
	p := c.Path
	x.dir(p + "/src")
	for _, f := range []string{"mvnw", "mvnw.cmd", ".mvn/wrapper/maven-wrapper.properties"} {
		x.file(p + "/" + f)
	}
	b := x.file(p + "/pom.xml")
	if b == nil {
		return
	}
	var pom struct {
		XMLName xml.Name `xml:"project"`
		Parent  struct {
			GroupID    string `xml:"groupId"`
			ArtifactID string `xml:"artifactId"`
			Version    string `xml:"version"`
		} `xml:"parent"`
		Properties struct {
			Java string `xml:"java.version"`
		} `xml:"properties"`
		Build struct {
			Plugins []struct {
				ArtifactID string `xml:"artifactId"`
			} `xml:"plugins>plugin"`
		} `xml:"build"`
	}
	err := xml.Unmarshal(b, &pom)
	if err == nil && pom.Properties.Java != fmt.Sprint(c.Java) {
		err = fmt.Errorf("pom.xml java.version must match metadata (%d)", c.Java)
	}
	if err == nil && (pom.Parent.GroupID != "org.springframework.boot" || pom.Parent.ArtifactID != "spring-boot-starter-parent") {
		err = fmt.Errorf("expected Spring Boot parent")
	}
	if err == nil {
		found := false
		for _, pl := range pom.Build.Plugins {
			found = found || pl.ArtifactID == "spring-boot-maven-plugin"
		}
		if !found {
			err = fmt.Errorf("missing spring-boot-maven-plugin for executable JAR")
		}
	}
	x.add("java-config", p+"/pom.xml", err)
}
func (x *checker) python(c project.Component) {
	p := c.Path
	b := x.file(p + "/pyproject.toml")
	x.file(p + "/uv.lock")
	x.file(p + "/main.py")
	if b != nil {
		var err error
		if !regexp.MustCompile(`(?m)^\[project\]\s*$`).Match(b) || !regexp.MustCompile(`(?m)^requires-python\s*=\s*["'][^"']+["']`).Match(b) {
			err = fmt.Errorf("expected uv project with requires-python")
		}
		x.add("python-config", p+"/pyproject.toml", err)
	}
}
