package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type HolidayCalendarImport struct {
	Year   int                  `json:"year"`
	Months []HolidayImportMonth `json:"month"`
}
type HolidayImportMonth struct {
	ID       int                `json:"id"`
	NameTH   string             `json:"name_th"`
	NameEN   string             `json:"name_en"`
	Holidays []HolidayImportDay `json:"holidays"`
}
type HolidayImportDay struct {
	Date   int    `json:"date"`
	Day    string `json:"day"`
	NameTH string `json:"name_th"`
	NameEN string `json:"name_en"`
}
type HolidayImportRow struct {
	Date   string `json:"date"`
	Day    string `json:"day"`
	NameTH string `json:"name_th"`
	NameEN string `json:"name_en"`
	Action string `json:"action"`
}
type HolidayImportResult struct {
	Year    int                `json:"year"`
	Created int                `json:"created"`
	Skipped int                `json:"skipped"`
	Rows    []HolidayImportRow `json:"rows"`
}

func PreviewHolidayImport(s *State, input HolidayCalendarImport) (HolidayImportResult, error) {
	out := HolidayImportResult{Year: input.Year, Rows: []HolidayImportRow{}}
	if input.Year < 2000 || input.Year > 2200 || len(input.Months) < 1 || len(input.Months) > 12 {
		return out, errors.New("use a Gregorian year 2000-2200 and 1-12 months")
	}
	months := map[int]bool{}
	weekdays := []string{"su", "mo", "tu", "we", "th", "fr", "sa"}
	aliases := map[string]string{"sun": "su", "sunday": "su", "mon": "mo", "monday": "mo", "tue": "tu", "tues": "tu", "tuesday": "tu", "wed": "we", "wednesday": "we", "thu": "th", "thur": "th", "thurs": "th", "thursday": "th", "thurseday": "th", "fri": "fr", "friday": "fr", "sat": "sa", "saturday": "sa"}
	staged := []Record{}
	for _, month := range input.Months {
		if month.ID < 1 || month.ID > 12 || months[month.ID] || utf8.RuneCountInString(month.NameEN) > 100 || utf8.RuneCountInString(month.NameTH) > 100 {
			return out, errors.New("month IDs must be unique integers from 1 to 12")
		}
		months[month.ID] = true
		for i, holiday := range month.Holidays {
			if len(out.Rows) >= 500 {
				return out, errors.New("import at most 500 holidays at a time")
			}
			fail := func(reason string) (HolidayImportResult, error) {
				return out, fmt.Errorf("month %d, holiday %d: %s", month.ID, i+1, reason)
			}
			date := time.Date(input.Year, time.Month(month.ID), holiday.Date, 0, 0, 0, 0, time.UTC)
			if holiday.Date < 1 || date.Year() != input.Year || int(date.Month()) != month.ID {
				return fail("invalid calendar date")
			}
			en, th := strings.TrimSpace(holiday.NameEN), strings.TrimSpace(holiday.NameTH)
			if en == "" && th == "" || utf8.RuneCountInString(en) > 200 || utf8.RuneCountInString(th) > 200 {
				return fail("provide an English or Thai holiday name up to 200 characters")
			}
			day := strings.ToLower(strings.TrimSpace(holiday.Day))
			if alias := aliases[day]; alias != "" {
				day = alias
			}
			actual := weekdays[int(date.Weekday())]
			if day != "" && day != actual {
				return fail("weekday does not match the calendar date")
			}
			row := HolidayImportRow{Date: date.Format("2006-01-02"), Day: actual, NameTH: th, NameEN: en, Action: "create"}
			matches := func(r Record) bool {
				if r.Kind != "holiday" || r.Status != "active" || (Text(r, "team") != "" && Text(r, "team") != "All teams") || !HolidayOn(r, date) {
					return false
				}
				for _, existing := range []string{r.Title, Text(r, "nameEn"), Text(r, "nameTh")} {
					for _, name := range []string{en, th} {
						if name != "" && strings.EqualFold(strings.TrimSpace(existing), name) {
							return true
						}
					}
				}
				return false
			}
			duplicate := false
			for _, r := range s.Records {
				if matches(r) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				for _, r := range staged {
					if matches(r) {
						duplicate = true
						break
					}
				}
			}
			if duplicate {
				row.Action = "skip"
				out.Skipped++
			} else {
				out.Created++
				staged = append(staged, ImportedHolidayRecord(row, Employee{}, "", ""))
			}
			out.Rows = append(out.Rows, row)
		}
	}
	if len(out.Rows) == 0 {
		return out, errors.New("include at least one holiday")
	}
	return out, nil
}
func ImportedHolidayRecord(row HolidayImportRow, u Employee, id, created string) Record {
	title := row.NameEN
	if title == "" {
		title = row.NameTH
	}
	return Record{ID: id, CompanyID: CompanyID(u.CompanyID), EmployeeID: u.ID, Kind: "holiday", Title: title, Status: "active", CreatedAt: created, Data: map[string]any{"date": row.Date, "nameEn": row.NameEN, "nameTh": row.NameTH, "team": "All teams", "recurrence": "none", "observed": "none", "source": "json-import"}}
}
