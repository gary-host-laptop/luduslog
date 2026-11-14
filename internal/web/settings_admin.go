package web

import (
	"context"
	"net/http"
	"strconv"

	"luduslog/internal/config"
)

// instanceConfig overlays the admin-editable settings table on the startup
// configuration. Secrets and infrastructure values are never read from the DB.
func (s *Server) instanceConfig(ctx context.Context) config.Config {
	c := s.cfg
	for _, def := range config.EditableSettings {
		if v, ok := s.st.LookupSetting(ctx, def.Key); ok {
			if applied, err := c.WithSetting(def.Key, v); err == nil {
				c = applied
			}
		}
	}
	return c
}

type adminSettingField struct {
	config.SettingDef
	Value string
}

type adminSettingsData struct {
	Page
	Tab      string
	Settings []adminSettingField
	Error    string
}

func (s *Server) adminSettingsPage(w http.ResponseWriter, r *http.Request) {
	cfg := s.instanceConfig(r.Context())
	values := make([]adminSettingField, 0, len(config.EditableSettings))
	for _, def := range config.EditableSettings {
		values = append(values, adminSettingField{SettingDef: def, Value: config.SettingValue(cfg, def.Key)})
	}
	s.render(w, r, "admin_settings", adminSettingsData{Page: s.page(r, "Instance settings"), Tab: "settings", Settings: values})
}

func (s *Server) adminSettingsSave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if err := r.ParseForm(); err != nil {
		s.serverError(w, r, err)
		return
	}
	values := map[string]string{}
	for _, def := range config.EditableSettings {
		values[def.Key] = r.FormValue(def.Key)
	}
	next, err := s.cfg.WithSettings(values)
	if err != nil {
		fields := make([]adminSettingField, 0, len(config.EditableSettings))
		for _, def := range config.EditableSettings {
			fields = append(fields, adminSettingField{SettingDef: def, Value: values[def.Key]})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "admin_settings", adminSettingsData{
			Page: s.page(r, "Instance settings"), Tab: "settings", Settings: fields, Error: err.Error(),
		})
		return
	}
	old := s.instanceConfig(r.Context())
	if err := s.st.SetSettings(r.Context(), values); err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, def := range config.EditableSettings {
		before, after := config.SettingValue(old, def.Key), config.SettingValue(next, def.Key)
		if before == after {
			continue
		}
		detail := def.Key + ": " + strconv.Quote(before) + " -> " + strconv.Quote(after)
		if err := s.st.Audit(r.Context(), u.ID, "setting_change", detail); err != nil {
			s.flash(w, r, "error", "Settings saved but the audit entry failed: "+err.Error())
			s.redirect(w, r, "/admin/settings")
			return
		}
	}
	s.flash(w, r, "success", "Instance settings saved. Changes take effect immediately.")
	s.redirect(w, r, "/admin/settings")
}
