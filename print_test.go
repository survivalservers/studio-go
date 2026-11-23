package client

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// A client prints without its API key, before and after a call: no fmt verb on a Client, a *Client or a struct
// holding either (in an exported or an unexported field) shows it, and neither does a reflection dump of every string
// and byte slice the client holds, unexported fields included (what go-spew and testify's diff print).
func TestAPrintedClientShowsNoKey(t *testing.T) {
	const key = "gsk_live_EXAMPLE_not_a_real_key"
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, key, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		checkPrintouts(t, when, c, key)
	}
	check("before a call")
	if _, err := c.Get(context.Background(), "/v0/regions", nil); err != nil {
		t.Fatal(err)
	}
	if !c.HasKey() || sent != "Bearer "+key {
		t.Fatalf("the key is not sent: has a key %v, Authorization %q", c.HasKey(), sent)
	}
	check("after a call")
}

func checkPrintouts(t *testing.T, when string, c *Client, key string) {
	t.Helper()
	type holder struct {
		Exported   *Client
		unexported Client
	}
	values := map[string]any{"*Client": c, "Client": *c, "a struct holding one": holder{Exported: c, unexported: *c}}
	for name, v := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
			if s := fmt.Sprintf(verb, v); showsKey(s, key) {
				t.Errorf("%s: %s with %s shows the key: %s", when, name, verb, s)
			}
		}
	}
	var held []string
	stringsIn(reflect.ValueOf(c), map[uintptr]bool{}, &held)
	if len(held) == 0 {
		t.Fatal("the reflection walk found no strings: it does not reach the client's fields")
	}
	for _, s := range held {
		if strings.Contains(s, key) {
			t.Errorf("%s: a reflection dump of the client shows the key: %q", when, s)
		}
	}
}

// showsKey: s holds the key as text or in hex (fmt's %x and %X of a string field).
func showsKey(s, key string) bool {
	h := hex.EncodeToString([]byte(key))
	return strings.Contains(s, key) || strings.Contains(strings.ToLower(s), h)
}

// stringsIn collects every string and byte slice reachable from v through pointers, interfaces, structs, slices,
// arrays and maps, unexported fields included. Funcs and channels are not followed (a dump cannot see inside them).
func stringsIn(v reflect.Value, seen map[uintptr]bool, out *[]string) {
	switch v.Kind() {
	case reflect.String:
		*out = append(*out, v.String())
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		stringsIn(v.Elem(), seen, out)
	case reflect.Interface:
		if !v.IsNil() {
			stringsIn(v.Elem(), seen, out)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			stringsIn(v.Field(i), seen, out)
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			*out = append(*out, string(v.Bytes()))
			return
		}
		for i := range v.Len() {
			stringsIn(v.Index(i), seen, out)
		}
	case reflect.Map:
		for it := v.MapRange(); it.Next(); {
			stringsIn(it.Key(), seen, out)
			stringsIn(it.Value(), seen, out)
		}
	}
}
