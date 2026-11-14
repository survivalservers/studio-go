package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// notices.sh covers the modules built into a program and leaves out ours: those replaced by a folder of ours.
// A module replaced by another module is covered under the one that was built in.
func TestNoticesListsOnlyOthersModules(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("notices.sh is a bash script; release builds run on Linux")
	}
	info := "/tmp/survivalservers: go1.27.3\n" +
		"\tpath\tgithub.com/survivalservers/studio-go/cli/cmd/survivalservers\n" +
		"\tmod\tgithub.com/survivalservers/studio-go/cli\t(devel)\t\n" +
		"\tdep\tgithub.com/google/jsonschema-go\tv0.4.3\th1:abc=\n" +
		"\tdep\tgithub.com/survivalservers/studio-go\tv0.0.0\t\n" +
		"\t=>\t../client\t\t\n" +
		"\tdep\tgithub.com/survivalservers/studio-go/mcp\tv0.0.0\t\n" +
		"\t=>\t../mcp\t(devel)\t\n" +
		"\tdep\tgolang.org/x/sys\tv0.41.0\th1:def=\n" +
		"\tdep\texample.org/old\tv1.0.0\t\n" +
		"\t=>\texample.org/fork\tv1.0.1\th1:ghi=\n" +
		"\tbuild\t-trimpath=true\n"
	cmd := exec.Command("bash", "notices.sh", "--list", "-")
	cmd.Stdin = strings.NewReader(info)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("notices.sh --list: %v\n%s", err, out)
	}
	want := "github.com/google/jsonschema-go v0.4.3\ngolang.org/x/sys v0.41.0\nexample.org/fork v1.0.1\n"
	if string(out) != want {
		t.Errorf("modules covered:\n%s\nwant:\n%s", out, want)
	}
}

// The notices for the real program: Go's own licence, then every module built into it with its licence text, and
// none of ours. Release builds (release.sh, the SDK's release/build.sh) put this file beside the program.
func TestNoticesCoverEveryModuleBuiltIntoTheProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("notices.sh is a bash script; release builds run on Linux")
	}
	dir := t.TempDir()
	prog := filepath.Join(dir, "survivalservers")
	if out, err := exec.Command("go", "build", "-trimpath", "-o", prog, "./cmd/survivalservers").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	list, err := exec.Command("bash", "notices.sh", "--list", prog).Output()
	if err != nil {
		t.Fatalf("notices.sh --list: %v", err)
	}
	modules := strings.Fields(strings.ReplaceAll(string(list), "\n", " "))
	if len(modules) < 2 || len(modules)%2 != 0 {
		t.Fatalf("modules covered: %q", list)
	}
	file := filepath.Join(dir, "THIRD_PARTY_NOTICES.txt")
	if out, err := exec.Command("bash", "notices.sh", prog, file).CombinedOutput(); err != nil {
		t.Fatalf("notices.sh: %v\n%s", err, out)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.HasPrefix(text, "Third-party notices for survivalservers\n") || !strings.Contains(text, "\n==== Go go1.") {
		t.Errorf("no title or no Go licence:\n%.300s", text)
	}
	for i := 0; i < len(modules); i += 2 {
		head := "\n==== " + modules[i] + " " + modules[i+1] + " ("
		at := strings.Index(text, head)
		if at < 0 {
			t.Errorf("no notice for %s %s", modules[i], modules[i+1])
			continue
		}
		body := text[at+len(head):]
		if next := strings.Index(body[1:], "\n==== "); next >= 0 {
			body = body[:next+1]
		}
		if len(strings.TrimSpace(body)) < 100 {
			t.Errorf("%s %s: its licence text is missing or too short: %q", modules[i], modules[i+1], body)
		}
	}
	if strings.Contains(text, ".go)\n") {
		t.Errorf("a source file is in the notices as a licence (go-sdk ships copyright_test.go):\n%s", headings(text))
	}
	for _, ours := range []string{"studio-go", "github.com/survivalservers/studio-go/"} {
		if strings.Contains(string(list), ours) {
			t.Errorf("a module of ours is covered as a third party's (%s):\n%s", ours, list)
		}
	}
}

// headings lists the notices' section lines.
func headings(text string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "==== ") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
