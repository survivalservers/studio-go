package localapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// The spec, loaded once: every reply a test gets is checked against it (status code declared, body matches the
// schema, Retry-After present on 429), so the server and openapi.yaml cannot drift apart.

type specDoc struct {
	raw      map[string]any
	compiler *jsonschema.Compiler
	mu       sync.Mutex
	cache    map[string]*jsonschema.Schema
	seen     map[string]bool // "METHOD path status" seen in tests
}

var (
	specOnce sync.Once
	spec     *specDoc
	specErr  error
)

func loadSpec(t testing.TB) *specDoc {
	t.Helper()
	specOnce.Do(func() {
		b, err := os.ReadFile("openapi.yaml")
		if err != nil {
			specErr = err
			return
		}
		var y any
		if err = yaml.Unmarshal(b, &y); err != nil {
			specErr = err
			return
		}
		// Round-trip through JSON so numbers and maps have the shapes the schema library expects.
		jb, err := json.Marshal(y)
		if err != nil {
			specErr = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(jb))
		if err != nil {
			specErr = err
			return
		}
		c := jsonschema.NewCompiler()
		c.DefaultDraft(jsonschema.Draft2020)
		c.AssertFormat()
		if err = c.AddResource("file:///openapi.json", doc); err != nil {
			specErr = err
			return
		}
		raw := map[string]any{}
		json.Unmarshal(jb, &raw)
		spec = &specDoc{raw: raw, compiler: c, cache: map[string]*jsonschema.Schema{}, seen: map[string]bool{}}
	})
	if specErr != nil {
		t.Fatalf("load openapi.yaml: %v", specErr)
	}
	return spec
}

func (d *specDoc) schema(t testing.TB, pointer string) *jsonschema.Schema {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.cache[pointer]; ok {
		return s
	}
	s, err := d.compiler.Compile("file:///openapi.json#" + pointer)
	if err != nil {
		t.Fatalf("compile %s: %v", pointer, err)
	}
	d.cache[pointer] = s
	return s
}

// resolve follows a local $ref inside the spec.
func (d *specDoc) resolve(v map[string]any) (map[string]any, string) {
	ref, _ := v["$ref"].(string)
	if ref == "" {
		return v, ""
	}
	var cur any = d.raw
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = cur.(map[string]any)[part]
	}
	return cur.(map[string]any), strings.TrimPrefix(ref, "#")
}

func jsonPointerEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// check validates one reply against the operation in the spec.
func (d *specDoc) check(t testing.TB, method, path string, rec *httptest.ResponseRecorder) {
	t.Helper()
	paths := d.raw["paths"].(map[string]any)
	item, ok := paths[path].(map[string]any)
	if !ok {
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: not in the spec but answered %d", method, path, rec.Code)
		}
		return
	}
	op, ok := item[strings.ToLower(method)].(map[string]any)
	if !ok {
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: method not in the spec but answered %d", method, path, rec.Code)
		}
		return
	}
	code := strconv.Itoa(rec.Code)
	resp, ok := op["responses"].(map[string]any)[code].(map[string]any)
	if !ok {
		t.Errorf("%s %s: status %s is not declared in the spec (body %s)", method, path, code, rec.Body.String())
		return
	}
	d.mu.Lock()
	d.seen[method+" "+path+" "+code] = true
	d.mu.Unlock()
	resp, base := d.resolve(resp)
	if base == "" {
		base = "/paths/" + jsonPointerEscape(path) + "/" + strings.ToLower(method) + "/responses/" + code
	}
	if hs, ok := resp["headers"].(map[string]any); ok {
		for h := range hs {
			if rec.Header().Get(h) == "" {
				t.Errorf("%s %s %s: header %s missing", method, path, code, h)
			}
		}
	}
	content, _ := resp["content"].(map[string]any)
	if len(content) == 0 {
		if rec.Body.Len() != 0 {
			t.Errorf("%s %s %s: spec has no body but got %q", method, path, code, rec.Body.String())
		}
		return
	}
	ct := strings.TrimSpace(strings.SplitN(rec.Header().Get("Content-Type"), ";", 2)[0])
	if _, ok := content[ct]; !ok {
		t.Errorf("%s %s %s: content type %q not in the spec", method, path, code, ct)
		return
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Errorf("%s %s %s: body is not JSON: %v", method, path, code, err)
		return
	}
	sch := d.schema(t, base+"/content/"+jsonPointerEscape(ct)+"/schema")
	if err := sch.Validate(inst); err != nil {
		t.Errorf("%s %s %s: body does not match the spec: %v\nbody: %s", method, path, code, err, rec.Body.String())
	}
}

// TestRoutesMatchSpec: every call in the spec is served (it answers 401 without a token, not 404 or 405), and the
// server serves nothing the spec does not describe.
func TestRoutesMatchSpec(t *testing.T) {
	d := loadSpec(t)
	s := NewServer(Options{})
	var specOps []string
	for path, item := range d.raw["paths"].(map[string]any) {
		for method := range item.(map[string]any) {
			switch method {
			case "get", "put", "post", "patch", "delete":
			default:
				continue
			}
			m := strings.ToUpper(method)
			specOps = append(specOps, m+" "+path)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(m, path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without a token: got %d, want 401", m, path, rec.Code)
			}
			d.check(t, m, path, rec)
		}
	}
	var served []string
	for path, methods := range s.routes {
		for m := range methods {
			served = append(served, m+" "+path)
		}
	}
	sort.Strings(specOps)
	sort.Strings(served)
	if fmt.Sprint(specOps) != fmt.Sprint(served) {
		t.Errorf("server and spec differ:\nspec:   %v\nserver: %v", specOps, served)
	}
}

func jsonUnmarshal(b []byte) (any, error) { return jsonschema.UnmarshalJSON(bytes.NewReader(b)) }
