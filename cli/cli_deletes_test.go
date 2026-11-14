package cli

import "testing"

// A pipeline that deploys every commit can clean up with the same tool: delete builds and versions, and deprecate a
// version (an app holds 200 versions).
func TestBuildAndVersionDeletes(t *testing.T) {
	f := newFake(t)
	f.json("DELETE /v0/builds/bld_1", 204, nil)
	f.run(t, "builds", "delete", "bld_1").must(t, 0, "Build bld_1 deleted.")
	f.json("DELETE /v0/apps/arena/versions/2.3.0", 204, nil)
	f.run(t, "versions", "delete", "arena", "2.3.0").must(t, 0, "Version 2.3.0 of arena deleted.")

	f.json("PATCH /v0/apps/arena/versions/2.2.0", 200, map[string]any{"name": "2.2.0", "deprecated": true})
	f.run(t, "versions", "deprecate", "arena", "2.2.0").must(t, 0, "is deprecated: no new starts")
	f.run(t, "versions", "deprecate", "arena", "2.2.0", "--undo").must(t, 0, "takes new starts again")
	calls := f.called("PATCH", "/v0/apps/arena/versions/2.2.0")
	if len(calls) != 2 || calls[0].Body["deprecated"] != true || calls[1].Body["deprecated"] != false {
		t.Errorf("deprecate bodies: %v", calls)
	}

	// A version in use answers 409; the tool says so and fails.
	f.json("DELETE /v0/builds/bld_2", 409, map[string]any{"error": map[string]any{"code": "in_use", "message": "a version uses this build"}})
	f.run(t, "builds", "delete", "bld_2").must(t, 1, "a version uses this build")
	f.run(t, "builds", "delete").must(t, 2)
}
