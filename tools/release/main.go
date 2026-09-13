// Command release builds and packages Forge for all supported platforms.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

type target struct {
	goos, goarch string
	archive      string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	output := fs.String("output", "dist", "artifact output directory")
	version := fs.String("version", "dev", "release version")
	commit := fs.String("commit", "unknown", "source commit")
	date := fs.String("date", time.Now().UTC().Format(time.RFC3339), "build date in RFC3339 form")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := validateVersion(*version); err != nil {
		return fmt.Errorf("invalid version: %w", err)
	}
	for name, value := range map[string]string{"version": *version, "commit": *commit, "date": *date} {
		if err := validLinkValue(value); err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
	}
	stamp, err := time.Parse(time.RFC3339, *date)
	if err != nil {
		return fmt.Errorf("invalid date: %w", err)
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		return err
	}
	buildDir, err := os.MkdirTemp("", "forge-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(buildDir)

	var archives []string
	for _, target := range targets(*version) {
		binaryName := "forge"
		if target.goos == "windows" {
			binaryName += ".exe"
		}
		binary := filepath.Join(buildDir, target.goos+"-"+target.goarch, binaryName)
		if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
			return err
		}
		if err := build(binary, target, *version, *commit, *date); err != nil {
			return err
		}
		archivePath := filepath.Join(*output, target.archive)
		if target.goos == "windows" {
			err = writeZIP(archivePath, binary, binaryName, stamp)
		} else {
			err = writeTarGZ(archivePath, binary, binaryName, stamp)
		}
		if err != nil {
			return err
		}
		archives = append(archives, target.archive)
		fmt.Println(archivePath)
	}
	return writeChecksums(*output, archives)
}

var portableVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]*$`)

func validateVersion(version string) error {
	if !portableVersion.MatchString(version) || strings.HasSuffix(version, ".") {
		return fmt.Errorf("must be a portable filename segment using letters, digits, dot, underscore, plus, or hyphen")
	}
	base := strings.ToUpper(strings.SplitN(version, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
		(len(base) == 4 && ((strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9')) {
		return fmt.Errorf("must not use a Windows reserved name")
	}
	return nil
}

func targets(version string) []target {
	version = strings.TrimPrefix(version, "v")
	var result []target
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			ext := ".tar.gz"
			if goos == "windows" {
				ext = ".zip"
			}
			result = append(result, target{goos, goarch, fmt.Sprintf("forge_%s_%s_%s%s", version, goos, goarch, ext)})
		}
	}
	return result
}

func build(output string, target target, version, commit, date string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	ldflags := strings.Join([]string{
		"-s", "-w",
		"-X", "main.version=" + version,
		"-X", "main.commit=" + commit,
		"-X", "main.date=" + date,
	}, " ")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", output, "./cmd/forge")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.goos, "GOARCH="+target.goarch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build %s/%s: %w", target.goos, target.goarch, err)
	}
	return nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from working directory")
		}
		dir = parent
	}
}

func writeTarGZ(destination, source, name string, stamp time.Time) (err error) {
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer func() { err = closeWithError(out, err) }()
	gz := gzip.NewWriter(out)
	gz.Header.ModTime = stamp
	defer func() { err = closeWithError(gz, err) }()
	tw := tar.NewWriter(gz)
	defer func() { err = closeWithError(tw, err) }()
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	header := &tar.Header{Name: name, Mode: 0o755, Size: info.Size(), ModTime: stamp}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	return copyFile(tw, source)
}

func writeZIP(destination, source, name string, stamp time.Time) (err error) {
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer func() { err = closeWithError(out, err) }()
	zw := zip.NewWriter(out)
	defer func() { err = closeWithError(zw, err) }()
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o755)
	header.Modified = stamp.UTC()
	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	return copyFile(w, source)
}

func copyFile(destination io.Writer, source string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(destination, in)
	return err
}

func closeWithError(closer io.Closer, prior error) error {
	if err := closer.Close(); prior == nil {
		return err
	}
	return prior
}

func writeChecksums(dir string, archives []string) (err error) {
	sort.Strings(archives)
	out, err := os.Create(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	defer func() { err = closeWithError(out, err) }()
	for _, name := range archives {
		file, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if _, err := fmt.Fprintf(out, "%x  %s\n", hash.Sum(nil), name); err != nil {
			return err
		}
	}
	return nil
}

func validLinkValue(value string) error {
	if value == "" {
		return fmt.Errorf("must not be empty")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '\'' || r == '"' {
			return fmt.Errorf("must not contain whitespace, control characters, or quotes")
		}
	}
	return nil
}
