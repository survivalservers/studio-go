package client

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLabCorePath runs the core path against a running API with a real machine: a Windows zip (GS_TEST_ZIP)
// uploaded in parts as a build, a version from GS_TEST_VERSION (its settings without name and build) staged in
// GS_TEST_REGION (default us-central), the event stream followed, a game server started, listed and stopped, and its
// running and stopped events read. Needs GS_API_URL and GS_API_KEY too; skipped without GS_TEST_ZIP (tests/clients/lab.sh runs it on
// the lab). Storage links go through Go's default proxy settings (HTTP_PROXY).
func TestLabCorePath(t *testing.T) {
	zip := os.Getenv("GS_TEST_ZIP")
	if zip == "" {
		t.Skip("GS_TEST_ZIP is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	c, err := New(os.Getenv("GS_API_URL"), os.Getenv("GS_API_KEY"), nil, "client-lab-test")
	if err != nil {
		t.Fatal(err)
	}
	region := os.Getenv("GS_TEST_REGION")
	if region == "" {
		region = "us-central"
	}
	var settings map[string]any
	raw, err := os.ReadFile(os.Getenv("GS_TEST_VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	slug := "core-go-" + itoa(int(time.Now().UnixMilli()%1e9))
	var app map[string]any
	if err := c.JSON(ctx, Call{Method: http.MethodPost, Path: "/v0/apps", Body: map[string]any{"name": slug, "slug": slug, "os": "windows"}}, &app); err != nil {
		t.Fatal(err)
	}

	parts := 0
	build, err := c.UploadZip(ctx, slug, zip, UploadOptions{Progress: func(done, total int) { parts = total }})
	if err != nil {
		t.Fatal(err)
	}
	buildID := str(build["id"])
	get := func(path string) func(context.Context) (map[string]any, error) {
		return func(ctx context.Context) (map[string]any, error) { return c.Get(ctx, path, nil) }
	}
	b, _, err := Wait(ctx, 10*time.Minute, 2*time.Second, get("/v0/builds/"+buildID), BuildSettled, nil)
	if err != nil || str(b["status"]) != "ready" {
		t.Fatalf("build %s: %v %v", buildID, b, err)
	}

	settings["name"], settings["build"], settings["regions"] = "1.0.0", buildID, []string{region}
	if _, err := c.Do(ctx, Call{Method: http.MethodPost, Path: "/v0/apps/" + slug + "/versions", Body: settings}); err != nil {
		t.Fatal(err)
	}
	v, _, err := Wait(ctx, 15*time.Minute, 2*time.Second, get("/v0/apps/"+slug+"/versions/1.0.0"), func(v map[string]any) (bool, string) {
		ok, _ := VersionSettled(v)
		return ok, "version"
	}, nil)
	if ready, _, _ := VersionRegions(v); err != nil || ready == 0 {
		t.Fatalf("version: %v %v", v, err)
	}

	// The event stream, followed from before the start.
	var mu sync.Mutex
	var seen []map[string]any
	sctx, stop := context.WithCancel(ctx)
	body, err := c.Stream(sctx, Call{Method: http.MethodGet, Path: "/v0/events/stream", Query: url.Values{"types": {"game_server.*"}}, Accept: "text/event-stream"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer body.Close()
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if d, ok := strings.CutPrefix(sc.Text(), "data:"); ok {
				var e map[string]any
				if json.Unmarshal([]byte(strings.TrimSpace(d)), &e) == nil {
					mu.Lock()
					seen = append(seen, e)
					mu.Unlock()
				}
			}
		}
	}()
	time.Sleep(time.Second)

	var gs map[string]any
	if err := c.JSON(ctx, Call{Method: http.MethodPost, Path: "/v0/game-servers", Body: map[string]any{"app": slug, "version": "1.0.0",
		"start_mode": "cold", "placement": map[string]any{"regions": []string{region}}, "labels": map[string]any{"client": "go-core"}}}, &gs); err != nil {
		t.Fatal(err)
	}
	id := str(gs["id"])
	g, _, err := Wait(ctx, 5*time.Minute, 2*time.Second, get("/v0/game-servers/"+id), GameServerSettled, nil)
	if err != nil || str(g["status"]) != "running" {
		t.Fatalf("game server %s: %v %v", id, g, err)
	}
	list, err := c.Get(ctx, "/v0/game-servers", url.Values{"label": {"client:go-core"}})
	if err != nil || !slices.ContainsFunc(asList(list["data"]), func(x map[string]any) bool { return str(x["id"]) == id }) {
		t.Fatalf("not listed by its label: %v %v", list, err)
	}
	if _, err := c.Do(ctx, Call{Method: http.MethodPost, Path: "/v0/game-servers/" + id + "/stop"}); err != nil {
		t.Fatal(err)
	}
	g, _, err = Wait(ctx, 5*time.Minute, 2*time.Second, get("/v0/game-servers/"+id), func(g map[string]any) (bool, string) {
		return str(g["status"]) == "stopped" || str(g["status"]) == "failed", "stop"
	}, nil)
	if err != nil || str(g["status"]) != "stopped" {
		t.Fatalf("stop: %v %v", g, err)
	}
	var types []string
	deadline := time.Now().Add(time.Minute)
	for {
		types = types[:0]
		mu.Lock()
		for _, e := range seen {
			if str(asMap(e["data"])["id"]) == id {
				types = append(types, str(e["type"]))
			}
		}
		mu.Unlock()
		if slices.Contains(types, "game_server.running") && slices.Contains(types, "game_server.stopped") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("events for %s: %v", id, types)
		}
		time.Sleep(time.Second)
	}
	stop()
	<-done
	t.Logf("core path passed: app %s, build %s (%d parts), version 1.0.0 in %s, game server %s running and stopped, events %s",
		slug, buildID, parts, region, id, strings.Join(types, ", "))
}
