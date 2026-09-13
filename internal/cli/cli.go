// Package cli provides human and machine adapters for Forge services.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"forge/internal/apperror"
	"forge/internal/doctor"
	"forge/internal/generator"
	"forge/internal/project"
	"forge/internal/runner"
	"forge/internal/scaffold"
	"forge/internal/validate"
	"github.com/spf13/cobra"
)

type Dependencies struct {
	Runner                runner.Runner
	Generator             scaffold.Generator
	Version, Commit, Date string
}
type app struct {
	ctx                  context.Context
	in                   io.Reader
	out, errout          io.Writer
	deps                 Dependencies
	json, nonInteractive bool
	details              map[string]any
}

func jsonRequested(args []string) bool {
	enabled := false
	for _, v := range args {
		if v == "--" {
			break
		}
		if v == "--json" {
			enabled = true
		}
		if strings.HasPrefix(v, "--json=") {
			enabled = strings.TrimPrefix(v, "--json=") != "false"
		}
	}
	return enabled
}

// Execute writes exactly one JSON document in JSON mode, including parse failures.
func Execute(ctx context.Context, args []string, in io.Reader, out, errout io.Writer, deps Dependencies) int {
	if deps.Runner == nil {
		deps.Runner = runner.OSRunner{}
	}
	if deps.Version == "" {
		deps.Version = "dev"
	}
	if deps.Generator == nil {
		deps.Generator = &generator.Service{Runner: deps.Runner, Stderr: errout}
	}
	a := &app{ctx: ctx, in: in, out: out, errout: errout, deps: deps, json: jsonRequested(args), details: map[string]any{}}
	root := a.root()
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errout)
	err := root.ExecuteContext(ctx)
	if err != nil {
		err = discoveryError(err)
		a.json = jsonRequested(args)
	}
	if err != nil {
		if ctx.Err() != nil {
			err = &apperror.Error{Code: "CANCELED", Message: "Operation canceled.", Cause: ctx.Err()}
		}
		e := apperror.Normalize(err)
		if a.json {
			obj := map[string]any{"status": "error", "code": e.Code, "message": e.Message}
			if e.Dependency != "" {
				obj["dependency"] = e.Dependency
			}
			if e.Path != "" {
				obj["path"] = e.Path
			}
			if e.Command != "" {
				obj["command"] = e.Command
				obj["exitCode"] = e.ExitCode
			}
			for k, v := range a.details {
				obj[k] = v
			}
			_ = json.NewEncoder(out).Encode(obj)
		} else {
			fmt.Fprintf(errout, "%s: %s\n", e.Code, e.Message)
			if checks, ok := a.details["checks"].([]validate.Check); ok {
				for _, c := range checks {
					if c.Status == "error" {
						fmt.Fprintf(errout, "  %s: %s\n", c.Path, c.Message)
					}
				}
			}
		}
		return apperror.Exit(err)
	}
	return 0
}
func (a *app) emit(v any, human string) error {
	if a.json {
		return json.NewEncoder(a.out).Encode(v)
	}
	_, err := fmt.Fprintln(a.out, human)
	return err
}
func invalid(err error) error {
	if err == nil {
		return nil
	}
	return &apperror.Error{Code: "INVALID_ARGUMENT", Message: err.Error(), Cause: err}
}
func exact(n int) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error { return invalid(cobra.ExactArgs(n)(c, args)) }
}
func (a *app) root() *cobra.Command {
	r := &cobra.Command{Use: "forge", Short: "Create and inspect company-standard monorepos", SilenceUsage: true, SilenceErrors: true, Args: exact(0), RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	r.CompletionOptions.DisableDefaultCmd = true
	r.PersistentFlags().BoolVar(&a.json, "json", a.json, "Emit one JSON document; disables prompts")
	r.PersistentFlags().BoolVar(&a.nonInteractive, "non-interactive", false, "Never prompt; use defaults for unspecified options")
	r.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return invalid(err) })
	r.SetHelpFunc(func(c *cobra.Command, _ []string) {
		if a.json {
			_ = json.NewEncoder(a.out).Encode(map[string]any{"status": "success", "help": c.UsageString(), "description": c.Short})
		} else {
			fmt.Fprintln(a.out, c.Short)
			fmt.Fprint(a.out, c.UsageString())
		}
	})
	r.AddCommand(a.create(), a.doctor(), a.describe(), a.validate(), &cobra.Command{Use: "version", Short: "Show Forge build information", Args: exact(0), RunE: func(_ *cobra.Command, _ []string) error {
		return a.emit(map[string]any{"status": "success", "version": a.deps.Version, "commit": a.deps.Commit, "date": a.deps.Date}, "forge "+a.deps.Version)
	}})
	// Cobra's command-discovery errors occur before flag error handling.
	r.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return invalid(err) })
	r.SetHelpCommand(&cobra.Command{Use: "help [command]", Short: "Show command help", Args: func(c *cobra.Command, args []string) error { return invalid(cobra.MaximumNArgs(1)(c, args)) }, RunE: func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return r.Help()
		}
		c, _, err := r.Find(args)
		if err != nil {
			return invalid(err)
		}
		return c.Help()
	}})
	return r
}
func selectionFlags(c *cobra.Command, o *project.Options) {
	f := c.Flags()
	f.StringVar(&o.Frontend, "frontend", o.Frontend, "Frontend: nextjs|none")
	f.StringVar(&o.Backend, "backend", o.Backend, "Backend: springboot|none")
	f.IntVar(&o.Java, "java", o.Java, "Java version (21)")
	f.StringVar(&o.Python, "python", o.Python, "Python service: uv|none")
	f.StringVar(&o.CI, "ci", o.CI, "CI: gitlab|github|none")
	f.BoolVar(&o.Docker, "docker", o.Docker, "Generate Dockerfiles and image CI jobs")
}
func (a *app) create() *cobra.Command {
	o := project.Defaults()
	c := &cobra.Command{Use: "create NAME", Short: "Generate a new project without overwriting an existing destination", Args: exact(1)}
	selectionFlags(c, &o)
	c.Flags().StringVar(&o.Output, "output", o.Output, "Existing parent directory for NAME")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		o.Name = args[0]
		if !a.json && !a.nonInteractive && terminalStreams(a.in, a.errout) {
			if err := a.prompt(cmd, &o); err != nil {
				return err
			}
		}
		s := scaffold.Service{Generator: a.deps.Generator, Version: a.deps.Version, Preflight: func(ctx context.Context, c *project.Config) error {
			r := doctor.Check(ctx, a.deps.Runner, c, true)
			return r.Err()
		}}
		result, err := s.Create(a.ctx, o)
		if err != nil {
			if len(result.Validation.Checks) > 0 {
				a.details["checks"] = result.Validation.Checks
			}
			return err
		}
		return a.emit(result, "Created "+result.Path+"\nStructural validation passed.")
	}
	return c
}
func pathCommand(use, short string) (*cobra.Command, *string) {
	p := new(string)
	*p = "."
	c := &cobra.Command{Use: use, Short: short, Args: exact(0)}
	c.Flags().StringVar(p, "path", ".", "Project directory")
	return c, p
}
func (a *app) describe() *cobra.Command {
	c, p := pathCommand("describe", "Describe a project from forge.yaml")
	c.RunE = func(_ *cobra.Command, _ []string) error {
		cfg, err := project.Load(*p)
		if err != nil {
			return err
		}
		lines := []string{cfg.Project + " (" + cfg.Type + ")"}
		for _, v := range cfg.Components() {
			lines = append(lines, "  "+v.Path+": "+v.Framework)
		}
		return a.emit(struct {
			Status string `json:"status"`
			*project.Config
		}{"success", cfg}, strings.Join(lines, "\n"))
	}
	return c
}
func (a *app) validate() *cobra.Command {
	c, p := pathCommand("validate", "Validate project structure and configuration offline")
	c.RunE = func(_ *cobra.Command, _ []string) error {
		cfg, err := project.Load(*p)
		if err != nil {
			return err
		}
		r := validate.Run(*p, cfg)
		if r.Status != "success" {
			a.details["checks"] = r.Checks
			return apperror.New("VALIDATION_FAILED", "Project structural validation failed.")
		}
		return a.emit(r, fmt.Sprintf("Validated %s: %d checks passed.", cfg.Project, len(r.Checks)))
	}
	return c
}
func (a *app) doctor() *cobra.Command {
	c, p := pathCommand("doctor", "Inspect required tools, versions, and platform")
	o := project.Defaults()
	selectionFlags(c, &o)
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		var cfg *project.Config
		selection := false
		for _, n := range []string{"frontend", "backend", "java", "python", "ci", "docker"} {
			selection = selection || cmd.Flags().Changed(n)
		}
		if selection {
			o.Name = "doctor-check"
			if err := o.Validate(); err != nil {
				return err
			}
			cfg = project.New(o)
		} else {
			loaded, err := project.Load(*p)
			if err == nil {
				cfg = loaded
			} else {
				_, statErr := os.Lstat(filepath.Join(*p, project.MetadataFile))
				if cmd.Flags().Changed("path") || !os.IsNotExist(statErr) {
					return err
				}
			}
		}
		r := doctor.Check(a.ctx, a.deps.Runner, cfg, false)
		if err := r.Err(); err != nil {
			a.details["tools"] = r.Tools
			a.details["platform"] = r.Platform
			return err
		}
		if a.ctx.Err() != nil {
			return a.ctx.Err()
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Platform: %s %s\n", r.Platform.OS, r.Platform.Arch)
		for _, name := range []string{"git", "java", "mvn", "node", "npm", "npx", "uv", "docker"} {
			v := r.Tools[name]
			state := "unavailable (optional)"
			if v.Installed {
				state = strings.Split(v.Version, "\n")[0]
				if !v.Compatible {
					state += " (unsupported)"
				}
			}
			fmt.Fprintf(&b, "%-8s %s\n", name, state)
		}
		if cfg == nil {
			b.WriteString("Inventory complete. Use selection flags to check a planned project.")
		} else {
			b.WriteString("Required tools ready.")
		}
		return a.emit(r, b.String())
	}
	return c
}

// classifyDiscoveryError maps Cobra's unknown commands without parsing arbitrary service errors.
func discoveryError(err error) error {
	var e *apperror.Error
	if errors.As(err, &e) {
		return err
	}
	if strings.HasPrefix(err.Error(), "unknown command ") {
		return invalid(err)
	}
	return err
}
