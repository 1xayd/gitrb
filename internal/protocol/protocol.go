package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	ProtocolVersion = 1
	SchemaVersion   = 1
)

// Snapshot is the transport format shared by the Studio plugin and the CLI.
// It intentionally stores properties as JSON values so the protocol can evolve
// without requiring the Go client to understand every Roblox data type.
type Snapshot struct {
	SchemaVersion int     `json:"schemaVersion"`
	Project       string  `json:"project,omitempty"`
	PlaceID       int64   `json:"placeId,omitempty"`
	GameID        int64   `json:"gameId,omitempty"`
	Roots         []*Node `json:"roots"`
}

type Node struct {
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name"`
	ClassName  string         `json:"className"`
	ParentPath string         `json:"parentPath,omitempty"`
	Order      int            `json:"order"`
	Properties map[string]any `json:"properties,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Script     *Script        `json:"script,omitempty"`
	Children   []*Node        `json:"children,omitempty"`
}

type Script struct {
	Source string `json:"source"`
}

type SyncPushRequest struct {
	Protocol     int       `json:"protocol"`
	Project      string    `json:"project,omitempty"`
	BaseRevision string    `json:"baseRevision,omitempty"`
	Force        bool      `json:"force,omitempty"`
	Source       string    `json:"source,omitempty"`
	Snapshot     *Snapshot `json:"snapshot"`
}

type PushStartRequest struct {
	Protocol     int    `json:"protocol"`
	Project      string `json:"project,omitempty"`
	BaseRevision string `json:"baseRevision,omitempty"`
	Force        bool   `json:"force,omitempty"`
	TotalChunks  int    `json:"totalChunks"`
	Size         int    `json:"size"`
}

type PushChunkRequest struct {
	UploadID string `json:"uploadId"`
	Index    int    `json:"index"`
	Total    int    `json:"total"`
	Data     string `json:"data"`
}

type PullResponse struct {
	OK         bool      `json:"ok"`
	Protocol   int       `json:"protocol"`
	Project    string    `json:"project"`
	Revision   string    `json:"revision"`
	Mode       string    `json:"mode"`
	Snapshot   *Snapshot `json:"snapshot,omitempty"`
	TransferID string    `json:"transferId,omitempty"`
	Chunks     int       `json:"chunks,omitempty"`
	Bytes      int       `json:"bytes,omitempty"`
	Warnings   []string  `json:"warnings,omitempty"`
}

type PullChunkResponse struct {
	OK       bool   `json:"ok"`
	Index    int    `json:"index"`
	Chunks   int    `json:"chunks"`
	Revision string `json:"revision"`
	Data     string `json:"data"`
}

type SyncResult struct {
	OK             bool     `json:"ok"`
	Protocol       int      `json:"protocol"`
	Project        string   `json:"project"`
	Revision       string   `json:"revision"`
	ChangedFiles   []string `json:"changedFiles,omitempty"`
	RemovedFiles   []string `json:"removedFiles,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	ServerRevision string   `json:"serverRevision,omitempty"`
}

type StatusResponse struct {
	OK       bool      `json:"ok"`
	Protocol int       `json:"protocol"`
	Project  string    `json:"project"`
	Revision string    `json:"revision"`
	Git      GitStatus `json:"git"`
	Warnings []string  `json:"warnings,omitempty"`
}

type GitStatus struct {
	IsRepository bool   `json:"isRepository"`
	Branch       string `json:"branch,omitempty"`
	Clean        bool   `json:"clean"`
	Porcelain    string `json:"porcelain,omitempty"`
}

type CommitRequest struct {
	Message string `json:"message"`
}

type CommitResponse struct {
	OK        bool   `json:"ok"`
	Committed bool   `json:"committed"`
	Output    string `json:"output,omitempty"`
}

type ErrorResponse struct {
	OK             bool   `json:"ok"`
	Error          string `json:"error"`
	Code           string `json:"code,omitempty"`
	ServerRevision string `json:"serverRevision,omitempty"`
}

func NewSnapshot(project string) *Snapshot {
	return &Snapshot{SchemaVersion: SchemaVersion, Project: project, Roots: make([]*Node, 0)}
}

func (s *Snapshot) Validate() error {
	if s == nil {
		return fmt.Errorf("snapshot is required")
	}
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SchemaVersion
	}
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported snapshot schema %d", s.SchemaVersion)
	}
	seen := map[string]bool{}
	var visit func(*Node, string) error
	visit = func(n *Node, parent string) error {
		if n == nil {
			return fmt.Errorf("snapshot contains a null node")
		}
		if n.Name == "" {
			return fmt.Errorf("node has an empty name")
		}
		if n.ClassName == "" {
			return fmt.Errorf("node %q has no className", n.Name)
		}
		key := parent + "/" + n.Name + "#" + fmt.Sprint(n.Order)
		if seen[key] {
			return fmt.Errorf("duplicate node key %q", key)
		}
		seen[key] = true
		for _, child := range n.Children {
			if err := visit(child, key); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range s.Roots {
		if err := visit(root, ""); err != nil {
			return err
		}
	}
	return nil
}

// Revision is a content hash. encoding/json sorts map keys, which keeps this
// stable across Go processes while preserving Roblox child order in slices.
func Revision(s *Snapshot) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func MarshalSnapshot(s *Snapshot) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

func UnmarshalSnapshot(data []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}
