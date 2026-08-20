package plugin_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mpyw/sqlc-gen-go-dynamic/internal/plugin"
)

// request is a protojson-shaped GenerateRequest, as sqlc writes one: proto field names, and
// bytes fields — options among them — base64 encoded.
func request(t *testing.T, options string) []byte {
	t.Helper()
	req := map[string]any{
		"settings": map[string]any{
			"engine": "postgresql",
			"codegen": map[string]any{
				"out":     "gen",
				"options": base64.StdEncoding.EncodeToString([]byte(options)),
			},
		},
		"queries": []any{
			map[string]any{
				"name": "SearchUsers",
				"cmd":  ":many",
				"text": "select id, note from users where 1 = 1\n" +
					"  /*%if activeOnly*/ and status = $1 /*%end*/",
				"comments": []string{},
				"params": []any{
					map[string]any{
						"number": 1,
						"column": map[string]any{
							"name":     "status",
							"not_null": true,
							"type":     map[string]any{"name": "text"},
						},
					},
				},
				"columns": []any{
					map[string]any{"name": "id", "not_null": true, "type": map[string]any{"name": "pg_catalog.int8"}},
					map[string]any{"name": "note", "not_null": false, "type": map[string]any{"name": "text"}},
				},
			},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRun(t *testing.T) {
	var out bytes.Buffer
	if err := plugin.Run(bytes.NewReader(request(t, `{"package":"db","filename":"q.gen.go"}`)), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	var resp plugin.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Files) != 1 || resp.Files[0].Name != "q.gen.go" {
		t.Fatalf("files = %+v, want one named q.gen.go", resp.Files)
	}
	src := string(resp.Files[0].Contents)
	t.Logf("\n%s", src)
	for _, want := range []string{
		"package db",
		"and status = sqlc.arg('status')",
		"ActiveOnly bool",
		"Status     string",
		"ID   int64",
		"Note *string", // nullable, so scanning NULL has somewhere to go
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// The contents field is bytes, which protojson renders as base64; encoding/json does the same
// for a []byte, which is what lets sqlc read the response back.
func TestRunEncodesContentsAsBase64(t *testing.T) {
	var out bytes.Buffer
	if err := plugin.Run(bytes.NewReader(request(t, `{"package":"db"}`)), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	var raw struct {
		Files []struct {
			Name     string `json:"name"`
			Contents string `json:"contents"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(raw.Files[0].Contents)
	if err != nil {
		t.Fatalf("contents is not base64: %v", err)
	}
	if !strings.HasPrefix(string(decoded), "// Code generated") {
		t.Errorf("decoded contents = %q", decoded[:40])
	}
	if raw.Files[0].Name != "queries.gen.go" {
		t.Errorf("name = %q, want the default filename", raw.Files[0].Name)
	}
}

func TestRunErrors(t *testing.T) {
	t.Run("no package", func(t *testing.T) {
		var out bytes.Buffer
		err := plugin.Run(bytes.NewReader(request(t, `{}`)), &out)
		if err == nil || !strings.Contains(err.Error(), "options.package") {
			t.Errorf("error = %v, want it to ask for the package", err)
		}
	})
	t.Run("unmapped type", func(t *testing.T) {
		req := request(t, `{"package":"db"}`)
		var m map[string]any
		if err := json.Unmarshal(req, &m); err != nil {
			t.Fatal(err)
		}
		q := m["queries"].([]any)[0].(map[string]any)
		q["columns"].([]any)[0].(map[string]any)["type"] = map[string]any{"name": "hstore"}
		b, _ := json.Marshal(m)
		var out bytes.Buffer
		err := plugin.Run(bytes.NewReader(b), &out)
		if err == nil || !strings.Contains(err.Error(), "no mapping") {
			t.Errorf("error = %v, want it to name the unmapped type", err)
		}
	})
}
