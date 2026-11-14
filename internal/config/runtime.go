package config

import (
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SettingDef describes a non-secret instance setting that can be edited from
// the admin UI. Secrets and infrastructure paths are deliberately absent.
type SettingDef struct {
	Key     string
	Label   string
	Kind    string // text | textarea | select | email | number | bool
	Help    string
	Options []string
}

var EditableSettings = []SettingDef{
	{Key: "name", Label: "Instance name", Kind: "text", Help: "Shown in the header, page titles, and federation metadata."},
	{Key: "description", Label: "Instance description", Kind: "textarea", Help: "A short description for the about page and NodeInfo."},
	{Key: "registration", Label: "Registration mode", Kind: "select", Options: []string{"open", "invite", "approval", "closed"}, Help: "Takes effect immediately for new registrations."},
	{Key: "invite_code", Label: "Invite code", Kind: "text", Help: "Required when the registration mode is invite. Leave empty to disable invite registration."},
	{Key: "contact_email", Label: "Contact email", Kind: "email", Help: "Public contact address shown on the about page."},
	{Key: "admin_email", Label: "Admin email", Kind: "email", Help: "Operational contact address for instance administrators."},
	{Key: "max_comment_chars", Label: "Maximum comment length", Kind: "number", Help: "Between 100 and 20,000 characters."},
	{Key: "federate_remote_ratings", Label: "Federate remote ratings", Kind: "bool", Help: "Default for federation-related features that consume remote ratings."},
}

func SettingValue(c Config, key string) string {
	switch key {
	case "name":
		return c.Name
	case "description":
		return c.InstanceDesc
	case "registration":
		return c.Registration
	case "invite_code":
		return c.InviteCode
	case "contact_email":
		return c.ContactEmail
	case "admin_email":
		return c.AdminEmail
	case "max_comment_chars":
		return strconv.Itoa(c.MaxCommentChars)
	case "federate_remote_ratings":
		return strconv.FormatBool(c.FederateRemoteRatings)
	default:
		return ""
	}
}

func IsEditableSetting(key string) bool {
	for _, def := range EditableSettings {
		if def.Key == key {
			return true
		}
	}
	return false
}

// WithSetting validates and applies one editable value. Unknown keys and
// secrets cannot be changed through this path.
func (c Config) WithSetting(key, value string) (Config, error) {
	if !IsEditableSetting(key) {
		return c, fmt.Errorf("unknown setting %q", key)
	}
	value = strings.TrimSpace(value)
	switch key {
	case "name":
		if value == "" {
			return c, fmt.Errorf("instance name cannot be empty")
		}
		if utf8.RuneCountInString(value) > 80 {
			return c, fmt.Errorf("instance name must be 80 characters or fewer")
		}
		c.Name = value
	case "description":
		if utf8.RuneCountInString(value) > 500 {
			return c, fmt.Errorf("description must be 500 characters or fewer")
		}
		c.InstanceDesc = value
	case "registration":
		switch value {
		case "open", "invite", "approval", "closed":
			c.Registration = value
		default:
			return c, fmt.Errorf("registration must be open, invite, approval, or closed")
		}
	case "invite_code":
		if utf8.RuneCountInString(value) > 128 {
			return c, fmt.Errorf("invite code must be 128 characters or fewer")
		}
		c.InviteCode = value
	case "contact_email":
		if value != "" {
			if _, err := mail.ParseAddress(value); err != nil {
				return c, fmt.Errorf("contact email is not valid")
			}
		}
		c.ContactEmail = value
	case "admin_email":
		if value != "" {
			if _, err := mail.ParseAddress(value); err != nil {
				return c, fmt.Errorf("admin email is not valid")
			}
		}
		c.AdminEmail = value
	case "max_comment_chars":
		n, err := strconv.Atoi(value)
		if err != nil || n < 100 || n > 20000 {
			return c, fmt.Errorf("maximum comment length must be between 100 and 20,000")
		}
		c.MaxCommentChars = n
	case "federate_remote_ratings":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return c, fmt.Errorf("federate remote ratings must be true or false")
		}
		c.FederateRemoteRatings = b
	}
	return c, nil
}

func (c Config) WithSettings(values map[string]string) (Config, error) {
	for _, def := range EditableSettings {
		if v, ok := values[def.Key]; ok {
			var err error
			c, err = c.WithSetting(def.Key, v)
			if err != nil {
				return c, err
			}
		}
	}
	if c.Registration == "invite" && c.InviteCode == "" {
		return c, fmt.Errorf("invite registration requires an invite code")
	}
	return c, nil
}
