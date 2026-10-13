package domain_test

import (
	"northstar/backend/internal/domain"
	"northstar/backend/internal/seed"
	"testing"
)

func fixture(t *testing.T) domain.State {
	t.Helper()
	s, err := seed.Demo()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func leave(id, start, end string) domain.Record {
	return domain.Record{ID: id, Kind: "leave", EmployeeID: "maya", Title: "Vacation", Status: "pending", Data: map[string]any{"type": "vacation", "startDate": start, "endDate": end, "days": 999.0}}
}
func TestLeaveWorkingDaysAndReservedBalance(t *testing.T) {
	s := fixture(t)
	u := *s.Employee("maya")
	r := leave("new", "2026-10-22", "2026-10-26")
	if err := domain.Validate(&s, u, &r, nil); err != nil {
		t.Fatal(err)
	}
	if domain.Number(r, "days") != 2 {
		t.Fatalf("got %v days", r.Data["days"])
	}
	for _, test := range []struct{ name, start, end string }{{"overlap", "2026-10-19", "2026-10-20"}, {"balance", "2026-12-01", "2026-12-31"}, {"weekend", "2026-10-24", "2026-10-25"}, {"cross year", "2026-12-31", "2027-01-04"}, {"reverse", "2026-10-26", "2026-10-22"}} {
		t.Run(test.name, func(t *testing.T) {
			r := leave("new", test.start, test.end)
			if err := domain.Validate(&s, u, &r, nil); err == nil {
				t.Fatal("expected invalid leave to fail")
			}
		})
	}
	reserved := leave("reserved", "2026-12-01", "2026-12-10")
	if err := domain.Validate(&s, u, &reserved, nil); err != nil {
		t.Fatal(err)
	}
	s.Records = append(s.Records, reserved)
	r = leave("new", "2026-12-14", "2026-12-22")
	if err := domain.Validate(&s, u, &r, nil); err == nil {
		t.Fatal("pending leave must reserve balance")
	}
}
func TestOvertimeCalculatedAndOverlapRejected(t *testing.T) {
	s := fixture(t)
	u := *s.Employee("maya")
	r := domain.Record{ID: "new", Kind: "overtime", EmployeeID: u.ID, Title: "Extra work", Status: "pending", Data: map[string]any{"date": "2026-10-14", "startTime": "18:00", "endTime": "20:30", "hours": 999.0, "projectId": "atlas", "reason": "Finish handoff"}}
	if err := domain.Validate(&s, u, &r, nil); err != nil {
		t.Fatal(err)
	}
	if domain.Number(r, "hours") != 2.5 {
		t.Fatal("hours must be calculated on the server")
	}
	r.Data["date"] = "2026-10-13"
	if err := domain.Validate(&s, u, &r, nil); err == nil {
		t.Fatal("overlapping hours allowed")
	}
	r.Data["date"] = "2026-10-14"
	r.Data["endTime"] = "17:00"
	if err := domain.Validate(&s, u, &r, nil); err == nil {
		t.Fatal("reverse times allowed")
	}
}
func TestSelfApprovalAndForgedApprovedCreate(t *testing.T) {
	s := fixture(t)
	owner := *s.Employee("emma")
	r := leave("new", "2026-11-09", "2026-11-10")
	r.EmployeeID = owner.ID
	r.Status = "approved"
	if err := domain.Validate(&s, owner, &r, nil); err == nil {
		t.Fatal("approved creation allowed")
	}
	old := r
	old.Status = "pending"
	if err := domain.Validate(&s, owner, &r, &old); err == nil {
		t.Fatal("self approval allowed")
	}
}
func TestTaskCyclesAndAllocations(t *testing.T) {
	s := fixture(t)
	u := *s.Employee("maya")
	for _, r := range s.Records {
		if r.ID == "t2" {
			old := r
			r.Data["dependsOn"] = []string{"t3"}
			if err := domain.Validate(&s, u, &r, &old); err == nil {
				t.Fatal("dependency cycle allowed")
			}
		}
	}
	for _, r := range s.Records {
		if r.ID == "atlas" {
			old := r
			r.Data["allocations"] = map[string]any{"maya": 61.0}
			if err := domain.Validate(&s, *s.Employee("emma"), &r, &old); err == nil {
				t.Fatal("over-allocation allowed")
			}
		}
	}
}
func TestCommentOwnership(t *testing.T) {
	s := fixture(t)
	r := domain.Record{Kind: "comment", EmployeeID: "jordan", Data: map[string]any{"projectId": "atlas"}}
	maya := *s.Employee("maya")
	if !domain.CanRead(&s, maya, r) {
		t.Fatal("teammates must be able to read discussion")
	}
	if domain.CanEdit(&s, maya, r) {
		t.Fatal("teammates must not edit each other's posts")
	}
}
