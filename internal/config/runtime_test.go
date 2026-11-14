package config

import "testing"

func baseConfig() Config {
	return Config{
		Name: "LudusLog", Registration: "open", MaxCommentChars: 5000,
		SessionKey: "session-secret", SMTPPass: "smtp-secret",
		IgdbClientID: "igdb-id", IgdbSecret: "igdb-secret",
		TLSKey: "/secret/key.pem", DBPath: "/secret/db.sqlite",
	}
}

func TestEditableSettingsValidation(t *testing.T) {
	c := baseConfig()
	for key, value := range map[string]string{
		"name":                    "",
		"registration":            "everyone",
		"contact_email":           "not-an-email",
		"admin_email":             "also-bad",
		"max_comment_chars":       "20",
		"federate_remote_ratings": "maybe",
	} {
		if _, err := c.WithSetting(key, value); err == nil {
			t.Errorf("invalid %s=%q was accepted", key, value)
		}
	}
	next, err := c.WithSettings(map[string]string{
		"name": "My Log", "description": "Games.", "registration": "invite",
		"invite_code": "let-me-in", "contact_email": "hello@example.test",
		"admin_email": "admin@example.test", "max_comment_chars": "2500",
		"federate_remote_ratings": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.Name != "My Log" || next.Registration != "invite" || next.InviteCode != "let-me-in" || next.MaxCommentChars != 2500 || !next.FederateRemoteRatings {
		t.Fatalf("settings were not applied: %+v", next)
	}
	if _, err := c.WithSettings(map[string]string{"registration": "invite", "invite_code": ""}); err == nil {
		t.Fatal("invite mode without a code was accepted")
	}
}

func TestSecretsAreNotEditable(t *testing.T) {
	c := baseConfig()
	for _, key := range []string{"session_key", "smtp_pass", "igdb_secret", "tls_key", "db", "listen", "data_dir", "unknown"} {
		if IsEditableSetting(key) {
			t.Errorf("%s is in the editable allowlist", key)
		}
		if _, err := c.WithSetting(key, "changed"); err == nil {
			t.Errorf("%s was changed through the public settings path", key)
		}
	}
	if c.SessionKey != "session-secret" || c.SMTPPass != "smtp-secret" || c.IgdbSecret != "igdb-secret" || c.TLSKey != "/secret/key.pem" || c.DBPath != "/secret/db.sqlite" {
		t.Fatal("a secret was modified")
	}
}
