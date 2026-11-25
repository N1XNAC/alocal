// Package gui implements the Alocal desktop GUI server. It wraps Alocal's
// existing Go libraries and exposes them via HTTP/JSON endpoints consumed by
// the embedded web frontend.
package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"alocal/internal/catalog"
	"alocal/internal/hardware"
	"alocal/internal/hf"
	"alocal/internal/local"
	"alocal/internal/match"
	"alocal/internal/runtime"
)

// Server is the Alocal GUI HTTP server.
type Server struct {
	mu       sync.Mutex
	catalog  *catalog.Catalog
	profile  *hardware.Profile
	store    *local.ModelStore
	client   *hf.Client
	server   *runtime.Server
	active   *local.Model
	downloads map[string]*downloadState
}

// NewServer creates a GUI server backed by Alocal's libraries.
func NewServer() (*Server, error) {
	store, err := local.LoadModels()
	if err != nil {
		store = &local.ModelStore{Models: []local.Model{}}
	}

	cat, err := catalog.Load()
	if err != nil {
		return nil, fmt.Errorf("load catalog: %w", err)
	}

	s := &Server{
		catalog:   cat,
		store:     store,
		client:    hf.New(),
		downloads: make(map[string]*downloadState),
	}
	s.profile = detectHardware()
	return s, nil
}

func detectHardware() *hardware.Profile {
	p, err := hardware.SystemDetector{Options: hardware.Options{Verbose: false}}.Detect(context.Background())
	if err != nil {
		log.Printf("hardware detection failed: %v", err)
		return nil
	}
	return p
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// System
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/hardware", s.handleHardware)

	// Models / catalogue
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/models/search", s.handleModelsSearch)
	mux.HandleFunc("/api/models/categories", s.handleCategories)
	mux.HandleFunc("/api/models/download", s.handleModelDownload)
	mux.HandleFunc("/api/models/download/status", s.handleDownloadStatus)
	mux.HandleFunc("/api/models/local", s.handleLocalModels)
	mux.HandleFunc("/api/models/delete", s.handleModelDelete)

	// Chat
	mux.HandleFunc("/api/chat/send", s.handleChatSend)
	mux.HandleFunc("/api/chat/stream", s.handleChatStream)
	mux.HandleFunc("/api/chat/models", s.handleChatModels)
	mux.HandleFunc("/api/chat/load", s.handleChatLoadModel)
	mux.HandleFunc("/api/chat/unload", s.handleChatUnloadModel)

	// Recommendations
	mux.HandleFunc("/api/recommend", s.handleRecommend)

	return mux
}

func (s *Server) Listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

func (s *Server) Serve(l net.Listener) error {
	return http.Serve(l, s.Handler())
}

// ---------- Helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func keyForDownload(repo, file string) string {
	return repo + "::" + file
}

func (s *Server) ensureModelsDir() error {
	dir := filepath.Join(local.DataDir(), "models")
	return os.MkdirAll(dir, 0o755)
}

var _ = time.Second

// ---------- Status ----------

type statusResponse struct {
	Version   string `json:"version"`
	Mode      string `json:"mode"`
	Model     string `json:"model,omitempty"`
	Hardware  bool   `json:"hardware"`
	Runtime   bool   `json:"runtime"`
	Downloads int    `json:"downloads"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	model := ""
	if s.active != nil {
		model = s.active.ID
	}
	s.mu.Lock()
	dl := len(s.downloads)
	s.mu.Unlock()
	writeJSON(w, 200, statusResponse{
		Version:   "0.3.0",
		Mode:      "gui",
		Model:     model,
		Hardware:  s.profile != nil,
		Runtime:   s.server != nil,
		Downloads: dl,
	})
}

// ---------- Hardware ----------

func (s *Server) handleHardware(w http.ResponseWriter, r *http.Request) {
	if s.profile == nil {
		s.profile = detectHardware()
	}
	writeJSON(w, 200, s.profile)
}

// ---------- Models / Catalogue ----------

type modelListResponse struct {
	Models  []catalog.Model `json:"models"`
	Total   int             `json:"total"`
	Filtered int            `json:"filtered"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, modelListResponse{
		Models:  s.catalog.Models,
		Total:   len(s.catalog.Models),
		Filtered: len(s.catalog.Models),
	})
}

func (s *Server) handleModelsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	var filtered []catalog.Model
	if q == "" {
		filtered = s.catalog.Models
	} else {
		for _, m := range s.catalog.Models {
			if strings.Contains(strings.ToLower(m.DisplayName), q) ||
				strings.Contains(strings.ToLower(m.ID), q) ||
				strings.Contains(strings.ToLower(m.Family), q) {
				filtered = append(filtered, m)
			}
		}
	}
	writeJSON(w, 200, modelListResponse{
		Models:  filtered,
		Total:   len(s.catalog.Models),
		Filtered: len(filtered),
	})
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	// Return simple category info
	writeJSON(w, 200, map[string]any{
		"all": len(s.catalog.Models),
		"local": len(s.store.Models),
	})
}

