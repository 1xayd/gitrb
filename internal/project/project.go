package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitrb/internal/protocol"
)

const (
	ConfigFile       = "gitrb.json"
	stateDir         = ".gitrb"
	indexFile        = "index.json"
	metaFile         = "meta.json"
	nodeMetadataFile = ".gitrb-node.json"
)

type Config struct {
	SchemaVersion   int      `json:"schemaVersion"`
	Name            string   `json:"name"`
	SourceDirectory string   `json:"sourceDirectory"`
	IncludeServices []string `json:"includeServices,omitempty"`
	Ignore          []string `json:"ignore,omitempty"`
}

type index struct {
	SchemaVersion int      `json:"schemaVersion"`
	Files         []string `json:"files"`
}

type metadata struct {
	SchemaVersion int   `json:"schemaVersion"`
	PlaceID       int64 `json:"placeId,omitempty"`
	GameID        int64 `json:"gameId,omitempty"`
}

type nodeFile struct {
	SchemaVersion int            `json:"schemaVersion"`
	ID            string         `json:"id,omitempty"`
	Name          string         `json:"name"`
	ClassName     string         `json:"className"`
	ParentPath    string         `json:"parentPath,omitempty"`
	Order         int            `json:"order"`
	Properties    map[string]any `json:"properties,omitempty"`
	Attributes    map[string]any `json:"attributes,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	ScriptFile    string         `json:"scriptFile,omitempty"`
}

type WriteResult struct {
	Revision     string
	ChangedFiles []string
	RemovedFiles []string
}

type nodeRecord struct {
	relDir string
	file   nodeFile
}

func DefaultConfig(name string) Config {
	if strings.TrimSpace(name) == "" {
		name = "RobloxProject"
	}
	return Config{SchemaVersion: 1, Name: name, SourceDirectory: "src"}
}

func Init(dir, name string) (Config, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Config{}, err
	}
	configPath := filepath.Join(dir, ConfigFile)
	if _, err := os.Stat(configPath); err == nil {
		return Config{}, fmt.Errorf("%s already exists", configPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	cfg := DefaultConfig(name)
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	if err := writeJSON(configPath, cfg); err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(filepath.Join(dir, cfg.SourceDirectory), 0o755); err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(filepath.Join(dir, stateDir), 0o755); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadConfig(dir string) (Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", ConfigFile, err)
	}
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateConfig(cfg Config) error {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = 1
	}
	if cfg.SchemaVersion != 1 {
		return fmt.Errorf("unsupported project schema %d", cfg.SchemaVersion)
	}
	if strings.TrimSpace(cfg.Name) == "" {
		return fmt.Errorf("project name is required")
	}
	if filepath.IsAbs(cfg.SourceDirectory) || cfg.SourceDirectory == "." || cfg.SourceDirectory == ".." || strings.Contains(filepath.ToSlash(cfg.SourceDirectory), "../") {
		return fmt.Errorf("sourceDirectory must stay inside the project")
	}
	return nil
}

func WriteSnapshot(dir string, snapshot *protocol.Snapshot) (WriteResult, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return WriteResult{}, err
	}
	if err := snapshot.Validate(); err != nil {
		return WriteResult{}, err
	}
	snapshot.Project = cfg.Name
	root := filepath.Join(dir, cfg.SourceDirectory)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return WriteResult{}, err
	}

	old, err := readIndex(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return WriteResult{}, err
	}
	result := WriteResult{}
	for _, rel := range old.Files {
		if !isSafeRelative(rel) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			// Stale files are removed below once the new file set is known.
		}
	}

	newFiles := make(map[string]struct{})
	for i, n := range snapshot.Roots {
		if n == nil {
			continue
		}
		segment := uniqueSegment(n.Name, "", i, snapshot.Roots)
		if err := writeNode(dir, filepath.Join(cfg.SourceDirectory, segment), n, newFiles, &result); err != nil {
			return WriteResult{}, err
		}
	}

	oldSet := make(map[string]struct{}, len(old.Files))
	for _, rel := range old.Files {
		oldSet[rel] = struct{}{}
	}
	for rel := range oldSet {
		if _, keep := newFiles[rel]; keep {
			continue
		}
		if !isSafeRelative(rel) {
			continue
		}
		path := filepath.Join(dir, rel)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return WriteResult{}, fmt.Errorf("remove stale generated file %s: %w", rel, err)
		}
		result.RemovedFiles = append(result.RemovedFiles, rel)
		cleanupEmptyParents(filepath.Dir(path), root)
	}

	files := make([]string, 0, len(newFiles))
	for rel := range newFiles {
		files = append(files, rel)
	}
	sort.Strings(files)
	if err := writeJSON(filepath.Join(dir, stateDir, indexFile), index{SchemaVersion: 1, Files: files}); err != nil {
		return WriteResult{}, err
	}
	meta := metadata{SchemaVersion: 1, PlaceID: snapshot.PlaceID, GameID: snapshot.GameID}
	if err := writeJSON(filepath.Join(dir, stateDir, metaFile), meta); err != nil {
		return WriteResult{}, err
	}

	readBack, err := ReadSnapshot(dir)
	if err != nil {
		return WriteResult{}, err
	}
	result.Revision, err = protocol.Revision(readBack)
	if err != nil {
		return WriteResult{}, err
	}
	sort.Strings(result.ChangedFiles)
	sort.Strings(result.RemovedFiles)
	return result, nil
}

func ReadSnapshot(dir string) (*protocol.Snapshot, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	s := protocol.NewSnapshot(cfg.Name)
	if b, err := os.ReadFile(filepath.Join(dir, stateDir, metaFile)); err == nil {
		var m metadata
		if json.Unmarshal(b, &m) == nil {
			s.PlaceID, s.GameID = m.PlaceID, m.GameID
		}
	}
	root := filepath.Join(dir, cfg.SourceDirectory)
	records := make([]nodeRecord, 0)
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != nodeMetadataFile {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var nf nodeFile
		if err := json.Unmarshal(b, &nf); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		relDir, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		records = append(records, nodeRecord{relDir: filepath.ToSlash(relDir), file: nf})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		return depth(records[i].relDir) < depth(records[j].relDir)
	})
	nodes := make(map[string]*protocol.Node, len(records))
	for _, rec := range records {
		n := &protocol.Node{
			ID: rec.file.ID, Name: rec.file.Name, ClassName: rec.file.ClassName,
			ParentPath: rec.file.ParentPath, Order: rec.file.Order,
			Properties: rec.file.Properties, Attributes: rec.file.Attributes,
			Tags: rec.file.Tags, Children: make([]*protocol.Node, 0),
		}
		if rec.file.ScriptFile != "" {
			if !isSafeRelative(rec.file.ScriptFile) {
				return nil, fmt.Errorf("unsafe scriptFile %q", rec.file.ScriptFile)
			}
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rec.relDir), filepath.FromSlash(rec.file.ScriptFile)))
			if err != nil {
				return nil, fmt.Errorf("read script for %s: %w", rec.relDir, err)
			}
			n.Script = &protocol.Script{Source: string(b)}
		}
		nodes[rec.relDir] = n
	}
	for _, rec := range records {
		n := nodes[rec.relDir]
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rec.relDir)))
		if parent == "." {
			s.Roots = append(s.Roots, n)
			continue
		}
		p, ok := nodes[parent]
		if !ok {
			return nil, fmt.Errorf("node %s has missing parent metadata", rec.relDir)
		}
		p.Children = append(p.Children, n)
	}
	sortNodes(s.Roots)
	return s, nil
}

func Revision(dir string) (string, *protocol.Snapshot, error) {
	s, err := ReadSnapshot(dir)
	if err != nil {
		return "", nil, err
	}
	r, err := protocol.Revision(s)
	return r, s, err
}

func writeNode(projectDir, relDir string, n *protocol.Node, files map[string]struct{}, result *WriteResult) error {
	dir := filepath.Join(projectDir, filepath.FromSlash(relDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	nf := nodeFile{SchemaVersion: 1, ID: n.ID, Name: n.Name, ClassName: n.ClassName, ParentPath: n.ParentPath, Order: n.Order, Properties: n.Properties, Attributes: n.Attributes, Tags: n.Tags}
	if n.Script != nil {
		nf.ScriptFile = scriptFilename(n.ClassName)
	}
	nodeRel := filepath.ToSlash(filepath.Join(relDir, nodeMetadataFile))
	changed, err := writeJSONIfChanged(filepath.Join(projectDir, filepath.FromSlash(nodeRel)), nf)
	if err != nil {
		return err
	}
	files[nodeRel] = struct{}{}
	if changed {
		result.ChangedFiles = append(result.ChangedFiles, nodeRel)
	}
	if n.Script != nil {
		scriptRel := filepath.ToSlash(filepath.Join(relDir, nf.ScriptFile))
		changed, err := writeTextIfChanged(filepath.Join(projectDir, filepath.FromSlash(scriptRel)), n.Script.Source)
		if err != nil {
			return err
		}
		files[scriptRel] = struct{}{}
		if changed {
			result.ChangedFiles = append(result.ChangedFiles, scriptRel)
		}
	}
	seen := map[string]int{}
	for i, child := range n.Children {
		if child == nil {
			continue
		}
		occurrence := seen[child.Name]
		seen[child.Name] = occurrence + 1
		segment := uniqueChildSegment(child.Name, occurrence, i, n.Children)
		if err := writeNode(projectDir, filepath.ToSlash(filepath.Join(relDir, segment)), child, files, result); err != nil {
			return err
		}
	}
	return nil
}

func uniqueSegment(name, _ string, index int, siblings []*protocol.Node) string {
	base := safeSegment(name)
	count := 0
	for i := 0; i < index; i++ {
		if siblings[i] != nil && siblings[i].Name == name {
			count++
		}
	}
	if count == 0 && !hasEarlierSegmentCollision(base, name, index, siblings) {
		return base
	}
	return base + "~" + strconv.Itoa(count+1)
}

func uniqueChildSegment(name string, occurrence, index int, siblings []*protocol.Node) string {
	base := safeSegment(name)
	if occurrence == 0 {
		for i := 0; i < index; i++ {
			if siblings[i] != nil && safeSegment(siblings[i].Name) == base && siblings[i].Name != name {
				return base + "~1"
			}
		}
		return base
	}
	return base + "~" + strconv.Itoa(occurrence+1)
}

func hasEarlierSegmentCollision(base, name string, index int, siblings []*protocol.Node) bool {
	for i := 0; i < index; i++ {
		if siblings[i] != nil && siblings[i].Name != name && safeSegment(siblings[i].Name) == base {
			return true
		}
	}
	return false
}

func scriptFilename(className string) string {
	switch className {
	case "LocalScript":
		return "source.client.luau"
	case "ModuleScript":
		return "source.module.luau"
	default:
		return "source.server.luau"
	}
}

func safeSegment(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "_unnamed"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '.' || r == ' ' || r == '-' || r == '_':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	result := strings.Trim(b.String(), ". ")
	if result == "" || result == "." || result == ".." {
		result = "_unnamed"
	}
	if isWindowsReserved(result) {
		result = "_" + result
	}
	return result
}

func isWindowsReserved(s string) bool {
	switch strings.ToUpper(strings.TrimSuffix(s, ".")) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func readIndex(dir string) (index, error) {
	b, err := os.ReadFile(filepath.Join(dir, stateDir, indexFile))
	if err != nil {
		return index{}, err
	}
	var i index
	if err := json.Unmarshal(b, &i); err != nil {
		return index{}, err
	}
	return i, nil
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeBytes(path, b)
}

func writeJSONIfChanged(path string, value any) (bool, error) {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, err
	}
	b = append(b, '\n')
	return writeBytesIfChanged(path, b)
}

func writeTextIfChanged(path, value string) (bool, error) {
	return writeBytesIfChanged(path, []byte(value))
}

func writeBytes(path string, b []byte) error {
	_, err := writeBytesIfChanged(path, b)
	return err
}

func writeBytesIfChanged(path string, b []byte) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(b) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

func cleanupEmptyParents(dir, stop string) {
	for dir != stop && isWithin(dir, stop) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func isSafeRelative(path string) bool {
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func isWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func depth(rel string) int {
	if rel == "." || rel == "" {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

func sortNodes(nodes []*protocol.Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Order != nodes[j].Order {
			return nodes[i].Order < nodes[j].Order
		}
		return nodes[i].Name < nodes[j].Name
	})
	for _, n := range nodes {
		sortNodes(n.Children)
	}
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
