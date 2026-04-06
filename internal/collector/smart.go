package collector

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"

	"github.com/abaeyens/zfs-nas-dashboard/internal/config"
)

// devicePathRe validates /dev/disk/by-id/ paths from zpool status output.
// Only known device-type prefixes (ata-, nvme-, wwn-, scsi-, usb-, sas-) are
// accepted, preventing pool names and vdev group names (e.g. raidz2-0) from
// being mistaken for disk devices.
var devicePathRe = regexp.MustCompile(`^/dev/disk/by-id/(ata|nvme|wwn|scsi|usb|sas|mpath)-[a-zA-Z0-9_.:-]+$`)

// DiskInfo holds SMART data for a single disk, with pre-computed status strings.
type DiskInfo struct {
	Dev    string `json:"dev"`
	ByID   string `json:"by_id"`
	Model  string `json:"model"`
	Serial string `json:"serial"`

	Health string `json:"health"` // "PASSED" / "FAILED" / "UNKNOWN"

	Celsius        int    `json:"celsius"`
	CelsiusStatus  string `json:"celsius_status"`
	PowerOnHours   int64  `json:"power_on_hours"`
	ReallocSectors int64  `json:"reallocated_sectors"`
	ReallocStatus  string `json:"reallocated_status"`
	PendingSectors int64  `json:"pending_sectors"`
	PendingStatus  string `json:"pending_status"`
	UncorrErrors   int64  `json:"uncorrectable_errors"`
	UncorrStatus   string `json:"uncorrectable_status"`
}

// Smart discovers all disks in the ZFS pool and returns their SMART data.
// run is used to execute external commands; pass SystemRunner for production.
func Smart(cfg *config.Config, run CommandRunner) ([]DiskInfo, error) {
	devices, err := discoverDevices(cfg, run)
	if err != nil {
		return nil, err
	}

	var disks []DiskInfo
	for _, byID := range devices {
		info, err := readDisk(cfg, run, byID)
		if err != nil {
			// Log and skip rather than failing the whole request.
			continue
		}
		disks = append(disks, info)
	}
	return disks, nil
}

// SystemRunner executes commands using os/exec with argument lists (no shell).
func SystemRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// discoverDevices parses `zpool status -v` to extract /dev/disk/by-id/ paths.
func discoverDevices(cfg *config.Config, run CommandRunner) ([]string, error) {
	out, err := run("zpool", "status", "-v", cfg.PoolName)
	if err != nil {
		return nil, fmt.Errorf("zpool status: %w", err)
	}

	var devices []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		candidate := f[0]
		// Accept bare by-id names (no /dev/ prefix) and full paths.
		if !strings.Contains(candidate, "/") {
			candidate = "/dev/disk/by-id/" + candidate
		}
		if devicePathRe.MatchString(candidate) && !seen[candidate] {
			seen[candidate] = true
			devices = append(devices, candidate)
		}
	}
	return devices, nil
}

// readDisk queries smartctl for a single device.
func readDisk(cfg *config.Config, run CommandRunner, byID string) (DiskInfo, error) {
	info := DiskInfo{ByID: byID}

	// Resolve the by-id symlink to the real device node (e.g. /dev/sdc).
	// smartctl requires the actual block device so the kernel can look it up
	// in sysfs for device-type detection; the by-id path alone is not enough
	// inside a container where only the target device node is bind-mounted.
	dev := resolveDevice(byID)
	info.Dev = dev

	// -i: identity (model, serial)
	iOut, err := run("smartctl", "-i", "-j", dev)
	if err == nil {
		parseIdentity(iOut, &info)
	}

	// -A: attributes
	aOut, err := run("smartctl", "-A", "-j", dev)
	if err == nil {
		parseAttributes(aOut, cfg, &info)
	}

	// -H: overall health
	hOut, err := run("smartctl", "-H", "-j", dev)
	if err == nil {
		parseHealth(hOut, &info)
	} else {
		info.Health = "UNKNOWN"
	}

	return info, nil
}

