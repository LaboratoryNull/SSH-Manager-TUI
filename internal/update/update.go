package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	maxManifestSize = 1 << 20
	maxBinarySize   = 256 << 20
)

type Manifest struct {
	Version   string              `json:"version"`
	Artifacts map[string]Artifact `json:"artifacts"`
}

type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}

type Options struct {
	CurrentVersion string
	ManifestURL    string
	Token          string
	Executable     string
	CheckOnly      bool
	Client         *http.Client
	GOOS           string
	GOARCH         string
}

type Result struct {
	CurrentVersion string
	LatestVersion  string
	Available      bool
	Updated        bool
}

func Run(ctx context.Context, options Options) (Result, error) {
	result := Result{CurrentVersion: options.CurrentVersion}
	manifestURL, err := secureURL(options.ManifestURL)
	if err != nil {
		return result, fmt.Errorf("update manifest: %w", err)
	}
	client := updateClient(options.Client, manifestURL.Hostname())
	manifest, err := fetchManifest(ctx, client, manifestURL, options.Token)
	if err != nil {
		return result, err
	}
	result.LatestVersion = manifest.Version
	comparison, err := compareReleaseVersions(options.CurrentVersion, manifest.Version)
	if err != nil {
		return result, err
	}
	if comparison >= 0 {
		return result, nil
	}
	result.Available = true
	if options.CheckOnly {
		return result, nil
	}

	goos, goarch := options.GOOS, options.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	artifact, ok := manifest.Artifacts[goos+"-"+goarch]
	if !ok {
		return result, fmt.Errorf("release %s has no artifact for %s/%s", manifest.Version, goos, goarch)
	}
	artifactURL, err := resolveArtifactURL(manifestURL, artifact.URL)
	if err != nil {
		return result, err
	}
	target := options.Executable
	if target == "" {
		target, err = os.Executable()
		if err != nil {
			return result, fmt.Errorf("locate executable: %w", err)
		}
	}
	if resolved, resolveErr := filepath.EvalSymlinks(target); resolveErr == nil {
		target = resolved
	}
	if err := replaceExecutable(ctx, client, manifestURL.Hostname(), options.Token, artifactURL, artifact, target); err != nil {
		return result, err
	}
	result.Updated = true
	return result, nil
}

func fetchManifest(ctx context.Context, client *http.Client, manifestURL *url.URL, token string) (Manifest, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL.String(), nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("create manifest request: %w", err)
	}
	setHeaders(request, token)
	response, err := client.Do(request)
	if err != nil {
		return Manifest{}, fmt.Errorf("download update manifest: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("download update manifest: HTTP %s", response.Status)
	}
	var manifest Manifest
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxManifestSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode update manifest: %w", err)
	}
	if !validReleaseVersion(manifest.Version) {
		return Manifest{}, fmt.Errorf("manifest contains invalid release version %q", manifest.Version)
	}
	if len(manifest.Artifacts) == 0 {
		return Manifest{}, errors.New("manifest contains no artifacts")
	}
	return manifest, nil
}

func replaceExecutable(ctx context.Context, client *http.Client, manifestHost, token string, artifactURL *url.URL, artifact Artifact, target string) error {
	wantHash := strings.ToLower(strings.TrimSpace(artifact.SHA256))
	decodedHash, err := hex.DecodeString(wantHash)
	if err != nil || len(decodedHash) != sha256.Size {
		return fmt.Errorf("artifact has invalid SHA-256 checksum")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL.String(), nil)
	if err != nil {
		return fmt.Errorf("create artifact request: %w", err)
	}
	if strings.EqualFold(artifactURL.Hostname(), manifestHost) {
		setHeaders(request, token)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download update: HTTP %s", response.Status)
	}

	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("inspect current executable: %w", err)
	}
	directory := filepath.Dir(target)
	temporary, err := os.CreateTemp(directory, ".sshm-update-*")
	if err != nil {
		return fmt.Errorf("create update beside %s: %w", target, err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, maxBinarySize+1))
	if copyErr != nil {
		temporary.Close()
		return fmt.Errorf("write update: %w", copyErr)
	}
	if written > maxBinarySize {
		temporary.Close()
		return fmt.Errorf("update exceeds %d MiB limit", maxBinarySize>>20)
	}
	if artifact.Size > 0 && written != artifact.Size {
		temporary.Close()
		return fmt.Errorf("update size is %d bytes, expected %d", written, artifact.Size)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), wantHash) {
		temporary.Close()
		return errors.New("downloaded update failed SHA-256 verification")
	}
	mode := info.Mode().Perm()
	if mode&0o111 == 0 {
		mode = 0o755
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set update permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync update: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close update: %w", err)
	}
	if err := os.Rename(temporaryName, target); err != nil {
		return fmt.Errorf("replace %s: %w", target, err)
	}
	if directoryHandle, err := os.Open(directory); err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return nil
}

func secureURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid URL %q", value)
	}
	if parsed.Scheme == "https" {
		return parsed, nil
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return parsed, nil
	}
	return nil, fmt.Errorf("URL must use HTTPS")
}

func resolveArtifactURL(manifestURL *url.URL, value string) (*url.URL, error) {
	reference, err := url.Parse(strings.TrimSpace(value))
	if err != nil || value == "" {
		return nil, fmt.Errorf("invalid artifact URL %q", value)
	}
	return secureURL(manifestURL.ResolveReference(reference).String())
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func setHeaders(request *http.Request, token string) {
	request.Header.Set("Accept", "application/json, application/octet-stream")
	request.Header.Set("User-Agent", "sshm-updater")
	if token != "" {
		request.Header.Set("PRIVATE-TOKEN", token)
	}
}

func updateClient(source *http.Client, manifestHost string) *http.Client {
	client := &http.Client{Timeout: 2 * time.Minute}
	if source != nil {
		*client = *source
		if client.Timeout == 0 {
			client.Timeout = 2 * time.Minute
		}
	}
	previousCheck := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if previousCheck != nil {
			if err := previousCheck(request, via); err != nil {
				return err
			}
		}
		if !strings.EqualFold(request.URL.Hostname(), manifestHost) {
			request.Header.Del("PRIVATE-TOKEN")
		}
		return nil
	}
	return client
}

func compareReleaseVersions(current, latest string) (int, error) {
	latestVersion, ok := parseSemver(latest)
	if !ok {
		return 0, fmt.Errorf("invalid latest release version %q", latest)
	}
	currentVersion, ok := parseSemver(current)
	if !ok {
		if strings.TrimPrefix(current, "v") == strings.TrimPrefix(latest, "v") {
			return 0, nil
		}
		return -1, nil
	}
	return currentVersion.compare(latestVersion), nil
}

func validReleaseVersion(value string) bool {
	_, ok := parseSemver(value)
	return ok
}

type semver struct {
	major, minor, patch int
	prerelease          []string
}

func parseSemver(value string) (semver, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	value, _, _ = strings.Cut(value, "+")
	core, prerelease, hasPrerelease := strings.Cut(value, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	numbers := make([]int, 3)
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semver{}, false
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return semver{}, false
		}
		numbers[index] = number
	}
	parsed := semver{major: numbers[0], minor: numbers[1], patch: numbers[2]}
	if hasPrerelease {
		if prerelease == "" {
			return semver{}, false
		}
		parsed.prerelease = strings.Split(prerelease, ".")
		for _, identifier := range parsed.prerelease {
			if identifier == "" {
				return semver{}, false
			}
			for _, character := range identifier {
				if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-') {
					return semver{}, false
				}
			}
		}
	}
	return parsed, true
}

func (left semver) compare(right semver) int {
	for _, pair := range [][2]int{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for index := 0; index < min(len(left.prerelease), len(right.prerelease)); index++ {
		comparison := compareIdentifier(left.prerelease[index], right.prerelease[index])
		if comparison != 0 {
			return comparison
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func compareIdentifier(left, right string) int {
	leftNumber, leftErr := strconv.Atoi(left)
	rightNumber, rightErr := strconv.Atoi(right)
	switch {
	case leftErr == nil && rightErr == nil:
		if leftNumber < rightNumber {
			return -1
		}
		if leftNumber > rightNumber {
			return 1
		}
		return 0
	case leftErr == nil:
		return -1
	case rightErr == nil:
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
