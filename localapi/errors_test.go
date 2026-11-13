package localapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every error's type links to the public API's errors page, at its code's own anchor: no stand-in address.
func TestAnErrorLinksToTheErrorsPage(t *testing.T) {
	rec := httptest.NewRecorder()
	problem(rec, http.StatusUnprocessableEntity, "validation_failed", "a field is not valid", nil)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["type"] != "https://studio.survivalservers.com/docs/reference/errors/#validation_failed" {
		t.Fatalf("type %v", body["type"])
	}
	if strings.Contains(rec.Body.String(), "example.com") {
		t.Fatalf("a stand-in address in %s", rec.Body)
	}
}
