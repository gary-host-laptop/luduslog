// Package web serves the server-rendered LudusLog application.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"luduslog/internal/auth"
	"luduslog/internal/config"
	"luduslog/internal/igdb"
	"luduslog/internal/mail"
	"luduslog/internal/markdown"
	"luduslog/internal/models"
	"luduslog/internal/queue"
	"luduslog/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	cfg     config.Config
	st      *store.Store
	auth    *auth.Service
	q       *queue.Queue
	log     *slog.Logger
	mail    *mail.Mailer
	igdb    *igdb.Client
	pages   map[string]*template.Template
	mux     *http.ServeMux
	static  http.Handler
	started time.Time
}

func New(cfg config.Config, st *store.Store, q *queue.Queue, log *slog.Logger) (*Server, error) {
	s := &Server{
		cfg:     cfg,
		st:      st,
		auth:    &auth.Service{Store: st},
		q:       q,
		log:     log,
		mail:    mail.New(cfg, log),
		pages:   map[string]*template.Template{},
		mux:     http.NewServeMux(),
		started: time.Now(),
	}
	if cfg.HasIGDB() {
		s.igdb = igdb.New(cfg.IgdbClientID, cfg.IgdbSecret)
	}
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	s.static = http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

func (s *Server) parseTemplates() error {
	funcs := templateFuncs(s)
	entries, err := fs.Glob(assets, "templates/*.html")
	if err != nil {
		return err
	}
	for _, file := range entries {
		name := strings.TrimSuffix(path.Base(file), ".html")
		if name == "layout" || strings.HasPrefix(name, "_") {
			continue
		}
		t, err := template.New("layout").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/_partials.html", "templates/"+path.Base(file))
		if err != nil {
			return fmt.Errorf("parse %s: %w", file, err)
		}
		s.pages[name] = t
	}
	partials, err := template.New("partials").Funcs(funcs).ParseFS(assets, "templates/_partials.html")
	if err != nil {
		return err
	}
	s.pages["partials"] = partials
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler().ServeHTTP(w, r)
}

func (s *Server) handler() http.Handler {
	return s.recoverer(s.securityHeaders(s.logging(s.withSession(s.csrfProtect(s.mux)))))
}

// Page carries the fields every template can rely on.
type Page struct {
	Title          string
	Desc           string
	User           *models.User
	CSRF           string
	Path           string
	Query          url.Values
	Flash          []Flash
	Unread         int
	Instance       config.Config
	Year           int
	Error          string
	Message        string
	IsAdmin        bool
	CanModerate    bool
	CanEditCatalog bool
	SearchQuery    string
	SearchTab      string
}

type Flash struct {
	Kind string
	Text string
}

func (s *Server) page(r *http.Request, title string) Page {
	csrf, _ := r.Context().Value(ctxCSRF).(string)
	u, _ := r.Context().Value(ctxUser).(*models.User)
	unread := 0
	if u != nil {
		unread = s.st.UnreadNotifications(r.Context(), u.ID)
	}
	return Page{
		Title:          title,
		User:           u,
		CSRF:           csrf,
		Path:           r.URL.Path,
		Query:          r.URL.Query(),
		Flash:          ctxValue[[]Flash](r.Context(), ctxFlash),
		Unread:         unread,
		Instance:       s.instanceConfig(r.Context()),
		Year:           time.Now().UTC().Year(),
		IsAdmin:        u.IsAdmin(),
		CanModerate:    u.CanModerate(),
		CanEditCatalog: u.CanEditCatalog(),
		SearchQuery:    r.URL.Query().Get("q"),
	}
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	t, ok := s.pages[name]
	if !ok {
		s.log.Error("template not found", "name", name)
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		s.log.Error("render", "template", name, "err", err)
	}
}

func (s *Server) renderStatus(w http.ResponseWriter, r *http.Request, name string, status int, data any) {
	t, ok := s.pages[name]
	if !ok {
		s.log.Error("template not found", "name", name)
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		s.log.Error("render", "template", name, "err", err)
	}
}

func (s *Server) execute(w http.ResponseWriter, name string, data any) {
	t, ok := s.pages[name]
	if !ok {
		http.NotFound(w, nil)
		return
	}
	t.ExecuteTemplate(w, "layout", data)
}

func (s *Server) renderFragment(w http.ResponseWriter, name, tmplName string, data any) {
	t, ok := s.pages[name]
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, tmplName, data); err != nil {
		s.log.Error("render fragment", "tmpl", tmplName, "err", err)
	}
}

func (s *Server) absolute(p string) string { return s.cfg.BaseURL + p }

func (s *Server) userURL(u *models.User) string {
	return s.absolute("/users/" + url.PathEscape(u.Username))
}

func (s *Server) isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, p string) {
	if s.isHTMX(r) {
		w.Header().Set("HX-Redirect", p)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, p, http.StatusSeeOther)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	s.render(w, r, "error", struct {
		Page
		Code int
	}{s.page(r, "Not found"), 404})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request", "path", r.URL.Path, "err", err)
	w.WriteHeader(http.StatusInternalServerError)
	s.render(w, r, "error", struct {
		Page
		Code int
	}{s.page(r, "Something went wrong"), 500})
}

func safeHTML(s string) template.HTML { return template.HTML(s) }

func md(s string) template.HTML { return template.HTML(markdown.Render(s)) }

func mdPlain(s string) string { return markdown.PlainText(s, 0) }

func ctxValue[T any](ctx context.Context, key any) T {
	v, _ := ctx.Value(key).(T)
	return v
}
