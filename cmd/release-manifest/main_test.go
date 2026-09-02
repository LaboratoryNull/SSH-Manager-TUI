package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ssh-manager-tui/internal/update"
)

func TestGenerateCreatesManifestForEverySupportedPlatform(t *testing.T) {
	directory := t.TempDir()
	artifacts := map[string]string{
		"linux-amd64": "sshm-linux-amd64", "linux-arm64": "sshm-linux-arm64",
		"darwin-amd64": "sshm-darwin-amd64", "darwin-arm64": "sshm-darwin-arm64",
		"windows-amd64": "sshm-windows-amd64.exe", "windows-arm64": "sshm-windows-arm64.exe",
	}
	for platform, name := range artifacts {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(platform), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(directory, "manifest.json")
	if err := generate(directory, "v1.2.3", "https://updates.example/v1.2.3", output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var manifest update.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "v1.2.3" || len(manifest.Artifacts) != len(artifacts) {
		t.Fatalf("manifest = %#v", manifest)
	}
	for platform, artifact := range manifest.Artifacts {
		if artifact.SHA256 == "" || artifact.Size == 0 || artifact.URL != "https://updates.example/v1.2.3/"+artifacts[platform] {
			t.Fatalf("artifact %s = %#v", platform, artifact)
		}
	}
}
