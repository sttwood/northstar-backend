package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

type projectEnvironment struct {
	URL      string               `json:"url"`
	APIURL   string               `json:"apiUrl"`
	Branch   string               `json:"branch"`
	Version  string               `json:"version"`
	Status   string               `json:"status"`
	Notes    string               `json:"notes"`
	Docs     []environmentDoc     `json:"docs"`
	Timeline []environmentEvent   `json:"timeline"`
	Accounts []environmentAccount `json:"accounts"`
}
type environmentDoc struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}
type environmentEvent struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Date        string `json:"date"`
	Description string `json:"description"`
	Status      string `json:"status"`
}
type environmentAccount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	URL       string `json:"url"`
	SecretRef string `json:"secretRef"`
	Status    string `json:"status"`
}

func environmentURL(value string, required bool) bool {
	if value == "" {
		return !required
	}
	parsed, err := url.Parse(value)
	return err == nil && Contains([]string{"http", "https"}, parsed.Scheme) && parsed.Hostname() != "" && parsed.User == nil && len(value) <= 500
}
func environmentText(value string, required bool, maximum int) bool {
	return (!required || strings.TrimSpace(value) != "") && utf8.RuneCountInString(value) <= maximum
}

func validateEnvironments(raw any) error {
	if raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return errors.New("invalid project environments")
	}
	var environments map[string]*projectEnvironment
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&environments); err != nil {
		return errors.New("invalid environment fields; store account references, never passwords or tokens")
	}
	if len(environments) > 4 {
		return errors.New("only dev, sit, uat and prod environments are supported")
	}
	for name, environment := range environments {
		if !Contains([]string{"dev", "sit", "uat", "prod"}, name) || environment == nil {
			return errors.New("invalid project environment")
		}
		if !environmentURL(environment.URL, false) || !environmentURL(environment.APIURL, false) || !Contains([]string{"healthy", "maintenance", "offline"}, environment.Status) || !environmentText(environment.Notes, false, 4000) || !environmentText(environment.Branch, false, 500) || !environmentText(environment.Version, false, 500) {
			return errors.New("invalid environment settings or URL")
		}
		if len(environment.Docs) > 100 || len(environment.Timeline) > 100 || len(environment.Accounts) > 100 {
			return errors.New("each environment list is limited to 100 items")
		}
		ids := map[string]bool{}
		validID := func(id string) bool {
			if !environmentText(id, true, 100) || ids[id] {
				return false
			}
			ids[id] = true
			return true
		}
		for _, doc := range environment.Docs {
			if !validID(doc.ID) || !environmentText(doc.Title, true, 500) || !environmentText(doc.Description, false, 4000) || !environmentURL(doc.URL, true) {
				return errors.New("invalid environment document")
			}
		}
		for _, event := range environment.Timeline {
			if !validID(event.ID) || !environmentText(event.Title, true, 500) || !environmentText(event.Description, false, 4000) || !Contains([]string{"planned", "complete"}, event.Status) {
				return errors.New("invalid environment timeline event")
			}
			if _, err := date(event.Date); err != nil {
				return err
			}
		}
		for _, account := range environment.Accounts {
			if !validID(account.ID) || !environmentText(account.Name, true, 500) || !environmentText(account.Username, true, 500) || !environmentText(account.Role, true, 500) || !environmentURL(account.URL, false) || !environmentURL(account.SecretRef, false) || !Contains([]string{"active", "disabled"}, account.Status) {
				return errors.New("invalid environment account reference")
			}
		}
	}
	return nil
}
