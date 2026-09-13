// Package project owns the versioned Forge project contract.
package project

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"forge/internal/apperror"
	"gopkg.in/yaml.v3"
)

const MetadataFile = "forge.yaml"

type Options struct {
	Name, Frontend, Backend, Python, CI, Output string
	Java                                        int
	Docker                                      bool
}

func Defaults() Options {
	return Options{Frontend: "nextjs", Backend: "springboot", Python: "none", CI: "gitlab", Java: 21, Docker: true, Output: "."}
}

var slug = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var componentPath = regexp.MustCompile(`^[a-z][a-z0-9_-]*(/[a-z][a-z0-9_-]*)*$`)
var reserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9]|lpt[0-9])(?:\..*)?$`)

func (o Options) Validate() error {
	if len(o.Name) > 63 || !slug.MatchString(o.Name) || reserved.MatchString(o.Name) {
		return apperror.New("INVALID_ARGUMENT", "Project name must be a lowercase slug (1–63 characters), not a Windows reserved name.")
	}
	for _, v := range []struct {
		name, value string
		allowed     []string
	}{{"frontend", o.Frontend, []string{"nextjs", "none"}}, {"backend", o.Backend, []string{"springboot", "none"}}, {"python", o.Python, []string{"uv", "none"}}, {"ci", o.CI, []string{"gitlab", "github", "none"}}} {
		valid := false
		for _, a := range v.allowed {
			valid = valid || v.value == a
		}
		if !valid {
			return apperror.New("INVALID_ARGUMENT", fmt.Sprintf("Unsupported %s %q.", v.name, v.value))
		}
	}
	if o.Java != 21 {
		return apperror.New("UNSUPPORTED_VERSION", "Forge V1 supports Java 21.")
	}
	if o.Frontend == "none" && o.Backend == "none" && o.Python == "none" {
		return apperror.New("INVALID_ARGUMENT", "Select at least one component.")
	}
	return nil
}

type Component struct {
	Path           string `yaml:"path" json:"path"`
	Framework      string `yaml:"framework" json:"framework"`
	PackageManager string `yaml:"packageManager,omitempty" json:"packageManager,omitempty"`
	BuildTool      string `yaml:"buildTool,omitempty" json:"buildTool,omitempty"`
	Version        string `yaml:"version,omitempty" json:"version,omitempty"`
	Java           int    `yaml:"java,omitempty" json:"java,omitempty"`
}
type CIConfig struct {
	Provider string `yaml:"provider" json:"provider"`
}
type ContainerConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}
type Config struct {
	SchemaVersion int             `yaml:"schemaVersion" json:"schemaVersion"`
	Project       string          `yaml:"project" json:"project"`
	Type          string          `yaml:"type" json:"type"`
	ForgeVersion  string          `yaml:"forgeVersion,omitempty" json:"forgeVersion,omitempty"`
	Frontend      *Component      `yaml:"frontend,omitempty" json:"frontend,omitempty"`
	Backend       *Component      `yaml:"backend,omitempty" json:"backend,omitempty"`
	Python        *Component      `yaml:"python,omitempty" json:"python,omitempty"`
	CI            CIConfig        `yaml:"ci" json:"ci"`
	Container     ContainerConfig `yaml:"container" json:"container"`
	ManagedFiles  []string        `yaml:"managedFiles,omitempty" json:"managedFiles,omitempty"`
}

func New(o Options) *Config {
	c := &Config{SchemaVersion: 1, Project: o.Name, Type: "monorepo", CI: CIConfig{o.CI}, Container: ContainerConfig{o.Docker}}
	if o.Frontend == "nextjs" {
		c.Frontend = &Component{Path: "frontend", Framework: "nextjs", PackageManager: "npm"}
	}
	if o.Backend == "springboot" {
		c.Backend = &Component{Path: "backend", Framework: "springboot", BuildTool: "maven", Java: o.Java}
	}
	if o.Python == "uv" {
		c.Python = &Component{Path: "services/ai", Framework: "python", PackageManager: "uv"}
	}
	return c
}
func (c *Config) Components() []Component {
	var out []Component
	for _, p := range []*Component{c.Frontend, c.Backend, c.Python} {
		if p != nil {
			out = append(out, *p)
		}
	}
	return out
}

// SafePath checks portable relative paths before converting with filepath.FromSlash.
func SafePath(p string) bool {
	if p == "" || strings.ContainsAny(p, "\\:\x00\r\n\t*?\"<>|") || strings.HasPrefix(p, "/") {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." || s == ".." || strings.TrimRight(s, " .") != s || reserved.MatchString(s) {
			return false
		}
	}
	return true
}
func (c *Config) Validate() error {
	invalid := func(s string) error { return apperror.New("INVALID_PROJECT", s) }
	if c == nil {
		return invalid("Missing Forge metadata.")
	}
	if c.SchemaVersion != 1 {
		return invalid("Unsupported forge.yaml schemaVersion; expected 1.")
	}
	if c.Type != "monorepo" {
		return invalid("Project type must be monorepo.")
	}
	o := Defaults()
	o.Name = c.Project
	o.Frontend = "none"
	o.Backend = "none"
	o.Python = "none"
	o.CI = c.CI.Provider
	if c.Frontend != nil {
		if c.Frontend.Framework != "nextjs" || c.Frontend.PackageManager != "npm" || c.Frontend.Java != 0 || c.Frontend.BuildTool != "" {
			return invalid("Invalid frontend configuration.")
		}
		o.Frontend = "nextjs"
	}
	if c.Backend != nil {
		if c.Backend.Framework != "springboot" || c.Backend.BuildTool != "maven" || c.Backend.PackageManager != "" {
			return invalid("Invalid backend configuration.")
		}
		o.Backend = "springboot"
		o.Java = c.Backend.Java
	}
	if c.Python != nil {
		if c.Python.Framework != "python" || c.Python.PackageManager != "uv" || c.Python.Java != 0 || c.Python.BuildTool != "" {
			return invalid("Invalid Python configuration.")
		}
		o.Python = "uv"
	}
	if err := o.Validate(); err != nil {
		return invalid(err.Error())
	}
	paths := []string{}
	for _, v := range c.Components() {
		if !SafePath(v.Path) || !componentPath.MatchString(v.Path) {
			return invalid("Unsafe component path: " + v.Path)
		}
		p := strings.ToLower(v.Path)
		for _, other := range paths {
			if p == other || strings.HasPrefix(p, other+"/") || strings.HasPrefix(other, p+"/") {
				return invalid("Component paths must not overlap.")
			}
		}
		paths = append(paths, p)
	}
	seen := map[string]bool{}
	for _, p := range c.ManagedFiles {
		key := strings.ToLower(p)
		if !SafePath(p) || seen[key] {
			return invalid("Unsafe or duplicate managed file path: " + p)
		}
		seen[key] = true
	}
	return nil
}
func Load(root string) (*Config, error) {
	p := filepath.Join(root, MetadataFile)
	f, err := os.Open(p)
	if err != nil {
		return nil, &apperror.Error{Code: "INVALID_PROJECT", Message: "Cannot read forge.yaml: " + err.Error(), Path: p, Cause: err}
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 1024*1024 {
		return nil, apperror.New("INVALID_PROJECT", "Forge metadata exceeds 1 MiB.")
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	var c Config
	if err = d.Decode(&c); err != nil {
		return nil, apperror.New("INVALID_PROJECT", "Invalid forge.yaml: "+err.Error())
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return nil, apperror.New("INVALID_PROJECT", "forge.yaml must contain exactly one YAML document.")
	}
	if err = c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}
func Save(root string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	p := filepath.Join(root, MetadataFile)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(b)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
