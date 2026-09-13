package runner

import (
	"fmt"
	"strings"
)

// windowsBatchCommandLine produces the command text consumed by cmd.exe for a
// .cmd/.bat wrapper. Batch parsing cannot faithfully preserve several cmd.exe
// metacharacters, so those arguments are rejected rather than reinterpreted.
func windowsBatchCommandLine(path string, args []string) (string, error) {
	if err := validateBatchToken(path); err != nil {
		return "", fmt.Errorf("unsafe batch command path: %w", err)
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteBatchToken(path))
	for _, arg := range args {
		if err := validateBatchToken(arg); err != nil {
			return "", fmt.Errorf("unsafe batch argument: %w", err)
		}
		parts = append(parts, quoteBatchToken(arg))
	}
	return strings.Join(parts, " "), nil
}

func validateBatchToken(value string) error {
	if strings.ContainsAny(value, "\"&|<>^%!\r\n") {
		return fmt.Errorf("contains cmd.exe syntax")
	}
	return nil
}

func quoteBatchToken(value string) string {
	return `"` + value + `"`
}
