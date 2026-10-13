package domain

import "testing"

func TestHierarchyValidationAndIndependentAccess(t *testing.T) {
	owner := Employee{ID: "owner", CompanyID: "one", Role: "owner"}
	member := Employee{ID: "member", CompanyID: "one", Role: "employee"}
	parent := Record{ID: "parent", CompanyID: "one", Kind: "project", EmployeeID: owner.ID, Status: "active", Data: map[string]any{"members": []string{member.ID}}}
	child := Record{ID: "child", CompanyID: "one", Kind: "project", EmployeeID: owner.ID, Status: "active", Data: map[string]any{"parentId": "parent", "members": []string{owner.ID}}}
	s := State{Records: []Record{parent, child, {ID: "foreign", CompanyID: "two", Kind: "project", Status: "active"}}}
	if err := validateProjectParent(&s, child); err != nil {
		t.Fatal(err)
	}
	if !ProjectAccess(&s, member, "parent") || ProjectAccess(&s, member, "child") || ProjectAccess(&s, owner, "foreign") || ProjectAccess(&s, owner, "missing") {
		t.Fatal("hierarchy leaked access")
	}
	for _, id := range []string{"parent", "child", "missing", "foreign"} {
		candidate := parent
		candidate.Data = map[string]any{"parentId": id}
		if validateProjectParent(&s, candidate) == nil {
			t.Fatalf("accepted invalid parent %s", id)
		}
	}
	archived := parent
	archived.Status = "archived"
	if validateProjectParent(&s, archived) == nil {
		t.Fatal("archived active child's parent")
	}
	s.Records[0].Status = "archived"
	if validateProjectParent(&s, child) == nil {
		t.Fatal("active child accepted archived parent")
	}
	resource := File{CompanyID: "one", EmployeeID: member.ID, Category: "project-resource", ProjectID: "child"}
	if CanReadFile(&s, member, resource) || CanDeleteFile(&s, member, resource) {
		t.Fatal("former uploader bypassed membership")
	}
	ref := File{CompanyID: "one", EmployeeID: member.ID, Category: "payslip-reference"}
	for _, role := range []string{"hr", "employee"} {
		u := member
		u.Role = role
		if CanReadFile(&s, u, ref) || CanDeleteFile(&s, u, ref) {
			t.Fatal("reference exposed to non-owner")
		}
	}
	if !CanReadFile(&s, owner, ref) {
		t.Fatal("owner reference unavailable")
	}
}

func TestPayslipTemplateValidation(t *testing.T) {
	valid := func() Record {
		return Record{Kind: "template", Data: map[string]any{"layout": "table", "fields": []any{"Employee name", "Base salary", "Net pay"}, "infoFields": []any{"Employee name"}, "earningFields": []any{"Base salary"}, "fieldLabels": map[string]any{"Base salary": "Salary"}, "accentColor": "#123abc"}}
	}
	if err := validatePayslipTemplate(valid()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]any{"layout": 42, "accentColor": "red", "earningFields": []any{42}, "infoFields": []any{"Base salary"}, "fieldLabels": map[string]any{"not-a-field": "Bad"}, "fields": []any{"Base salary", "Base salary", "Net pay"}}
	for key, value := range cases {
		r := valid()
		r.Data[key] = value
		if validatePayslipTemplate(r) == nil {
			t.Fatalf("accepted invalid %s", key)
		}
	}
	hr := Employee{ID: "hr", Role: "hr"}
	s := State{}
	r := valid()
	if CanEdit(&s, hr, r) {
		t.Fatal("HR can edit owner template")
	}
}
