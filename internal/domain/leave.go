package domain

import (
	"errors"
	"math"
	"time"
)

func HolidayOn(r Record, d time.Time) bool {
	base, err := time.Parse("2006-01-02", Text(r, "date"))
	if err != nil {
		return false
	}
	annual := Text(r, "recurrence") == "annual"
	if annual && d.Year() < base.Year() {
		return false
	}
	years := []int{base.Year()}
	if annual {
		years = []int{d.Year() - 1, d.Year()}
	}
	for _, year := range years {
		if year < base.Year() {
			continue
		}
		candidate := time.Date(year, base.Month(), base.Day(), 0, 0, 0, 0, time.UTC)
		if candidate.Month() != base.Month() {
			continue
		}
		if candidate.Format("2006-01-02") == d.Format("2006-01-02") {
			return true
		}
		if Text(r, "observed") == "next-weekday" {
			for candidate.Weekday() == time.Saturday || candidate.Weekday() == time.Sunday {
				candidate = candidate.AddDate(0, 0, 1)
			}
			if candidate.Format("2006-01-02") == d.Format("2006-01-02") {
				return true
			}
		}
	}
	return false
}
func LeaveDuration(s *State, u Employee, r Record, start, end time.Time) (float64, int64, int64, error) {
	unit := Text(r, "unit")
	if unit == "" {
		unit = "day"
	}
	hours := 8.0
	c := s.Company(u.CompanyID)
	if c != nil && c.WorkdayHours > 0 {
		hours = c.WorkdayHours
	}
	ws, _ := time.Parse("15:04", "09:00")
	we, _ := time.Parse("15:04", "18:00")
	if c != nil {
		if v, err := time.Parse("15:04", c.WorkdayStart); err == nil {
			ws = v
		}
		if v, err := time.Parse("15:04", c.WorkdayEnd); err == nil {
			we = v
		}
	}
	workStart, workEnd := ws.Hour()*60+ws.Minute(), we.Hour()*60+we.Minute()
	breakStart, breakEnd := workStart+int(hours*30), workEnd-int(hours*30)
	if unit == "day" {
		return float64(Workdays(s, u, start, end)), start.Unix(), end.Add(24 * time.Hour).Unix(), nil
	}
	if start != end {
		return 0, 0, 0, errors.New("hourly or half-day leave must be on one date")
	}
	if Workdays(s, u, start, end) == 0 {
		return 0, 0, 0, errors.New("select a working day")
	}
	if unit == "half-day" {
		period := Text(r, "period")
		if period != "morning" && period != "afternoon" {
			return 0, 0, 0, errors.New("choose morning or afternoon")
		}
		from, to := workStart, breakStart
		if period == "afternoon" {
			from, to = breakEnd, workEnd
		}
		return 0.5, start.Unix() + int64(from*60), start.Unix() + int64(to*60), nil
	}
	if unit != "hour" {
		return 0, 0, 0, errors.New("leave unit must be day, half-day or hour")
	}
	from, err := time.Parse("15:04", Text(r, "startTime"))
	if err != nil {
		return 0, 0, 0, errors.New("valid start time required")
	}
	to, err := time.Parse("15:04", Text(r, "endTime"))
	if err != nil || !to.After(from) {
		return 0, 0, 0, errors.New("end time must follow start time")
	}
	minutes := to.Sub(from).Minutes()
	if math.Mod(minutes, 30) != 0 {
		return 0, 0, 0, errors.New("hourly leave must use 30-minute increments within workday hours")
	}
	fromMin := from.Hour()*60 + from.Minute()
	toMin := to.Hour()*60 + to.Minute()
	if fromMin < workStart || toMin > workEnd {
		return 0, 0, 0, errors.New("hourly leave must fall within company working times")
	}
	minutes -= float64(max(0, min(toMin, breakEnd)-max(fromMin, breakStart)))
	if minutes <= 0 || minutes/60 > hours {
		return 0, 0, 0, errors.New("select working hours outside the unpaid break")
	}
	return minutes / (hours * 60), start.Unix() + int64(fromMin*60), start.Unix() + int64(toMin*60), nil
}
