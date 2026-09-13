package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTargetsUsePortableArchiveNames(t *testing.T) {
	got := targets("v1.2.3")
	want := []string{
		"forge_1.2.3_darwin_amd64.tar.gz",
		"forge_1.2.3_darwin_arm64.tar.gz",
		"forge_1.2.3_linux_amd64.tar.gz",
		"forge_1.2.3_linux_arm64.tar.gz",
		"forge_1.2.3_windows_amd64.zip",
		"forge_1.2.3_windows_arm64.zip",
	}
	if len(got) != len(want) {
		t.Fatalf("targets length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].archive != want[i] {
			t.Errorf("target %d archive = %q, want %q", i, got[i].archive, want[i])
		}
	}
}

func TestBuildEmbedsVersionMetadata(t *testing.T) {
	binaryName := "forge"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(t.TempDir(), binaryName)
	if err := build(binary, target{goos: runtime.GOOS, goarch: runtime.GOARCH}, "1.2.3", "abc123", "2026-09-06T00:00:00Z"); err != nil {
		t.Fatalf("build() error = %v", err)
	}
	data, err := exec.Command(binary, "version", "--json").Output()
	if err != nil {
		t.Fatalf("built forge version: %v", err)
	}
	var got struct{ Version, Commit, Date string }
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode version output: %v", err)
	}
	if got.Version != "1.2.3" || got.Commit != "abc123" || got.Date != "2026-09-06T00:00:00Z" {
		t.Fatalf("metadata = %#v", got)
	}
}

func TestValidateVersionRejectsUnsafeFilenameSegments(t *testing.T) {
	for _, version := range []string{"", ".", "..", "x/../../outside", `x\outside`, "x:y", "trailing.", "CON", "com1", "has space"} {
		t.Run(version, func(t *testing.T) {
			if err := validateVersion(version); err == nil {
				t.Fatalf("validateVersion(%q) accepted unsafe version", version)
			}
		})
	}
	for _, version := range []string{"dev", "v1.2.3", "1.2.3-rc.1+build.7", "release_1"} {
		if err := validateVersion(version); err != nil {
			t.Errorf("validateVersion(%q) error = %v", version, err)
		}
	}
}

func TestInvalidVersionDoesNotCreateOutputDirectory(t *testing.T) {
	output := filepath.Join(t.TempDir(), "dist")
	err := run([]string{"--version", "x/../../outside", "--output", output})
	if err == nil {
		t.Fatal("run() accepted unsafe version")
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("output directory was touched, stat error = %v", statErr)
	}
}

func TestWriteChecksumsIncludesEveryArchive(t *testing.T) {
	dir := t.TempDir()
	for name, contents := range map[string]string{"forge_linux.tar.gz": "linux", "forge_windows.zip": "windows"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeChecksums(dir, []string{"forge_windows.zip", "forge_linux.tar.gz"}); err != nil {
		t.Fatalf("writeChecksums() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	want := "caf90169eefa5f807d577486b9f795ab86ae2983c5c20806cff959117e90af18  forge_linux.tar.gz\n" +
		"340d600392818df2413382dc7d8325c360d83ea49a262d31760348484bbc10b5  forge_windows.zip\n"
	if string(data) != want {
		t.Fatalf("SHA256SUMS = %q, want %q", data, want)
	}
	if strings.Contains(string(data), dir) {
		t.Fatal("SHA256SUMS contains a machine-specific path")
	}
}
