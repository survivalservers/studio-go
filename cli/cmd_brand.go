package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	api "github.com/survivalservers/studio-go"
)

// Branding: a studio's name, icon and accent color, and an app's icon, color and banner. Pictures go up as their
// own bytes (PNG, JPEG or WebP, up to 1 MiB); the API makes its own files from them.

// pictureTypes are the Content-Types sent for a picture's file name; anything else goes as octet-stream and the API
// reads the type from the bytes.
var pictureTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp"}

// cmdPicture sets or removes a picture: "icon" or "banner" of an app, or "studio" for the studio's icon.
func cmdPicture(kind string) command {
	return func(ctx context.Context, c *ctxt, args []string) error {
		remove := c.fs.Bool("remove", false, "remove it instead")
		help := "APP FILE | APP --remove"
		min, max := 1, 2
		if kind == "studio" {
			help, min, max = "FILE | --remove", 0, 1
		}
		pos, err := c.parse(args, help, min, max)
		if err != nil {
			return err
		}
		path, what := "/v0/studio/icon", "The studio's icon"
		if kind != "studio" {
			path, what = "/v0/apps/"+esc(pos[0])+"/"+kind, fmt.Sprintf("%s's %s", pos[0], kind)
			pos = pos[1:]
		}
		if *remove {
			if len(pos) != 0 {
				return usagef("give a file or --remove, not both")
			}
			out, err := c.call(ctx, http.MethodDelete, path, nil, nil)
			if err != nil {
				return err
			}
			return c.show(out, what+" is removed.")
		}
		if len(pos) != 1 {
			return usagef("expected %s", help)
		}
		data, err := os.ReadFile(pos[0])
		if err != nil {
			return err
		}
		out, err := c.call(ctx, http.MethodPut, path, nil, api.File{Data: data, ContentType: pictureTypes[strings.ToLower(filepath.Ext(pos[0]))]})
		if err != nil {
			return err
		}
		pic, _ := out["icon"].(map[string]any)
		if kind == "banner" {
			pic, _ = out["banner"].(map[string]any)
		}
		line := what + " is set."
		u := str(pic["url_256"])
		if u == "" {
			u = str(pic["url_1600"])
		}
		if u != "" {
			line += " " + u
			// Picture addresses are signed and expire within a day: say when, so nobody keeps this one.
			if exp, err := time.Parse(time.RFC3339, str(pic["expires_at"])); err == nil {
				line += " (this link works until " + exp.UTC().Format("2006-01-02 15:04") + " UTC)"
			}
		}
		return c.show(out, line)
	}
}

// colorArg is a color as #rrggbb, or nil for --clear.
func colorArg(pos []string, clear bool) (any, error) {
	switch {
	case clear && len(pos) == 0:
		return nil, nil
	case !clear && len(pos) == 1:
		return pos[0], nil
	}
	return nil, usagef("give a color as #rrggbb, or --clear")
}

func cmdAppColor(ctx context.Context, c *ctxt, args []string) error {
	clear := c.fs.Bool("clear", false, "go back to a color made from the name")
	pos, err := c.parse(args, "APP #rrggbb | APP --clear", 1, 2)
	if err != nil {
		return err
	}
	color, err := colorArg(pos[1:], *clear)
	if err != nil {
		return err
	}
	out, err := c.call(ctx, http.MethodPatch, "/v0/apps/"+esc(pos[0]), nil, map[string]any{"color": color})
	if err != nil {
		return err
	}
	return c.show(out, fmt.Sprintf("%s's color is %s.", pos[0], firstOf(str(out["color"]), "made from its name")))
}

func cmdStudioRename(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "NAME", 1, 1)
	if err != nil {
		return err
	}
	out, err := c.call(ctx, http.MethodPatch, "/v0/studio", nil, map[string]any{"name": pos[0]})
	if err != nil {
		return err
	}
	return c.show(out, fmt.Sprintf("The studio is now %s. Its short name stays %s, so addresses and scripts that use it keep working.", str(out["name"]), str(out["slug"])))
}

func cmdStudioAccent(ctx context.Context, c *ctxt, args []string) error {
	clear := c.fs.Bool("clear", false, "go back to the panel's own color")
	pos, err := c.parse(args, "#rrggbb | --clear", 0, 1)
	if err != nil {
		return err
	}
	color, err := colorArg(pos, *clear)
	if err != nil {
		return err
	}
	out, err := c.call(ctx, http.MethodPatch, "/v0/studio", nil, map[string]any{"accent_color": color})
	if err != nil {
		return err
	}
	return c.show(out, fmt.Sprintf("The studio's accent color is %s.", firstOf(str(out["accent_color"]), "the panel's own")))
}
