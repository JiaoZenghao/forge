package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forge/internal/apperror"
)

func TestOSRunnerPassesDirectoryEnvironmentAndStreams(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	spec := helperSpec("inspect")
	spec.Dir = dir
	spec.Env = append(spec.Env, "FORGE_RUNNER_VALUE=from-spec")
	spec.Stdin = strings.NewReader("from-stdin")
	spec.Stdout = &stdout
	spec.Stderr = &stderr

	err := (OSRunner{}).Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	cwdLine, output, ok := strings.Cut(stdout.String(), "\n")
	if !ok || !strings.HasPrefix(cwdLine, "cwd=") {
		t.Fatalf("stdout missing working directory: %q", stdout.String())
	}
	cwd := strings.TrimPrefix(cwdLine, "cwd=")
	if !filepath.IsAbs(cwd) {
		t.Fatalf("child working directory is not absolute: %q", cwd)
	}
	gotDir, err := os.Stat(cwd)
	if err != nil {
		t.Fatalf("Stat(child working directory): %v", err)
	}
	wantDir, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(temp dir): %v", err)
	}
	// Windows short/long paths and symlink aliases can identify the same directory.
	if !os.SameFile(gotDir, wantDir) {
		t.Fatalf("child working directory %q differs from %q", cwd, dir)
	}
	if output != "env=from-spec\nstdin=from-stdin\n" {
		t.Fatalf("stdout after working directory = %q", output)
	}
	if stderr.String() != "helper-stderr\n" {
		t.Fatalf("stderr = %q, want helper stderr", stderr.String())
	}
}

func TestOSRunnerReturnsStructuredExitError(t *testing.T) {
	err := (OSRunner{}).Run(context.Background(), helperSpec("exit", "23"))
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("Run() error type = %T, want *apperror.Error", err)
	}
	if appErr.Code != "COMMAND_FAILED" || appErr.Command != os.Args[0] || appErr.ExitCode != 23 {
		t.Fatalf("Run() error = %#v", appErr)
	}
}

func TestOSRunnerCancellationStopsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := (OSRunner{}).Run(ctx, helperSpec("sleep"))
	if time.Since(started) > 3*time.Second {
		t.Fatalf("Run() did not promptly stop canceled child")
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "CANCELED" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %#v, want CANCELED wrapping deadline", err)
	}
}

func TestOSRunnerCancellationDoesNotWaitForDescendantPipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	var stdout bytes.Buffer
	spec := helperSpec("spawn-descendant")
	spec.Stdout = &stdout
	err := (OSRunner{}).Run(ctx, spec)
	if time.Since(started) > 3*time.Second {
		t.Fatalf("Run() waited for a descendant holding inherited output")
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "CANCELED" {
		t.Fatalf("Run() error = %#v, want CANCELED", err)
	}
}

func TestOSRunnerPreservesLiteralArguments(t *testing.T) {
	args := []string{"space value", "semi;colon", "$(not-a-command)", "quote'and\"double", "*"}
	var stdout bytes.Buffer
	spec := helperSpec(append([]string{"args"}, args...)...)
	spec.Stdout = &stdout
	if err := (OSRunner{}).Run(context.Background(), spec); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := strings.Join(args, "\n") + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want literal args %q", stdout.String(), want)
	}
}

func TestExecutableName(t *testing.T) {
	tests := []struct{ command, goos, want string }{
		{"npm", "windows", "npm.cmd"},
		{"npx", "windows", "npx.cmd"},
		{"node", "windows", "node"},
		{"npm.cmd", "windows", "npm.cmd"},
		{"npm", "linux", "npm"},
	}
	for _, tt := range tests {
		if got := executableName(tt.command, tt.goos); got != tt.want {
			t.Errorf("executableName(%q, %q) = %q, want %q", tt.command, tt.goos, got, tt.want)
		}
	}
}

func TestWindowsBatchCommandLineQuotesRepresentableArguments(t *testing.T) {
	got, err := windowsBatchCommandLine(`C:\Program Files\nodejs\npx.cmd`, []string{"create-next-app@latest", "space value", "", "--yes"})
	if err != nil {
		t.Fatalf("windowsBatchCommandLine() error = %v", err)
	}
	want := `"C:\Program Files\nodejs\npx.cmd" "create-next-app@latest" "space value" "" "--yes"`
	if got != want {
		t.Fatalf("command line = %q, want %q", got, want)
	}
}

func TestWindowsBatchCommandLineRejectsInterpreterSyntax(t *testing.T) {
	for _, arg := range []string{`a&whoami`, `a|whoami`, `>file`, `%PATH%`, `a^b`, "line\nbreak", `has"quote`} {
		t.Run(arg, func(t *testing.T) {
			if _, err := windowsBatchCommandLine(`C:\node\npm.cmd`, []string{arg}); err == nil {
				t.Fatalf("windowsBatchCommandLine(%q) accepted unsafe argument", arg)
			}
		})
	}
}

func TestResolveFindsExecutable(t *testing.T) {
	got, err := Resolve(os.Args[0])
	if err != nil {
		t.Fatalf("Resolve(test executable) error = %v", err)
	}
	if !filepath.IsAbs(got) && !strings.ContainsRune(got, filepath.Separator) {
		t.Fatalf("Resolve(test executable) = %q, want resolved path", got)
	}
}

func helperSpec(args ...string) Spec {
	return Spec{
		Command: os.Args[0],
		Args:    append([]string{"-test.run=TestRunnerHelperProcess", "--"}, args...),
		Env:     []string{"GO_WANT_FORGE_RUNNER_HELPER=1"},
	}
}

func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FORGE_RUNNER_HELPER") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(97)
	}
	args := os.Args[separator+1:]
	switch args[0] {
	case "inspect":
		data := make([]byte, 64)
		n, _ := os.Stdin.Read(data)
		cwd, _ := os.Getwd()
		fmt.Printf("cwd=%s\nenv=%s\nstdin=%s\n", cwd, os.Getenv("FORGE_RUNNER_VALUE"), data[:n])
		fmt.Fprintln(os.Stderr, "helper-stderr")
	case "args":
		for _, arg := range args[1:] {
			fmt.Println(arg)
		}
	case "exit":
		if args[1] == "23" {
			os.Exit(23)
		}
		os.Exit(98)
	case "sleep":
		time.Sleep(30 * time.Second)
	case "spawn-descendant":
		child := exec.Command(os.Args[0], "-test.run=TestRunnerHelperProcess", "--", "sleep")
		child.Env = append(os.Environ(), "GO_WANT_FORGE_RUNNER_HELPER=1")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(96)
		}
		fmt.Println("descendant-started")
		time.Sleep(30 * time.Second)
	default:
		os.Exit(99)
	}
	os.Exit(0)
}
