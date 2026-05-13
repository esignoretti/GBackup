package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"

	"github.com/esignoretti/gbackup/internal/config"
	"github.com/esignoretti/gbackup/internal/storage"
)

//go:embed templates/*
var templateFS embed.FS

type Server struct {
	Config *config.Config
	Store  *storage.Client
	Addr   string
}

type DashboardData struct {
	Domain string
	Bucket string
	Region string
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	tmplContent, err := templateFS.ReadFile("templates/index.html")
	if err != nil {
		return fmt.Errorf("reading template: %w", err)
	}
	tmpl := template.Must(template.New("dashboard").Parse(string(tmplContent)))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		tmpl.Execute(w, DashboardData{
			Domain: s.Config.Workspace.Domain,
			Bucket: s.Config.Storage.Bucket,
			Region: s.Config.Storage.Region,
		})
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"domain": s.Config.Workspace.Domain,
			"bucket": s.Config.Storage.Bucket,
		})
	})

	fmt.Printf("Web dashboard starting on %s\n", s.Addr)
	return http.ListenAndServe(s.Addr, mux)
}
