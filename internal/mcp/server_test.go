package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// errEchoFailure is the echo tool's static failure sentinel.
var errEchoFailure = errors.New("echo failure")

// newTestServer builds a Server with no query service; only tools whose
// handlers avoid the DB can be invoked, plus a custom echo tool for
// exercising the tools/call machinery.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s := New(":0", nil)
	s.registerTool(Tool{
		Name:        "echo",
		Description: "echoes the message argument",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"message": map[string]any{schemaType: schemaTypeString},
			},
			schemaRequired: []string{"message"},
		},
		Handler: func(_ context.Context, args map[string]any) (any, error) {
			msg := getStr(args, "message")
			if msg == "fail" {
				return nil, errEchoFailure
			}
			return map[string]any{"echo": msg}, nil
		},
	})
	return s
}

// postMCP posts a JSON-RPC body to the server's /mcp endpoint and decodes
// the JSON response.
func postMCP(t *testing.T, s *Server, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleMCP(rec, req)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not JSON: %q", rec.Body.String())
	}
	return resp
}

func rpcID(id string) json.RawMessage { return json.RawMessage(id) }

func TestHandleMCPInitialize(t *testing.T) {
	s := newTestServer(t)
	resp := postMCP(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	result, _ := resp["result"].(map[string]any)
	if result == nil || result["protocolVersion"] != "2025-03-26" {
		t.Fatalf("initialize result wrong: %v", resp)
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "fantasy-football-engine" {
		t.Fatalf("serverInfo wrong: %v", info)
	}
}

func TestHandleMCPToolsList(t *testing.T) {
	s := newTestServer(t)
	resp := postMCP(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("tools must be registered: %v", resp)
	}
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		tm, _ := tl.(map[string]any)
		names = append(names, tm["name"].(string))
	}
	if !slices.Contains(names, "echo") {
		t.Fatalf("echo tool missing: %v", names)
	}
}

func TestHandleMCPToolsCall(t *testing.T) {
	s := newTestServer(t)

	// Successful call returns text content wrapping the JSON result.
	resp := postMCP(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hi"}}}`)
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	content, _ := result[contentKey].([]any)
	if len(content) != 1 {
		t.Fatalf("content missing: %v", result)
	}
	entry, _ := content[0].(map[string]any)
	if entry[schemaType] != textType || !strings.Contains(entry[textType].(string), `"echo": "hi"`) {
		t.Fatalf("content wrong: %v", entry)
	}

	// Missing required argument is rejected with -32602.
	resp = postMCP(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)
	rpcErr, _ := resp["error"].(map[string]any)
	if rpcErr["code"] != any(float64(-32602)) || !strings.Contains(rpcErr["message"].(string), "message") {
		t.Fatalf("missing-arg error wrong: %v", rpcErr)
	}

	// Unknown tool is rejected with -32601.
	resp = postMCP(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	rpcErr, _ = resp["error"].(map[string]any)
	if rpcErr["code"] != any(float64(-32601)) {
		t.Fatalf("unknown tool error wrong: %v", rpcErr)
	}

	// Handler errors come back as an isError content result, not an RPC error.
	resp = postMCP(t, s, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"echo","arguments":{"message":"fail"}}}`)
	if resp["error"] != nil {
		t.Fatalf("handler error must not be an RPC error: %v", resp["error"])
	}
	result, _ = resp["result"].(map[string]any)
	if result[isErrorKey] != true {
		t.Fatalf("isError must be set: %v", result)
	}

	// Invalid params JSON is rejected with -32602.
	resp = postMCP(t, s, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":"not-an-object"}`)
	rpcErr, _ = resp["error"].(map[string]any)
	if rpcErr["code"] != any(float64(-32602)) {
		t.Fatalf("invalid params error wrong: %v", rpcErr)
	}
}

func TestHandleMCPErrors(t *testing.T) {
	s := newTestServer(t)

	// GET is rejected.
	req := httptest.NewRequest(http.MethodGet, "/mcp", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleMCP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET must 405, got %d", rec.Code)
	}

	// Malformed body yields a parse error with null id.
	resp := postMCP(t, s, `{nope`)
	rpcErr, _ := resp["error"].(map[string]any)
	if rpcErr["code"] != any(float64(-32700)) {
		t.Fatalf("parse error wrong: %v", rpcErr)
	}
	if resp["id"] != nil {
		t.Fatalf("parse error id must be null: %v", resp["id"])
	}

	// Unknown method yields -32601.
	resp = postMCP(t, s, `{"jsonrpc":"2.0","id":8,"method":"bogus/method"}`)
	rpcErr, _ = resp["error"].(map[string]any)
	if rpcErr["code"] != any(float64(-32601)) || !strings.Contains(rpcErr["message"].(string), "bogus/method") {
		t.Fatalf("unknown method error wrong: %v", rpcErr)
	}
}

func TestHandleHTTPHealth(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/", "/health", "/healthz"} {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		s.handleHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d", path, rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: bad JSON %q", path, rec.Body.String())
		}
		if body["status"] != "ok" || body["service"] != "fantasy-football-engine" {
			t.Fatalf("%s: body wrong: %v", path, body)
		}
		if n := len(s.tools); body[toolsKey] != any(float64(n)) {
			t.Fatalf("%s: tool count wrong: %v", path, body[toolsKey])
		}
	}
	// Unknown path 404s.
	req := httptest.NewRequest(http.MethodGet, "/other", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path: %d", rec.Code)
	}
}

