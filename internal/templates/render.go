package templates

import (
	"bytes"
	"embed"
	"fmt"
	"path"
	"strings"
	"text/template"

	"forge/internal/project"
)

//go:embed assets/*
var assets embed.FS

type templateData struct {
	Project      string
	Path         string
	Frontend     bool
	Backend      bool
	Python       bool
	Docker       bool
	FrontendPath string
	BackendPath  string
	PythonPath   string
}

// Render returns the company-owned files for c without touching the filesystem.
func Render(c *project.Config) (map[string][]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("render templates: nil project config")
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("render templates: %w", err)
	}
	files := map[string][]byte{}
	add := func(output, asset string, data templateData) error {
		if !portable(output) {
			return fmt.Errorf("render templates: unsafe output path %q", output)
		}
		body, err := assets.ReadFile("assets/" + asset)
		if err != nil {
			return err
		}
		// GitHub expressions also use double braces; shield their opener while
		// executing Go templates, then restore it in the rendered workflow.
		source := strings.ReplaceAll(string(body), "${{", "$<<")
		t, err := template.New(asset).Option("missingkey=error").Parse(source)
		if err != nil {
			return fmt.Errorf("parse %s: %w", asset, err)
		}
		var b bytes.Buffer
		if err := t.Execute(&b, data); err != nil {
			return fmt.Errorf("render %s: %w", asset, err)
		}
		files[output] = bytes.ReplaceAll(b.Bytes(), []byte("$<<"), []byte("${{"))
		return nil
	}
	base := templateData{Project: c.Project, Frontend: c.Frontend != nil, Backend: c.Backend != nil, Python: c.Python != nil, Docker: c.Container.Enabled}
	if c.Frontend != nil {
		base.FrontendPath = c.Frontend.Path
	}
	if c.Backend != nil {
		base.BackendPath = c.Backend.Path
	}
	if c.Python != nil {
		base.PythonPath = c.Python.Path
	}
	for output, asset := range map[string]string{"README.md": "README.md.tmpl", "AGENTS.md": "AGENTS.md.tmpl", ".gitignore": "gitignore.tmpl", "docs/.gitkeep": "empty.tmpl", "scripts/.gitkeep": "empty.tmpl", "deploy/.gitkeep": "empty.tmpl"} {
		if err := add(output, asset, base); err != nil {
			return nil, err
		}
	}
	components := []struct {
		name      string
		component *project.Component
	}{{"frontend", c.Frontend}, {"backend", c.Backend}, {"python", c.Python}}
	if c.CI.Provider == "gitlab" {
		if err := add(".gitlab-ci.yml", "gitlab-root.yml.tmpl", base); err != nil {
			return nil, err
		}
		for _, x := range components {
			if x.component != nil {
				d := templateData{Project: c.Project, Path: x.component.Path}
				if err := add(path.Join(".gitlab", x.name+"-ci.yml"), "gitlab-"+x.name+boolSuffix(c.Container.Enabled)+".yml.tmpl", d); err != nil {
					return nil, err
				}
			}
		}
	} else if c.CI.Provider == "github" {
		for _, x := range components {
			if x.component != nil {
				d := templateData{Project: c.Project, Path: x.component.Path}
				if err := add(path.Join(".github/workflows", x.name+"-ci.yml"), "github-"+x.name+boolSuffix(c.Container.Enabled)+".yml.tmpl", d); err != nil {
					return nil, err
				}
			}
		}
	} else if c.CI.Provider != "none" {
		return nil, fmt.Errorf("render templates: unsupported CI provider %q", c.CI.Provider)
	}
	if c.Container.Enabled {
		for _, x := range components {
			if x.component != nil {
				d := templateData{Project: c.Project, Path: x.component.Path}
				if err := add(path.Join(x.component.Path, "Dockerfile"), "docker-"+x.name+".tmpl", d); err != nil {
					return nil, err
				}
				dockerignore := "dockerignore.tmpl"
				if x.name == "backend" {
					dockerignore = "dockerignore-backend.tmpl"
				}
				if err := add(path.Join(x.component.Path, ".dockerignore"), dockerignore, d); err != nil {
					return nil, err
				}
			}
		}
	}
	return files, nil
}

func boolSuffix(enabled bool) string {
	if enabled {
		return "-docker"
	}
	return ""
}
func portable(name string) bool {
	return name != "" && !strings.Contains(name, "\\") && path.IsAbs(name) == false && path.Clean(name) == name && name != "." && !strings.HasPrefix(name, "../")
}
