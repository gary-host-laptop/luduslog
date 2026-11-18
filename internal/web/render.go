package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"luduslog/internal/markdown"
	"luduslog/internal/models"
)

func templateFuncs(s *Server) template.FuncMap {
	return template.FuncMap{
		"safe":            safeHTML,
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
		"timeago":         timeAgo,
		"date":            dateFmt,
		"dateLong":        dateLong,
		"year":            func(t time.Time) int { return t.Year() },
		"add":             func(a, b int) int { return a + b },
		"sub":             func(a, b int) int { return a - b },
		"inc":             func(a int) int { return a + 1 },
		"dec":             func(a int) int { return a - 1 },
		"mul":             func(a, b float64) float64 { return a * b },
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
		"plural":      plural,
		"truncate":    truncate,
		"creditNames": creditNames,
		"initials":    initials,
		"dict":        dict,
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
		"privacyLabel": privacyLabel,
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
		"siteName":  func() string { return s.cfg.Name },
		"instance":  func() string { return s.cfg.BaseURL },
		"abs":       func(p string) string { return s.cfg.BaseURL + p },
		"hasPrefix": strings.HasPrefix,
		"lower":     strings.ToLower,
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

// beatTime formats an IGDB time-to-beat value in seconds as hours and minutes.
func beatTime(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	minutes := int(math.Round(float64(seconds) / 60))
	if minutes < 1 {
		minutes = 1
	}
	hours := minutes / 60
	remainder := minutes % 60
	switch {
	case hours == 0:
		return fmt.Sprintf("%dm", remainder)
	case remainder == 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dh %dm", hours, remainder)
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
