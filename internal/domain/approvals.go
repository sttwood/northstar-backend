package domain

import (
	"encoding/json"
	"errors"
	"time"
)

type ApprovalStep struct {
	Stage     string   `json:"stage"`
	Approvers []string `json:"approvers"`
	Status    string   `json:"status"`
	DecidedBy string   `json:"decidedBy,omitempty"`
	DecidedAt string   `json:"decidedAt,omitempty"`
	Note      string   `json:"note,omitempty"`
}

func RequestKind(kind string) bool {
	return Contains([]string{"leave", "overtime", "document", "request"}, kind)
}
func ApprovalSteps(r Record) []ApprovalStep {
	b, _ := json.Marshal(r.Data["approvalSteps"])
	var out []ApprovalStep
	_ = json.Unmarshal(b, &out)
	return out
}
func CanApprove(s *State, u Employee, r Record) bool {
	return u.Role == "owner" && RequestKind(r.Kind) && r.Status == "pending" && r.EmployeeID != u.ID && Active(u) && CompanyID(u.CompanyID) == CompanyID(r.CompanyID)
}
func InitializeApproval(s *State, r *Record) error {
	if !RequestKind(r.Kind) {
		return nil
	}
	if s.Company(r.CompanyID) == nil {
		return errors.New("company policy missing")
	}
	approvers := []string{}
	for _, u := range s.Employees {
		if u.Role == "owner" && Active(u) && u.ID != r.EmployeeID && CompanyID(u.CompanyID) == CompanyID(r.CompanyID) {
			approvers = append(approvers, u.ID)
		}
	}
	if len(approvers) == 0 {
		return errors.New("another active owner is required to review this request")
	}
	r.Data["approvalSteps"] = []ApprovalStep{{Stage: "owner", Approvers: approvers, Status: "pending"}}
	r.Data["approvalIndex"] = float64(0)
	r.Data["currentApproverIds"] = approvers
	r.Data["approvalPolicy"] = "owner-only-v1"
	delete(r.Data, "decidedBy")
	delete(r.Data, "decidedAt")
	return nil
}
func PreserveApproval(r *Record, old Record) {
	for _, key := range []string{"approvalSteps", "approvalIndex", "currentApproverIds", "decidedBy", "decidedAt", "approvalPolicy"} {
		delete(r.Data, key)
		if value, ok := old.Data[key]; ok {
			r.Data[key] = value
		}
	}
}
func AdvanceApproval(s *State, u Employee, r *Record, old Record) error {
	if r.Status == old.Status || !RequestKind(r.Kind) {
		return nil
	}
	if !CanApprove(s, u, old) {
		return errors.New("only the current approval stage can decide this request")
	}
	if r.Status != "approved" && r.Status != "rejected" {
		return errors.New("invalid approval decision")
	}
	steps := ApprovalSteps(old)
	if len(steps) == 0 {
		r.Data["currentApproverIds"] = []string{}
		r.Data["approvalPolicy"] = "owner-only-v1"
		r.Data["decidedBy"] = u.ID
		r.Data["decidedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		return nil
	}
	index := int(Number(old, "approvalIndex"))
	if index < 0 || index >= len(steps) {
		return errors.New("invalid approval state")
	}
	steps[index].Status = r.Status
	steps[index].DecidedBy = u.ID
	steps[index].DecidedAt = time.Now().UTC().Format(time.RFC3339Nano)
	steps[index].Note = Text(*r, "decisionNote")
	// Owner-only policy also resolves requests created under the previous reporting-line policy.
	for i := index + 1; i < len(steps); i++ {
		if steps[i].Status == "pending" {
			steps[i].Status = "superseded"
		}
	}
	r.Data["currentApproverIds"] = []string{}
	r.Data["decidedBy"] = u.ID
	r.Data["decidedAt"] = steps[index].DecidedAt
	r.Data["approvalPolicy"] = "owner-only-v1"
	r.Data["approvalSteps"] = steps
	return nil
}
func ValidateManager(s *State, employeeID, managerID string) error {
	if managerID == "" {
		return nil
	}
	seen := map[string]bool{employeeID: true}
	id := managerID
	for id != "" {
		if seen[id] {
			return errors.New("reporting lines cannot contain cycles")
		}
		seen[id] = true
		manager := s.Employee(id)
		if manager == nil || !Active(*manager) {
			return errors.New("manager must be an active employee in this company")
		}
		id = manager.ManagerID
	}
	return nil
}