// --- param coercion helpers ---

func TestMCPParamHelpers(t *testing.T) {
	m := map[string]any{
		"s":    "str",
		"n":    float64(7),
		"i":    9,
		"b":    true,
		"nilv": nil,
	}
	if getStr(m, "s") != "str" || getStr(m, "missing") != "" || getStr(m, "nilv") != "" {
		t.Fatal("getStr wrong")
	}
	if getInt(m, "n", 3) != 7 || getInt(m, "i", 3) != 9 || getInt(m, "missing", 3) != 3 {
		t.Fatal("getInt wrong")
	}
	if toInt("not-a-number") != 0 || toInt(nil) != 0 {
		t.Fatal("toInt must reject unsupported types")
	}
	if !getBool(m, "b") || getBool(m, "s") || getBool(m, "missing") {
		t.Fatal("getBool wrong")
	}
}

func TestToStrSlice(t *testing.T) {
	got := toStrSlice([]any{"x", float64(1)})
	if len(got) != 2 || got[0] != "x" || got[1] != "1" {
		t.Fatalf("toStrSlice wrong: %v", got)
	}
	if toStrSlice(nil) != nil || toStrSlice("nope") != nil {
		t.Fatal("toStrSlice must yield nil for non-arrays")
	}
}

func TestMissingRequired(t *testing.T) {
	schema := map[string]any{schemaRequired: []string{"a", "b"}}
	if got := missingRequired(schema, map[string]any{"a": 1, "b": 2}); len(got) != 0 {
		t.Fatalf("complete args: %v", got)
	}
	got := missingRequired(schema, map[string]any{"a": 1})
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("missing b: %v", got)
	}
	if missingRequired(map[string]any{}, nil) != nil {
		t.Fatal("no required list must yield nil")
	}
}

func TestWriteJSONRPCResultAndError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONRPCResult(rec, rpcID(`42`), map[string]any{"k": "v"})
	if ct := rec.Header().Get("Content-Type"); ct != contentTypeJSON {
		t.Fatalf("content type: %q", ct)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["jsonrpc"] != jsonrpcVersion || resp["id"] != any(float64(42)) {
		t.Fatalf("result envelope wrong: %v", resp)
	}

	// A nil id is rendered as JSON null, not a missing key.
	rec = httptest.NewRecorder()
	writeJSONRPCError(rec, nil, -32700, "parse error")
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"id":null`)) {
		t.Fatalf("nil id must marshal as null: %s", rec.Body.String())
	}
	rpcErr, _ := resp["error"].(map[string]any)
	if rpcErr["code"] != any(float64(-32700)) || rpcErr["message"] != "parse error" {
		t.Fatalf("error envelope wrong: %v", rpcErr)
	}
}
