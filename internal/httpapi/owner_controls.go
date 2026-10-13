package httpapi

import (
	"net/http"
	"northstar/backend/internal/domain"
	"northstar/backend/internal/store"
	"strings"
	"unicode/utf8"
)

func (a *API) ownerControlRoutes(mux *http.ServeMux) {
	for path, fn := range map[string]func(http.ResponseWriter, *http.Request) error{
		"GET /api/workspace/status":               a.workspaceStatus,
		"PATCH /api/workspace/status":             a.workspaceStatusSave,
		"GET /api/points/{employee}":              a.employeePoints,
		"POST /api/points/{employee}/adjustments": a.adjustPoints,
		"GET /api/points/{employee}/adjustments":  a.pointHistory,
	} {
		mux.Handle(path, a.auth(endpoint(fn)))
	}
}
func (a *API) workspaceStatus(w http.ResponseWriter, r *http.Request) error {
	c, err := a.companyByID(r.Context(), current(r).CompanyID)
	if err != nil {
		return err
	}
	respond(w, 200, c.WorkspaceStatus.Normalized())
	return nil
}
func (a *API) workspaceStatusSave(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "only the owner may change app status")
	}
	var v domain.WorkspaceStatus
	if err := decode(w, r, &v); err != nil {
		return err
	}
	v.Message = strings.TrimSpace(v.Message)
	v.MessageTH = strings.TrimSpace(v.MessageTH)
	if err := domain.ValidateWorkspaceStatus(v); err != nil {
		return fail(400, err.Error())
	}
	v.UpdatedAt = stamp()
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		if s.Employee(u.ID).Role != "owner" {
			return fail(403, "only the owner may change app status")
		}
		c := s.Company(u.CompanyID)
		if c == nil {
			return fail(404, "company not found")
		}
		c.WorkspaceStatus = v
		audit(s, u, "changed app status to "+v.Mode, c.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 200, v)
	return nil
}
func pointsReader(u domain.Employee, target string) error {
	if u.ID != target && u.Role != "owner" {
		return fail(403, "you may only view your own points")
	}
	return nil
}
func (a *API) employeePoints(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	target := r.PathValue("employee")
	if err := pointsReader(u, target); err != nil {
		return err
	}
	// Wallet queries always bind the authenticated tenant; an unknown ID exposes no cross-company data.
	u.ID = target
	v, err := a.store.(store.ShopRepository).ShopWallet(r.Context(), u)
	if err != nil {
		return err
	}
	respond(w, 200, v)
	return nil
}
func (a *API) pointHistory(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	target := r.PathValue("employee")
	if err := pointsReader(u, target); err != nil {
		return err
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > 100 {
		return fail(400, "invalid points filter")
	}
	v, err := a.store.(store.ShopRepository).PointAdjustments(r.Context(), u, target, cursor, communityLimit(r))
	if err != nil {
		return err
	}
	respond(w, 200, v)
	return nil
}
func (a *API) adjustPoints(w http.ResponseWriter, r *http.Request) error {
	u := current(r)
	if u.Role != "owner" {
		return fail(403, "only the owner may adjust points")
	}
	var body struct {
		Mode            string `json:"mode"`
		Amount          int    `json:"amount"`
		Reason          string `json:"reason"`
		Key             string `json:"key"`
		ExpectedBalance *int   `json:"expectedBalance"`
	}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if !domain.Contains([]string{"add", "subtract", "set"}, body.Mode) || body.Amount < 0 || body.Amount > 1000000 || (body.Mode != "set" && body.Amount == 0) || body.Reason == "" || utf8.RuneCountInString(body.Reason) > 500 || len(body.Key) < 16 || len(body.Key) > 100 || strings.TrimSpace(body.Key) != body.Key {
		return fail(400, "enter a valid points action, amount, reason and adjustment key")
	}
	if body.Mode == "set" && body.ExpectedBalance == nil {
		return fail(400, "reload the current balance before setting points")
	}
	target := r.PathValue("employee")
	var result domain.Operation
	err := a.store.Update(r.Context(), func(s *domain.State) error {
		if err := communityActor(s, u); err != nil {
			return err
		}
		if s.Employee(u.ID).Role != "owner" {
			return fail(403, "only the owner may adjust points")
		}
		e := s.Employee(target)
		if e == nil || !domain.Active(*e) {
			return fail(404, "active employee not found")
		}
		for _, op := range s.Operations {
			if op.Kind == "points-adjustment" && op.Data["key"] == body.Key {
				if op.EmployeeID != target || op.Data["mode"] != body.Mode || op.Data["amount"] != float64(body.Amount) || op.Data["reason"] != body.Reason || op.Data["actorId"] != u.ID {
					return fail(409, "adjustment key already used")
				}
				result = op
				return nil
			}
		}
		wallet := domain.CommunityWallet(s, target)
		if body.ExpectedBalance != nil && wallet.Balance != *body.ExpectedBalance {
			return fail(409, "points balance changed; reload and try again")
		}
		delta := body.Amount
		if body.Mode == "subtract" {
			delta = -delta
		}
		if body.Mode == "set" {
			delta = body.Amount - wallet.Balance
		}
		next := wallet.Balance + delta
		if next < 0 || next > 1000000000 {
			return fail(409, "points balance must be between 0 and 1000000000")
		}
		result = domain.Operation{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), EmployeeID: target, Kind: "points-adjustment", Status: "applied", CreatedAt: stamp(), Data: map[string]any{"mode": body.Mode, "amount": float64(body.Amount), "delta": float64(delta), "before": float64(wallet.Balance), "after": float64(next), "reason": body.Reason, "key": body.Key, "actorId": u.ID}}
		s.Operations = append(s.Operations, result)
		s.Operations = append(s.Operations, domain.Operation{ID: id(), CompanyID: domain.CompanyID(u.CompanyID), EmployeeID: target, Kind: "notification", Status: "unread", CreatedAt: stamp(), Data: map[string]any{"title": "Your points balance was adjusted", "kind": "points-adjustment"}})
		audit(s, u, "adjusted employee points", result.ID)
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, 200, result)
	return nil
}

func (a *API) checkWorkspaceAccess(w http.ResponseWriter, r *http.Request, u domain.Employee) bool {
	if u.Role == "owner" {
		return true
	}
	path := r.URL.Path
	if path == "/api/workspace/status" || path == "/api/auth/logout" || path == "/api/auth/password" || strings.HasPrefix(path, "/api/security/mfa") {
		return true
	}
	c, err := a.companyByID(r.Context(), u.CompanyID)
	if err != nil {
		writeError(w, err)
		return false
	}
	status := c.WorkspaceStatus.Normalized()
	blocked := status.Mode == "maintenance" || (status.Mode == "read-only" && r.Method != "GET" && r.Method != "HEAD")
	if !blocked {
		return true
	}
	w.Header().Set("Retry-After", "30")
	respond(w, 503, map[string]any{"error": "Workspace access is temporarily restricted.", "workspaceStatus": status})
	return false
}
