package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"luduslog/internal/federation"
	"luduslog/internal/models"
	"luduslog/internal/store"
)

const activityJSON = "application/activity+json; charset=utf-8"

func writeJSON(w http.ResponseWriter, code int, v any, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := 200
	if err := s.st.DB.PingContext(r.Context()); err != nil {
		status = "degraded"
		code = 503
	}
	writeJSON(w, code, map[string]any{"status": status, "name": s.instanceConfig(r.Context()).Name, "time": time.Now().UTC()}, "application/json")
}

func (s *Server) actorKey(r *http.Request, u *models.User) (string, error) {
	existing, err := s.st.ActorKey(r.Context(), u.ID)
	if err == nil && existing != "" {
		return existing, nil
	}
	pem, err := federation.GenerateKey()
	if err != nil {
		return "", err
	}
	if err := s.st.SetActorKey(r.Context(), u.ID, pem); err != nil {
		return "", err
	}
	return pem, nil
}

func (s *Server) actorURL(u *models.User) string {
	return s.absolute("/users/" + url.PathEscape(u.Username))
}

func (s *Server) actorDocument(r *http.Request, u *models.User) (map[string]any, error) {
	key, err := s.actorKey(r, u)
	if err != nil {
		return nil, err
	}
	actor := s.actorURL(u)
	doc := map[string]any{
		"@context":                  []any{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"id":                        actor,
		"type":                      "Person",
		"preferredUsername":         u.Username,
		"name":                      u.Username,
		"summary":                   markdownHTML(u.Bio),
		"url":                       actor,
		"inbox":                     actor + "/inbox",
		"outbox":                    actor + "/outbox",
		"followers":                 actor + "/followers",
		"following":                 actor + "/following",
		"manuallyApprovesFollowers": u.ManuallyApproves,
		"discoverable":              u.Federation,
		"published":                 u.CreatedAt.UTC().Format(time.RFC3339),
		"publicKey": map[string]any{
			"id": actor + "#main-key", "owner": actor, "publicKeyPem": key,
		},
		"attachment": []any{map[string]any{"type": "PropertyValue", "name": "Profile", "value": actor}},
	}
	if u.Avatar != "" {
		doc["icon"] = map[string]any{"type": "Image", "url": absoluteURL(s.cfg.BaseURL, u.Avatar)}
	}
	if u.Header != "" {
		doc["image"] = map[string]any{"type": "Image", "url": absoluteURL(s.cfg.BaseURL, u.Header)}
	}
	for _, l := range s.st.SocialLinksFor(r.Context(), u.ID) {
		doc["attachment"] = append(doc["attachment"].([]any), map[string]any{"type": "PropertyValue", "name": l.Site, "value": l.URL})
	}
	return doc, nil
}

func (s *Server) apActor(w http.ResponseWriter, r *http.Request, u *models.User) {
	doc, err := s.actorDocument(r, u)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, 200, doc, activityJSON)
}

func (s *Server) webfinger(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resource")
	if !strings.HasPrefix(resource, "acct:") {
		http.Error(w, "bad resource", 400)
		return
	}
	acct := strings.TrimPrefix(resource, "acct:")
	at := strings.LastIndex(acct, "@")
	if at <= 0 {
		http.Error(w, "bad resource", 400)
		return
	}
	name, host := acct[:at], acct[at+1:]
	base, _ := url.Parse(s.cfg.BaseURL)
	if !strings.EqualFold(host, base.Host) {
		http.NotFound(w, r)
		return
	}
	u, err := s.st.UserByUsername(r.Context(), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	actor := s.actorURL(u)
	writeJSON(w, 200, map[string]any{
		"subject": "acct:" + u.Username + "@" + base.Host,
		"aliases": []string{actor},
		"links": []any{
			map[string]any{"rel": "self", "type": "application/activity+json", "href": actor},
			map[string]any{"rel": "http://webfinger.net/rel/profile-page", "type": "text/html", "href": actor},
		},
	}, "application/jrd+json")
}

func (s *Server) nodeinfoDiscovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"links": []any{map[string]any{
		"rel": "http://nodeinfo.diaspora.software/ns/schema/2.1", "href": s.absolute("/nodeinfo/2.1"),
	}}}, "application/json")
}

