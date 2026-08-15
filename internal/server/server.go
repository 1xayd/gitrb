package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitrb/internal/project"
	"gitrb/internal/protocol"
	"gitrb/internal/vcs"
)

const (
	maxJSONBody = 64 << 20
	chunkBytes  = 240 << 10
	inlineBytes = 420 << 10
)

type Server struct {
	ProjectDir string
	Token      string

	mu        sync.Mutex
	uploads   map[string]*upload
	transfers map[string]*transfer
}

type upload struct {
	Project      string
	BaseRevision string
	Force        bool
	Total        int
	Size         int
	Chunks       map[int]string
	Created      time.Time
}

type transfer struct {
	Data     []byte
	Revision string
	Created  time.Time
}

func New(projectDir, token string) *Server {
	return &Server{ProjectDir: projectDir, Token: token, uploads: make(map[string]*upload), transfers: make(map[string]*transfer)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/status", s.handleStatus)
	mux.HandleFunc("/v1/sync/push", s.handlePush)
	mux.HandleFunc("/v1/sync/push/start", s.handlePushStart)
	mux.HandleFunc("/v1/sync/push/chunk", s.handlePushChunk)
	mux.HandleFunc("/v1/sync/pull", s.handlePull)
	mux.HandleFunc("/v1/sync/pull/chunk", s.handlePullChunk)
	mux.HandleFunc("/v1/git/commit", s.handleGitCommit)
	mux.HandleFunc("/v1/git/push", s.handleGitPush)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-GitRB-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "set the configured token in Authorization or X-GitRB-Token")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) authorized(r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	if r.Header.Get("X-GitRB-Token") == s.Token {
		return true
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == s.Token
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required")
		return
	}
	cfg, err := project.LoadConfig(s.ProjectDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_error", err.Error())
		return
	}
	revision, _, err := project.Revision(s.ProjectDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "protocol": protocol.ProtocolVersion, "project": cfg.Name, "revision": revision})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required")
		return
	}
	cfg, err := project.LoadConfig(s.ProjectDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_error", err.Error())
		return
	}
	revision, _, err := project.Revision(s.ProjectDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, protocol.StatusResponse{OK: true, Protocol: protocol.ProtocolVersion, Project: cfg.Name, Revision: revision, Git: vcs.Status(s.ProjectDir)})
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required")
		return
	}
	var req protocol.SyncPushRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.Protocol != 0 && req.Protocol != protocol.ProtocolVersion {
		writeError(w, http.StatusBadRequest, "unsupported_protocol", fmt.Sprintf("protocol %d is not supported", req.Protocol))
		return
	}
	if req.Snapshot == nil {
		writeError(w, http.StatusBadRequest, "missing_snapshot", "snapshot is required")
		return
	}
	result, status, err := s.apply(req.Project, req.BaseRevision, req.Force, req.Snapshot)
	if err != nil {
		writeErrorWithRevision(w, status, errorCode(err), err.Error(), result.ServerRevision)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePushStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required")
		return
	}
	var req protocol.PushStartRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.TotalChunks < 1 || req.TotalChunks > 10000 || req.Size < 1 || req.Size > maxJSONBody*4 {
		writeError(w, http.StatusBadRequest, "invalid_transfer", "invalid chunk count or size")
		return
	}
	id := randomID()
	s.mu.Lock()
	s.cleanupLocked()
	s.uploads[id] = &upload{Project: req.Project, BaseRevision: req.BaseRevision, Force: req.Force, Total: req.TotalChunks, Size: req.Size, Chunks: make(map[int]string), Created: time.Now()}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "protocol": protocol.ProtocolVersion, "uploadId": id, "chunks": req.TotalChunks})
}

func (s *Server) handlePushChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required")
		return
	}
	var req protocol.PushChunkRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	s.mu.Lock()
	s.cleanupLocked()
	u, ok := s.uploads[req.UploadID]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "transfer_not_found", "upload has expired or does not exist")
		return
	}
	if req.Total != u.Total || req.Index < 0 || req.Index >= u.Total {
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "invalid_chunk", "chunk index or total does not match upload")
		return
	}
	u.Chunks[req.Index] = req.Data
	complete := len(u.Chunks) == u.Total
	if !complete {
		count := len(u.Chunks)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "complete": false, "received": count, "total": u.Total})
		return
	}
	parts := make([]string, u.Total)
	for i := range parts {
		parts[i] = u.Chunks[i]
	}
	projectName, baseRevision, force, expectedSize := u.Project, u.BaseRevision, u.Force, u.Size
	delete(s.uploads, req.UploadID)
	s.mu.Unlock()
	raw := strings.Join(parts, "")
	if expectedSize != len(raw) {
		writeError(w, http.StatusBadRequest, "invalid_transfer", "assembled snapshot size does not match upload")
		return
	}
	snapshot, err := protocol.UnmarshalSnapshot([]byte(raw))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_snapshot", err.Error())
		return
	}
	result, status, err := s.apply(projectName, baseRevision, force, snapshot)
	if err != nil {
		writeErrorWithRevision(w, status, errorCode(err), err.Error(), result.ServerRevision)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required")
		return
	}
	cfg, err := project.LoadConfig(s.ProjectDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_error", err.Error())
		return
	}
	revision, snapshot, err := project.Revision(s.ProjectDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_error", err.Error())
		return
	}
	raw, err := protocol.MarshalSnapshot(snapshot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode_error", err.Error())
		return
	}
	if len(raw) <= inlineBytes {
		writeJSON(w, http.StatusOK, protocol.PullResponse{OK: true, Protocol: protocol.ProtocolVersion, Project: cfg.Name, Revision: revision, Mode: "inline", Snapshot: snapshot})
		return
	}
	id := randomID()
	s.mu.Lock()
	s.cleanupLocked()
	s.transfers[id] = &transfer{Data: raw, Revision: revision, Created: time.Now()}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, protocol.PullResponse{OK: true, Protocol: protocol.ProtocolVersion, Project: cfg.Name, Revision: revision, Mode: "chunked", TransferID: id, Chunks: chunkCount(raw), Bytes: len(raw)})
}

