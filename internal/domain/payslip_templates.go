package domain

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

func validatePayslipTemplate(r Record) error {
	if value, ok := r.Data["fields"]; ok {
		switch fields := value.(type) {
		case []string:
		case []any:
			for _, field := range fields {
				if _, valid := field.(string); !valid {
					return errors.New("template fields must be text labels")
				}
			}
		default:
			return errors.New("template fields must be a list")
		}
	}
	fields := Strings(r, "fields")
	if len(fields) > 60 {
		return errors.New("template supports at most 60 fields")
	}
	seen := map[string]bool{}
	for _, field := range fields {
		if strings.TrimSpace(field) == "" || utf8.RuneCountInString(field) > 80 || seen[field] {
			return errors.New("template fields must be unique labels of 1–80 characters")
		}
		seen[field] = true
	}
	if layout := Text(r, "layout"); layout != "" && layout != "classic" && layout != "table" {
		return errors.New("invalid payslip layout")
	}
	if color := Text(r, "accentColor"); color != "" && !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(color) {
		return errors.New("use a six-digit hex accent color")
	}
	for _, key := range []string{"company", "taxId", "address", "payslipTitle", "footer", "layout", "accentColor"} {
		if value, ok := r.Data[key]; ok {
			s, valid := value.(string)
			maximum := map[string]int{"company": 200, "taxId": 200, "address": 1000, "payslipTitle": 120, "footer": 2000, "layout": 20, "accentColor": 7}[key]
			if !valid || utf8.RuneCountInString(s) > maximum {
				return errors.New("invalid payslip company details")
			}
		}
	}
	grouped := map[string]bool{}
	if value, ok := r.Data["fieldLabels"]; ok {
		labels, valid := value.(map[string]any)
		if !valid {
			return errors.New("field labels must be an object")
		}
		for field, raw := range labels {
			label, valid := raw.(string)
			if !seen[field] || !valid || utf8.RuneCountInString(label) > 160 {
				return errors.New("invalid printed field label")
			}
		}
	}
	for _, key := range []string{"infoFields", "earningFields", "deductionFields", "ytdFields"} {
		value, ok := r.Data[key]
		if !ok {
			continue
		}
		raw, a := value.([]any)
		_, b := value.([]string)
		if !a && !b {
			return errors.New("template sections must be field lists")
		}
		for _, field := range raw {
			if _, valid := field.(string); !valid {
				return errors.New("template sections must be field lists")
			}
		}
		for _, field := range Strings(r, key) {
			if field == "Net pay" || !seen[field] || grouped[field] {
				return errors.New("each section field must exist once in the template")
			}
			grouped[field] = true
		}
	}
	if Text(r, "layout") == "table" {
		if len(Strings(r, "earningFields")) == 0 {
			return errors.New("table payslip needs an earnings field")
		}
		if !seen["Net pay"] {
			return errors.New("table payslip needs the Net pay field")
		}
		for _, field := range fields {
			if field != "Net pay" && !grouped[field] {
				return errors.New("assign each field to a payslip section")
			}
		}
	}
	return nil
}
