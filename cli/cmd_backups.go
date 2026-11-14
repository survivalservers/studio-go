package cli

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func cmdBackupsTake(ctx context.Context, c *ctxt, args []string) error {
	label := c.fs.String("label", "", "your name for it")
	lock := c.fs.Bool("lock", false, "keep it until you delete it")
	pos, err := c.parse(args, "SERVER [--label L] [--lock]", 1, 1)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if *label != "" {
		body["label"] = *label
	}
	if *lock {
		body["locked"] = true
	}
	b, err := c.call(ctx, http.MethodPost, "/v0/persistent-servers/"+esc(pos[0])+"/backups", nil, body)
	if err != nil {
		return err
	}
	return c.show(b, fmt.Sprintf("Backup %s of %s is %s.", str(b["id"]), pos[0], str(b["status"])))
}

func cmdBackupsLock(locked bool) func(context.Context, *ctxt, []string) error {
	return func(ctx context.Context, c *ctxt, args []string) error {
		pos, err := c.parse(args, "BACKUP", 1, 1)
		if err != nil {
			return err
		}
		b, err := c.call(ctx, http.MethodPatch, "/v0/backups/"+esc(pos[0]), nil, map[string]any{"locked": locked})
		if err != nil {
			return err
		}
		if locked {
			return c.show(b, "Locked. It stays until you unlock and delete it.")
		}
		return c.show(b, "Unlocked. It goes when its time comes.")
	}
}

func cmdBackupsDelete(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "BACKUP", 1, 1)
	if err != nil {
		return err
	}
	if _, err := c.call(ctx, http.MethodDelete, "/v0/backups/"+esc(pos[0]), nil, nil); err != nil {
		return err
	}
	return c.show(map[string]any{"id": pos[0], "deleted": true}, "Backup "+pos[0]+" deleted.")
}

// cmdBackupsDownload waits for the zip, saves it, and checks every file against its SHA256SUMS.
func cmdBackupsDownload(ctx context.Context, c *ctxt, args []string) error {
	out := c.fs.String("o", "", "where to save it (default BACKUP.zip)")
	pos, err := c.parse(args, "BACKUP [-o file.zip]", 1, 1)
	if err != nil {
		return err
	}
	file := *out
	if file == "" {
		file = pos[0] + ".zip"
	}
	if _, err := c.call(ctx, http.MethodPost, "/v0/backups/"+esc(pos[0])+"/export", nil, nil); err != nil {
		return err
	}
	done, err := c.wait(ctx, waitOn{"the download", "", nil}, time.Hour, func(ctx context.Context) (map[string]any, error) {
		return c.get(ctx, "/v0/backups/"+esc(pos[0])+"/export", nil)
	}, func(e map[string]any) (bool, string) {
		switch str(e["status"]) {
		case "ready":
			return true, ""
		case "failed":
			return true, str(e["error"])
		}
		return false, ""
	})
	if err != nil {
		return err
	}
	if str(done["status"]) != "ready" {
		return errors.New(str(done["error"]))
	}
	if err := saveFrom(ctx, str(done["url"]), file); err != nil {
		return err
	}
	n, err := checkSums(file)
	if err != nil {
		return err
	}
	return c.show(done, fmt.Sprintf("Saved %s: %d files, every one checked.", file, n))
}

func saveFrom(ctx context.Context, link, file string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("storage answered %d", resp.StatusCode)
	}
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// checkSums reads a downloaded backup's SHA256SUMS and hashes every file it names.
func checkSums(file string) (int, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return 0, err
	}
	defer zr.Close()
	want := map[string]string{}
	found := false
	for _, f := range zr.File {
		if f.Name != "SHA256SUMS" {
			continue
		}
		found = true
		rc, err := f.Open()
		if err != nil {
			return 0, err
		}
		sc := bufio.NewScanner(rc)
		for sc.Scan() {
			if sum, name, ok := strings.Cut(sc.Text(), "  "); ok {
				want[name] = sum
			}
		}
		rc.Close()
	}
	if !found {
		return 0, nil // a backup taken as a zip has no list
	}
	n := 0
	for _, f := range zr.File {
		sum, ok := want[f.Name]
		if !ok {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return n, err
		}
		h := sha256.New()
		_, err = io.Copy(h, rc)
		rc.Close()
		if err != nil {
			return n, err
		}
		if hex.EncodeToString(h.Sum(nil)) != sum {
			return n, fmt.Errorf("%s does not match its SHA-256", f.Name)
		}
		n++
	}
	return n, nil
}

// cmdBackupsUpload sends a zip of saves in as an Imported backup, and waits until it is one.
func cmdBackupsUpload(ctx context.Context, c *ctxt, args []string) error {
	label := c.fs.String("label", "", "your name for it")
	pos, err := c.parse(args, "SERVER saves.zip [--label L]", 2, 2)
	if err != nil {
		return err
	}
	fi, err := os.Stat(pos[1])
	if err != nil {
		return err
	}
	body := map[string]any{"bytes": fi.Size()}
	if *label != "" {
		body["label"] = *label
	}
	base := "/v0/persistent-servers/" + esc(pos[0]) + "/backups/uploads"
	up, err := c.call(ctx, http.MethodPost, base, nil, body)
	if err != nil {
		return err
	}
	if err := putTo(ctx, str(up["url"]), pos[1], fi.Size()); err != nil {
		return err
	}
	if _, err := c.call(ctx, http.MethodPost, base+"/"+esc(str(up["id"]))+"/complete", nil, nil); err != nil {
		return err
	}
	done, err := c.wait(ctx, waitOn{"the save", "the file is uploaded: survivalservers backups list " + pos[0] + " shows the backup once it is checked (running this command again would upload it a second time)", nil}, time.Hour, func(ctx context.Context) (map[string]any, error) {
		return c.get(ctx, base+"/"+esc(str(up["id"])), nil)
	}, func(u map[string]any) (bool, string) {
		switch str(u["status"]) {
		case "done":
			return true, ""
		case "failed":
			return true, str(u["error"])
		}
		return false, ""
	})
	if err != nil {
		return err
	}
	if str(done["status"]) != "done" {
		return errors.New(str(done["error"]))
	}
	return c.show(done, fmt.Sprintf("Backup %s holds your save. The persistent server stays as it is until you restore it: survivalservers restore %s --backup %s", str(done["backup"]), pos[0], str(done["backup"])))
}

func putTo(ctx context.Context, link, file string, size int64) error {
	if _, err := url.Parse(link); err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, link, f)
	if err != nil {
		return err
	}
	req.ContentLength = size
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("storage answered %d", resp.StatusCode)
	}
	return nil
}
