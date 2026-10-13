package domain

import "errors"

// Hierarchy groups work. It never inherits membership or payroll permissions.
func validateProjectParent(s *State, r Record) error {
	parent, exists := r.Data["parentId"]
	if exists {
		if _, ok := parent.(string); !ok {
			return errors.New("parent project must be an ID")
		}
	}
	id := Text(r, "parentId")
	visited := map[string]bool{r.ID: true}
	first := true
	for id != "" {
		if visited[id] {
			return errors.New("project hierarchy contains a cycle")
		}
		visited[id] = true
		var ancestor *Record
		for i := range s.Records {
			p := &s.Records[i]
			if p.ID == id && p.Kind == "project" && CompanyID(p.CompanyID) == CompanyID(r.CompanyID) {
				ancestor = p
				break
			}
		}
		if ancestor == nil {
			return errors.New("parent project not found in this company")
		}
		if first && r.Status == "active" && ancestor.Status != "active" {
			return errors.New("an active child needs an active parent project")
		}
		first = false
		id = Text(*ancestor, "parentId")
	}
	if r.Status == "archived" {
		for _, p := range s.Records {
			if p.Kind == "project" && Text(p, "parentId") == r.ID && p.Status == "active" {
				return errors.New("archive or reassign active child projects first")
			}
		}
	}
	return nil
}

func CanReadFile(s *State, u Employee, f File) bool {
	if CompanyID(u.CompanyID) != CompanyID(f.CompanyID) {
		return false
	}
	switch f.Category {
	case "community-image":
		if f.EmployeeID == u.ID || Manager(u) {
			return true
		}
		for _, r := range s.Records {
			if r.ID == f.RecordID && CommunityKind(r.Kind) {
				return true
			}
		}
		return false
	case "avatar":
		employee := s.Employee(f.EmployeeID)
		return employee != nil && employee.AvatarFileID == f.ID
	case "project-resource":
		return f.ProjectID != "" && ProjectAccess(s, u, f.ProjectID)
	case "payslip-reference":
		return u.Role == "owner"
	default:
		return Manager(u) || f.EmployeeID == u.ID
	}
}
func CanDeleteFile(s *State, u Employee, f File) bool {
	if !CanReadFile(s, u, f) {
		return false
	}
	if f.Category == "community-image" {
		return f.RecordID == "" && (f.EmployeeID == u.ID || Manager(u))
	}
	if f.Category == "avatar" {
		return f.EmployeeID == u.ID || Manager(u)
	}
	if f.Category == "payslip" {
		return u.Role == "owner"
	}
	if f.Category == "project-resource" {
		if f.Environment != "" {
			return Manager(u)
		}
		return Manager(u) || f.EmployeeID == u.ID
	}
	if f.Category == "payslip-reference" {
		return u.Role == "owner"
	}
	return Manager(u) || (f.EmployeeID == u.ID && f.Category == "attachment")
}
