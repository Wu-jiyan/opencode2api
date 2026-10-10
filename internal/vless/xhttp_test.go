package vless

import (
	"encoding/json"
	"strings"
	"testing"
)

// Documentation values only: reserved example.com names and the reserved
// 550e8400- UUID range, so no real endpoint or credential enters the repository.
const (
	xhttpLink = "vless://550e8400-e29b-41d4-a716-446655440001@tunnel.example.com:443" +
		"?security=tls&type=xhttp&mode=stream-one" +
		"&extra=%7B%22xPaddingObfsMode%22%3Atrue%2C%22xPaddingHeader%22%3A%22abcd12%22%7D" +
		"&host=tunnel.example.com&sni=tunnel.example.com&path=%2Fep%2Fdload" +
		"&encryption=none#Example"
)

// An xhttp node whose Worker requires padding obfuscation only connects when
// both the mode and the extras reach xray. Losing either one fails during the
// TLS handshake, which surfaces upstream as an opaque EOF.
func TestParseURIKeptsXHTTPModeAndExtras(t *testing.T) {
	node, err := ParseURI(xhttpLink)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if node.XHTTPMode != "stream-one" {
		t.Errorf("XHTTPMode = %q, want %q", node.XHTTPMode, "stream-one")
	}
	if got, ok := node.XHTTPExtra["xPaddingObfsMode"]; !ok || got != true {
		t.Errorf("XHTTPExtra[xPaddingObfsMode] = %v, want true", got)
	}
	if got := node.XHTTPExtra["xPaddingHeader"]; got != "abcd12" {
		t.Errorf("XHTTPExtra[xPaddingHeader] = %v, want %q", got, "abcd12")
	}
}

// A malformed extras object must not reject the link: a node without padding is
// still valid for every Worker that does not ask for it.
func TestParseURIIgnoresMalformedExtras(t *testing.T) {
	link := "vless://550e8400-e29b-41d4-a716-446655440002@tunnel.example.com:443" +
		"?security=tls&type=xhttp&extra=not-json&encryption=none"
	node, err := ParseURI(link)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if node.XHTTPExtra != nil {
		t.Errorf("XHTTPExtra = %v, want nil", node.XHTTPExtra)
	}
}

func TestStreamSettingsForwardsXHTTPParameters(t *testing.T) {
	node, err := ParseURI(xhttpLink)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	stream, err := streamSettings(node)
	if err != nil {
		t.Fatalf("streamSettings: %v", err)
	}
	settings, ok := stream["xhttpSettings"].(map[string]any)
	if !ok {
		t.Fatalf("xhttpSettings missing; got %#v", stream["xhttpSettings"])
	}
	if settings["mode"] != "stream-one" {
		t.Errorf("mode = %v, want stream-one", settings["mode"])
	}
	if settings["path"] != "/ep/dload" {
		t.Errorf("path = %v, want /ep/dload", settings["path"])
	}
	extra, ok := settings["extra"].(map[string]any)
	if !ok {
		t.Fatalf("extra missing; got %#v", settings["extra"])
	}
	if extra["xPaddingHeader"] != "abcd12" {
		t.Errorf("extra[xPaddingHeader] = %v, want abcd12", extra["xPaddingHeader"])
	}
	// host is not an xhttp setting and was silently dropped by xray before.
	if _, present := settings["host"]; present {
		t.Error("xhttpSettings must not carry host")
	}
}

// The rendered document must survive a JSON round trip, since it is written to
// disk and handed to xray verbatim.
func TestInstanceConfigRendersXHTTP(t *testing.T) {
	node, err := ParseURI(xhttpLink)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	body, err := instanceConfig(node, "127.0.0.1", 24100)
	if err != nil {
		t.Fatalf("instanceConfig: %v", err)
	}
	var doc struct {
		Outbounds []struct {
			StreamSettings map[string]any `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("instanceConfig produced invalid JSON: %v", err)
	}
	if len(doc.Outbounds) != 1 {
		t.Fatalf("got %d outbounds, want 1", len(doc.Outbounds))
	}
	settings, ok := doc.Outbounds[0].StreamSettings["xhttpSettings"].(map[string]any)
	if !ok {
		t.Fatalf("xhttpSettings missing from rendered config")
	}
	if settings["mode"] != "stream-one" {
		t.Errorf("mode = %v, want stream-one", settings["mode"])
	}
	if _, ok := settings["extra"]; !ok {
		t.Error("extras were dropped from the rendered config")
	}
}

// Two links that differ only in their padding parameters reach different
// Workers, so the fingerprint has to tell them apart or a rebuilt listener
// would keep serving the stale one.
func TestFingerprintSeparatesDifferentXHTTPExtras(t *testing.T) {
	other := strings.Replace(xhttpLink, "%22xPaddingHeader%22%3A%22abcd12%22",
		"%22xPaddingHeader%22%3A%22zzzz99%22", 1)
	first, err := ParseURI(xhttpLink)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	second, err := ParseURI(other)
	if err != nil {
		t.Fatalf("ParseURI(other): %v", err)
	}
	if first.Fingerprint() == second.Fingerprint() {
		t.Error("fingerprints match although the padding header differs")
	}
	// The fingerprint must also be stable across calls, or every rotation pass
	// would consider every slot stale and rebuild the whole pool.
	if first.Fingerprint() != first.Fingerprint() {
		t.Error("fingerprint is not stable across calls")
	}
}