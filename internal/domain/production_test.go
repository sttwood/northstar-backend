package domain

import (
	"testing"
	"time"
)

func TestFractionalLeaveBreaksAndOverlap(t *testing.T) {
	s := State{Companies: []Company{DefaultPolicy("default", "Company", "company")}, Employees: []Employee{{ID: "e", Role: "employee", LeaveAllowance: 20}}}
	u := s.Employees[0]
	first := Record{ID: "morning", Kind: "leave", EmployeeID: "e", Title: "Morning", Status: "pending", Data: map[string]any{"type": "vacation", "startDate": "2026-11-02", "endDate": "2026-11-02", "unit": "half-day", "period": "morning"}}
	if err := Validate(&s, u, &first, nil); err != nil || Number(first, "days") != 0.5 {
		t.Fatalf("half day: %v %v", first, err)
	}
	s.Records = append(s.Records, first)
	afternoon := first
	afternoon.ID = "afternoon"
	afternoon.Data = map[string]any{"type": "vacation", "startDate": "2026-11-02", "endDate": "2026-11-02", "unit": "hour", "startTime": "14:00", "endTime": "16:00"}
	if err := Validate(&s, u, &afternoon, nil); err != nil || Number(afternoon, "days") != 0.25 {
		t.Fatalf("disjoint hourly leave: %v", err)
	}
	afternoon.Data["startTime"] = "12:00"
	if err := Validate(&s, u, &afternoon, nil); err == nil {
		t.Fatal("overlapping hours accepted")
	}
	afternoon.Data["startTime"] = "13:00"
	afternoon.Data["endTime"] = "14:00"
	if err := Validate(&s, u, &afternoon, nil); err == nil {
		t.Fatal("unpaid break counted as leave")
	}
	s.Records = nil
	afternoon.Data["startTime"] = "09:00"
	afternoon.Data["endTime"] = "18:00"
	if err := Validate(&s, u, &afternoon, nil); err != nil || Number(afternoon, "days") != 1 {
		t.Fatalf("full work window should exclude unpaid break: %v", err)
	}
}
func TestAnnualObservedHolidayAcrossYearBoundary(t *testing.T) {
	r := Record{Kind: "holiday", Data: map[string]any{"date": "2026-12-31", "recurrence": "annual", "observed": "next-weekday"}}
	day, _ := time.Parse("2006-01-02", "2029-01-01")
	if !HolidayOn(r, day) {
		t.Fatal("previous-year Sunday holiday was not observed on Monday")
	}
	r.Data["date"] = "2024-02-29"
	day, _ = time.Parse("2006-01-02", "2026-03-01")
	if HolidayOn(r, day) {
		t.Fatal("leap day shifted into March")
	}
}
func TestOwnerApprovalIgnoresLegacyReportingRolesAndRejectsSelfApproval(t *testing.T) {
	c := DefaultPolicy("default", "Company", "company")
	c.ApprovalStages = []string{"manager", "hr", "owner"}
	employee := Employee{ID: "e", Role: "employee", ManagerID: "manager"}
	manager := Employee{ID: "manager", Role: "employee"}
	hr := Employee{ID: "hr", Role: "hr"}
	owner := Employee{ID: "owner", Role: "owner"}
	s := State{Companies: []Company{c}, Employees: []Employee{employee, manager, hr, owner}}
	r := Record{ID: "r", Kind: "request", EmployeeID: "e", Status: "pending", Data: map[string]any{}}
	if err := InitializeApproval(&s, &r); err != nil {
		t.Fatal(err)
	}
	if !CanApprove(&s, owner, r) || CanApprove(&s, employee, r) || CanApprove(&s, manager, r) || CanApprove(&s, hr, r) || len(ApprovalSteps(r)) != 1 {
		t.Fatal("owner-only policy bypassed")
	}
	next := r
	next.Data = map[string]any{}
	PreserveApproval(&next, r)
	next.Status = "approved"
	if AdvanceApproval(&s, manager, &next, r) == nil {
		t.Fatal("employee manager approved")
	}
	if err := AdvanceApproval(&s, owner, &next, r); err != nil || next.Status != "approved" {
		t.Fatalf("owner decision failed: %v", err)
	}
	own := r
	own.EmployeeID = owner.ID
	if CanApprove(&s, owner, own) {
		t.Fatal("owner self-approved")
	}
	legacy := r
	legacy.Data = map[string]any{"approvalIndex": float64(0), "approvalSteps": []ApprovalStep{{Stage: "manager", Status: "pending", Approvers: []string{manager.ID}}, {Stage: "hr", Status: "pending", Approvers: []string{hr.ID}}}, "currentApproverIds": []string{manager.ID}}
	next = legacy
	next.Data = map[string]any{}
	PreserveApproval(&next, legacy)
	next.Status = "approved"
	if AdvanceApproval(&s, owner, &next, legacy) != nil || ApprovalSteps(next)[1].Status != "superseded" {
		t.Fatal("legacy pending policy was not resolved by owner")
	}
	s.Employees[1].ManagerID = "e"
	if ValidateManager(&s, "e", "manager") == nil {
		t.Fatal("reporting cycle accepted")
	}
}
func TestThaiPayroll2026ProgressiveBracketsAndSocialSecurity(t *testing.T) {
	for _, test := range []struct{ income, tax int64 }{{150000, 0}, {300000, 7500}, {500000, 27500}, {750000, 65000}, {1000000, 115000}, {2000000, 365000}, {5000000, 1265000}, {6000000, 1615000}} {
		if got := ThaiAnnualTax(test.income * 100); got != test.tax*100 {
			t.Errorf("%d: got %d want %d", test.income, got, test.tax*100)
		}
	}
	if ThaiSocialSecurity(65000, true) != 87500 || ThaiSocialSecurity(1650, true) != 8300 || ThaiSocialSecurity(0, true) != 0 {
		t.Fatal("2026 social security or baht rounding incorrect")
	}
	result, err := CalculateThaiPayroll(PayrollInput{Year: 2026, Month: 1, Salary: 65000, SocialSecurityWage: 65000, Insured: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectedAnnualGross != 780000 || result.TaxableAnnualIncome != 609500 || result.AnnualTax != 43925 || result.Withholding != 3660.42 || result.NetPay != 60464.58 {
		t.Fatalf("unexpected payroll: %+v", result)
	}
	if _, err := CalculateThaiPayroll(PayrollInput{Year: 2027, Month: 1}); err == nil {
		t.Fatal("unsupported tax year silently accepted")
	}
}