func (s *Server) handlePullChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET is required")
		return
	}
	id := r.URL.Query().Get("transferId")
	index, err := strconv.Atoi(r.URL.Query().Get("index"))
	if id == "" || err != nil {
		writeError(w, http.StatusBadRequest, "invalid_chunk", "transferId and index are required")
		return
	}
	s.mu.Lock()
	s.cleanupLocked()
	t, ok := s.transfers[id]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "transfer_not_found", "download has expired or does not exist")
		return
	}
	bounds := chunkBounds(t.Data)
	chunks := len(bounds)
	if index < 0 || index >= chunks {
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "invalid_chunk", "chunk index is out of range")
		return
	}
	start, end := bounds[index][0], bounds[index][1]
	data := string(t.Data[start:end])
	revision := t.Revision
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, protocol.PullChunkResponse{OK: true, Index: index, Chunks: chunks, Revision: revision, Data: data})
}

func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required")
		return
	}
	var req protocol.CommitRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := vcs.Commit(s.ProjectDir, req.Message)
	if err != nil {
		writeError(w, http.StatusBadRequest, "git_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGitPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST is required")
		return
	}
	var req struct {
		Remote string `json:"remote"`
		Branch string `json:"branch"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	out, err := vcs.Push(s.ProjectDir, req.Remote, req.Branch)
	if err != nil {
		writeError(w, http.StatusBadRequest, "git_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}

func (s *Server) apply(requestProject, baseRevision string, force bool, snapshot *protocol.Snapshot) (protocol.SyncResult, int, error) {
	if snapshot == nil {
		return protocol.SyncResult{}, http.StatusBadRequest, fmt.Errorf("snapshot is required")
	}
	if err := snapshot.Validate(); err != nil {
		return protocol.SyncResult{}, http.StatusBadRequest, err
	}
	cfg, err := project.LoadConfig(s.ProjectDir)
	if err != nil {
		return protocol.SyncResult{}, http.StatusInternalServerError, err
	}
	if requestProject != "" && requestProject != cfg.Name {
		return protocol.SyncResult{}, http.StatusConflict, fmt.Errorf("project mismatch: server is %q", cfg.Name)
	}
	currentRevision, current, err := project.Revision(s.ProjectDir)
	if err != nil {
		return protocol.SyncResult{}, http.StatusInternalServerError, err
	}
	if !force && baseRevision != "" && baseRevision != currentRevision {
		return protocol.SyncResult{ServerRevision: currentRevision}, http.StatusConflict, fmt.Errorf("stale base revision; pull before pushing")
	}
	if !force && baseRevision == "" && len(current.Roots) > 0 {
		return protocol.SyncResult{ServerRevision: currentRevision}, http.StatusConflict, fmt.Errorf("base revision is required for a non-empty project")
	}
	result, err := project.WriteSnapshot(s.ProjectDir, snapshot)
	if err != nil {
		return protocol.SyncResult{}, http.StatusInternalServerError, err
	}
	return protocol.SyncResult{OK: true, Protocol: protocol.ProtocolVersion, Project: cfg.Name, Revision: result.Revision, ChangedFiles: result.ChangedFiles, RemovedFiles: result.RemovedFiles}, http.StatusOK, nil
}

func (s *Server) cleanupLocked() {
	cutoff := time.Now().Add(-15 * time.Minute)
	for id, u := range s.uploads {
		if u.Created.Before(cutoff) {
			delete(s.uploads, id)
		}
	}
	for id, t := range s.transfers {
		if t.Created.Before(cutoff) {
			delete(s.transfers, id)
		}
	}
}

func readJSON(r *http.Request, value any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBody))
	if err != nil {
		return err
	}
	if len(data) == maxJSONBody {
		return fmt.Errorf("request body is too large")
	}
	return json.Unmarshal(data, value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorWithRevision(w, status, code, message, "")
}

func writeErrorWithRevision(w http.ResponseWriter, status int, code, message, revision string) {
	writeJSON(w, status, protocol.ErrorResponse{OK: false, Error: message, Code: code, ServerRevision: revision})
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "stale base") || strings.Contains(err.Error(), "project mismatch") || strings.Contains(err.Error(), "base revision") {
		return "conflict"
	}
	return "sync_error"
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

func chunkCount(data []byte) int {
	return len(chunkBounds(data))
}

func chunkBounds(data []byte) [][2]int {
	if len(data) == 0 {
		return [][2]int{{0, 0}}
	}
	bounds := make([][2]int, 0, (len(data)+chunkBytes-1)/chunkBytes)
	for start := 0; start < len(data); {
		end := start + chunkBytes
		if end > len(data) {
			end = len(data)
		}
		for end > start && end < len(data) && data[end]&0xc0 == 0x80 {
			end--
		}
		if end <= start {
			end = minInt(len(data), start+chunkBytes)
		}
		bounds = append(bounds, [2]int{start, end})
		start = end
	}
	return bounds
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
