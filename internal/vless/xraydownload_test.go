package vless

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestXrayAssetNaming(t *testing.T) {
	asset, err := xrayAsset()
	if err != nil {
		t.Skipf("platform %s/%s has no official asset: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	want := map[string]string{
		"linux/amd64":   "Xray-linux-64.zip",
		"linux/arm64":   "Xray-linux-arm64-v8a.zip",
		"windows/amd64": "Xray-windows-64.zip",
		"darwin/arm64":  "Xray-macos-arm64-v8a.zip",
	}[runtime.GOOS+"/"+runtime.GOARCH]
	if want != "" && asset != want {
		t.Fatalf("asset = %q, want %q", asset, want)
	}
}

func TestParseDigest(t *testing.T) {
	// Shape taken from a real Xray .dgst file.
	const sample = "MD5= 4eb0d1234b56c1f3d3f1b6cd3e0f4b56\n" +
		"SHA1= 9a0b7a1e6dcb0c7ec0e8dcb0c7ec0e8dcb0c7ec0\n" +
		"SHA2-256= 23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae\n" +
		"SHA2-512= e8bc40a0\n"
	const want = "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae"
	if got := parseDigest(sample); got != want {
		t.Fatalf("parseDigest = %q, want %q", got, want)
	}
	if got := parseDigest("SHA1= abc\n"); got != "" {
		t.Fatalf("parseDigest should ignore non SHA2-256 lines, got %q", got)
	}
}

func TestReleaseAssetURL(t *testing.T) {
	if got := releaseAssetURL("https://ghfast.top/", "v1.2.3", "Xray-linux-64.zip"); got !=
		"https://ghfast.top/https://github.com/XTLS/Xray-core/releases/download/v1.2.3/Xray-linux-64.zip" {
		t.Fatalf("unexpected mirrored URL %q", got)
	}
	if got := releaseAssetURL("", "v1.2.3", "Xray-linux-64.zip"); got !=
		"https://github.com/XTLS/Xray-core/releases/download/v1.2.3/Xray-linux-64.zip" {
		t.Fatalf("unexpected direct URL %q", got)
	}
}

// TestInstallXray downloads the real release and verifies the resulting binary
// runs. It reaches the network, so it is skipped unless explicitly requested.
func TestInstallXray(t *testing.T) {
	if os.Getenv("OPENCODE2API_TEST_XRAY_DOWNLOAD") != "1" {
		t.Skip("set OPENCODE2API_TEST_XRAY_DOWNLOAD=1 to run the download test")
	}
	dir := filepath.Join(t.TempDir(), "bin", "xray")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	path, err := installXray(ctx, dir, "", nil)
	if err != nil {
		t.Fatalf("installXray: %v", err)
	}
	if filepath.Base(path) != xrayExecutableName() {
		t.Fatalf("installed %q, want %q", filepath.Base(path), xrayExecutableName())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat installed binary: %v", err)
	}
	if info.Size() < 1<<20 {
		t.Fatalf("installed binary is implausibly small: %d bytes", info.Size())
	}
	// A wrong-platform binary would fail here, which proves the asset name and
	// the extracted file agree.
	if output, err := exec.Command(path, "version").CombinedOutput(); err != nil {
		t.Fatalf("installed binary does not run: %v (%s)", err, output)
	}
	if staging := path + ".download"; fileExists(staging) {
		t.Fatalf("staging file %s was left behind", staging)
	}
}

func TestExtractExecutableRejectsMissingEntry(t *testing.T) {
	if _, err := extractExecutable([]byte("not a zip"), t.TempDir(), "xray"); err == nil {
		t.Fatal("expected an error for a malformed archive")
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
