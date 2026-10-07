package procs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseV2(t *testing.T) {
	cases := []struct {
		content  string
		expected float64
		detected bool
	}{
		{"150000 100000\n", 1.5, true},
		{"100000 100000", 1.0, true},
		{"max 100000\n", 0, false},
		{"max", 0, false},
		{"", 0, false},
		{"0 100000", 0, false},
		{"-1 100000", 0, false},
		{"garbage", 0, false},
	}
	for _, testCase := range cases {
		quota, ok := parseV2(testCase.content)
		if ok != testCase.detected || (ok && quota != testCase.expected) {
			t.Errorf("parseV2(%q) = (%v, %v), want (%v, %v)", testCase.content, quota, ok, testCase.expected, testCase.detected)
		}
	}
}

func TestParseV1(t *testing.T) {
	if quota, ok := parseV1("200000\n", "100000\n"); !ok || quota != 2 {
		t.Errorf("parseV1 quota pair = (%v, %v), want (2, true)", quota, ok)
	}
	if _, ok := parseV1("-1\n", "100000\n"); ok {
		t.Error("an unlimited v1 quota (-1) must not be detected as a cap")
	}
	if _, ok := parseV1("garbage\n", "100000\n"); ok {
		t.Error("garbage must not be detected as a cap")
	}
}

func TestSelfGroupPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cgroup")
	prev := selfCgroup
	selfCgroup = file
	defer func() { selfCgroup = prev }()

	procSelfCgroup := "12:pids:/system.slice/opencode2api.service\n10:cpu:/system.slice/opencode2api.service\n0::/system.slice/opencode2api.service\n"
	if err := os.WriteFile(file, []byte(procSelfCgroup), 0o600); err != nil {
		t.Fatal(err)
	}
	unified := selfGroupPaths("")
	if len(unified) != 1 || unified[0] != "/system.slice/opencode2api.service" {
		t.Errorf("unified paths = %v, want the single 0:: path", unified)
	}
	v1 := selfGroupPaths("cpu")
	if len(v1) != 1 || v1[0] != "/system.slice/opencode2api.service" {
		t.Errorf("cpu paths = %v, want the single cpu path", v1)
	}
	if other := selfGroupPaths("memory"); len(other) != 0 {
		t.Errorf("memory paths = %v, want none", other)
	}

	// A container's own root reports "/" which the direct root reads cover.
	if err := os.WriteFile(file, []byte("0::/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if paths := selfGroupPaths(""); len(paths) != 0 {
		t.Errorf("root relative path must be skipped, got %v", paths)
	}
}
