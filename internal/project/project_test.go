package project

import (
	"os"
	"path/filepath"
	"testing"

	"gitrb/internal/protocol"
)

func TestSnapshotRoundTripAndStaleFileCleanup(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir, "Fixture"); err != nil {
		t.Fatal(err)
	}
	snapshot := &protocol.Snapshot{
		SchemaVersion: protocol.SchemaVersion,
		Project:       "ignored-by-config",
		Roots: []*protocol.Node{{
			Name: "Workspace", ClassName: "Workspace", Order: 0,
			Children: []*protocol.Node{{
				Name: "Main", ClassName: "Script", Order: 0,
				Script:     &protocol.Script{Source: "print('hello')"},
				Attributes: map[string]any{"Enabled": true},
			}},
		}},
	}
	first, err := WriteSnapshot(dir, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision == "" || len(first.ChangedFiles) == 0 {
		t.Fatalf("expected generated files: %#v", first)
	}
	read, err := ReadSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if read.Project != "Fixture" || read.Roots[0].Children[0].Script.Source != "print('hello')" {
		t.Fatalf("unexpected round trip: %#v", read)
	}

	snapshot.Roots[0].Children[0].Script = nil
	second, err := WriteSnapshot(dir, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.RemovedFiles) != 1 {
		t.Fatalf("expected script file removal, got %#v", second.RemovedFiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "Workspace", "Main", "source.server.luau")); !os.IsNotExist(err) {
		t.Fatalf("stale script still exists: %v", err)
	}
}
