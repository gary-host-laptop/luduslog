// Package catalog contains the shared game-catalog logic used by the CLI, the
// admin web handlers and the trusted catalog editor. Validation lives here so
// a future API can reuse it without going through the HTML handlers.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"luduslog/internal/models"
	"luduslog/internal/store"
)

// Service exposes catalog editing operations backed by the store.
type Service struct {
	Store *store.Store
}

func New(st *store.Store) *Service {
	return &Service{Store: st}
}

// ValidationError carries human-readable problems rendered back in the form.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string { return strings.Join(e.Problems, "; ") }

// IsValidation reports whether err came from user input rather than the store.
func IsValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

// GameInput is the complete editable surface of a catalog record.
type GameInput struct {
	Title         string
	Summary       string
	CoverURL      string
	BannerURL     string
	ReleaseDate   string
	ReleaseYear   int
	ReleaseStatus string
	Category      string
	Franchise     string
	EditionName   string
	ParentSlug    string
	Adult         bool
	Restricted    bool

	Platforms []string
	Genres    []string
	Themes    []string
	Modes     []string
	AltNames  []string
	Companies []models.CompanyRole
	Releases  []models.ReleaseDate

	Screenshots []string
	Videos      []string

	TimeHastily    int
	TimeNormally   int
	TimeCompletely int

	// Relations are keyed by the editor field name: remakes, updates, ports,
	// bundles, related, contents. Values are game slugs.
	Relations map[string][]string
}

var relationKinds = map[string]string{
	"related":  "related",
	"remakes":  "remake",
	"updates":  "update",
	"ports":    "port",
	"bundles":  "bundle",
	"contents": "bundle_content",
}

var validStatuses = map[string]bool{
	"": true, "released": true, "upcoming": true, "cancelled": true, "early_access": true,
	"rumored": true, "delisted": true, "alpha": true, "beta": true, "offline": true,
}

var validCategories = map[string]bool{
	"main_game": true, "dlc_addon": true, "expansion": true, "standalone_expansion": true,
	"mod": true, "episode": true, "season": true, "remake": true, "remaster": true,
	"expanded_game": true, "port": true, "fork": true, "pack": true, "bundle": true, "update": true,
}

var validCompanyRoles = map[string]bool{
	"developer": true, "publisher": true, "porting": true, "supporting": true,
}

// Create inserts a manual catalog entry. Its slug is derived once from the
// title and never changes on later edits.
func (s *Service) Create(ctx context.Context, in GameInput) (*models.Game, error) {
	g, err := s.build(ctx, in, nil)
	if err != nil {
		return nil, err
	}
	g.Slug = s.uniqueSlug(ctx, slugify(in.Title))
	g.ID = 0
	return s.Store.SaveCatalogGame(ctx, g)
}

// Update rewrites the public metadata of an existing record. IGDB identity,
// slug, timestamps and derived counters are preserved by the store.
func (s *Service) Update(ctx context.Context, slug string, in GameInput) (*models.Game, error) {
	current, err := s.Store.GameBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	g, err := s.build(ctx, in, current)
	if err != nil {
		return nil, err
	}
	g.ID = current.ID
	g.Slug = current.Slug
	return s.Store.SaveCatalogGame(ctx, g)
}