// resolveDevice finds the canonical /dev/sdX (or /dev/nvmeXnY) that has the
// same major:minor device number as byID.  Inside a Docker container the
// by-id path is a plain device node (not a symlink), so path resolution is
// not useful; matching on Rdev is the reliable approach.
// Falls back to byID if the lookup fails for any reason.
func resolveDevice(byID string) string {
	fi, err := os.Stat(byID)
	if err != nil {
		return byID
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return byID
	}
	target := st.Rdev

	entries, err := os.ReadDir("/dev")
	if err != nil {
		return byID
	}
	for _, e := range entries {
		name := e.Name()
		if !blockDevRe.MatchString(name) {
			continue
		}
		fi2, err := os.Stat("/dev/" + name)
		if err != nil {
			continue
		}
		st2, ok2 := fi2.Sys().(*syscall.Stat_t)
		if ok2 && st2.Rdev == target {
			return "/dev/" + name
		}
	}
	return byID
}

// blockDevRe matches simple top-level block device names: sda-sdzzz, nvme0n1, etc.
var blockDevRe = regexp.MustCompile(`^(sd[a-z]+|nvme\d+n\d+|hd[a-z]+|vd[a-z]+)$`)

// ---- JSON shapes ----

type smartctlIdentityJSON struct {
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`
}

type smartctlAttrJSON struct {
	ID  int `json:"id"`
	Raw struct {
		Value int64 `json:"value"`
	} `json:"raw"`
}

type smartctlAttrsJSON struct {
	ATASmartAttributes struct {
		Table []smartctlAttrJSON `json:"table"`
	} `json:"ata_smart_attributes"`
}

type smartctlHealthJSON struct {
	SmartStatus struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
}

func parseIdentity(data []byte, info *DiskInfo) {
	var v smartctlIdentityJSON
	if err := json.Unmarshal(data, &v); err == nil {
		info.Model = v.ModelName
		info.Serial = v.SerialNumber
	}
}

func parseAttributes(data []byte, cfg *config.Config, info *DiskInfo) {
	var v smartctlAttrsJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return
	}
	attrMap := map[int]int64{}
	for _, a := range v.ATASmartAttributes.Table {
		attrMap[a.ID] = a.Raw.Value
	}

	// Temperature: prefer attr 194, fall back to 190.
	if t, ok := attrMap[194]; ok && t > 0 {
		info.Celsius = int(t)
	} else if t, ok := attrMap[190]; ok && t > 0 {
		info.Celsius = int(t)
	}
	info.CelsiusStatus = tempStatus(info.Celsius, cfg.TempWarnC, cfg.TempCritC)

	info.ReallocSectors = attrMap[5]
	info.ReallocStatus = thresholdStatus(info.ReallocSectors, int64(cfg.ReallocWarn), int64(cfg.ReallocCrit))

	info.PendingSectors = attrMap[197]
	info.PendingStatus = thresholdStatus(info.PendingSectors, int64(cfg.PendingWarn), int64(cfg.PendingCrit))

	info.UncorrErrors = attrMap[198]
	info.UncorrStatus = thresholdStatus(info.UncorrErrors, int64(cfg.UncorrWarn), int64(cfg.UncorrCrit))

	info.PowerOnHours = attrMap[9]
}

func parseHealth(data []byte, info *DiskInfo) {
	var v smartctlHealthJSON
	if err := json.Unmarshal(data, &v); err == nil {
		if v.SmartStatus.Passed {
			info.Health = "PASSED"
		} else {
			info.Health = "FAILED"
		}
	} else {
		info.Health = "UNKNOWN"
	}
}

// ---- Status helpers (shared with zfs.go) ----

func tempStatus(val, warn, crit int) string {
	switch {
	case val >= crit:
		return "red"
	case val >= warn:
		return "amber"
	default:
		return "green"
	}
}

func thresholdStatus(val, warn, crit int64) string {
	switch {
	case val >= crit:
		return "red"
	case val >= warn:
		return "amber"
	default:
		return "green"
	}
}
