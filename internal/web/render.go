package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"luduslog/internal/markdown"
	"luduslog/internal/models"
	"luduslog/internal/store"
)

func templateFuncs(s *Server) template.FuncMap {
	return template.FuncMap{
		"safe":            safeHTML,
		"safeURL":         func(s string) template.URL { return template.URL(s) },
		"md":              md,
		"plain":           mdPlain,
		"avatar":          avatarURL,
		"header":          headerURL,
		"cover":           coverURL,
		"cover2x":         cover2xURL,
		"coverSrcset":     coverSrcset,
		"userCover":       userCoverURL,
		"screenshotThumb": screenshotThumb,
		"screenshotFull":  screenshotFull,
		"stars":           starsHTML,
		"rating":          ratingText,
		"halfStarLabel":   halfStarLabel,
		"beatTime":        beatTime,
		"beatParts":       beatParts,
		"timeago":         timeAgo,
		"date":            dateFmt,
		"dateSlash":       dateSlash,
		"dateLong":        dateLong,
		"releaseDay":      releaseDay,
		"releaseLabel":    releaseLabel,
		"releaseDate":     releaseDate,
		"atof": func(s string) float64 {
			f, _ := strconv.ParseFloat(s, 64)
			return f
		},
		"query":       url.QueryEscape,
		"queryEscape": url.QueryEscape,
		"shelfAction": shelfAction,
		"kindLabel":   activityKindLabel,
		"slice":       func(values ...string) []string { return values },
		"sameTitle":   sameTitle,
		"altNames":    displayAltNames,
		"year":        func(t time.Time) int { return t.Year() },
		"add":         func(a, b int) int { return a + b },
		"sub":         func(a, b int) int { return a - b },
		"inc":         func(a int) int { return a + 1 },
		"dec":         func(a int) int { return a - 1 },
		"mul":         func(a, b float64) float64 { return a * b },
		"div": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"seq":     seq,
		"float64": func(i int) float64 { return float64(i) },
		"pct": func(n, total int) int {
			if total == 0 {
				return 0
			}
			return int(math.Round(float64(n) * 100 / float64(total)))
		},
		"plural":       plural,
		"truncate":     truncate,
		"tagPreview":   tagPreview,
		"creditNames":  creditNames,
		"relationRole": relationRole,
		"ancestorTab":  ancestorTab,
		"initials":     initials,
		"dict":         dict,
		"derefInt": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"json":     toJSON,
		"listPath": listPath,
		"userPath": func(u *models.User) string { return "/users/" + u.Username },
		"reviewPath": func(l *models.Log) string {
			if l.User == nil {
				return "/reviews/" + strconv.FormatInt(l.ID, 10)
			}
			return "/users/" + l.User.Username + "/reviews/" + strconv.FormatInt(l.ID, 10)
		},
		"privacyLabel":      privacyLabel,
		"notePrivacyLabel":  notePrivacyLabel,
		"playedStatusLabel": playedStatusLabel,
		"shelfLabel": func(v string) string {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "playing":
				return "Playing"
			case "played":
				return "Played"
			case "backlog":
				return "Backlog"
			case "wishlist":
				return "Wishlist"
			}
			return "Unshelved"
		},
		"playedStatuses": func() []string { return store.PlayedStatuses },
		"firstSessions": func(sessions []models.Session, n int) []models.Session {
			if n > len(sessions) {
				n = len(sessions)
			}
			return sessions[:n]
		},
		"ratingScaleMax": func(scale int) int {
			if scale == 10 {
				return 10
			}
			return 5
		},
		"hoursAny": func(v any) string {
			f, _ := v.(float64)
			if f == 0 {
				return "0"
			}
			return fmt.Sprintf("%.1fh", f)
		},
		"hours": func(h float64) string {
			if h == 0 {
				return "—"
			}
			if h < 1 {
				return fmt.Sprintf("%dm", int(math.Round(h*60)))
			}
			return fmt.Sprintf("%.1fh", h)
		},
		"durationHours": func(h float64) int {
			total := int(math.Round(h * 60))
			if total < 0 {
				total = 0
			}
			return total / 60
		},
		"durationMinutes": func(h float64) int {
			total := int(math.Round(h * 60))
			if total < 0 {
				total = 0
			}
			return total % 60
		},
		"siteName":  func() string { return s.cfg.Name },
		"instance":  func() string { return s.cfg.BaseURL },
		"abs":       func(p string) string { return s.cfg.BaseURL + p },
		"hasPrefix": strings.HasPrefix,
		"lower":     strings.ToLower,
		"join":      strings.Join,
		"first": func(list []string) string {
			if len(list) > 0 {
				return list[0]
			}
			return ""
		},
		"contains": func(list []string, v string) bool {
			for _, x := range list {
				if x == v {
					return true
				}
			}
			return false
		},
		"agoYear": func(t time.Time) int { return t.Year() },
	}
}

