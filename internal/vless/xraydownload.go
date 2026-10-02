package vless

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// xrayAsset returns the official release asset name for the running platform.
// The naming follows Xray's own matrix: 64 means amd64, and 32-bit ARM uses
// the v7a suffix.
func xrayAsset() (string, error) {
	var osPart, archPart string
	switch runtime.GOOS {
	case "linux":
		osPart = "linux"
	case "windows":
		osPart = "windows"
	case "darwin":
		osPart = "macos"
	default:
		return "", fmt.Errorf("automatic xray download is not supported on %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		archPart = "64"
	case "arm64":
		archPart = "arm64-v8a"
	case "386":
		archPart = "32"
	case "arm":
		archPart = "arm32-v7a"
	default:
		return "", fmt.Errorf("automatic xray download is not supported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return "Xray-" + osPart + "-" + archPart + ".zip", nil
}

func xrayExecutableName() string {
	if runtime.GOOS == "windows" {
		return "xray.exe"
	}
	return "xray"
}

// Release mirrors for the Xray-core release assets, expressed as URL prefixes
// placed in front of xrayOrigin. An empty prefix therefore means "use the
// authoritative host directly". Mirrors are tried in the listed order and the
// first verified archive wins.
//
// The order reflects measured sustained throughput for the ~20 MiB archive on a
// mainland connection, not ping latency: gh-proxy.com sustains roughly
// 0.5 MiB/s, ghfast.top an order of magnitude less, ghproxy.net almost none,
// and a direct copy of GitHub frequently times out entirely. A host that
// answers a HEAD request quickly can still be far too slow for the payload, so
// the ordering deliberately favours sustained bandwidth.
var xrayMirrors = []string{
	"https://gh-proxy.com/",
	"https://ghfast.top/",
	"https://ghproxy.net/",
	"",
}

const (
	xrayOrigin            = "https://github.com"
	xrayLatestReleaseAPI  = "https://api.github.com/repos/XTLS/Xray-core/releases/latest"
	mirroredReleaseFormat = "%s%s/XTLS/Xray-core/releases/download/%s/%s"
	// Each mirror gets its own budget so one unreachable host cannot stall the
	// chain for long.
	xrayProbeTimeout = 15 * time.Second
	// Fetching a ~20 MiB archive plus its digest needs a far larger budget than
	// the metadata probe.
	xrayDownloadTimeout = 10 * time.Minute
	// The release archive is roughly 20 MiB; the cap only exists so a captive
	// portal or an error page cannot fill the disk.
	xrayArchiveLimit = 128 << 20
)

// resolveXrayRelease picks the release tag to install. A pinned version is used
// as-is; otherwise the latest release is queried from the GitHub API.
func resolveXrayRelease(ctx context.Context, version string) (string, error) {
	version = strings.TrimSpace(version)
	if version != "" {
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		return version, nil
	}
	response, err := get(ctx, xrayLatestReleaseAPI, xrayProbeTimeout)
	if err != nil {
		return "", fmt.Errorf("query latest xray release: %w", err)
	}
	defer drainBody(response)
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("query latest xray release: unexpected status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read latest xray release: %w", err)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("parse latest xray release: %w", err)
	}
	if release.TagName == "" {
		return "", errors.New("latest xray release response has no tag_name")
	}
	return release.TagName, nil
}

func releaseAssetURL(mirror, tag, asset string) string {
	return fmt.Sprintf(mirroredReleaseFormat, mirror, xrayOrigin, tag, asset)
}

// fetchVerified retrieves one asset and verifies the SHA2-256 published next to
// it. The digest is fetched from the same mirror, so an accelerator cannot
// serve a substituted archive without also rewriting the checksum.
func fetchVerified(ctx context.Context, mirror, tag, asset string) ([]byte, error) {
	digest, err := fetchText(ctx, releaseAssetURL(mirror, tag, asset+".dgst"))
	if err != nil {
		return nil, err
	}
	expected := parseDigest(digest)
	if expected == "" {
		return nil, errors.New("checksum file has no SHA2-256 entry")
	}
	payload, err := fetchBytes(ctx, releaseAssetURL(mirror, tag, asset), xrayArchiveLimit)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	if actual := hex.EncodeToString(sum[:]); actual != expected {
		return nil, fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
	}
	return payload, nil
}

// parseDigest pulls the SHA2-256 line out of an Xray .dgst file. The file also
// carries MD5, SHA1, and SHA2-512; only the strongest widely available digest
// is used.
func parseDigest(content string) string {
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "SHA2-256") {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

// installXray downloads the release archive into dir and returns the path of
// the extracted executable.
func installXray(ctx context.Context, dir, version string, logger *slog.Logger) (string, error) {
	asset, err := xrayAsset()
	if err != nil {
		return "", err
	}
	tag, err := resolveXrayRelease(ctx, version)
	if err != nil {
		return "", err
	}
	var lastErr error
	for _, mirror := range xrayMirrors {
		payload, err := fetchVerified(ctx, mirror, tag, asset)
		if err != nil {
			lastErr = err
			if logger != nil {
				logger.Warn("xray mirror attempt failed", "component", "vless", "event", "xray_mirror_failed",
					"mirror", displayMirror(mirror), "error", err)
			}
			continue
		}
		path, err := extractExecutable(payload, dir, xrayExecutableName())
		if err != nil {
			lastErr = err
			continue
		}
		if logger != nil {
			logger.Info("xray installed", "component", "vless", "event", "xray_installed",
				"version", tag, "mirror", displayMirror(mirror), "path", path)
		}
		return path, nil
	}
	return "", fmt.Errorf("no mirror provided a verified xray archive: %w", lastErr)
}

// extractExecutable pulls the binary out of the release zip and writes it with
// the executable bit set. geoip.dat and geosite.dat are skipped: the generated
// per-node configs reference neither, so extracting them would only waste
// roughly 30 MiB of disk.
func extractExecutable(archive []byte, dir, name string) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return "", fmt.Errorf("open xray archive: %w", err)
	}
	for _, entry := range reader.File {
		if filepath.Base(entry.Name) != name || entry.FileInfo().IsDir() {
			continue
		}
		source, err := entry.Open()
		if err != nil {
			return "", fmt.Errorf("read %s from xray archive: %w", name, err)
		}
		defer source.Close()
		payload, err := io.ReadAll(io.LimitReader(source, xrayArchiveLimit))
		if err != nil {
			return "", fmt.Errorf("extract %s: %w", name, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create xray directory: %w", err)
		}
		// Write to a staging name and rename, so a failed download never leaves
		// a half-written executable at the path the pool is about to execute.
		target := filepath.Join(dir, name)
		staging := target + ".download"
		if err := os.WriteFile(staging, payload, 0o755); err != nil {
			return "", fmt.Errorf("write %s: %w", staging, err)
		}
		if err := os.Rename(staging, target); err != nil {
			_ = os.Remove(staging)
			return "", fmt.Errorf("install %s: %w", target, err)
		}
		return target, nil
	}
	return "", fmt.Errorf("xray archive does not contain %s", name)
}

// get issues a GET with an explicit timeout. Mirrors are chosen by trying them
// in order, so this only keeps each host's budget uniform.
func get(ctx context.Context, url string, timeout time.Duration) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "opencode2api/vless")
	return (&http.Client{Timeout: timeout}).Do(request)
}

func fetchBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	// The archive is ~20 MiB, so a short probe timeout would abort valid
	// downloads on a slow mirror; the size cap bounds the damage instead.
	response, err := get(ctx, url, xrayDownloadTimeout)
	if err != nil {
		return nil, err
	}
	defer drainBody(response)
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit))
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func fetchText(ctx context.Context, url string) (string, error) {
	payload, err := fetchBytes(ctx, url, 1<<20)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func drainBody(response *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
}

func displayMirror(mirror string) string {
	if mirror == "" {
		return "github.com (direct)"
	}
	return mirror
}
