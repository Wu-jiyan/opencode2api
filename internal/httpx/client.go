package httpx

// clientUserAgent is the User-Agent a real OpenCode client sends.
//
// The gateway used to build "opencode/<version> (GOOS GOARCH; <go runtime
// version>)". That is the right shape but the wrong token: measured against the
// live gate on 2026-10-14, the Go runtime marker is the one thing unique to the
// string that is refused —
//
//	opencode/1.18.31 (linux amd64; go1.24.13)   -> 403 FreeTierError
//	opencode/1.19.0                        bare -> 403 FreeTierError
//	opencode/1.19.0 linux                       -> 200
//	opencode/1.19.0 (linux; x64)                -> 200
//	opencode/1.18.31 (windows 10.0.26100; x64)  -> 200
//	opencode/1.19.0 ai-sdk/provider-utils/4.0.23 -> 200
//	  runtime/bun/1.3.14
//
// So a bare version is not enough and a runtime token is expected, but nothing
// checks which token it is. This sends exactly what the client sends — which is
// the whole point of the mimicry — and drops the Go runtime marker, so a change
// of Go toolchain can never alter this header again.
const clientUserAgent = "opencode/1.19.0 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14"

func UserAgent() string {
	return clientUserAgent
}