func (s *Service) build(ctx context.Context, in GameInput, current *models.Game) (*models.Game, error) {
	var problems []string
	g := &models.Game{
		Title:         strings.TrimSpace(in.Title),
		Summary:       strings.TrimSpace(in.Summary),
		CoverURL:      strings.TrimSpace(in.CoverURL),
		BannerURL:     strings.TrimSpace(in.BannerURL),
		ReleaseDate:   strings.TrimSpace(in.ReleaseDate),
		ReleaseYear:   in.ReleaseYear,
		ReleaseStatus: strings.TrimSpace(in.ReleaseStatus),
		Category:      strings.TrimSpace(in.Category),
		Franchise:     strings.TrimSpace(in.Franchise),
		EditionName:   strings.TrimSpace(in.EditionName),
		Adult:         in.Adult,
		Restricted:    in.Restricted,
		Screenshots:   cleanLines(in.Screenshots),
		Videos:        cleanLines(in.Videos),
	}
	if g.Title == "" {
		problems = append(problems, "Title is required.")
	} else if len(g.Title) > 200 {
		problems = append(problems, "Title must be 200 characters or fewer.")
	}
	if g.ReleaseStatus == "" {
		g.ReleaseStatus = "released"
	}
	if g.Category == "" {
		g.Category = "main_game"
	}
	if !validStatuses[g.ReleaseStatus] {
		problems = append(problems, "Unknown release status.")
	}
	if !validCategories[g.Category] {
		problems = append(problems, "Unknown game category.")
	}
	if g.ReleaseYear != 0 && (g.ReleaseYear < 1950 || g.ReleaseYear > 2100) {
		problems = append(problems, "Release year must be between 1950 and 2100.")
	}
	if g.ReleaseDate != "" {
		if d, err := time.Parse("2006-01-02", g.ReleaseDate); err != nil {
			problems = append(problems, "Release date must use YYYY-MM-DD.")
		} else if g.ReleaseYear == 0 {
			g.ReleaseYear = d.Year()
		}
	}
	for _, raw := range []struct{ field, value string }{
		{"Cover URL", g.CoverURL}, {"Banner URL", g.BannerURL},
	} {
		if raw.value != "" {
			u, err := url.Parse(raw.value)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				problems = append(problems, raw.field+" must be an http(s) URL.")
			}
		}
	}

	platforms, errs := s.resolvePlatforms(ctx, in.Platforms)
	problems = append(problems, errs...)
	g.Platforms = platforms
	g.Genres = cleanUnique(in.Genres)
	g.Themes = cleanUnique(in.Themes)
	g.Modes = cleanUnique(in.Modes)
	g.AltNames = cleanUnique(in.AltNames)

	for _, c := range in.Companies {
		name := strings.TrimSpace(c.Name)
		role := strings.TrimSpace(strings.ToLower(c.Role))
		if name == "" {
			continue
		}
		if !validCompanyRoles[role] {
			problems = append(problems, fmt.Sprintf("Company %q needs a role of developer, publisher, porting, or supporting.", name))
			continue
		}
		g.Companies = append(g.Companies, models.CompanyRole{Name: name, Role: role})
	}

	for _, rd := range in.Releases {
		release := models.ReleaseDate{
			Platform: strings.TrimSpace(rd.Platform),
			Region:   strings.TrimSpace(rd.Region),
			Date:     strings.TrimSpace(rd.Date),
			Human:    strings.TrimSpace(rd.Human),
			Status:   strings.TrimSpace(rd.Status),
		}
		if release.Platform == "" && release.Date == "" && release.Region == "" {
			continue
		}
		if release.Platform != "" {
			pid, err := s.Store.EnsurePlatform(ctx, release.Platform)
			if err != nil {
				problems = append(problems, "Could not save release platform "+release.Platform+".")
				continue
			}
			release.PlatformID = pid
		}
		if release.Date != "" {
			if _, err := time.Parse("2006-01-02", release.Date); err != nil {
				problems = append(problems, "Release dates must use YYYY-MM-DD.")
			}
		}
		g.ReleaseDates = append(g.ReleaseDates, release)
	}

	if in.TimeHastily < 0 || in.TimeNormally < 0 || in.TimeCompletely < 0 {
		problems = append(problems, "Time to beat cannot be negative.")
	} else if in.TimeHastily > 0 || in.TimeNormally > 0 || in.TimeCompletely > 0 {
		g.TimeToBeat = &models.TimeToBeat{Hastily: in.TimeHastily, Normally: in.TimeNormally, Completely: in.TimeCompletely}
	}

	if parentSlug := strings.TrimSpace(in.ParentSlug); parentSlug != "" {
		if current != nil && strings.EqualFold(parentSlug, current.Slug) {
			problems = append(problems, "A game cannot be its own parent.")
		} else if parent, err := s.Store.GameBySlug(ctx, parentSlug); err != nil {
			problems = append(problems, "Parent game "+parentSlug+" was not found.")
		} else {
			cyclic := false
			if current != nil {
				seen := map[int64]bool{current.ID: true}
				next := parent
				for hops := 0; next != nil && hops < 100; hops++ {
					if seen[next.ID] {
						cyclic = true
						break
					}
					seen[next.ID] = true
					if next.ParentID == nil {
						break
					}
					next, _ = s.Store.GameByID(ctx, *next.ParentID)
				}
			}
			if cyclic {
				problems = append(problems, "That parent would create a cycle.")
			} else {
				g.ParentID = &parent.ID
			}
		}
	}

	relations, relationErrs := s.resolveRelations(ctx, in.Relations, current)
	problems = append(problems, relationErrs...)
	for field, list := range relations {
		switch field {
		case "related":
			g.Related = list
		case "remakes":
			g.Remakes = list
		case "updates":
			g.Updates = list
		case "ports":
			g.Ports = list
		case "bundles":
			g.Bundles = list
		case "contents":
			g.Contents = list
		}
	}

	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return g, nil
}

func (s *Service) resolvePlatforms(ctx context.Context, names []string) ([]models.Platform, []string) {
	var out []models.Platform
	var problems []string
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			problems = append(problems, "Platform "+name+" is listed more than once.")
			continue
		}
		seen[key] = true
		id, err := s.Store.EnsurePlatform(ctx, name)
		if err != nil {
			problems = append(problems, "Could not save platform "+name+".")
			continue
		}
		out = append(out, models.Platform{ID: id, Name: name})
	}
	return out, problems
}

