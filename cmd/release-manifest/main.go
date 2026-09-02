package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ssh-manager-tui/internal/update"
)

var releaseVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func main() {
	directory := flag.String("dir", "dist", "directory containing release binaries")
	version := flag.String("version", "", "release SemVer, for example v1.2.0")
	baseURL := flag.String("base-url", "", "public release asset base URL")
	output := flag.String("output", "dist/manifest.json", "output manifest path")
	flag.Parse()
	if err := generate(*directory, *version, *baseURL, *output); err != nil {
		fmt.Fprintln(os.Stderr, "release-manifest:", err)
		os.Exit(1)
	}
}

func generate(directory, version, baseURL, output string) error {
	if !releaseVersion.MatchString(version) {
		return fmt.Errorf("invalid release version %q", version)
	}
	parsedBase, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsedBase.Scheme != "https" || parsedBase.Host == "" {
		return fmt.Errorf("base URL must be an absolute HTTPS URL")
	}
	manifest := update.Manifest{Version: version, Artifacts: make(map[string]update.Artifact)}
	artifacts := []struct {
		platform string
		name     string
	}{
		{"linux-amd64", "sshm-linux-amd64"},
		{"linux-arm64", "sshm-linux-arm64"},
		{"darwin-amd64", "sshm-darwin-amd64"},
		{"darwin-arm64", "sshm-darwin-arm64"},
		{"windows-amd64", "sshm-windows-amd64.exe"},
		{"windows-arm64", "sshm-windows-arm64.exe"},
	}
	for _, artifact := range artifacts {
		platform, name := artifact.platform, artifact.name
		filePath := filepath.Join(directory, name)
		digest, size, err := fileDigest(filePath)
		if err != nil {
			return err
		}
		manifest.Artifacts[platform] = update.Artifact{
			URL: parsedBase.String() + "/" + name, SHA256: digest, Size: size,
		}
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	return atomicWrite(output, encoded)
}

func fileDigest(filePath string) (string, int64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", filePath, err)
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash %s: %w", filePath, err)
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

func atomicWrite(target string, data []byte) error {
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".manifest-*")
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set manifest permissions: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return fmt.Errorf("replace manifest: %w", err)
	}
	return nil
}
