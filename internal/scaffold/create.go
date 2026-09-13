// Package scaffold coordinates a staged, validated project creation transaction.
package scaffold

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"forge/internal/apperror"
	"forge/internal/project"
	"forge/internal/templates"
	"forge/internal/validate"
)

type Generator interface {
	Generate(context.Context, string, *project.Config) error
}
type Service struct {
	Generator Generator
	Preflight func(context.Context, *project.Config) error
	Version   string
}
type Result struct {
	Status     string          `json:"status"`
	Path       string          `json:"path"`
	Project    *project.Config `json:"project"`
	Validation validate.Report `json:"validation"`
}

func (s Service) Create(ctx context.Context, o project.Options) (result Result, err error) {
	if err = o.Validate(); err != nil {
		return result, err
	}
	parent, err := filepath.Abs(o.Output)
	if err != nil {
		return result, err
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return result, &apperror.Error{Code: "INVALID_ARGUMENT", Message: "Output parent must exist: " + err.Error(), Path: o.Output, Cause: err}
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return result, apperror.New("INVALID_ARGUMENT", "Output parent must be a directory.")
	}
	target := filepath.Join(parent, o.Name)
	if _, err = os.Lstat(target); err == nil {
		return result, exists(target)
	} else if !os.IsNotExist(err) {
		return result, err
	}
	c := project.New(o)
	c.ForgeVersion = s.Version
	if s.Preflight != nil {
		if err = s.Preflight(ctx, c); err != nil {
			return result, err
		}
	}
	if err = ctx.Err(); err != nil {
		return result, canceled(err)
	}
	lockPath := filepath.Join(parent, ".forge-"+o.Name+".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, &apperror.Error{Code: "PROJECT_ALREADY_EXISTS", Message: "Cannot reserve project name; another create may be running. Inspect " + lockPath, Path: lockPath, Cause: err}
	}
	lockInfo, _ := lock.Stat()
	if closeErr := lock.Close(); closeErr != nil {
		os.Remove(lockPath)
		return result, closeErr
	}
	defer func() {
		if current, e := os.Lstat(lockPath); e == nil && os.SameFile(lockInfo, current) {
			if e = os.Remove(lockPath); e != nil && err == nil {
				err = fmt.Errorf("project created but cannot remove reservation %s: %w", lockPath, e)
			}
		}
	}()
	stage, err := os.MkdirTemp(parent, ".forge-"+o.Name+"-")
	if err != nil {
		return result, err
	}
	published := false
	defer func() {
		if !published {
			if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
				err = &apperror.Error{Code: "GENERATION_FAILED", Message: fmt.Sprintf("Generation failed (%v); cleanup failed for %s: %v", err, stage, cleanupErr), Path: stage, Cause: err}
			}
		}
	}()
	if s.Generator == nil {
		return result, apperror.New("GENERATION_FAILED", "No ecosystem generator configured.")
	}
	if err = s.Generator.Generate(ctx, stage, c); err != nil {
		var structured *apperror.Error
		if errors.As(err, &structured) {
			return result, err
		}
		return result, &apperror.Error{Code: "GENERATION_FAILED", Message: err.Error(), Cause: err}
	}
	if err = ctx.Err(); err != nil {
		return result, canceled(err)
	}
	files, err := templates.Render(c)
	if err != nil {
		return result, &apperror.Error{Code: "GENERATION_FAILED", Message: "Render company templates: " + err.Error(), Cause: err}
	}
	for p := range files {
		c.ManagedFiles = append(c.ManagedFiles, p)
	}
	sort.Strings(c.ManagedFiles)
	for _, p := range c.ManagedFiles {
		if !project.SafePath(p) {
			return result, apperror.New("GENERATION_FAILED", "Unsafe template output path: "+p)
		}
		if err = writeNew(stage, p, files[p]); err != nil {
			return result, &apperror.Error{Code: "GENERATION_FAILED", Message: "Cannot write company file: " + err.Error(), Path: p, Cause: err}
		}
	}
	if err = project.Save(stage, c); err != nil {
		return result, err
	}
	report := validate.Run(stage, c)
	if report.Status != "success" {
		result.Validation = report
		return result, apperror.New("VALIDATION_FAILED", "Generated project failed structural validation; see checks.")
	}
	if err = ctx.Err(); err != nil {
		return result, canceled(err)
	}
	if err = publish(stage, target); err != nil {
		if _, statErr := os.Lstat(target); statErr == nil {
			return result, exists(target)
		}
		return result, &apperror.Error{Code: "GENERATION_FAILED", Message: "Cannot atomically publish project: " + err.Error(), Path: target, Cause: err}
	}
	published = true
	return Result{Status: "success", Path: target, Project: c, Validation: report}, nil
}
func exists(p string) error {
	return &apperror.Error{Code: "PROJECT_ALREADY_EXISTS", Message: "Project destination already exists; Forge will not overwrite it.", Path: p}
}
func canceled(err error) error {
	return &apperror.Error{Code: "CANCELED", Message: "Project creation canceled.", Cause: err}
}
func writeNew(root, p string, b []byte) error {
	// os.Root anchors traversal and rejects escaping symlinks in generator output.
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	local := filepath.FromSlash(p)
	if err = r.MkdirAll(filepath.Dir(local), 0755); err != nil {
		return err
	}
	f, err := r.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
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
