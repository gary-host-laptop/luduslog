// Command luduslog runs the server and provides small operational helpers.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"luduslog/internal/auth"
	"luduslog/internal/backloggd"
	"luduslog/internal/config"
	"luduslog/internal/federation"
	"luduslog/internal/igdb"
	"luduslog/internal/models"
	"luduslog/internal/queue"
	"luduslog/internal/store"
	"luduslog/internal/web"
)

var version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	// The config flag may appear before or after positional arguments, so pull
	// it out ourselves before the standard flag package stops at the first one.
	cfgPathValue := "config.toml"
	args := make([]string, 0, len(os.Args)-2)
	for i := 2; i < len(os.Args); i++ {
		if (os.Args[i] == "-config" || os.Args[i] == "--config") && i+1 < len(os.Args) {
			cfgPathValue = os.Args[i+1]
			i++
			continue
		}
		args = append(args, os.Args[i])
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := &cfgPathValue
	var rest []string
	switch cmd {
	case "admin", "igdb", "import", "export-user":
		fs.Parse(args)
		rest = fs.Args()
	case "serve", "migrate", "seed", "backup":
		fs.Parse(args)
		rest = fs.Args()
	case "version":
		fmt.Println("luduslog", version)
		return
	case "help":
		usage()
	default:
		usage()
	}
	cfg := config.Load(*cfgPath)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	switch cmd {
	case "serve":
		runServe(ctx, cfg, logger)
	case "migrate":
		withStore(cfg, logger, func(st *store.Store) error {
			if err := st.Migrate(ctx); err != nil {
				return err
			}
			fmt.Println("migrations applied")
			return nil
		})
	case "seed":
		withStore(cfg, logger, func(st *store.Store) error {
			if err := st.Migrate(ctx); err != nil {
				return err
			}
			return seed(ctx, st, cfg)
		})
	case "admin":
		runAdmin(ctx, cfg, logger, rest)
	case "backup":
		runBackup(ctx, cfg, rest)
	case "export-user":
		runExport(ctx, cfg, logger, rest)
	case "igdb":
		runIGDB(ctx, cfg, logger, rest)
	case "import":
		runImport(ctx, cfg, logger, rest)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `LudusLog

  luduslog serve [-config config.toml]
  luduslog migrate [-config config.toml]
  luduslog seed [-config config.toml]
  luduslog admin create <username> <email> [password]
  luduslog backup <destination.db>	  luduslog export-user <username> [out.json]
	  luduslog igdb sync [--limit 200]
	  luduslog import backloggd <export.json|export.csv> --user <username> [--no-fetch]
	  luduslog version
`)
	os.Exit(2)
}

func withStore(cfg config.Config, logger *slog.Logger, fn func(*store.Store) error) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fatal(logger, "open database", err)
	}
	defer st.DB.Close()
	if err := fn(st); err != nil {
		fatal(logger, "command", err)
	}
}

func fatal(logger *slog.Logger, msg string, err error) {
	logger.Error(msg, "err", err)
	os.Exit(1)
}

func runServe(ctx context.Context, cfg config.Config, logger *slog.Logger) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fatal(logger, "open database", err)
	}
	defer st.DB.Close()
	if err := st.Migrate(ctx); err != nil {
		fatal(logger, "migrate", err)
	}
	q := queue.New(st, logger)
	q.Register("deliver", deliveryHandler(st, cfg.BaseURL, logger))
	q.Start(4)
	defer q.Stop()

	srv, err := web.New(cfg, st, q, logger)
	if err != nil {
		fatal(logger, "web server", err)
	}
	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "base_url", cfg.BaseURL)
		var err error
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			err = httpSrv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(logger, "server", err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdown); err != nil {
		logger.Error("shutdown", "err", err)
	}
}

type deliveryPayload struct {
	UserID  int64  `json:"user_id"`
	Inbox   string `json:"inbox"`
	Payload string `json:"payload"`
}

func deliveryHandler(st *store.Store, baseURL string, logger *slog.Logger) queue.Handler {
	client := &http.Client{Timeout: 15 * time.Second}
	return func(ctx context.Context, raw string) error {
		var p deliveryPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil // malformed jobs are dropped
		}
		u, err := st.UserByID(ctx, p.UserID)
		if err != nil {
			return nil
		}
		key, err := st.ActorKey(ctx, u.ID)
		if err != nil || key == "" {
			return fmt.Errorf("no actor key for %d", u.ID)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Inbox, strings.NewReader(p.Payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/activity+json")
		req.Header.Set("User-Agent", "LudusLog/"+version)
		req.Header.Set("Accept", "application/activity+json")
		keyID := strings.TrimSuffix(baseURL, "/") + "/users/" + u.Username + "#main-key"
		if err := federation.SignRequest(req, keyID, key, []byte(p.Payload)); err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			logger.Info("delivered", "inbox", p.Inbox, "status", resp.StatusCode)
			return nil
		}
		return fmt.Errorf("delivery to %s: %s", p.Inbox, resp.Status)
	}
}

