package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"time"

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
	if err := s.validateAddr(); err != nil {
		return err
	}

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
		if err := tmpl.Execute(w, DashboardData{
			Domain: s.Config.Workspace.Domain,
			Bucket: s.Config.Storage.Bucket,
			Region: s.Config.Storage.Region,
		}); err != nil {
			log.Printf("dashboard render error: %v", err)
		}
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"domain": s.Config.Workspace.Domain,
			"bucket": s.Config.Storage.Bucket,
		}); err != nil {
			log.Printf("status encode error: %v", err)
		}
	})

	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	fmt.Printf("Web dashboard starting on %s\n", s.Addr)
	return srv.ListenAndServe()
}

func (s *Server) validateAddr() error {
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", s.Addr, err)
	}
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return nil
	default:
		return fmt.Errorf("refusing to bind to %q: dashboard must be localhost-only", host)
	}
}
