// Package doctor inspects relevant tools without installing anything.
package doctor

import (
	"context"
	"fmt"
	"forge/internal/apperror"
	"forge/internal/project"
	"forge/internal/runner"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Tool struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version,omitempty"`
	Required   bool   `json:"required"`
	Compatible bool   `json:"compatible"`
	Message    string `json:"message,omitempty"`
}
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}
type Report struct {
	Status   string          `json:"status"`
	Platform Platform        `json:"platform"`
	Tools    map[string]Tool `json:"tools"`
}

// cappedBuffer prevents verbose/misconfigured executables from consuming unbounded memory.
type cappedBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(b.b) < 4096 {
		remain := 4096 - len(b.b)
		if len(p) > remain {
			p = p[:remain]
		}
		b.b = append(b.b, p...)
	}
	return n, nil
}

var versionPatterns = map[string]*regexp.Regexp{
	"java":   regexp.MustCompile(`(?m)^(?:openjdk|java)(?: version)?\s+"?([0-9]+(?:\.[0-9]+)*)`),
	"mvn":    regexp.MustCompile(`(?m)^Apache Maven ([0-9]+(?:\.[0-9]+)*)`),
	"node":   regexp.MustCompile(`(?m)^v([0-9]+(?:\.[0-9]+)*)\s*$`),
	"npm":    regexp.MustCompile(`(?m)^([0-9]+(?:\.[0-9]+){1,2})\s*$`),
	"npx":    regexp.MustCompile(`(?m)^([0-9]+(?:\.[0-9]+){1,2})\s*$`),
	"git":    regexp.MustCompile(`(?m)^git version ([0-9]+(?:\.[0-9]+)*)`),
	"docker": regexp.MustCompile(`(?m)^Docker version ([0-9]+(?:\.[0-9]+)*)`),
	"uv":     regexp.MustCompile(`(?m)^uv ([0-9]+(?:\.[0-9]+)*)`),
}
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func toolVersion(name, output string) string {
	p := versionPatterns[name]
	if p == nil {
		return ""
	}
	m := p.FindStringSubmatch(ansi.ReplaceAllString(output, ""))
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
func compatible(name, version string) bool {
	if version == "" {
		return false
	}
	major, _ := strconv.Atoi(strings.Split(version, ".")[0])
	switch name {
	case "node":
		return major >= 22
	case "java":
		return major == 21
	case "mvn":
		return major >= 3
	case "npm", "npx":
		return major >= 10
	default:
		return true
	}
}
func Check(ctx context.Context, r runner.Runner, c *project.Config, creation bool) Report {
	result := Report{Status: "success", Platform: Platform{runtime.GOOS, runtime.GOARCH}, Tools: map[string]Tool{}}
	if r == nil {
		r = runner.OSRunner{}
	}
	required := map[string]bool{}
	if c != nil {
		if c.Frontend != nil {
			required["node"] = true
			required["npm"] = true
			required["npx"] = creation
		}
		if c.Backend != nil && !creation {
			required["java"] = true
			required["mvn"] = true
		}
		if c.Python != nil {
			required["uv"] = true
		}
	}
	for _, name := range []string{"git", "java", "mvn", "node", "npm", "npx", "uv", "docker"} {
		// Creation checks only tools it will actually execute.
		if creation && !required[name] {
			result.Tools[name] = Tool{Message: "Not required for generation."}
			continue
		}
		args := []string{"--version"}
		if name == "java" {
			args = []string{"-version"}
		}
		var b cappedBuffer
		probe, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := r.Run(probe, runner.Spec{Command: name, Args: args, Stdout: &b, Stderr: &b, Env: []string{"CI=true", "NO_COLOR=1"}})
		cancel()
		v := toolVersion(name, string(b.b))
		tool := Tool{Required: required[name], Installed: err == nil, Version: v}
		tool.Compatible = tool.Installed && compatible(name, v)
		if err != nil {
			tool.Message = "Tool unavailable or version probe failed: " + err.Error()
		} else if !tool.Compatible {
			tool.Message = fmt.Sprintf("Unsupported %s version. Require Node >=22, npm/npx >=10, Java 21, Maven >=3.", name)
		}
		if tool.Required && !tool.Compatible {
			result.Status = "error"
		}
		result.Tools[name] = tool
	}
	if ctx.Err() != nil {
		result.Status = "error"
	}
	return result
}
func (r Report) Err() error {
	for _, name := range []string{"node", "npm", "npx", "java", "mvn", "uv"} {
		t := r.Tools[name]
		if t.Required && !t.Compatible {
			code := "UNSUPPORTED_VERSION"
			if !t.Installed {
				code = "MISSING_DEPENDENCY"
			}
			return &apperror.Error{Code: code, Message: t.Message, Dependency: name}
		}
	}
	return nil
}