func (s *Server) handleModelDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	var req struct {
		Repo string `json:"repo"`
		File string `json:"file"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if req.Repo == "" || req.File == "" {
		http.Error(w, "repo and file required", 400)
		return
	}

	key := keyForDownload(req.Repo, req.File)
	s.mu.Lock()
	if ds, ok := s.downloads[key]; ok && (ds.Status == "downloading" || ds.Status == "pending") {
		s.mu.Unlock()
		writeJSON(w, 200, ds)
		return
	}
	s.mu.Unlock()

	if err := s.ensureModelsDir(); err != nil {
		http.Error(w, "cannot create models dir: "+err.Error(), 500)
		return
	}

	ds := &downloadState{
		Repo:   req.Repo,
		File:   req.File,
		Done:   0,
		Total:  0,
		Status: "pending",
		Start:  time.Now(),
	}
	s.mu.Lock()
	s.downloads[key] = ds
	s.mu.Unlock()

	go func() {
		ds.Status = "downloading"
		dest := local.ModelPath(req.Repo, req.File)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			ds.Status = "error"
			ds.Error = err.Error()
			return
		}
		err := s.client.Download(context.Background(), req.Repo, req.File, dest, func(done, total int64) {
			ds.Done = done
			ds.Total = total
		})
		if err != nil {
			ds.Status = "error"
			ds.Error = err.Error()
			return
		}
		if err := hf.VerifyGGUF(dest); err != nil {
			ds.Status = "error"
			ds.Error = "verify failed: " + err.Error()
			return
		}
		info, err := os.Stat(dest)
		if err != nil {
			ds.Status = "error"
			ds.Error = err.Error()
			return
		}
		model := local.Model{
			ID:        req.Repo + "/" + req.File,
			Repo:      req.Repo,
			File:      req.File,
			Path:      dest,
			SizeBytes: info.Size(),
			Quant:     quantFromName(req.File),
		}
		if err := s.store.Add(model); err != nil {
			ds.Status = "error"
			ds.Error = "registry add failed: " + err.Error()
			return
		}
		ds.Status = "completed"
		ds.Done = ds.Total
	}()

	writeJSON(w, 200, ds)
}

func (s *Server) handleDownloadStatus(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	file := r.URL.Query().Get("file")
	key := keyForDownload(repo, file)
	s.mu.Lock()
	ds, ok := s.downloads[key]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, 200, downloadState{Repo: repo, File: file, Status: "not_found"})
		return
	}
	writeJSON(w, 200, ds)
}

func (s *Server) handleLocalModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"models": s.store.Models, "count": len(s.store.Models)})
}

func (s *Server) handleModelDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", 405)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id required", 400)
		return
	}
	ok, err := s.store.Remove(id)
	if err != nil || !ok {
		http.Error(w, "model not found", 404)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// ---------- Chat ----------

type chatSendRequest struct {
	Message     string  `json:"message"`
	ModelID     string  `json:"model_id,omitempty"`
	System      string  `json:"system,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type chatSendResponse struct {
	Response string `json:"response"`
	Model    string `json:"model"`
	Tokens   int    `json:"tokens"`
}

func (s *Server) handleChatSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	var req chatSendRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	if req.ModelID != "" && (s.active == nil || s.active.ID != req.ModelID) {
		if err := s.loadModel(req.ModelID); err != nil {
			http.Error(w, "failed to load model: "+err.Error(), 500)
			return
		}
	}
	if s.server == nil {
		http.Error(w, "no model loaded", 400)
		return
	}

	client := runtime.NewClient(s.server.BaseURL())
	msgs := []runtime.Message{
		{Role: "system", Content: req.System},
		{Role: "user", Content: req.Message},
	}
	var tokens int
	var resp strings.Builder
	err = client.ChatStream(context.Background(), msgs, runtime.GenOptions{
		Temperature: req.Temperature,
	}, func(token string, done bool, n int) {
		if done {
			tokens = n
			return
		}
		resp.WriteString(token)
	})
	if err != nil {
		http.Error(w, "chat error: "+err.Error(), 500)
		return
	}
	writeJSON(w, 200, chatSendResponse{
		Response: resp.String(),
		Model:    s.active.ID,
		Tokens:   tokens,
	})
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	var req chatSendRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	if req.ModelID != "" && (s.active == nil || s.active.ID != req.ModelID) {
		if err := s.loadModel(req.ModelID); err != nil {
			http.Error(w, "failed to load model: "+err.Error(), 500)
			return
		}
	}
	if s.server == nil {
		http.Error(w, "no model loaded", 400)
		return
	}

	client := runtime.NewClient(s.server.BaseURL())
	msgs := []runtime.Message{
		{Role: "system", Content: req.System},
		{Role: "user", Content: req.Message},
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}

	err = client.ChatStream(context.Background(), msgs, runtime.GenOptions{
		Temperature: req.Temperature,
	}, func(token string, done bool, n int) {
		if done {
			fmt.Fprintf(w, "event: done\ndata: {\"tokens\": %d}\n\n", n)
			flusher.Flush()
			return
		}
		fmt.Fprintf(w, "event: token\ndata: %s\n\n", jsonEscape(token))
		flusher.Flush()
	})
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", jsonEscape(err.Error()))
		flusher.Flush()
	}
}

