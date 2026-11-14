package federation

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"luduslog/internal/models"
)

const (
	Public    = "https://www.w3.org/ns/activitystreams#Public"
	ActorType = "Person"
)

// Actor builds the ActivityPub actor document for a local user.
func Actor(baseURL string, u *models.User, publicPEM string) map[string]any {
	id := baseURL + "/users/" + u.Username
	doc := map[string]any{
		"@context": []any{
			"https://www.w3.org/ns/activitystreams",
			"https://w3id.org/security/v1",
			map[string]any{
				"LudusLog":                  "https://luduslog.example/ns#",
				"manuallyApprovesFollowers": "as:manuallyApprovesFollowers",
				"discoverable":              "as:discoverable",
				"games":                     "LudusLog:games",
				"rating":                    "LudusLog:rating",
				"status":                    "LudusLog:status",
			},
		},
		"id":                        id,
		"type":                      ActorType,
		"preferredUsername":         u.Username,
		"name":                      u.Username,
		"summary":                   u.Bio,
		"url":                       id,
		"inbox":                     id + "/inbox",
		"outbox":                    id + "/outbox",
		"followers":                 id + "/followers",
		"following":                 id + "/following",
		"manuallyApprovesFollowers": u.ManuallyApproves,
		"discoverable":              !u.PrivateProfile,
		"published":                 u.CreatedAt.UTC().Format(time.RFC3339),
		"publicKey": map[string]any{
			"id":           id + "#main-key",
			"owner":        id,
			"publicKeyPem": publicPEM,
		},
	}
	if u.Avatar != "" {
		doc["icon"] = map[string]any{"type": "Image", "url": u.Avatar}
	}
	if u.Header != "" {
		doc["image"] = map[string]any{"type": "Image", "url": u.Header}
	}
	if u.HideSocialGraph {
		doc["followers"] = id + "/followers"
	}
	return doc
}

// Note builds an ActivityPub Note for a review or status.
func Note(baseURL string, u *models.User, l *models.Log, audience string) map[string]any {
	id := baseURL + "/users/" + u.Username + "/reviews/" + itoa(l.ID)
	note := map[string]any{
		"id":           id,
		"type":         "Note",
		"attributedTo": baseURL + "/users/" + u.Username,
		"content":      l.ReviewHTML,
		"published":    l.CreatedAt.UTC().Format(time.RFC3339),
		"to":           []string{Public},
		"cc":           []string{baseURL + "/users/" + u.Username + "/followers"},
		"url":          id,
		"tag":          []any{},
		"attachment":   []any{},
	}
	if audience == "followers" {
		note["to"] = []string{baseURL + "/users/" + u.Username + "/followers"}
		note["cc"] = []string{}
	}
	if l.Game != nil {
		note["name"] = l.Name
		note["LudusLog:game"] = map[string]any{
			"type":  "LudusLog:Game",
			"id":    baseURL + "/games/" + l.Game.Slug,
			"name":  l.Game.Title,
			"slug":  l.Game.Slug,
			"cover": l.Game.CoverURL,
		}
		note["LudusLog:rating"] = l.Rating
		note["LudusLog:status"] = l.Status
		note["LudusLog:platform"] = l.PlatformName
		note["LudusLog:playtime"] = l.Playtime
		note["attachment"] = []any{map[string]any{
			"type": "Document", "mediaType": "text/html", "url": id,
			"name": l.Game.Title,
		}}
	}
	if l.Spoiler {
		note["summary"] = "Spoilers"
		note["sensitive"] = true
	}
	return note
}

// ListObject builds a public Collection for a list.
func ListObject(baseURL string, u *models.User, l *models.List, games []models.ListEntry) map[string]any {
	id := baseURL + "/users/" + u.Username + "/lists/" + l.Slug
	items := []any{}
	for _, e := range games {
		if e.Game == nil {
			continue
		}
		items = append(items, map[string]any{
			"type":  "LudusLog:Game",
			"id":    baseURL + "/games/" + e.Game.Slug,
			"name":  e.Game.Title,
			"slug":  e.Game.Slug,
			"cover": e.Game.CoverURL,
			"note":  e.Note,
		})
	}
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           id,
		"type":         "OrderedCollection",
		"attributedTo": baseURL + "/users/" + u.Username,
		"summary":      l.Description,
		"name":         l.Title,
		"totalItems":   len(items),
		"orderedItems": items,
		"published":    l.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// InboxActivity is the subset of an inbound activity we care about.
type InboxActivity struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Actor  string          `json:"actor"`
	Object json.RawMessage `json:"object"`
	Target string          `json:"target"`
}

// ParseActivity decodes an ActivityPub activity body.
func ParseActivity(body []byte) (*InboxActivity, error) {
	var a InboxActivity
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, err
	}
	if a.Type == "" {
		return nil, errors.New("federation: activity has no type")
	}
	return &a, nil
}

// ParseActor extracts identity fields from a remote actor document.
type RemoteActor struct {
	ID           string
	Username     string
	Name         string
	Inbox        string
	SharedInbox  string
	Outbox       string
	KeyID        string
	PublicKeyPEM string
	Avatar       string
	Summary      string
	Domain       string
}

func ParseActor(body []byte) (*RemoteActor, error) {
	var doc struct {
		ID                string `json:"id"`
		PreferredUsername string `json:"preferredUsername"`
		Name              string `json:"name"`
		Inbox             string `json:"inbox"`
		Outbox            string `json:"outbox"`
		Summary           string `json:"summary"`
		URL               string `json:"url"`
		Endpoints         struct {
			SharedInbox string `json:"sharedInbox"`
		} `json:"endpoints"`
		Icon struct {
			URL any `json:"url"`
		} `json:"icon"`
		PublicKey struct {
			ID           string `json:"id"`
			PublicKeyPem string `json:"publicKeyPem"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if doc.ID == "" || doc.Inbox == "" {
		return nil, errors.New("federation: incomplete actor")
	}
	a := &RemoteActor{
		ID: doc.ID, Username: doc.PreferredUsername, Name: doc.Name,
		Inbox: doc.Inbox, SharedInbox: doc.Endpoints.SharedInbox, Outbox: doc.Outbox,
		KeyID: doc.PublicKey.ID, PublicKeyPEM: doc.PublicKey.PublicKeyPem,
		Summary: doc.Summary,
	}
	if a.Username == "" {
		parts := strings.Split(strings.TrimSuffix(doc.ID, "/"), "/")
		a.Username = parts[len(parts)-1]
	}
	a.Domain = domainOf(doc.ID)
	if m, ok := doc.Icon.URL.(map[string]any); ok {
		if s, ok := m["url"].(string); ok {
			a.Avatar = s
		}
	} else if s, ok := doc.Icon.URL.(string); ok {
		a.Avatar = s
	}
	return a, nil
}

func publicKeyFromActorJSON(body []byte) (string, error) {
	a, err := ParseActor(body)
	if err != nil {
		return "", err
	}
	if a.PublicKeyPEM == "" {
		return "", errors.New("federation: actor has no public key")
	}
	return a.PublicKeyPEM, nil
}

func domainOf(uri string) string {
	uri = strings.TrimPrefix(uri, "https://")
	uri = strings.TrimPrefix(uri, "http://")
	if i := strings.IndexAny(uri, "/?#"); i >= 0 {
		uri = uri[:i]
	}
	return strings.ToLower(uri)
}

func itoa(n int64) string {
	return strings.TrimSpace(jsonNumber(n))
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