func playedStatusLabel(status string) string {
	switch status {
	case "completed":
		return "Completed"
	case "playing":
		return "Playing"
	case "backlog":
		return "Backlog"
	case "wishlist":
		return "Wishlist"
	case "abandoned":
		return "Abandoned"
	case "retired":
		return "Retired"
	case "shelved":
		return "Shelved"
	case "replayed":
		return "Replayed"
	default:
		return "Played"
	}
}

func avatarURL(u *models.User) string {
	if u == nil {
		return "/static/avatar.svg"
	}
	if u.Avatar != "" {
		return u.Avatar
	}
	return "/static/avatar.svg"
}

func headerURL(u *models.User) string {
	if u != nil && u.Header != "" {
		return u.Header
	}
	return ""
}

func screenshotThumb(raw string) string {
	return strings.Replace(raw, "t_screenshot_big", "t_screenshot_med", 1)
}

func screenshotFull(raw string) string {
	return strings.Replace(raw, "t_screenshot_big", "t_1080p", 1)
}

func coverURL(g *models.Game) string {
	if g == nil || g.CoverURL == "" {
		return "/static/cover-placeholder.svg"
	}
	return strings.ReplaceAll(g.CoverURL, "t_cover_big_2x", "t_cover_big")
}

func cover2xURL(g *models.Game) string {
	if g == nil || g.CoverURL == "" {
		return "/static/cover-placeholder.svg"
	}
	return strings.ReplaceAll(g.CoverURL, "/t_cover_big/", "/t_cover_big_2x/")
}

func coverSrcset(g *models.Game) string {
	one := coverURL(g)
	two := cover2xURL(g)
	if one == two {
		return ""
	}
	return one + " 1x, " + two + " 2x"
}

func userCoverURL(u *models.User, g *models.Game) string {
	if u != nil && u.CoverMode == "none" {
		return "/static/cover-placeholder.svg"
	}
	return coverURL(g)
}

func creditNames(companies []models.CompanyRole) string {
	seen := make(map[string]bool, len(companies))
	var names []string
	for _, role := range []string{"developer", "publisher"} {
		for _, company := range companies {
			if company.Role != role || company.Name == "" || seen[company.Name] {
				continue
			}
			seen[company.Name] = true
			names = append(names, company.Name)
		}
	}
	return strings.Join(names, ", ")
}

func starsHTML(rating float64, scale int) template.HTML {
	max := 5
	if scale == 10 {
		max = 10
	}
	value := rating
	if max == 5 {
		value = rating / 2
	}
	var b strings.Builder
	b.WriteString(`<span class="stars" aria-label="` + fmt.Sprintf("%.1f", rating) + ` out of ` + strconv.Itoa(max*2) + `">`)
	for i := 1; i <= 5; i++ {
		class := "star"
		switch {
		case value >= float64(i):
			class += " full"
		case value >= float64(i)-0.5:
			class += " half"
		}
		b.WriteString(`<span class="` + class + `" aria-hidden="true">★</span>`)
	}
	b.WriteString("</span>")
	return template.HTML(b.String())
}

func ratingText(r float64, scale int) string {
	if r <= 0 {
		return "Unrated"
	}
	if scale == 10 {
		return strconv.FormatFloat(r, 'f', 1, 64)
	}
	return strconv.FormatFloat(r/2, 'f', 1, 64)
}

// beatDuration splits a time-to-beat duration into display parts.
type beatDuration struct {
	Hours   int
	Minutes int
}

func beatParts(seconds int) beatDuration {
	if seconds <= 0 {
		return beatDuration{}
	}
	minutes := int(math.Round(float64(seconds) / 60))
	if minutes < 1 {
		minutes = 1
	}
	return beatDuration{Hours: minutes / 60, Minutes: minutes % 60}
}

// beatTime formats an IGDB time-to-beat value in seconds as hours and minutes.
func beatTime(seconds int) string {
	parts := beatParts(seconds)
	if parts.Hours == 0 && parts.Minutes == 0 {
		return "—"
	}
	switch {
	case parts.Hours == 0:
		return fmt.Sprintf("%dm", parts.Minutes)
	case parts.Minutes == 0:
		return fmt.Sprintf("%dh", parts.Hours)
	default:
		return fmt.Sprintf("%dh %dm", parts.Hours, parts.Minutes)
	}
}

// halfStarLabel formats a 1-10 score as a compact half-star label.
func halfStarLabel(n int) string {
	whole := n / 2
	if n%2 == 0 {
		return strconv.Itoa(whole)
	}
	if whole == 0 {
		return "½"
	}
	return strconv.Itoa(whole) + "½"
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return t.Format("Jan 2")
	default:
		return t.Format("Jan 2, 2006")
	}
}

func dateFmt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func dateLong(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("January 2, 2006")
}

