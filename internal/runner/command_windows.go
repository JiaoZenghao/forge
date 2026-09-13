//go:build windows

package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func commandContext(ctx context.Context, path string, args []string) (*exec.Cmd, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".cmd" && ext != ".bat" {
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.WaitDelay = time.Second
		return cmd, nil
	}
	line, err := windowsBatchCommandLine(path, args)
	if err != nil {
		return nil, err
	}
	interpreter := os.Getenv("COMSPEC")
	if interpreter == "" {
		interpreter = "cmd.exe"
	}
	cmd := exec.CommandContext(ctx, interpreter)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `/d /s /v:off /c "` + line + `"`,
	}
	cmd.WaitDelay = time.Second
	return cmd, nil
}
