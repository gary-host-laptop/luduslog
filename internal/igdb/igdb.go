// Package igdb is a small IGDB API client. Game data is stored locally; the
// API is only contacted by the sync command and by explicit admin refreshes.
// See docs/igdb.md for attribution requirements.
package igdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"luduslog/internal/models"
)

type Client struct {
	ClientID     string
	ClientSecret string
	HTTP         *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func New(clientID, secret string) *Client {
	return &Client{ClientID: clientID, ClientSecret: secret, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expiry) {
		return nil
	}
	form := url.Values{
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://id.twitch.tv/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("igdb auth: %s: %s", resp.Status, b)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	c.token = out.AccessToken
	c.expiry = time.Now().Add(time.Duration(out.ExpiresIn-60) * time.Second)
	return nil
}

type apiGame struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	Summary          string `json:"summary"`
	FirstReleaseDate int64  `json:"first_release_date"`
	Cover            *struct {
		URL string `json:"url"`
	} `json:"cover"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Platforms []struct {
		Name string `json:"name"`
	} `json:"platforms"`
	InvolvedCompanies []struct {
		Developer bool `json:"developer"`
		Publisher bool `json:"publisher"`
		Company   struct {
			Name string `json:"name"`
		} `json:"company"`
	} `json:"involved_companies"`
	AggregatedRating float64 `json:"aggregated_rating"`
	RatingCount      int     `json:"rating_count"`
}

const gameFields = `fields id,name,slug,summary,first_release_date,cover.url,genres.name,platforms.name,involved_companies.developer,involved_companies.publisher,involved_companies.company.name,aggregated_rating,rating_count;`

// Games fetches up to limit games ordered by rating count.
func (c *Client) Games(ctx context.Context, limit int) ([]models.Game, error) {
	body := fmt.Sprintf(`%s
sort rating_count desc; where rating_count > 5 & version_parent = null; limit %d;`, gameFields, limit)
	return c.query(ctx, body)
}

// GamesByIDs fetches games by IGDB id in batches. Backloggd game ids are IGDB
// ids, so an export can populate its catalog without a title search.
func (c *Client) GamesByIDs(ctx context.Context, ids []int64) ([]models.Game, error) {
	seen := make(map[int64]bool, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	var out []models.Game
	for start := 0; start < len(unique); start += 100 {
		end := start + 100
		if end > len(unique) {
			end = len(unique)
		}
		parts := make([]string, end-start)
		for i, id := range unique[start:end] {
			parts[i] = strconv.FormatInt(id, 10)
		}
		body := fmt.Sprintf(`%s
where id = (%s); limit 100;`, gameFields, strings.Join(parts, ","))
		games, err := c.query(ctx, body)
		if err != nil {
			return out, err
		}
		out = append(out, games...)
		if end < len(unique) {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	return out, nil
}

func (c *Client) query(ctx context.Context, body string) ([]models.Game, error) {
	if err := c.authenticate(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.igdb.com/v4/games", bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Client-ID", c.ClientID)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("igdb games: %s: %s", resp.Status, b)
	}
	var raw []apiGame
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]models.Game, 0, len(raw))
	for _, r := range raw {
		g := models.Game{
			IGDBID: r.ID, Slug: r.Slug, Title: r.Name, Summary: r.Summary,
			ReleaseStatus: "released", RatingCount: r.RatingCount,
		}
		if g.Slug == "" {
			g.Slug = Slugify(r.Name)
		}
		if r.FirstReleaseDate > 0 {
			t := time.Unix(r.FirstReleaseDate, 0).UTC()
			g.ReleaseDate = t.Format("2006-01-02")
			g.ReleaseYear = t.Year()
		}
		if r.Cover != nil && r.Cover.URL != "" {
			g.CoverURL = strings.Replace(r.Cover.URL, "t_thumb", "t_cover_big", 1)
			if strings.HasPrefix(g.CoverURL, "//") {
				g.CoverURL = "https:" + g.CoverURL
			}
		}
		for _, gen := range r.Genres {
			g.Genres = append(g.Genres, gen.Name)
		}
		for _, p := range r.Platforms {
			g.Platforms = append(g.Platforms, models.Platform{Name: p.Name})
		}
		for _, ic := range r.InvolvedCompanies {
			role := ""
			switch {
			case ic.Developer:
				role = "developer"
			case ic.Publisher:
				role = "publisher"
			}
			if role != "" {
				g.Companies = append(g.Companies, models.CompanyRole{Name: ic.Company.Name, Role: role})
			}
		}
		out = append(out, g)
	}
	return out, nil
}

// Slugify turns a title into a URL slug.
func Slugify(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