func (s *Service) resolveRelations(ctx context.Context, input map[string][]string, current *models.Game) (map[string][]models.Game, []string) {
	out := map[string][]models.Game{}
	var problems []string
	for field, slugs := range input {
		if _, ok := relationKinds[field]; !ok {
			problems = append(problems, "Unknown relation field "+field+".")
			continue
		}
		seen := map[int64]bool{}
		for _, raw := range slugs {
			slug := strings.TrimSpace(raw)
			if slug == "" {
				continue
			}
			target, err := s.Store.GameBySlug(ctx, slug)
			if err != nil {
				problems = append(problems, "Related game "+slug+" was not found.")
				continue
			}
			if current != nil && target.ID == current.ID {
				problems = append(problems, "A game cannot be related to itself.")
				continue
			}
			if seen[target.ID] {
				problems = append(problems, "Related game "+slug+" is listed more than once.")
				continue
			}
			seen[target.ID] = true
			out[field] = append(out[field], models.Game{ID: target.ID, Slug: target.Slug, Title: target.Title})
		}
	}
	return out, problems
}

func (s *Service) uniqueSlug(ctx context.Context, base string) string {
	candidate := base
	for i := 2; i < 1000; i++ {
		if _, err := s.Store.GameBySlug(ctx, candidate); errors.Is(err, store.ErrNotFound) {
			return candidate
		}
		candidate = base + "-" + strconv.Itoa(i)
	}
	return base + "-" + randomSuffix()
}

func randomSuffix() string {
	return strconv.FormatInt(time.Now().UnixNano()%1000000, 10)
}

func slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "game"
	}
	return slug
}

func cleanLines(values []string) []string {
	var out []string
	for _, raw := range values {
		for _, line := range strings.Split(raw, "\n") {
			if v := strings.TrimSpace(line); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func cleanUnique(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range cleanLines(values) {
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

// Lines helpers keep the server-rendered textareas round-trippable.

func (in GameInput) PlatformLines() string { return strings.Join(in.Platforms, "\n") }
func (in GameInput) GenreLines() string    { return strings.Join(in.Genres, "\n") }
func (in GameInput) ThemeLines() string    { return strings.Join(in.Themes, "\n") }
func (in GameInput) ModeLines() string     { return strings.Join(in.Modes, "\n") }
func (in GameInput) AltNameLines() string  { return strings.Join(in.AltNames, "\n") }
func (in GameInput) ScreenshotLines() string {
	return strings.Join(in.Screenshots, "\n")
}
func (in GameInput) VideoLines() string { return strings.Join(in.Videos, "\n") }

func (in GameInput) CompanyLines() string {
	var lines []string
	for _, c := range in.Companies {
		lines = append(lines, c.Name+" | "+c.Role)
	}
	return strings.Join(lines, "\n")
}

func (in GameInput) ReleaseLines() string {
	var lines []string
	for _, rd := range in.Releases {
		lines = append(lines, strings.Join([]string{rd.Platform, rd.Region, rd.Date, rd.Status}, " | "))
	}
	return strings.Join(lines, "\n")
}

func (in GameInput) RelationLines(field string) string {
	return strings.Join(in.Relations[field], "\n")
}

// InputFromGame reconstructs form values from a loaded game, its outgoing
// relations and its parent's slug.
func InputFromGame(g *models.Game, relations map[string][]models.Game, parentSlug string) GameInput {
	in := GameInput{
		Title:         g.Title,
		Summary:       g.Summary,
		CoverURL:      g.CoverURL,
		BannerURL:     g.BannerURL,
		ReleaseDate:   g.ReleaseDate,
		ReleaseYear:   g.ReleaseYear,
		ReleaseStatus: g.ReleaseStatus,
		Category:      g.Category,
		Franchise:     g.Franchise,
		EditionName:   g.EditionName,
		Adult:         g.Adult,
		Restricted:    g.Restricted,
		ParentSlug:    parentSlug,
		Screenshots:   g.Screenshots,
		Videos:        g.Videos,
		Relations:     map[string][]string{},
	}
	for _, p := range g.Platforms {
		in.Platforms = append(in.Platforms, p.Name)
	}
	in.Genres = g.Genres
	in.Themes = g.Themes
	in.Modes = g.Modes
	in.AltNames = g.AltNames
	in.Companies = g.Companies
	in.Releases = g.ReleaseDates
	if g.TimeToBeat != nil {
		in.TimeHastily = g.TimeToBeat.Hastily
		in.TimeNormally = g.TimeToBeat.Normally
		in.TimeCompletely = g.TimeToBeat.Completely
	}
	for field, kind := range relationKinds {
		for _, rel := range relations[kind] {
			in.Relations[field] = append(in.Relations[field], rel.Slug)
		}
	}
	return in
}
