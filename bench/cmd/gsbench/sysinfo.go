package main

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/survivalservers/studio-go/bench/clock"
)

// sysinfo names the machine a result came from, using only what the OS already exposes.
func sysinfo() map[string]string {
	host, _ := os.Hostname()
	m := map[string]string{
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"threads":  strconv.Itoa(runtime.NumCPU()),
		"go":       runtime.Version(),
		"hostname": host,
		"clock":    clock.Source() + ", steps of " + clock.Resolution().String(),
	}
	switch runtime.GOOS {
	case "linux":
		m["cpu"] = firstField("/proc/cpuinfo", "model name")
		m["os_release"] = strings.Trim(firstField("/etc/os-release", "PRETTY_NAME="), `"`)
		if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			m["kernel"] = strings.TrimSpace(string(b))
		}
		if b, err := os.ReadFile("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"); err == nil {
			m["governor"] = strings.TrimSpace(string(b))
		}
	case "windows":
		m["cpu"] = regValue(`HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "ProcessorNameString")
		m["os_release"] = regValue(`HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductName")
		m["kernel"] = regValue(`HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "CurrentBuild")
	}
	return m
}

// firstField returns the value of the first line starting with key (after ":" or "=").
func firstField(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if !strings.HasPrefix(line, key) {
			continue
		}
		v := strings.TrimPrefix(line, key)
		v = strings.TrimLeft(v, " \t:=")
		return strings.TrimSpace(v)
	}
	return ""
}

// regValue reads one registry value through reg.exe, which every Windows has.
func regValue(path, name string) string {
	out, err := exec.Command("reg", "query", path, "/v", name).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == name {
			return strings.Join(f[2:], " ")
		}
	}
	return ""
}
