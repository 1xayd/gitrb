package format

import (
	"path/filepath"
	"testing"

	"gitrb/internal/protocol"
)

func TestModelRoundTripXMLAndBinary(t *testing.T) {
	t.Parallel()
	snapshot := &protocol.Snapshot{
		SchemaVersion: protocol.SchemaVersion,
		Project:       "Fixture",
		Roots: []*protocol.Node{{
			ID: "RBX1", Name: "Fixture", ClassName: "Model", Order: 0,
			Properties: map[string]any{
				"PrimaryPart": map[string]any{"__type": "InstanceRef", "path": ""},
			},
			Children: []*protocol.Node{{
				ID: "RBX2", Name: "Part", ClassName: "Part", Order: 0,
				Properties: map[string]any{
					"Anchored": true,
					"Size":     map[string]any{"__type": "Vector3", "x": 4.0, "y": 2.0, "z": 1.0},
				},
			}},
		}},
	}
	for _, ext := range []string{".rbxmx", ".rbxm"} {
		path := filepath.Join(t.TempDir(), "fixture"+ext)
		if _, err := WriteModel(path, snapshot); err != nil {
			t.Fatalf("write %s: %v", ext, err)
		}
		read, err := ReadModel(path)
		if err != nil {
			t.Fatalf("read %s: %v", ext, err)
		}
		if len(read.Snapshot.Roots) != 1 || len(read.Snapshot.Roots[0].Children) != 1 {
			t.Fatalf("read %s lost tree: %#v", ext, read.Snapshot.Roots)
		}
		part := read.Snapshot.Roots[0].Children[0]
		if part.Name != "Part" || part.ClassName != "Part" {
			t.Fatalf("read %s returned wrong part: %#v", ext, part)
		}
	}
}
