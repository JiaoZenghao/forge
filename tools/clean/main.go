// Command clean removes Forge's local development artifacts.
package main

import (
	"fmt"
	"os"
)

func main() {
	// Run from the repository root, as enforced by the root Taskfile.
	// Keep this list explicit: never remove shared Go caches or dependencies.
	for _, path := range []string{"dist", ".task", "coverage.out", "forge", "forge.exe"} {
		if err := os.RemoveAll(path); err != nil {
			fmt.Fprintf(os.Stderr, "clean: remove %s: %v\n", path, err)
			os.Exit(1)
		}
	}
}
