package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadHostFactsFromFixtures(t *testing.T) {
	root := t.TempDir()
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("etc/os-release", "NAME=\"Arch Linux\"\nPRETTY_NAME=\"Arch Linux\"\n")
	write("proc/sys/kernel/osrelease", "7.2.11-arch1-1\n")
	write("proc/uptime", "29000.50 100.0\n")
	write("proc/net/route", "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\n"+
		"eno1\t0003A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\n"+
		"eno1\t00000000\t0103A8C0\t0003\t0\t0\t100\t00000000\n")
	write("proc/swaps", "Filename\tType\tSize\tUsed\tPriority\n/dev/zram0 partition\t67108860\t0\t100\n/swapfile file\t134217724\t0\t-2\n")
	write("sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq", "5600000\n")
	for _, d := range []string{"nvme0n1", "nvme1n1"} {
		write("sys/block/"+d+"/device/model", "Lexar SSD NM610 PRO 2TB  \n")
		write("sys/block/"+d+"/size", "4000797360\n")
	}
	write("sys/block/loop0/size", "100\n")
	now := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
	f := readHostFacts(hostRoots{Proc: filepath.Join(root, "proc"), Sys: filepath.Join(root, "sys"), Etc: filepath.Join(root, "etc")}, now)
	if f.OS != "Arch Linux" || f.Kernel != "7.2.11-arch1-1" || f.Iface != "eno1" || f.MaxGHz != 5.6 {
		t.Fatalf("facts %+v", f)
	}
	if got := now.Sub(f.BootAt); got != 29000500*time.Millisecond {
		t.Fatalf("uptime %v", got)
	}
	if f.Swap != "zram 64 GiB + file 128 GiB" {
		t.Fatalf("swap %q", f.Swap)
	}
	if f.Storage != "2 × Lexar SSD NM610 PRO 2TB 2.0 TB NVMe" {
		t.Fatalf("storage %q", f.Storage)
	}
}

func TestGoBuildCacheSize(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOCACHE", dir)
	if err := os.MkdirAll(filepath.Join(dir, "0a"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i, n := range []int{100, 2000} {
		if err := os.WriteFile(filepath.Join(dir, "0a", string(rune('a'+i))), make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n, ok := goBuildCacheSize(context.Background()); !ok || n != 2100 {
		t.Fatalf("size %d %v", n, ok)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := goBuildCacheSize(ctx); ok {
		t.Fatal("cancelled walk reported a size")
	}
}
