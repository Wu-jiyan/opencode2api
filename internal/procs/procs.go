// Package procs caps the Go runtime's parallelism at the CPU budget the
// process is actually running under.
//
// Without a cap the runtime sizes GOMAXPROCS after every visible host core. On
// a Linux host that throttles this process with a cgroup quota — a Docker
// --cpus limit, a compose cpus value, a systemd CPUQuota — the scheduler still
// spins up workers for cores the budget never allows, and those threads spend
// their slices being throttled: wasted CPU and added latency on the streaming
// path. Aligning GOMAXPROCS with the quota removes that overhead. Environments
// without a quota are left exactly as the runtime configured them.
//
// Go 1.25 grows the same behavior natively; the package exists so source
// builds on the supported Go 1.24 get it too. An explicit GOMAXPROCS in the
// environment always wins, here and in the runtime.
package procs

import (
	"math"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
)

// The cgroup files are package variables so tests can supply their own trees.
var (
	cgroupV2Root  = "/sys/fs/cgroup"
	cgroupV1Root  = "/sys/fs/cgroup/cpu"
	selfCgroup    = "/proc/self/cgroup"
	cgroupV2Quota = "/sys/fs/cgroup/cpu.max"
)

// Result describes what Limit decided.
type Result struct {
	// QuotaCores is the detected CPU quota in cores, or 0 when no quota was
	// found or the environment won.
	QuotaCores float64
	// Applied reports whether GOMAXPROCS was lowered.
	Applied bool
	// Procs is the GOMAXPROCS value in effect after the call.
	Procs int
}

// Limit lowers GOMAXPROCS to the cgroup CPU quota when one is set and lower
// than the runtime default. The quota is rounded up to whole cores — the
// runtime needs at least one processor slot per parallel thread, and rounding
// a 1.5-core budget down to 1 would halve the usable capacity. The call never
// raises parallelism.
func Limit() Result {
	result := Result{Procs: runtime.GOMAXPROCS(0)}
	if os.Getenv("GOMAXPROCS") != "" {
		return result
	}
	quota, ok := detectQuota()
	if !ok || quota <= 0 {
		return result
	}
	result.QuotaCores = quota
	cores := int(math.Ceil(quota))
	if cores < 1 {
		cores = 1
	}
	if cores < result.Procs {
		result.Applied = true
		result.Procs = cores
		runtime.GOMAXPROCS(cores)
	}
	return result
}

// detectQuota reads the CPU quota of the current cgroup. The ordered lookups
// cover a container with its own cgroup namespace (the group root is the
// mount root), a process nested in a host systemd slice, and the cgroup v1
// controller layout. A false result means "no quota configured or none
// readable", which must leave the runtime untouched.
func detectQuota() (float64, bool) {
	if quota, ok := readV2(cgroupV2Quota); ok {
		return quota, true
	}
	for _, relative := range selfGroupPaths("") {
		if quota, ok := readV2(path.Join(cgroupV2Root, relative, "cpu.max")); ok {
			return quota, true
		}
	}
	if quota, ok := readV1(path.Join(cgroupV1Root, "cpu.cfs_quota_us"), path.Join(cgroupV1Root, "cpu.cfs_period_us")); ok {
		return quota, true
	}
	for _, relative := range selfGroupPaths("cpu") {
		group := path.Join(cgroupV1Root, relative)
		if quota, ok := readV1(path.Join(group, "cpu.cfs_quota_us"), path.Join(group, "cpu.cfs_period_us")); ok {
			return quota, true
		}
	}
	return 0, false
}

// selfGroupPaths returns the cgroup-relative paths /proc/self/cgroup reports
// for the unified hierarchy (empty controller) or for the named v1 controller.
func selfGroupPaths(controller string) []string {
	data, err := os.ReadFile(selfCgroup)
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 {
			continue
		}
		names, relative := parts[1], parts[2]
		if controller == "" {
			// The unified hierarchy reports an empty controller list.
			if names != "" {
				continue
			}
		} else {
			matched := false
			for _, name := range strings.Fields(names) {
				if name == controller {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if relative == "" || relative == "/" {
			continue // the direct root reads above already covered it
		}
		paths = append(paths, relative)
	}
	return paths
}

// readV2 parses a cgroup v2 cpu.max file: "<quota|max> <period>".
func readV2(file string) (float64, bool) {
	body, err := os.ReadFile(file)
	if err != nil {
		return 0, false
	}
	return parseV2(string(body))
}

func parseV2(content string) (float64, bool) {
	fields := strings.Fields(strings.TrimSpace(content))
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false
	}
	quota, err1 := strconv.ParseFloat(fields[0], 64)
	period, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || quota <= 0 || period <= 0 {
		return 0, false
	}
	return quota / period, true
}

// readV1 parses the cgroup v1 CFS quota pair. A quota of -1 means unlimited.
func readV1(quotaFile, periodFile string) (float64, bool) {
	rawQuota, err := os.ReadFile(quotaFile)
	if err != nil {
		return 0, false
	}
	rawPeriod, err := os.ReadFile(periodFile)
	if err != nil {
		return 0, false
	}
	return parseV1(string(rawQuota), string(rawPeriod))
}

func parseV1(rawQuota, rawPeriod string) (float64, bool) {
	quota, err1 := strconv.ParseFloat(strings.TrimSpace(rawQuota), 64)
	period, err2 := strconv.ParseFloat(strings.TrimSpace(rawPeriod), 64)
	if err1 != nil || err2 != nil || quota <= 0 || period <= 0 {
		return 0, false
	}
	return quota / period, true
}
