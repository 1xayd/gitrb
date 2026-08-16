package protocol

import (
	"strings"
	"testing"
)

func TestUnmarshalSnapshotAcceptsRobloxEmptyTables(t *testing.T) {
	data := []byte(`{
		"schemaVersion":1,
		"project":"Fixture",
		"roots":[{
			"name":"Workspace",
			"className":"Workspace",
			"order":0,
			"properties":[],
			"attributes":[],
			"children":[]
		}]
	}`)
	snapshot, err := UnmarshalSnapshot(data)
	if err != nil {
		t.Fatalf("decode Roblox empty tables: %v", err)
	}
	root := snapshot.Roots[0]
	if root.Properties == nil || len(root.Properties) != 0 {
		t.Fatalf("properties should decode as an empty map: %#v", root.Properties)
	}
	if root.Attributes == nil || len(root.Attributes) != 0 {
		t.Fatalf("attributes should decode as an empty map: %#v", root.Attributes)
	}
}

func TestUnmarshalSnapshotRejectsNonEmptyPropertyArray(t *testing.T) {
	data := []byte(`{
		"schemaVersion":1,
		"project":"Fixture",
		"roots":[{
			"name":"Workspace",
			"className":"Workspace",
			"order":0,
			"properties":[true],
			"children":[]
		}]
	}`)
	_, err := UnmarshalSnapshot(data)
	if err == nil || !strings.Contains(err.Error(), "properties") {
		t.Fatalf("expected a property-shape error, got %v", err)
	}
}