func runAdmin(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) {
	if len(args) < 3 || args[0] != "create" {
		fmt.Fprintln(os.Stderr, "usage: luduslog admin create <username> <email> [password]")
		os.Exit(2)
	}
	username, email := args[1], args[2]
	password := ""
	if len(args) > 3 {
		password = args[3]
	} else {
		fmt.Fprint(os.Stderr, "password: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		password = strings.TrimSpace(line)
	}
	if len(password) < 8 {
		fmt.Fprintln(os.Stderr, "password must be at least 8 characters")
		os.Exit(1)
	}
	withStore(cfg, logger, func(st *store.Store) error {
		if err := st.Migrate(ctx); err != nil {
			return err
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		u, err := st.CreateUser(ctx, username, email, hash, true)
		if err != nil {
			return err
		}
		st.SetApproved(ctx, u.ID, true)
		fmt.Printf("created admin %s (%d)\n", u.Username, u.ID)
		return nil
	})
}

func runBackup(ctx context.Context, cfg config.Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: luduslog backup <destination.db>")
		os.Exit(2)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer st.DB.Close()
	if _, err := st.DB.ExecContext(ctx, `VACUUM INTO ?`, args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "backup:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", args[0])
}

func runExport(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: luduslog export-user <username> [out.json]")
		os.Exit(2)
	}
	out := ""
	if len(args) > 1 {
		out = args[1]
	}
	withStore(cfg, logger, func(st *store.Store) error {
		u, err := st.UserByUsername(ctx, args[0])
		if err != nil {
			return err
		}
		logs, _, err := st.UserLogs(ctx, u.ID, false, 10000, 0)
		if err != nil {
			return err
		}
		lists, _ := st.ListsForUser(ctx, u.ID, true, 1000, 0)
		payload := map[string]any{
			"exported_at": time.Now().UTC(),
			"user": map[string]any{
				"username": u.Username, "email": u.Email, "bio": u.Bio, "created_at": u.CreatedAt,
			},
			"logs": logs, "lists": lists,
		}
		var w io.Writer = os.Stdout
		if out != "" {
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			defer f.Close()
			w = f
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	})
}

func seed(ctx context.Context, st *store.Store, cfg config.Config) error {
	var exists int
	st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM games`).Scan(&exists)
	if exists > 0 {
		fmt.Println("games already present; nothing to seed")
		return nil
	}
	games := []models.Game{
		{IGDBID: -1, Slug: "celeste", Title: "Celeste", ReleaseYear: 2018, ReleaseStatus: "released", Summary: "A precise platformer about climbing a mountain and facing yourself.", CoverURL: "/static/cover-placeholder.svg", Genres: []string{"Platformer"}, Platforms: []models.Platform{{Name: "PC"}, {Name: "Switch"}}, Companies: []models.CompanyRole{{Name: "Maddy Makes Games", Role: "developer"}}},
		{IGDBID: -2, Slug: "hollow-knight", Title: "Hollow Knight", ReleaseYear: 2017, ReleaseStatus: "released", Summary: "A hand-drawn metroidvania through the ruined kingdom of Hallownest.", CoverURL: "/static/cover-placeholder.svg", Genres: []string{"Metroidvania"}, Platforms: []models.Platform{{Name: "PC"}, {Name: "Switch"}}, Companies: []models.CompanyRole{{Name: "Team Cherry", Role: "developer"}}},
		{IGDBID: -3, Slug: "disco-elysium", Title: "Disco Elysium", ReleaseYear: 2019, ReleaseStatus: "released", Summary: "A detective RPG where your own mind is the toughest opponent.", CoverURL: "/static/cover-placeholder.svg", Genres: []string{"RPG"}, Platforms: []models.Platform{{Name: "PC"}}, Companies: []models.CompanyRole{{Name: "ZA/UM", Role: "developer"}}},
		{IGDBID: -4, Slug: "outer-wilds", Title: "Outer Wilds", ReleaseYear: 2019, ReleaseStatus: "released", Summary: "Explore a solar system stuck in a 22-minute time loop.", CoverURL: "/static/cover-placeholder.svg", Genres: []string{"Adventure"}, Platforms: []models.Platform{{Name: "PC"}, {Name: "Xbox"}}, Companies: []models.CompanyRole{{Name: "Mobius Digital", Role: "developer"}}},
		{IGDBID: -5, Slug: "tetris-effect", Title: "Tetris Effect", ReleaseYear: 2018, ReleaseStatus: "released", Summary: "Tetris reimagined as a synesthetic, meditative experience.", CoverURL: "/static/cover-placeholder.svg", Genres: []string{"Puzzle"}, Platforms: []models.Platform{{Name: "PC"}, {Name: "PS4"}}, Companies: []models.CompanyRole{{Name: "Monstars", Role: "developer"}}},
	}
	for i := range games {
		if _, err := st.SaveGame(ctx, &games[i]); err != nil {
			return fmt.Errorf("seed %s: %w", games[i].Title, err)
		}
	}
	for _, b := range [][3]string{
		{"first-log", "First Log", "Write your first log."},
		{"ten-games", "Ten Games", "Log ten games."},
		{"reviewer", "Reviewer", "Publish a review."},
		{"listmaker", "List Maker", "Create a public list."},
		{"social", "Good Company", "Follow another player."},
	} {
		st.DB.ExecContext(ctx, `INSERT OR IGNORE INTO badges(slug,name,description) VALUES(?,?,?)`, b[0], b[1], b[2])
	}
	st.DB.ExecContext(ctx, `INSERT INTO news(title,body,created_at) VALUES(?,?,?)`,
		"Welcome to "+cfg.Name, "This instance runs LudusLog, an open-source game log for the fediverse.", store.Now())
	fmt.Printf("seeded %d games, 5 badges and a news post\n", len(games))
	return nil
}

func runImport(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) {
	if len(args) < 2 || args[0] != "backloggd" {
		fmt.Fprintln(os.Stderr, "usage: luduslog import backloggd <export.json|export.csv> --user <username> [--no-fetch]")
		os.Exit(2)
	}
	path, username := args[1], ""
	fetch := true
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--user", "-user":
			if i+1 < len(args) {
				username = args[i+1]
				i++
			}
		case "--no-fetch":
			fetch = false
		}
	}
	if username == "" {
		fmt.Fprintln(os.Stderr, "--user is required")
		os.Exit(2)
	}
	f, err := os.Open(path)
	if err != nil {
		fatal(logger, "open export", err)
	}
	defer f.Close()
	entries, err := backloggd.Parse(f, path)
	if err != nil {
		fatal(logger, "parse export", err)
	}
	withStore(cfg, logger, func(st *store.Store) error {
		u, err := st.UserByUsername(ctx, username)
		if err != nil {
			return fmt.Errorf("user %q: %w", username, err)
		}
		opt := backloggd.ImportOptions{UserID: u.ID, Fetch: fetch}
		if fetch {
			if cfg.HasIGDB() {
				opt.Client = igdb.New(cfg.IgdbClientID, cfg.IgdbSecret)
			} else {
				fmt.Fprintln(os.Stderr, "IGDB credentials are not configured; importing only games already in the catalog")
			}
		}
		opt.Progress = func(done, total int) {
			if done < total {
				fmt.Fprintf(os.Stderr, "  %d/%d\n", done, total)
			}
		}
		res, err := backloggd.Import(ctx, st, entries, opt)
		if err != nil {
			return err
		}
		fmt.Println(res.Summary())
		if len(res.Missing) > 0 {
			limit := len(res.Missing)
			if limit > 20 {
				limit = 20
			}
			fmt.Fprintln(os.Stderr, "Unmatched games:")
			for _, title := range res.Missing[:limit] {
				fmt.Fprintln(os.Stderr, "  "+title)
			}
			if len(res.Missing) > limit {
				fmt.Fprintf(os.Stderr, "  … and %d more\n", len(res.Missing)-limit)
			}
		}
		for i, e := range res.Errors {
			if i >= 10 {
				fmt.Fprintf(os.Stderr, "  … and %d more errors\n", len(res.Errors)-i)
				break
			}
			fmt.Fprintln(os.Stderr, "  error: "+e)
		}
		return nil
	})
}

func runIGDB(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) {
	if len(args) < 1 || args[0] != "sync" {
		fmt.Fprintln(os.Stderr, "usage: luduslog igdb sync [--limit 200]")
		os.Exit(2)
	}
	limit := 200
	for i, a := range args {
		if a == "--limit" && i+1 < len(args) {
			limit, _ = strconv.Atoi(args[i+1])
		}
	}
	if !cfg.HasIGDB() {
		fmt.Fprintln(os.Stderr, "IGDB_CLIENT_ID and IGDB_CLIENT_SECRET are required")
		os.Exit(1)
	}
	withStore(cfg, logger, func(st *store.Store) error {
		return igdbSync(ctx, st, cfg, limit)
	})
}

func dirOf(p string) string { return filepath.Dir(p) }
