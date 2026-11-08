package tui

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// hostFacts are the slow-changing parts of the HOST summary. Each is read
// from /proc, /sys or /etc and costs a few small file reads.
type hostFacts struct {
	Hostname, OS, Kernel string
	BootAt               time.Time
	Iface, IP            string
	MaxGHz               float64
	Swap                 string // "zram 64 GiB + file 128 GiB"
	Storage              string // "2 × Lexar NM610 PRO 2.0 TB NVMe"
	CacheDir             string // go build cache, as the view names it
}

// hostRoots are the filesystem roots the facts are read under; tests point
// them at fixtures.
type hostRoots struct{ Proc, Sys, Etc string }

var liveHostRoots = hostRoots{Proc: "/proc", Sys: "/sys", Etc: "/etc"}

func readHostFacts(r hostRoots, now time.Time) hostFacts {
	var f hostFacts
	f.Hostname, _ = os.Hostname()
	f.OS = osPrettyName(filepath.Join(r.Etc, "os-release"))
	if b, err := os.ReadFile(filepath.Join(r.Proc, "sys/kernel/osrelease")); err == nil {
		f.Kernel = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(r.Proc, "uptime")); err == nil {
		if fields := strings.Fields(string(b)); len(fields) > 0 {
			if up, err := strconv.ParseFloat(fields[0], 64); err == nil {
				f.BootAt = now.Add(-time.Duration(up * float64(time.Second)))
			}
		}
	}
	f.Iface = defaultRouteIface(filepath.Join(r.Proc, "net/route"))
	if f.Iface != "" {
		f.IP = ifaceIPv4(f.Iface)
	}
	if b, err := os.ReadFile(filepath.Join(r.Sys, "devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq")); err == nil {
		if khz, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil {
			f.MaxGHz = khz / 1e6
		}
	}
	f.Swap = swapSummary(filepath.Join(r.Proc, "swaps"))
	f.Storage = storageSummary(filepath.Join(r.Sys, "block"))
	f.CacheDir = displayPath(goBuildCacheDir())
	return f
}

func osPrettyName(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			if u, err := strconv.Unquote(v); err == nil {
				return u
			}
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

// defaultRouteIface reads the interface of the IPv4 default route from
// /proc/net/route.
func defaultRouteIface(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 8 && fields[1] == "00000000" && fields[7] == "00000000" {
			return fields[0]
		}
	}
	return ""
}

func ifaceIPv4(name string) string {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}

// swapSummary groups /proc/swaps by kind: zram devices, files, partitions.
func swapSummary(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sizes := map[string]uint64{}
	var order []string
	for i, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) < 3 {
			continue
		}
		kind := fields[1]
		if strings.Contains(fields[0], "zram") {
			kind = "zram"
		}
		kib, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			continue
		}
		if _, ok := sizes[kind]; !ok {
			order = append(order, kind)
		}
		sizes[kind] += kib * 1024
	}
	var parts []string
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%s %s", k, gib(sizes[k])))
	}
	return strings.Join(parts, " + ")
}

// storageSummary lists the whole disks under /sys/block, grouped by model
// and size: "2 × Lexar NM610 PRO 2.0 TB NVMe".
func storageSummary(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	type disk struct{ model, size, bus string }
	count := map[disk]int{}
	var order []disk
	for _, e := range entries {
		name := e.Name()
		bus := ""
		switch {
		case strings.HasPrefix(name, "nvme"):
			bus = "NVMe"
		case strings.HasPrefix(name, "sd"), strings.HasPrefix(name, "vd"):
			bus = "disk"
		default:
			continue
		}
		model := ""
		if b, err := os.ReadFile(filepath.Join(dir, name, "device/model")); err == nil {
			model = strings.TrimSpace(string(b))
		}
		var bytes uint64
		if b, err := os.ReadFile(filepath.Join(dir, name, "size")); err == nil {
			if n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil {
				bytes = n * 512
			}
		}
		if bytes == 0 {
			continue
		}
		d := disk{model, decimalSize(bytes), bus}
		if count[d] == 0 {
			order = append(order, d)
		}
		count[d]++
	}
	var parts []string
	for _, d := range order {
		s := strings.TrimSpace(d.model + " " + d.size + " " + d.bus)
		if n := count[d]; n > 1 {
			s = fmt.Sprintf("%d × %s", n, s)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// goBuildCacheDir is where go keeps its build cache, without running go.
func goBuildCacheDir() string {
	if d := os.Getenv("GOCACHE"); d != "" && d != "off" {
		return d
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "go-build")
}

// goBuildCacheSize adds up the sizes of the files in the go build cache. It
// reads directory entries only and stops when ctx ends.
func goBuildCacheSize(ctx context.Context) (uint64, bool) {
	dir := goBuildCacheDir()
	if dir == "" {
		return 0, false
	}
	var total uint64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += uint64(info.Size())
			}
		}
		return nil
	})
	return total, err == nil
}

func displayPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(p, home); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
			return "~" + rest
		}
	}
	return p
}

// gib writes bytes in binary gigabytes, as free(1) does.
func gib(b uint64) string {
	g := float64(b) / (1 << 30)
	if g >= 10 {
		return fmt.Sprintf("%.0f GiB", g)
	}
	return fmt.Sprintf("%.1f GiB", g)
}

// decimalSize writes bytes in decimal units, as disks are sold.
func decimalSize(b uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(b)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if f >= 10 || i == 0 {
		return fmt.Sprintf("%.0f %s", f, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