func dateSlash(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006/01/02")
}

func releaseDate(s string) string {
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("2006/01/02")
	}
	return s
}

func releaseDay(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return ""
	}
	return t.Format("Jan 2")
}

// releaseLabel describes how far away a release is in UTC, matching the
// date('now') window used by the Coming soon rail.
func releaseLabel(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return ""
	}
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(t.Sub(today).Hours() / 24)
	switch {
	case days < 0:
		return ""
	case days == 0:
		return "Today"
	case days == 1:
		return "Tomorrow"
	default:
		return fmt.Sprintf("In %d days", days)
	}
}

func sameTitle(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

var executableNameExts = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".com": true, ".msi": true,
	".bin": true, ".sh": true, ".app": true, ".dll": true, ".lnk": true,
}

// displayAltNames hides launcher/executable artifacts and case-insensitive
// duplicates that IGDB sometimes lists as alternate titles.
func displayAltNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		if executableNameExts[path.Ext(lower)] {
			continue
		}
		if seen[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, name)
	}
	return out
}

func seq(from, to int) []int {
	var out []int
	if from > to {
		for i := from; i >= to; i-- {
			out = append(out, i)
		}
		return out
	}
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// tagPreview keeps list-card tags to a single row: at most three tags, plus a
// trailing "+N" pill when the list has more.
func tagPreview(tags []string) []string {
	if len(tags) <= 3 {
		return tags
	}
	out := make([]string, 0, 4)
	out = append(out, tags[:3]...)
	return append(out, "+"+strconv.Itoa(len(tags)-3))
}

func initials(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}
	return strings.ToUpper(string([]rune(name)[0:1]))
}

func dict(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			m[k] = kv[i+1]
		}
	}
	return m
}

func toJSON(v any) template.JS {
	b, _ := json.Marshal(v)
	return template.JS(b)
}

func listPath(u *models.User, l *models.List) string {
	return "/users/" + u.Username + "/lists/" + l.Slug
}

// relationRole names what a release is, for the tag on a related card.
func relationRole(category string) string {
	switch category {
	case "main_game":
		return "Main game"
	case "expanded_game", "update":
		return "Update"
	case "port":
		return "Port"
	case "remake":
		return "Remake"
	case "remaster":
		return "Remaster"
	case "dlc_addon":
		return "DLC"
	case "expansion", "standalone_expansion":
		return "Expansion"
	case "bundle", "pack":
		return "Bundle"
	case "mod":
		return "Mod"
	case "":
		return "Game"
	}
	r := []rune(strings.ReplaceAll(category, "_", " "))
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

// ancestorTab labels the ancestor group from the current game's own type, so a
// port page says "Port of" rather than a generic "Parents".
func ancestorTab(category string) string {
	switch category {
	case "port":
		return "Port of"
	case "expanded_game", "update":
		return "Update of"
	case "remake":
		return "Remake of"
	case "remaster":
		return "Remaster of"
	case "main_game":
		return "Edition of"
	default:
		return "Version of"
	}
}

func activityKindLabel(kind string) string {
	switch kind {
	case "review":
		return "Reviews"
	case "log":
		return "Journal logs"
	case "session":
		return "Play sessions"
	case "list":
		return "Lists"
	case "rating":
		return "Ratings"
	case "like":
		return "Likes"
	case "shelf":
		return "Shelf changes"
	case "follow":
		return "Follows"
	default:
		return kind
	}
}

type shelfActionInfo struct {
	Verb   string
	Detail string
}

// shelfAction turns the stored flag and on/off marker into readable copy. Rows
// written before the marker existed fall back to a generic shelf update.
func shelfAction(raw string) shelfActionInfo {
	parts := strings.SplitN(raw, ":", 2)
	shelf := parts[0]
	on := true
	if len(parts) == 2 {
		on = parts[1] != "0"
	}
	name := map[string]string{"playing": "Playing", "backlog": "Backlog", "wishlist": "Wishlist"}[shelf]
	switch shelf {
	case "playing":
		if on {
			return shelfActionInfo{Verb: "started playing"}
		}
		return shelfActionInfo{Verb: "stopped playing"}
	case "backlog", "wishlist":
		if on {
			return shelfActionInfo{Verb: "added", Detail: "to " + name}
		}
		return shelfActionInfo{Verb: "removed", Detail: "from " + name}
	default:
		return shelfActionInfo{Verb: "changed their shelf for"}
	}
}

func notePrivacyLabel(p string) string {
	switch p {
	case "everyone":
		return "Notes visible to everyone"
	case "none":
		return "Notes private"
	default:
		return "Notes visible to friends"
	}
}

func privacyLabel(p string) string {
	switch p {
	case "private":
		return "Private"
	case "unlisted":
		return "Unlisted"
	case "personal":
		return "Personal"
	default:
		return "Public"
	}
}

var _ = markdown.Render
