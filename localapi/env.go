package localapi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Environment variable names every game server gets. Per-port names come from PortEnv.
const (
	EnvID             = "GS_ID"
	EnvKind           = "GS_KIND"
	EnvApp            = "GS_APP"
	EnvVersion        = "GS_VERSION"
	EnvRegion         = "GS_REGION"
	EnvContinent      = "GS_CONTINENT"
	EnvPublicIP       = "GS_PUBLIC_IP"
	EnvCPU            = "GS_CPU"
	EnvMemoryMB       = "GS_MEMORY_MB"
	EnvLocalAPIURL    = "GS_LOCAL_API_URL"
	EnvToken          = "GS_TOKEN"
	EnvAllocationFile = "GS_ALLOCATION_FILE"
	EnvLogDir         = "GS_LOG_DIR"
	EnvArtifactDir    = "GS_ARTIFACT_DIR"

	// EnvAgentURLOld is GS_AGENT_URL, the older name of GS_LOCAL_API_URL. Env sets it to the same address, so a build
	// made before the rename (it reads only this name) still reaches the local API (while machines once left it out,
	// every such build ended at boot_timeout). It is not documented: new code reads GS_LOCAL_API_URL.
	// LocalAPIURLFromEnv reads it when GS_LOCAL_API_URL is not set, so a game works on a machine of either age.
	// It ends on 2026-12-01, when every live build is newer than the rename of 2026-11-07:
	// TestTheOldLocalAPINameHasAnEnd fails from that day while Env still sets it.
	EnvAgentURLOld = "GS_AGENT_URL"
)

// LocalAPIURLFromEnv returns the local API's address: GS_LOCAL_API_URL, else GS_AGENT_URL.
func LocalAPIURLFromEnv(getenv func(string) string) string {
	if u := getenv(EnvLocalAPIURL); u != "" {
		return u
	}
	return getenv(EnvAgentURLOld)
}

// RegionFromEnv returns the region and continent from GS_REGION and GS_CONTINENT.
func RegionFromEnv(getenv func(string) string) (region, continent string) {
	return getenv(EnvRegion), getenv(EnvContinent)
}

// EnvInput is everything the GS_* variables are made from.
type EnvInput struct {
	Self           Self
	CPU            float64
	MemoryMB       int
	LocalAPIURL    string
	Token          string
	AllocationFile string
	LogDir         string
	ArtifactDir    string
}

// PortEnv returns the variable names for a named port: GS_PORT_<NAME> and GS_PORT_<NAME>_INTERNAL.
func PortEnv(name string) (public, internal string) {
	n := "GS_PORT_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return n, n + "_INTERNAL"
}

// IsReserved reports whether a studio-chosen variable name collides with ours. Windows treats variable names
// without regard to case, so "gs_id" is reserved too.
func IsReserved(name string) bool {
	return len(name) >= 3 && strings.EqualFold(name[:3], "GS_")
}

// Env returns the GS_* variables as NAME=value strings, sorted by name. Empty values are left out.
func Env(in EnvInput) []string {
	s := in.Self
	m := map[string]string{
		EnvID: s.ID, EnvKind: s.Kind, EnvApp: s.App, EnvVersion: s.Version, EnvRegion: s.Region, EnvContinent: s.Continent,
		EnvPublicIP: s.PublicIP, EnvLocalAPIURL: in.LocalAPIURL, EnvAgentURLOld: in.LocalAPIURL, EnvToken: in.Token,
		EnvAllocationFile: in.AllocationFile, EnvLogDir: in.LogDir, EnvArtifactDir: in.ArtifactDir,
	}
	if in.CPU > 0 {
		m[EnvCPU] = strconv.FormatFloat(in.CPU, 'f', -1, 64)
	}
	if in.MemoryMB > 0 {
		m[EnvMemoryMB] = strconv.Itoa(in.MemoryMB)
	}
	for name, p := range s.Ports {
		pub, internal := PortEnv(name)
		m[pub] = strconv.Itoa(p.Port)
		if p.Internal > 0 {
			m[internal] = strconv.Itoa(p.Internal)
		}
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v != "" {
			out = append(out, k+"="+v)
		}
	}
	sort.Strings(out)
	return out
}

// WriteAllocationFile writes the allocation JSON so a reader only ever sees the whole file: it is written to a
// temporary file in the same folder and renamed into place. The agent writes it once per game server (on a fresh
// start before the process starts, on a ready server at allocation), so a game holding it open never blocks a rewrite.
func WriteAllocationFile(path string, a Allocation) error {
	b, err := AllocationFile(a)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".allocation-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("allocation file: %w", err)
	}
	return nil
}

// AllocationFile is the allocation file's contents (secret values left out), for a caller that writes it itself.
func AllocationFile(a Allocation) ([]byte, error) {
	b, err := json.MarshalIndent(normalAllocation(a), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ReadAllocationFile reads a file written by WriteAllocationFile.
func ReadAllocationFile(path string) (Allocation, error) {
	var a Allocation
	b, err := os.ReadFile(path)
	if err != nil {
		return a, err
	}
	return a, json.Unmarshal(b, &a)
}
