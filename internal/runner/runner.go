package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"forge/internal/apperror"
)

// Spec describes one process invocation. Args are passed as distinct arguments;
// they are never concatenated into a shell command on native executables.
type Spec struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

type Runner interface {
	Run(context.Context, Spec) error
}

type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, spec Spec) error {
	if err := ctx.Err(); err != nil {
		return canceledError(spec.Command, err)
	}
	path, err := Resolve(spec.Command)
	if err != nil {
		return err
	}
	cmd, err := commandContext(ctx, path, spec.Args)
	if err != nil {
		return &apperror.Error{
			Code:    "INVALID_ARGUMENT",
			Message: "command contains an argument that cannot be executed safely",
			Command: spec.Command,
			Cause:   err,
		}
	}
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr

	err = cmd.Run()
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return canceledError(spec.Command, ctxErr)
	}
	exitCode := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	return &apperror.Error{
		Code:     "COMMAND_FAILED",
		Message:  fmt.Sprintf("command %q failed", spec.Command),
		Command:  spec.Command,
		ExitCode: exitCode,
		Cause:    err,
	}
}

func Resolve(command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", &apperror.Error{Code: "MISSING_DEPENDENCY", Message: "command is required"}
	}
	name := executableName(command, runtime.GOOS)
	path, err := exec.LookPath(name)
	if err != nil {
		return "", &apperror.Error{
			Code:       "MISSING_DEPENDENCY",
			Message:    fmt.Sprintf("required command %q was not found", command),
			Dependency: command,
			Command:    command,
			Cause:      err,
		}
	}
	return path, nil
}

func executableName(command, goos string) string {
	if goos != "windows" {
		return command
	}
	if strings.ContainsAny(command, `/\\`) {
		return command
	}
	lower := strings.ToLower(command)
	if (lower == "npm" || lower == "npx") && !strings.HasSuffix(lower, ".cmd") {
		return command + ".cmd"
	}
	return command
}

func canceledError(command string, cause error) error {
	return &apperror.Error{
		Code:    "CANCELED",
		Message: fmt.Sprintf("command %q was canceled", command),
		Command: command,
		Cause:   cause,
	}
}
