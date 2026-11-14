// Package config loads LudusLog configuration from a TOML file and environment
// variables. Environment variables win over the file.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the full runtime configuration. Only a small number of values have
// no defaults; everything else is tuned through the config file/env.
type Config struct {
	Name                  string // instance display name; also the software brand
	Software              string // nodeinfo software name, stable across instances
	BaseURL               string // absolute URL, no trailing slash, e.g. https://log.example.com
	Listen                string
	DataDir               string
	DBPath                string
	SessionKey            string
	TLSCert               string
	TLSKey                string
	Registration          string // open | invite | approval | closed
	InviteCode            string
	IgdbClientID          string
	IgdbSecret            string
	SMTPHost              string
	SMTPPort              int
	SMTPUser              string
	SMTPPass              string
	SMTPFrom              string
	Demo                  bool
	AuthorizedFetch       bool
	FederateRemoteRatings bool
	MaxCommentChars       int
	AdminEmail            string
	InstanceDesc          string
	ContactEmail          string
	Metrics               bool
}

// Load reads optional TOML at path then overlays environment variables.
func Load(path string) Config {
	c := Config{
		Name:            "LudusLog",
		Software:        "luduslog",
		BaseURL:         "http://localhost:8080",
		Listen:          ":8080",
		DataDir:         "./data",
		Registration:    "open",
		SMTPPort:        587,
		MaxCommentChars: 5000,
		InstanceDesc:    "A federated place to log the games you play.",
	}
	if path != "" {
		if f, err := os.Open(path); err == nil {
			defer f.Close()
			loadTOML(f, &c)
		}
	}
	env := map[string]*string{
		"LUDUSLOG_NAME":           &c.Name,
		"LUDUSLOG_SOFTWARE":       &c.Software,
		"LUDUSLOG_BASE_URL":       &c.BaseURL,
		"LUDUSLOG_LISTEN":         &c.Listen,
		"LUDUSLOG_DATA_DIR":       &c.DataDir,
		"LUDUSLOG_DB":             &c.DBPath,
		"LUDUSLOG_SESSION_KEY":    &c.SessionKey,
		"LUDUSLOG_TLS_CERT":       &c.TLSCert,
		"LUDUSLOG_TLS_KEY":        &c.TLSKey,
		"LUDUSLOG_REGISTRATION":   &c.Registration,
		"LUDUSLOG_INVITE_CODE":    &c.InviteCode,
		"LUDUSLOG_IGDB_CLIENT_ID": &c.IgdbClientID,
		"LUDUSLOG_IGDB_SECRET":    &c.IgdbSecret,
		"LUDUSLOG_SMTP_HOST":      &c.SMTPHost,
		"LUDUSLOG_SMTP_USER":      &c.SMTPUser,
		"LUDUSLOG_SMTP_PASS":      &c.SMTPPass,
		"LUDUSLOG_SMTP_FROM":      &c.SMTPFrom,
		"LUDUSLOG_ADMIN_EMAIL":    &c.AdminEmail,
		"LUDUSLOG_INSTANCE_DESC":  &c.InstanceDesc,
		"LUDUSLOG_CONTACT_EMAIL":  &c.ContactEmail,
	}
	for k, dst := range env {
		if v, ok := os.LookupEnv(k); ok {
			*dst = v
		}
	}
	if v, ok := os.LookupEnv("LUDUSLOG_SMTP_PORT"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.SMTPPort = n
		}
	}
	if v, ok := os.LookupEnv("LUDUSLOG_DEMO"); ok {
		c.Demo = v == "1" || strings.EqualFold(v, "true")
	}
	if v, ok := os.LookupEnv("LUDUSLOG_AUTHORIZED_FETCH"); ok {
		c.AuthorizedFetch = v == "1" || strings.EqualFold(v, "true")
	}
	if v, ok := os.LookupEnv("LUDUSLOG_FEDERATE_REMOTE_RATINGS"); ok {
		c.FederateRemoteRatings = v == "1" || strings.EqualFold(v, "true")
	}
	if v, ok := os.LookupEnv("LUDUSLOG_METRICS"); ok {
		c.Metrics = v == "1" || strings.EqualFold(v, "true")
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.DBPath == "" {
		c.DBPath = c.DataDir + "/luduslog.db"
	}
	if c.SessionKey == "" {
		c.SessionKey = "dev-insecure-session-key-change-me"
	}
	return c
}

// loadTOML is a deliberately tiny TOML reader: flat key = "value" pairs and
// integer/bool values. It keeps the configuration dependency-free and obvious.
func loadTOML(f *os.File, c *Config) {
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		val = strings.Trim(val, `"`)
		if i := strings.Index(val, " #"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}
		switch strings.ToLower(key) {
		case "name":
			c.Name = val
		case "software":
			c.Software = val
		case "base_url":
			c.BaseURL = val
		case "listen":
			c.Listen = val
		case "data_dir":
			c.DataDir = val
		case "db":
			c.DBPath = val
		case "session_key":
			c.SessionKey = val
		case "tls_cert":
			c.TLSCert = val
		case "tls_key":
			c.TLSKey = val
		case "registration":
			c.Registration = val
		case "invite_code":
			c.InviteCode = val
		case "igdb_client_id":
			c.IgdbClientID = val
		case "igdb_secret":
			c.IgdbSecret = val
		case "smtp_host":
			c.SMTPHost = val
		case "smtp_port":
			if n, err := strconv.Atoi(val); err == nil {
				c.SMTPPort = n
			}
		case "smtp_user":
			c.SMTPUser = val
		case "smtp_pass":
			c.SMTPPass = val
		case "smtp_from":
			c.SMTPFrom = val
		case "admin_email":
			c.AdminEmail = val
		case "contact_email":
			c.ContactEmail = val
		case "instance_desc":
			c.InstanceDesc = val
		case "demo":
			c.Demo = val == "true" || val == "1"
		case "authorized_fetch":
			c.AuthorizedFetch = val == "true" || val == "1"
		}
	}
}

// Validate returns a descriptive error for unusable configurations.
func (c Config) Validate() error {
	if c.BaseURL == "" || !strings.HasPrefix(c.BaseURL, "http") {
		return fmt.Errorf("base_url must be an absolute http(s) url")
	}
	switch c.Registration {
	case "open", "invite", "approval", "closed":
	default:
		return fmt.Errorf("registration must be one of open, invite, approval, closed")
	}
	if c.IgdbClientID == "" || c.IgdbSecret == "" {
		// Not fatal: the catalog can run on seeded data.
	}
	return nil
}

// HasIGDB reports whether Twitch/IGDB credentials are configured.
func (c Config) HasIGDB() bool { return c.IgdbClientID != "" && c.IgdbSecret != "" }
