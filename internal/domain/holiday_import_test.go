package domain

import "testing"

func TestHolidayImportDatesNamesAndDuplicates(t *testing.T) {
	input := HolidayCalendarImport{Year: 2026, Months: []HolidayImportMonth{{ID: 1, Holidays: []HolidayImportDay{{Date: 1, Day: "th", NameEN: "New year", NameTH: "วันหยุดปีใหม่"}, {Date: 1, Day: "Thursday", NameEN: "NEW YEAR"}, {Date: 2, Day: "fr", NameTH: "วันหยุดพิเศษ"}}}}}
	result, err := PreviewHolidayImport(&State{}, input)
	if err != nil || result.Created != 2 || result.Skipped != 1 || result.Rows[0].NameTH != "วันหยุดปีใหม่" || result.Rows[0].Date != "2026-01-01" {
		t.Fatalf("import preview mismatch %+v %v", result, err)
	}
	existing := Record{Kind: "holiday", Title: "New year", Status: "active", Data: map[string]any{"date": "2025-01-01", "recurrence": "annual", "team": "All teams"}}
	result, err = PreviewHolidayImport(&State{Records: []Record{existing}}, input)
	if err != nil || result.Created != 1 || result.Skipped != 2 {
		t.Fatal("existing annual holiday duplicated")
	}
	existing.Data["team"] = "Engineering"
	result, err = PreviewHolidayImport(&State{Records: []Record{existing}}, input)
	if err != nil || result.Created != 2 {
		t.Fatal("team-specific holiday hid a company import")
	}
	leap := HolidayCalendarImport{Year: 2028, Months: []HolidayImportMonth{{ID: 2, Holidays: []HolidayImportDay{{Date: 29, Day: "tu", NameEN: "Leap holiday"}}}}}
	if _, err = PreviewHolidayImport(&State{}, leap); err != nil {
		t.Fatal(err)
	}
	leap.Year = 2026
	if _, err = PreviewHolidayImport(&State{}, leap); err == nil {
		t.Fatal("invalid leap day accepted")
	}
}
func TestHolidayImportRejectsInvalidInput(t *testing.T) {
	for _, input := range []HolidayCalendarImport{
		{Year: 2569, Months: []HolidayImportMonth{{ID: 1}}},
		{Year: 2026, Months: []HolidayImportMonth{{ID: 1}, {ID: 1}}},
		{Year: 2026, Months: []HolidayImportMonth{{ID: 13}}},
		{Year: 2026, Months: []HolidayImportMonth{{ID: 1, Holidays: []HolidayImportDay{{Date: 1, Day: "fr", NameEN: "Wrong weekday"}}}}},
		{Year: 2026, Months: []HolidayImportMonth{{ID: 1, Holidays: []HolidayImportDay{{Date: 0, NameEN: "Wrong date"}}}}},
		{Year: 2026, Months: []HolidayImportMonth{{ID: 1, Holidays: []HolidayImportDay{{Date: 1}}}}},
	} {
		if _, err := PreviewHolidayImport(&State{}, input); err == nil {
			t.Fatalf("invalid calendar accepted %+v", input)
		}
	}
}