func (s *Server) handleChatModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"models": s.store.Models, "count": len(s.store.Models)})
}

func (s *Server) handleChatLoadModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	var req struct {
		ModelID string `json:"model_id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if err := s.loadModel(req.ModelID); err != nil {
		http.Error(w, "failed to load model: "+err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "loaded", "model": s.active.ID})
}

func (s *Server) handleChatUnloadModel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.server != nil {
		_ = s.server.Close()
		s.server = nil
	}
	s.active = nil
	s.mu.Unlock()
	writeJSON(w, 200, map[string]string{"status": "unloaded"})
}

func (s *Server) loadModel(modelID string) error {
	s.mu.Lock()
	m, ok := s.store.Find(modelID)
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("model %s not found", modelID)
	}
	if s.server != nil {
		_ = s.server.Close()
	}
	report := func(text string) {
		log.Printf("runtime: %s", text)
	}
	srv, err := runtime.StartWithProgress(context.Background(), "", m.Path, runtime.Options{
		Context:   8192,
		GPULayers: -1,
	}, report)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.server = srv
	s.active = &m
	s.mu.Unlock()
	return nil
}

// ---------- Recommendations ----------

type recommendResponse struct {
	Hardware        string         `json:"hardware"`
	Score           float64        `json:"score"`
	MemoryGB        float64        `json:"memory_gb"`
	LimitGB         float64        `json:"limit_gb"`
	Recommendations []recommendation `json:"recommendations"`
}

type recommendation struct {
	ModelID   string  `json:"model_id"`
	Display   string  `json:"display"`
	Repo      string  `json:"repo"`
	File      string  `json:"file"`
	Quant     string  `json:"quant"`
	SizeGB    float64 `json:"size_gb"`
	Score     float64 `json:"score"`
	Fit       bool    `json:"fit"`
	Reason    string  `json:"reason"`
}

func (s *Server) handleRecommend(w http.ResponseWriter, r *http.Request) {
	if s.profile == nil {
		s.profile = detectHardware()
	}

	res := match.Recommend(s.profile, s.catalog, match.Options{Top: 5, Context: 8192})

	var recs []recommendation
	for _, rec := range res.Recommendations {
		m := rec.Model
		repo := m.Runtimes.LlamaCpp
		if repo == "" {
			repo = m.ID
		}
		// Strip hf.co/ prefix if present
		repo = strings.TrimPrefix(repo, "hf.co/")
		file := ""
		quant := m.Quant
		if quant == "" {
			quant = "Q4_K_M"
		}
		// Construct a likely GGUF filename
		if file == "" {
			file = strings.ReplaceAll(m.DisplayName, " ", "-") + "-" + quant + ".gguf"
		}
		recs = append(recs, recommendation{
			ModelID: m.ID,
			Display: m.DisplayName,
			Repo:    repo,
			File:    file,
			Quant:   quant,
			SizeGB:  rec.Need.RequiredGB,
			Score:   rec.Need.Score,
			Fit:     rec.Fit,
			Reason:  rec.Reason,
		})
	}

	writeJSON(w, 200, recommendResponse{
		Hardware:        fmt.Sprintf("%.1f / 100 %s", res.Hardware.Score, res.Hardware.Tier),
		Score:           res.Hardware.Score,
		MemoryGB:        res.EffectiveMemoryGB,
		LimitGB:         res.MemoryLimitGB,
		Recommendations: recs,
	})
}

// ---------- Utilities ----------

func quantFromName(name string) string {
	upper := strings.ToUpper(name)
	for _, q := range []string{"IQ4_XS", "Q4_K_M", "Q5_K_M", "Q6_K", "Q8_0", "Q3_K_M", "BF16", "F16", "FP16"} {
		if strings.Contains(upper, q) {
			return q
		}
	}
	return ""
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

type downloadState struct {
	Repo   string    `json:"repo"`
	File   string    `json:"file"`
	Done   int64     `json:"done"`
	Total  int64     `json:"total"`
	Status string    `json:"status"`
	Error  string    `json:"error,omitempty"`
	Start  time.Time `json:"start"`
}

var _ = io.EOF
