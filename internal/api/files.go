package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"bshop/internal/store"

	"github.com/google/uuid"
)

const maxUpload = 30 << 20

var allowedMime = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/webp": true, "image/gif": true, "image/svg+xml": true,
	"video/mp4": true, "video/webm": true,
}

func (s *Server) fileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/files", s.admin(s.uploadFile))
	mux.HandleFunc("GET /api/admin/files", s.admin(s.listFiles))
	mux.HandleFunc("DELETE /api/admin/files/{id}", s.admin(s.deleteFile))
	mux.HandleFunc("GET /files/{id}", s.serveFile)
	mux.HandleFunc("GET /manifest.webmanifest", s.manifest)
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		bad(w, "файл слишком большой (до 30 МБ)")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		bad(w, "выберите файл")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxUpload+1))
	if err != nil || len(data) > maxUpload {
		bad(w, "файл слишком большой (до 30 МБ)")
		return
	}
	mime := http.DetectContentType(data)
	if strings.HasSuffix(strings.ToLower(hdr.Filename), ".svg") && bytes.Contains(data[:min(len(data), 512)], []byte("<svg")) {
		mime = "image/svg+xml"
	}
	if i := strings.Index(mime, ";"); i > 0 {
		mime = mime[:i]
	}
	if !allowedMime[mime] {
		bad(w, "поддерживаются PNG, JPEG, WebP, GIF, SVG, MP4 и WebM")
		return
	}
	name := hdr.Filename
	if len(name) > 120 {
		name = name[:120]
	}
	file, err := store.PutFile(r.Context(), s.svc.Store.Pool, name, mime, data)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": file.ID, "name": file.Name, "mime": file.Mime, "size": file.Size, "url": "/files/" + file.ID.String()})
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	files, err := store.ListFiles(r.Context(), s.svc.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		bad(w, "неверный файл")
		return
	}
	if err := store.DeleteFile(r.Context(), s.svc.Store.Pool, id); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := store.GetFile(r.Context(), s.svc.Store.Pool, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", f.Mime)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if f.Mime == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	}
	http.ServeContent(w, r, f.Name, f.CreatedAt, bytes.NewReader(f.Data))
}

func fileURL(id string) string {
	if id == "" {
		return ""
	}
	return "/files/" + id
}

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	b := s.svc.Settings.Get().Branding
	icons := []map[string]string{}
	if b.PWAIconFile != "" {
		for _, size := range []string{"192x192", "512x512"} {
			icons = append(icons, map[string]string{"src": fileURL(b.PWAIconFile), "sizes": size, "purpose": "any maskable"})
		}
	} else {
		icons = append(icons, map[string]string{"src": "/icon.svg", "sizes": "any", "type": "image/svg+xml"})
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name": b.PWAName, "short_name": b.PWAShortName, "start_url": "/", "display": "standalone",
		"background_color": b.Background, "theme_color": b.Background, "icons": icons,
		"lang": "ru", "id": "/", "description": b.Tagline,
	})
}

var mimeCache sync.Map // file id -> mime

// mediaURL returns the file URL with a "#video" marker for videos so the client can pick <video>.
func (s *Server) mediaURL(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	mime, ok := mimeCache.Load(id)
	if !ok {
		uid, err := uuid.Parse(id)
		if err != nil {
			return ""
		}
		var m string
		if err := s.svc.Store.Pool.QueryRow(ctx, `SELECT mime FROM files WHERE id=$1`, uid).Scan(&m); err != nil {
			return ""
		}
		mimeCache.Store(id, m)
		mime = m
	}
	u := "/files/" + id
	if strings.HasPrefix(mime.(string), "video/") {
		u += "#video"
	}
	return u
}
