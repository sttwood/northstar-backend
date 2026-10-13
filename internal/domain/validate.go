package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var kinds = []string{"leave", "overtime", "document", "ticket", "request", "project", "task", "holiday", "benefit", "onboarding", "template", "comment"}

func Validate(s *State, u Employee, r *Record, previous *Record) error {
	if !Contains(kinds, r.Kind) {
		return errors.New("unknown record kind")
	}
	if Contains([]string{"template", "holiday", "benefit"}, r.Kind) && u.Role != "owner" {
		return errors.New("only the owner may manage payroll, benefits and holidays")
	}
	if r.Kind == "onboarding" && u.Role != "owner" && (previous == nil || r.EmployeeID != u.ID) {
		return errors.New("only the owner may assign onboarding")
	}
	r.Title = strings.TrimSpace(r.Title)
	if r.Title == "" || utf8.RuneCountInString(r.Title) > 200 {
		return errors.New("title must be between 1 and 200 characters")
	}
	if r.Data == nil {
		r.Data = map[string]any{}
	}
	if s.Employee(r.EmployeeID) == nil {
		return errors.New("employee does not exist")
	}
	if CompanyID(u.CompanyID) != CompanyID(r.CompanyID) || CompanyID(s.Employee(r.EmployeeID).CompanyID) != CompanyID(u.CompanyID) {
		return errors.New("company permission required")
	}
	if previous == nil && !Active(*s.Employee(r.EmployeeID)) {
		return errors.New("employee is not active")
	}
	if previous != nil && (previous.Kind != r.Kind || (r.Kind != "task" && previous.EmployeeID != r.EmployeeID)) {
		return errors.New("record kind and employee cannot be changed")
	}
	if previous == nil && Contains([]string{"leave", "overtime", "document", "request"}, r.Kind) && r.Status != "pending" {
		return errors.New("new requests must start pending and be reviewed separately")
	}
	if !Manager(u) {
		if previous == nil && Contains([]string{"holiday", "benefit", "project", "template"}, r.Kind) {
			return errors.New("manager permission required")
		}
		if r.Kind != "task" && r.EmployeeID != u.ID && (previous == nil || !CanApprove(s, u, *previous)) {
			return errors.New("you may only create your own requests")
		}
		if Contains([]string{"leave", "overtime", "document", "request"}, r.Kind) && r.Status != "pending" && (previous == nil || !CanApprove(s, u, *previous)) {
			return errors.New("only a manager can approve or reject a request")
		}
		if r.Kind == "ticket" && !Contains([]string{"open", "done"}, r.Status) {
			return errors.New("invalid ticket status")
		}
	}
	statuses := map[string][]string{
		"leave": {"pending", "approved", "rejected"}, "overtime": {"pending", "approved", "rejected"}, "document": {"pending", "approved", "rejected"}, "request": {"pending", "approved", "rejected"},
		"ticket": {"open", "in-progress", "done"}, "task": {"open", "in-progress", "done"}, "onboarding": {"open", "done"}, "project": {"active", "archived"}, "holiday": {"active"}, "benefit": {"active", "archived"}, "template": {"active"}, "comment": {"active"},
	}
	if !Contains(statuses[r.Kind], r.Status) {
		return errors.New("invalid status for this record")
	}
	if previous != nil && Contains([]string{"leave", "overtime", "document", "request"}, r.Kind) && previous.Status != "pending" {
		return errors.New("decided requests are immutable; create a new request")
	}
	if previous != nil && r.Kind == "onboarding" && u.Role != "owner" {
		r.Title = previous.Title
		r.Data = previous.Data
	}
	if r.Status == "rejected" && strings.TrimSpace(Text(*r, "decisionNote")) == "" {
		return errors.New("add a reason for rejecting this request")
	}
	if previous != nil && r.EmployeeID == u.ID && r.Status != previous.Status && Contains([]string{"leave", "overtime", "document", "request"}, r.Kind) {
		return errors.New("another manager must review your request")
	}
	switch r.Kind {
	case "leave":
		return validateLeave(s, *r)
	case "overtime":
		if _, err := date(Text(*r, "date")); err != nil {
			return err
		}
		start, err := time.Parse("15:04", Text(*r, "startTime"))
		if err != nil {
			return errors.New("valid start time required")
		}
		end, err := time.Parse("15:04", Text(*r, "endTime"))
		if err != nil || !end.After(start) {
			return errors.New("end time must follow start time on the same day")
		}
		hours := end.Sub(start).Hours()
		if hours > 16 {
			return errors.New("overtime cannot exceed 16 hours per request")
		}
		r.Data["hours"] = hours
		if len(Text(*r, "reason")) < 5 {
			return errors.New("add a short reason for overtime")
		}
		assigned := false
		for _, project := range s.Records {
			if project.Kind == "project" && project.ID == Text(*r, "projectId") && project.Status == "active" && Contains(Strings(project, "members"), r.EmployeeID) {
				assigned = true
			}
		}
		if !assigned {
			return errors.New("employee must be assigned to this project")
		}
		for _, other := range s.Records {
			if other.ID != r.ID && other.Kind == "overtime" && other.EmployeeID == r.EmployeeID && other.Status != "rejected" && Text(other, "date") == Text(*r, "date") && Text(other, "startTime") < Text(*r, "endTime") && Text(other, "endTime") > Text(*r, "startTime") {
				return errors.New("overtime overlaps an existing request")
			}
		}
	case "document":
		if !Contains([]string{"Employment certificate", "Salary certificate", "Tax certificate", "Other"}, Text(*r, "documentType")) || Text(*r, "purpose") == "" {
			return errors.New("document type and purpose are required")
		}
	case "ticket", "request":
		if Text(*r, "category") == "" || len(strings.TrimSpace(Text(*r, "description"))) < 5 {
			return errors.New("category and a description of at least 5 characters are required")
		}
		if r.Kind == "ticket" && !Contains([]string{"low", "normal", "high", "urgent"}, Text(*r, "priority")) {
			return errors.New("valid priority required")
		}
	case "holiday":
		for _, key := range []string{"nameEn", "nameTh"} {
			if value, exists := r.Data[key]; exists {
				if name, ok := value.(string); !ok || utf8.RuneCountInString(name) > 200 {
					return errors.New("holiday names must be text up to 200 characters")
				}
			}
		}
		if _, err := date(Text(*r, "date")); err != nil {
			return err
		}
		team := Text(*r, "team")
		if team == "" {
			r.Data["team"] = "All teams"
		}
		if recurrence := Text(*r, "recurrence"); recurrence != "" && recurrence != "none" && recurrence != "annual" {
			return errors.New("holiday recurrence must be none or annual")
		}
		if observed := Text(*r, "observed"); observed != "" && observed != "none" && observed != "next-weekday" {
			return errors.New("invalid holiday observance")
		}
	case "project":
		if err := validateProjectParent(s, *r); err != nil {
			return err
		}
		if err := validateEnvironments(r.Data["environments"]); err != nil {
			return err
		}
		start, err := date(Text(*r, "startDate"))
		if err != nil {
			return err
		}
		end, err := date(Text(*r, "dueDate"))
		if err != nil || end.Before(start) {
			return errors.New("project due date must follow start date")
		}
		members := Strings(*r, "members")
		if len(members) == 0 {
			return errors.New("assign at least one project member")
		}
		for _, id := range members {
			if s.Employee(id) == nil {
				return errors.New("project member does not exist")
			}
		}
		for _, task := range s.Records {
			if task.Kind == "task" && Text(task, "projectId") == r.ID && task.Status != "done" && !Contains(members, task.EmployeeID) {
				return errors.New("reassign unfinished tasks before removing a project member")
			}
		}
		if allocations, ok := r.Data["allocations"].(map[string]any); ok {
			for id, raw := range allocations {
				value, valid := raw.(float64)
				if !valid || value < 0 || value > 100 || !Contains(members, id) {
					return errors.New("allocations must be 0–100 for assigned members")
				}
				total := value
				for _, other := range s.Records {
					if other.ID != r.ID && other.Kind == "project" && other.Status == "active" {
						if a, ok := other.Data["allocations"].(map[string]any); ok {
							if n, ok := a[id].(float64); ok {
								total += n
							}
						}
					}
				}
				if r.Status == "active" && total > 100 {
					return fmt.Errorf("total allocation for %s exceeds 100%%", id)
				}
			}
		}
	case "task", "comment":
		projectID := Text(*r, "projectId")
		if !ProjectAccess(s, u, projectID) {
			return errors.New("project access required")
		}
		found := false
		for _, project := range s.Records {
			if project.Kind == "project" && project.ID == projectID && project.Status == "active" {
				found = true
				if r.Kind == "task" && !Contains(Strings(project, "members"), r.EmployeeID) {
					return errors.New("assignee must be a project member")
				}
			}
		}
		if !found {
			return errors.New("active project required")
		}
		if r.Kind == "task" {
			r.Data["assignee"] = r.EmployeeID
			if _, err := date(Text(*r, "dueDate")); err != nil {
				return err
			}
			for _, dep := range Strings(*r, "dependsOn") {
				if dep == r.ID {
					return errors.New("task cannot depend on itself")
				}
				valid := false
				for _, other := range s.Records {
					if other.Kind == "task" && other.ID == dep && Text(other, "projectId") == projectID {
						valid = true
					}
				}
				if !valid {
					return errors.New("dependency must be a task in this project")
				}
			}
			all := map[string][]string{}
			for _, task := range s.Records {
				if task.Kind == "task" {
					all[task.ID] = Strings(task, "dependsOn")
				}
			}
			all[r.ID] = Strings(*r, "dependsOn")
			visiting, done := map[string]bool{}, map[string]bool{}
			var visit func(string) bool
			visit = func(id string) bool {
				if visiting[id] {
					return true
				}
				if done[id] {
					return false
				}
				visiting[id] = true
				for _, dep := range all[id] {
					if visit(dep) {
						return true
					}
				}
				visiting[id] = false
				done[id] = true
				return false
			}
			if visit(r.ID) {
				return errors.New("task dependencies contain a cycle")
			}
		}
	case "onboarding":
		if Text(*r, "description") == "" {
			return errors.New("onboarding instructions required")
		}
	case "benefit":
		if Text(*r, "description") == "" || Text(*r, "contact") == "" {
			return errors.New("benefit description and contact required")
		}
	case "template":
		if len(Strings(*r, "fields")) == 0 {
			return errors.New("template needs at least one field")
		}
		if err := validatePayslipTemplate(*r); err != nil {
			return err
		}
	}
	return nil
}
func date(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return parsed, errors.New("valid date in YYYY-MM-DD format required")
	}
	return parsed, nil
}
func Workdays(s *State, employee Employee, start, end time.Time) int {
	days := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		holiday := false
		for _, r := range s.Records {
			if r.Kind == "holiday" && HolidayOn(r, d) && (Text(r, "team") == "All teams" || Text(r, "team") == employee.Team) {
				holiday = true
			}
		}
		if !holiday {
			days++
		}
	}
	return days
}
func validateLeave(s *State, r Record) error {
	start, err := date(Text(r, "startDate"))
	if err != nil {
		return err
	}
	end, err := date(Text(r, "endDate"))
	if err != nil || end.Before(start) {
		return errors.New("leave end date must follow start date")
	}
	if start.Year() != end.Year() || end.Sub(start).Hours() > 366*24 {
		return errors.New("submit leave separately for each calendar year")
	}
	if !Contains([]string{"vacation", "sick", "personal"}, Text(r, "type")) {
		return errors.New("valid leave type required")
	}
	employee := s.Employee(r.EmployeeID)
	days, intervalStart, intervalEnd, err := LeaveDuration(s, *employee, r, start, end)
	if err != nil {
		return err
	}
	if days == 0 {
		return errors.New("select at least one working day")
	}
	r.Data["days"] = days
	used := 0.0
	for _, other := range s.Records {
		if other.ID == r.ID || other.Kind != "leave" || other.EmployeeID != r.EmployeeID || other.Status == "rejected" {
			continue
		}
		if r.Status != "rejected" && Text(other, "startDate") <= Text(r, "endDate") && Text(other, "endDate") >= Text(r, "startDate") {
			otherStart, _ := date(Text(other, "startDate"))
			otherEnd, _ := date(Text(other, "endDate"))
			_, os, oe, e := LeaveDuration(s, *employee, other, otherStart, otherEnd)
			if e != nil || intervalStart < oe && intervalEnd > os {
				return errors.New("leave overlaps an existing request")
			}
		}
		if Text(other, "type") == "vacation" && strings.HasPrefix(Text(other, "startDate"), fmt.Sprint(start.Year())) {
			used += Number(other, "days")
		}
	}
	if r.Status != "rejected" && Text(r, "type") == "vacation" && used+days > employee.LeaveAllowance {
		return errors.New("not enough vacation balance, including pending requests")
	}
	return nil
}