func (s *Server) nodeinfo(w http.ResponseWriter, r *http.Request) {
	inst := s.instanceConfig(r.Context())
	var users, posts, comments int
	s.st.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE deactivated_at IS NULL`).Scan(&users)
	s.st.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM logs WHERE draft=0`).Scan(&posts)
	s.st.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM comments WHERE deleted_at IS NULL`).Scan(&comments)
	writeJSON(w, 200, map[string]any{
		"version":           "2.1",
		"software":          map[string]any{"name": "luduslog", "version": "0.1.0"},
		"protocols":         []string{"activitypub"},
		"services":          map[string]any{"inbound": []string{}, "outbound": []string{}},
		"openRegistrations": inst.Registration == "open",
		"usage":             map[string]any{"users": map[string]any{"total": users}, "localPosts": posts, "localComments": comments},
		"metadata":          map[string]any{"nodeName": inst.Name, "nodeDescription": inst.InstanceDesc},
	}, "application/json")
}

func (s *Server) outboxCollection(r *http.Request, u *models.User) (map[string]any, error) {
	logs, _, err := s.st.UserLogs(r.Context(), u.ID, false, 40, 0)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, l := range logs {
		if l.Draft {
			continue
		}
		items = append(items, s.reviewActivity(u, l))
	}
	return s.collection(s.actorURL(u)+"/outbox", items), nil
}

func (s *Server) apOutbox(w http.ResponseWriter, r *http.Request) {
	u, ok := s.apTarget(w, r)
	if !ok {
		return
	}
	col, err := s.outboxCollection(r, u)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, 200, col, activityJSON)
}

func (s *Server) apFollowers(w http.ResponseWriter, r *http.Request) {
	u, ok := s.apTarget(w, r)
	if !ok {
		return
	}
	items := []any{}
	for _, f := range s.st.LocalFollowers(r.Context(), u.ID) {
		items = append(items, s.actorURL(f))
	}
	for _, f := range s.st.RemoteFollowers(r.Context(), u.ID) {
		items = append(items, f.ActorURI)
	}
	writeJSON(w, 200, s.collection(s.actorURL(u)+"/followers", items), activityJSON)
}

func (s *Server) apFollowing(w http.ResponseWriter, r *http.Request) {
	u, ok := s.apTarget(w, r)
	if !ok {
		return
	}
	users, _ := s.st.Following(r.Context(), u.ID, 1000, 0)
	items := []any{}
	for _, f := range users {
		items = append(items, s.actorURL(f))
	}
	writeJSON(w, 200, s.collection(s.actorURL(u)+"/following", items), activityJSON)
}

func (s *Server) collection(id string, items []any) map[string]any {
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           id,
		"type":         "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}
}

func (s *Server) apTarget(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	u, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	return u, true
}

func (s *Server) reviewActivity(u *models.User, l *models.Log) map[string]any {
	actor := s.actorURL(u)
	page := s.absolute(fmt.Sprintf("/users/%s/reviews/%d", url.PathEscape(u.Username), l.ID))
	note := map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           page,
		"type":         "Note",
		"attributedTo": actor,
		"actor":        actor,
		"content":      markdownHTML(l.Review),
		"summary":      l.Name,
		"published":    l.CreatedAt.UTC().Format(time.RFC3339),
		"to":           []string{"https://www.w3.org/ns/activitystreams#Public"},
		"cc":           []string{actor + "/followers"},
		"url":          page,
		"inReplyTo":    nil,
	}
	if l.Game != nil {
		note["attachment"] = []any{map[string]any{
			"type": "Document", "name": l.Game.Title, "url": s.absolute("/games/" + url.PathEscape(l.Game.Slug)),
		}}
		note["tag"] = []any{map[string]any{"type": "Hashtag", "name": "#" + gameTag(l.Game.Title)}}
	}
	if l.Rating > 0 {
		note["rating"] = l.Rating / 2
		note["ratingScale"] = 5
	}
	if l.Spoiler {
		note["sensitive"] = true
	}
	return map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        page + "/activity",
		"type":      "Create",
		"actor":     actor,
		"published": l.CreatedAt.UTC().Format(time.RFC3339),
		"to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
		"object":    note,
	}
}

func gameTag(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Server) apGameActivity(w http.ResponseWriter, r *http.Request) {
	g, err := s.st.GameBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	logs, _, err := s.st.ReviewsForGame(r.Context(), g.ID, "new", 40, 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	items := []any{}
	for _, l := range logs {
		if l.User != nil && l.User.Federation && !l.Draft {
			items = append(items, s.reviewActivity(l.User, l))
		}
	}
	writeJSON(w, 200, s.collection(s.absolute("/games/"+url.PathEscape(g.Slug)+"/activity"), items), activityJSON)
}

func (s *Server) apInbox(w http.ResponseWriter, r *http.Request) {
	target, err := s.st.UserByUsername(r.Context(), r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	err = federation.VerifyRequest(r, body, func(keyID string) (string, error) {
		host := hostOf(keyID)
		if host != "" && s.st.IsDomainBlocked(r.Context(), host) {
			return "", fmt.Errorf("domain blocked")
		}
		return federation.FetchPublicKey(client, keyID)
	})
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "signature verification failed"}, "application/json")
		return
	}
	act, err := federation.ParseActivity(body)
	if err != nil {
		http.Error(w, "bad activity", 400)
		return
	}
	if act.ID != "" && s.st.InboxSeen(r.Context(), act.ID) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if act.ID != "" {
		s.st.MarkInboxSeen(r.Context(), act.ID, act.Actor, act.Type)
	}
	actor, _ := s.remoteActor(r, client, act.Actor)
	switch act.Type {
	case "Follow":
		object := rawString(act.Object)
		if object == "" {
			object = act.Target
		}
		if object == s.actorURL(target) || strings.HasSuffix(object, "/users/"+target.Username) {
			if actor != nil {
				s.st.SaveRemoteActor(r.Context(), actor)
				if !target.ManuallyApproves {
					s.st.AddRemoteFollow(r.Context(), actor.ActorURI, target.ID)
					s.enqueueDeliveryActivity(r, target, map[string]any{
						"@context": "https://www.w3.org/ns/activitystreams",
						"id":       s.actorURL(target) + "#accepts/" + fmt.Sprint(time.Now().UnixNano()),
						"type":     "Accept",
						"actor":    s.actorURL(target),
						"object":   act.ID,
						"to":       []string{actor.ActorURI},
					}, actor.Inbox)
					s.st.Notify(r.Context(), target.ID, "follow", nil, "user", int64(len(actor.ActorURI)), actor.Name+" followed you", "")
				}
			}
		}
	case "Undo":
		if actor != nil {
			s.st.RemoveRemoteFollow(r.Context(), actor.ActorURI, target.ID)
		}
	case "Create":
		s.handleRemoteCreate(r, act, target)
	case "Like":
		s.st.Notify(r.Context(), target.ID, "like", nil, "user", int64(len(act.Actor)), "Someone liked your post", "")
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleRemoteCreate(r *http.Request, act *federation.InboxActivity, target *models.User) {
	var obj struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Content      string `json:"content"`
		InReplyTo    string `json:"inReplyTo"`
		AttributedTo string `json:"attributedTo"`
	}
	raw := act.Object
	if len(raw) > 0 && raw[0] == '"' {
		json.Unmarshal(raw, &obj.ID)
		return
	}
	if json.Unmarshal(raw, &obj) != nil || obj.InReplyTo == "" {
		return
	}
	actor, _ := s.st.RemoteActorByURI(r.Context(), act.Actor)
	name, avatar := act.Actor, ""
	if actor != nil {
		name, avatar = actor.Name, actor.Avatar
	}
	s.st.InsertRemoteNote(r.Context(), obj.ID, act.Actor, name, avatar, obj.Content, obj.InReplyTo)
}

func (s *Server) remoteActor(r *http.Request, client *http.Client, uri string) (*store.RemoteActor, error) {
	if a, err := s.st.RemoteActorByURI(r.Context(), uri); err == nil && time.Since(a.FetchedAt) < 24*time.Hour {
		return a, nil
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/activity+json, application/ld+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("actor fetch: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	ra, err := federation.ParseActor(body)
	if err != nil {
		return nil, err
	}
	out := &store.RemoteActor{
		ActorURI: uri, Domain: hostOf(uri), Username: ra.Username, Name: ra.Name,
		Inbox: ra.Inbox, SharedInbox: ra.SharedInbox, Outbox: ra.Outbox,
		KeyID: ra.KeyID, PublicKeyPEM: ra.PublicKeyPEM,
		Avatar: ra.Avatar, Summary: ra.Summary, FetchedAt: time.Now(),
	}
	s.st.SaveRemoteActor(r.Context(), out)
	return out, nil
}

func (s *Server) enqueueDeliveryActivity(r *http.Request, u *models.User, activity map[string]any, inbox string) {
	if inbox == "" {
		return
	}
	payload, _ := json.Marshal(activity)
	job, _ := json.Marshal(map[string]any{"user_id": u.ID, "inbox": inbox, "payload": string(payload)})
	s.q.Enqueue(r.Context(), "deliver", string(job), 0)
}

func rawString(raw json.RawMessage) string {
	var s string
	json.Unmarshal(raw, &s)
	return s
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func absoluteURL(base, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")
}

func markdownHTML(s string) string { return string(md(s)) }
