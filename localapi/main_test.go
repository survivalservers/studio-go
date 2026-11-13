package localapi

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestMain also checks, after a full run, that the tests produced every reply the spec declares for every call,
// so a status code in openapi.yaml that the server can never return (or that no test covers) fails the build.
func TestMain(m *testing.M) {
	flag.Parse()
	code := m.Run()
	if code != 0 || spec == nil || flag.Lookup("test.run").Value.String() != "" {
		os.Exit(code)
	}
	var missing []string
	for path, item := range spec.raw["paths"].(map[string]any) {
		for method, op := range item.(map[string]any) {
			o, ok := op.(map[string]any)
			if !ok {
				continue
			}
			for status := range o["responses"].(map[string]any) {
				key := strings.ToUpper(method) + " " + path + " " + status
				if !spec.seen[key] {
					missing = append(missing, key)
				}
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		fmt.Fprintf(os.Stderr, "FAIL: replies declared in openapi.yaml that no test produced:\n  %s\n", strings.Join(missing, "\n  "))
		os.Exit(1)
	}
	os.Exit(code)
}
