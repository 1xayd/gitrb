package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gitrb/internal/project"
	"gitrb/internal/protocol"
)

func TestPushPullAndRevisionConflict(t *testing.T) {
	dir := t.TempDir()
	if _, err := project.Init(dir, "Fixture"); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Clean(dir), "secret")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	snapshot := &protocol.Snapshot{SchemaVersion: protocol.SchemaVersion, Project: "Fixture", Roots: []*protocol.Node{{Name: "Workspace", ClassName: "Workspace", Order: 0}}}
	push(t, ts.URL+"/v1/sync/push", "secret", protocol.SyncPushRequest{Protocol: 1, Project: "Fixture", Snapshot: snapshot})

	pullResponse := get(t, ts.URL+"/v1/sync/pull", "secret")
	var pull protocol.PullResponse
	decode(t, pullResponse, &pull)
	if pull.Mode != "inline" || pull.Revision == "" || pull.Snapshot == nil {
		t.Fatalf("unexpected pull response: %#v", pull)
	}

	stale := snapshot
	stale.Roots[0].Name = "Changed"
	request := protocol.SyncPushRequest{Protocol: 1, Project: "Fixture", BaseRevision: "wrong", Snapshot: stale}
	response := post(t, ts.URL+"/v1/sync/push", "secret", request)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expected conflict, got %d", response.StatusCode)
	}
}

func TestChunkedPullKeepsUnicodeAndBoundaries(t *testing.T) {
	dir := t.TempDir()
	if _, err := project.Init(dir, "Large"); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("-- комментарий 😀\n", 30000)
	snapshot := &protocol.Snapshot{SchemaVersion: 1, Project: "Large", Roots: []*protocol.Node{{Name: "Workspace", ClassName: "Workspace", Order: 0, Children: []*protocol.Node{{Name: "Main", ClassName: "ModuleScript", Order: 0, Script: &protocol.Script{Source: large}}}}}}
	if _, err := project.WriteSnapshot(dir, snapshot); err != nil {
		t.Fatal(err)
	}
	s := New(dir, "")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	response := get(t, ts.URL+"/v1/sync/pull", "")
	var pull protocol.PullResponse
	decode(t, response, &pull)
	if pull.Mode != "chunked" {
		t.Fatalf("expected chunked response, got %#v", pull)
	}
	var chunks strings.Builder
	for i := 0; i < pull.Chunks; i++ {
		chunkResponse := get(t, ts.URL+"/v1/sync/pull/chunk?transferId="+pull.TransferID+"&index="+itoa(i), "")
		var chunk protocol.PullChunkResponse
		decode(t, chunkResponse, &chunk)
		chunks.WriteString(chunk.Data)
	}
	decoded, err := protocol.UnmarshalSnapshot([]byte(chunks.String()))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Roots[0].Children[0].Script.Source != large {
		t.Fatal("chunked pull changed script source")
	}
}

func push(t *testing.T, url, token string, request protocol.SyncPushRequest) {
	t.Helper()
	response := post(t, url, token, request)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("push failed: %d %s", response.StatusCode, body)
	}
}

func post(t *testing.T, url, token string, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("X-GitRB-Token", token)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func get(t *testing.T, url, token string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("X-GitRB-Token", token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decode(t *testing.T, response *http.Response, value any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(value); err != nil {
		t.Fatal(err)
	}
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}
