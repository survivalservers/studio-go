package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const packEpoch = 1763000000

// One download's folder: each name with its mode as the build leaves it (the notices file comes out 0600, the umask may
// give 0664), created in the order given, which is not name order.
var packFiles = []struct {
	name string
	mode os.FileMode
}{
	{"survivalservers.exe", 0o775}, {"THIRD_PARTY_NOTICES.txt", 0o600}, {"z.txt", 0o664}, {"docs/b.md", 0o640},
	{"LICENSE", 0o664}, {"a.txt", 0o644}, {"docs/a.md", 0o600}, {"README.md", 0o664}, {"m.txt", 0o640},
	{"Bin/tool", 0o700}, {"c.txt", 0o644}, {"B.txt", 0o664},
}

// makePackDir writes packFiles under parent/name in the order of the indexes given.
func makePackDir(t *testing.T, parent, name string, order []int) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	for _, i := range order {
		f := packFiles[i]
		p := filepath.Join(dir, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("the file "+f.name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, f.mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func pack(t *testing.T, dir, out string) []byte {
	t.Helper()
	if b, err := exec.Command("bash", "pack.sh", dir, out, "1763000000").CombinedOutput(); err != nil {
		t.Fatalf("pack.sh %s: %v\n%s", filepath.Base(out), err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Every path under the folder, as LC_ALL=C sort orders them (byte order), folders with a trailing slash.
func packWant(base string) []string {
	paths := map[string]bool{base: true}
	for _, f := range packFiles {
		parts := strings.Split(f.name, "/")
		for i := 1; i < len(parts); i++ {
			paths[base+"/"+strings.Join(parts[:i], "/")] = true
		}
		paths[base+"/"+f.name] = false
	}
	keys := make([]string, 0, len(paths))
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for i, p := range keys {
		if paths[p] {
			keys[i] = p + "/"
		}
	}
	return keys
}

func wantPackMode(name string) os.FileMode {
	if strings.HasSuffix(name, "/") || name == "survivalservers.exe" || name == "Bin/tool" {
		return 0o755
	}
	return 0o644
}

func needPackTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("pack.sh is a bash script; release builds run on Linux")
	}
	for _, tool := range []string{"bash", "zip", "tar", "gzip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("pack.sh needs %s, which release builds have", tool)
		}
	}
}

// A Windows download: its entries in name order whatever order the files were made in (zip -r took the order the
// file system lists them in, which differs from machine to machine), the folder entries kept, fixed modes and times,
// no extra fields; two folders made in different orders give the same bytes.
func TestPackZipIsTheSameBytesOnEveryMachine(t *testing.T) {
	needPackTools(t)
	tmp := t.TempDir()
	base := "survivalservers_0.1.0_windows_amd64"
	one := pack(t, makePackDir(t, filepath.Join(tmp, "one"), base, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}), filepath.Join(tmp, "one.zip"))
	two := pack(t, makePackDir(t, filepath.Join(tmp, "two"), base, []int{7, 2, 11, 0, 5, 9, 1, 10, 3, 8, 4, 6}), filepath.Join(tmp, "two.zip"))
	if !bytes.Equal(one, two) {
		t.Errorf("the same files made in two orders give two different zips")
	}
	z, err := zip.NewReader(bytes.NewReader(one), int64(len(one)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	want := time.Unix(packEpoch, 0).UTC().Format(time.DateTime)
	for _, f := range z.File {
		names = append(names, f.Name)
		rel := strings.TrimPrefix(strings.TrimPrefix(f.Name, base), "/")
		if rel == "" {
			rel = "/"
		}
		if got := f.Mode().Perm(); got != wantPackMode(rel) {
			t.Errorf("%s: mode %o, want %o", f.Name, got, wantPackMode(rel))
		}
		if got := f.Modified.Format(time.DateTime); got != want {
			t.Errorf("%s: time %s, want %s (SOURCE_DATE_EPOCH in UTC)", f.Name, got, want)
		}
		if len(f.Extra) != 0 {
			t.Errorf("%s: %d bytes of extra fields (owner or extended times), want none", f.Name, len(f.Extra))
		}
	}
	if got, w := strings.Join(names, "\n"), strings.Join(packWant(base), "\n"); got != w {
		t.Errorf("entries:\n%s\nwant name order:\n%s", got, w)
	}
}

// A Linux or macOS download: the same order, modes and times, owner 0, and the same bytes from either folder.
func TestPackTarGzIsTheSameBytesOnEveryMachine(t *testing.T) {
	needPackTools(t)
	tmp := t.TempDir()
	base := "survivalservers_0.1.0_linux_amd64"
	one := pack(t, makePackDir(t, filepath.Join(tmp, "one"), base, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}), filepath.Join(tmp, "one.tar.gz"))
	two := pack(t, makePackDir(t, filepath.Join(tmp, "two"), base, []int{7, 2, 11, 0, 5, 9, 1, 10, 3, 8, 4, 6}), filepath.Join(tmp, "two.tar.gz"))
	if !bytes.Equal(one, two) {
		t.Errorf("the same files made in two orders give two different tar.gz files")
	}
	g, err := gzip.NewReader(bytes.NewReader(one))
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "" || !g.ModTime.IsZero() {
		t.Errorf("gzip header carries a name %q or a time %v", g.Name, g.ModTime)
	}
	r := tar.NewReader(g)
	var names []string
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
		rel := strings.TrimPrefix(strings.TrimPrefix(h.Name, base), "/")
		if rel == "" {
			rel = "/"
		}
		if got := os.FileMode(h.Mode).Perm(); got != wantPackMode(rel) {
			t.Errorf("%s: mode %o, want %o", h.Name, got, wantPackMode(rel))
		}
		if h.ModTime.Unix() != packEpoch || h.Uid != 0 || h.Gid != 0 {
			t.Errorf("%s: time %d owner %d:%d, want %d and 0:0", h.Name, h.ModTime.Unix(), h.Uid, h.Gid, packEpoch)
		}
	}
	if got, w := strings.Join(names, "\n"), strings.Join(packWant(base), "\n"); got != w {
		t.Errorf("entries:\n%s\nwant name order:\n%s", got, w)
	}
}

func TestPackRefusesWhatItCannotPack(t *testing.T) {
	needPackTools(t)
	tmp := t.TempDir()
	dir := makePackDir(t, tmp, "demo", []int{0})
	for _, args := range [][]string{{dir, filepath.Join(tmp, "x.zip"), "soon"}, {filepath.Join(tmp, "none"), filepath.Join(tmp, "x.zip"), "1"},
		{dir, filepath.Join(tmp, "x.rar"), "1"}} {
		cmd := exec.Command("bash", append([]string{"pack.sh"}, args...)...)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("pack.sh %v: no error\n%s", args, out)
		}
	}
}

// Only plain files and folders go into a download: zip follows a symbolic link and would pack
// the file it points to, wherever it is; tar would keep the link. A link to a file outside the folder or inside it, a
// link to a folder, a named pipe, or the folder itself being a link: each stops pack.sh before it changes or writes
// anything, for the zip and the tar.gz alike, naming the entry, and no archive is left behind.
func TestPackRefusesLinksAndAnythingButPlainFilesAndFolders(t *testing.T) {
	needPackTools(t)
	cases := []struct {
		name  string
		entry string // the entry the refusal must name, under the folder
		plant func(t *testing.T, tmp, dir string)
	}{
		{"a link to a file outside the folder", "outside-link", func(t *testing.T, tmp, dir string) {
			secret := filepath.Join(tmp, "not-for-the-download.txt")
			if err := os.WriteFile(secret, []byte("a file outside the download\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, secret, filepath.Join(dir, "outside-link"))
		}},
		{"a link to a file inside the folder", "docs/inside-link", func(t *testing.T, tmp, dir string) {
			mustSymlink(t, "../LICENSE", filepath.Join(dir, "docs", "inside-link"))
		}},
		{"a link to a folder outside", "outside-folder", func(t *testing.T, tmp, dir string) {
			mustSymlink(t, tmp, filepath.Join(dir, "outside-folder"))
		}},
		{"a named pipe", "pipe", func(t *testing.T, tmp, dir string) {
			if _, err := exec.LookPath("mkfifo"); err != nil {
				t.Skip("no mkfifo here")
			}
			if out, err := exec.Command("mkfifo", filepath.Join(dir, "pipe")).CombinedOutput(); err != nil {
				t.Fatalf("mkfifo: %v\n%s", err, out)
			}
		}},
	}
	for _, kind := range []string{".zip", ".tar.gz"} {
		for _, c := range cases {
			t.Run(c.name+" "+kind, func(t *testing.T) {
				tmp := t.TempDir()
				base := "survivalservers_0.1.0_test"
				dir := makePackDir(t, tmp, base, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
				c.plant(t, tmp, dir)
				out := filepath.Join(tmp, "download"+kind)
				refused(t, dir, out, base+"/"+c.entry)
			})
		}
		t.Run("the folder itself a link "+kind, func(t *testing.T) {
			tmp := t.TempDir()
			target := makePackDir(t, tmp, "target", []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
			link := filepath.Join(tmp, "survivalservers_0.1.0_test")
			mustSymlink(t, target, link)
			refused(t, link, filepath.Join(tmp, "download"+kind), "is a symbolic link")
		})
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// refused runs pack.sh, which must stop with exit 2 naming want, leave no archive and change no file's mode.
func refused(t *testing.T, dir, out, want string) {
	t.Helper()
	notices := filepath.Join(dir, "THIRD_PARTY_NOTICES.txt")
	cmd := exec.Command("bash", "pack.sh", dir, out, "1763000000")
	b, err := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); err == nil || code != 2 {
		t.Fatalf("pack.sh: exit %d, want 2 (refused)\n%s", code, b)
	}
	if !strings.Contains(string(b), want) {
		t.Errorf("pack.sh's refusal does not name %q:\n%s", want, b)
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Errorf("an archive was left behind: %s", out)
	}
	fi, err := os.Stat(notices)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("pack.sh changed the folder before refusing: notices mode %o, want 0600 as made", fi.Mode().Perm())
	}
}
