//go:build windows

package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsBatchWrapperRunsFromSpacedParenthesesPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Program Files (x86)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "argument printer.cmd")
	content := "@echo off\r\nsetlocal DisableDelayedExpansion\r\necho [%~1]\r\necho [%~2]\r\n"
	if err := os.WriteFile(wrapper, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := (OSRunner{}).Run(context.Background(), Spec{
		Command: wrapper,
		Args:    []string{"first value", "(second value)"},
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := strings.ReplaceAll(stdout.String(), "\r\n", "\n")
	if got != "[first value]\n[(second value)]\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestWindowsBatchUsesVerbatimCmdCommandLine(t *testing.T) {
	cmd, err := commandContext(context.Background(), `C:\Program Files (x86)\nodejs\npm.cmd`, []string{"install value"})
	if err != nil {
		t.Fatal(err)
	}
	want := `/d /s /v:off /c ""C:\Program Files (x86)\nodejs\npm.cmd" "install value""`
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil, want a verbatim Windows command line")
	}
	if cmd.SysProcAttr.CmdLine != want {
		t.Fatalf("CmdLine = %q, want %q", cmd.SysProcAttr.CmdLine, want)
	}
}
