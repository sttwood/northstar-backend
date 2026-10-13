package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const DefaultCompany = "default"

type tenantKey struct{}

func Tenant(ctx context.Context) string { value, _ := ctx.Value(tenantKey{}).(string); return value }
func WithTenant(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, tenantKey{}, CompanyID(id))
}
func CompanyID(value string) string {
	if value == "" {
		return DefaultCompany
	}
	return value
}

type Company struct {
	WorkspaceStatus           WorkspaceStatus `json:"workspaceStatus"`
	ID                        string          `json:"id"`
	Name                      string          `json:"name"`
	Slug                      string          `json:"slug"`
	Domain                    string          `json:"domain"`
	Timezone                  string          `json:"timezone"`
	WorkdayHours              float64         `json:"workdayHours"`
	WorkdayStart              string          `json:"workdayStart"`
	WorkdayEnd                string          `json:"workdayEnd"`
	RequireMFA                bool            `json:"requireMfa"`
	ApprovalStages            []string        `json:"approvalStages"`
	RetentionDays             int             `json:"retentionDays"`
	AuditRetentionDays        int             `json:"auditRetentionDays"`
	NotificationRetentionDays int             `json:"notificationRetentionDays"`
	DriveFolderID             string          `json:"driveFolderId,omitempty"`
	Country                   string          `json:"country"`
	TaxYear                   int             `json:"taxYear"`
	CreatedAt                 string          `json:"createdAt"`
}

func DefaultPolicy(id, name, slug string) Company {
	return Company{ID: id, Name: name, Slug: slug, Timezone: "Asia/Bangkok", WorkdayHours: 8, WorkdayStart: "09:00", WorkdayEnd: "18:00", ApprovalStages: []string{"owner"}, RetentionDays: 3650, AuditRetentionDays: 3650, NotificationRetentionDays: 90, Country: "TH", TaxYear: 2026, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}

type Operation struct {
	ID         string         `json:"id"`
	CompanyID  string         `json:"companyId"`
	Kind       string         `json:"kind"`
	EmployeeID string         `json:"employeeId,omitempty"`
	Status     string         `json:"status"`
	CreatedAt  string         `json:"createdAt"`
	ExpiresAt  string         `json:"expiresAt,omitempty"`
	Attempts   int            `json:"attempts"`
	NextAt     string         `json:"nextAt,omitempty"`
	Data       map[string]any `json:"data"`
}
type AccountSecurity struct {
	EmployeeID     string   `json:"employeeId"`
	CompanyID      string   `json:"companyId"`
	TOTPSecret     string   `json:"totpSecret,omitempty"`
	PendingSecret  string   `json:"pendingSecret,omitempty"`
	Enabled        bool     `json:"enabled"`
	LastStep       int64    `json:"lastStep"`
	RecoveryHashes []string `json:"recoveryHashes,omitempty"`
	GoogleSub      string   `json:"googleSub,omitempty"`
}

func (s *State) Company(id string) *Company {
	id = CompanyID(id)
	for i := range s.Companies {
		if s.Companies[i].ID == id {
			return &s.Companies[i]
		}
	}
	return nil
}
func (s *State) Account(id string) *AccountSecurity {
	for i := range s.Security {
		if s.Security[i].EmployeeID == id {
			return &s.Security[i]
		}
	}
	u := s.Employee(id)
	if u == nil {
		return nil
	}
	s.Security = append(s.Security, AccountSecurity{EmployeeID: id, CompanyID: CompanyID(u.CompanyID), LastStep: -1})
	return &s.Security[len(s.Security)-1]
}
func Active(u Employee) bool { return u.Status == "" || u.Status == "active" }
func TenantCompany(s *State) string {
	if len(s.Companies) == 1 {
		return s.Companies[0].ID
	}
	return DefaultCompany
}
func Normalize(s *State) {
	if s.Company(DefaultCompany) == nil {
		s.Companies = append(s.Companies, DefaultPolicy(DefaultCompany, "Northstar", "northstar"))
	}
	for i := range s.Employees {
		s.Employees[i].CompanyID = CompanyID(s.Employees[i].CompanyID)
	}
	for i := range s.Records {
		s.Records[i].CompanyID = CompanyID(s.Records[i].CompanyID)
	}
	for i := range s.Files {
		s.Files[i].CompanyID = CompanyID(s.Files[i].CompanyID)
	}
	for i := range s.Audit {
		s.Audit[i].CompanyID = CompanyID(s.Audit[i].CompanyID)
	}
}
func Scoped(s State, tenant string) State {
	if tenant == "" {
		return s
	}
	tenant = CompanyID(tenant)
	out := State{Employees: []Employee{}, Records: []Record{}, Files: []File{}, Audit: []Audit{}}
	for _, v := range s.Companies {
		if v.ID == tenant {
			out.Companies = append(out.Companies, v)
		}
	}
	for _, v := range s.Employees {
		if CompanyID(v.CompanyID) == tenant {
			out.Employees = append(out.Employees, v)
		}
	}
	for _, v := range s.Records {
		if CompanyID(v.CompanyID) == tenant {
			out.Records = append(out.Records, v)
		}
	}
	for _, v := range s.Files {
		if CompanyID(v.CompanyID) == tenant {
			out.Files = append(out.Files, v)
		}
	}
	for _, v := range s.Audit {
		if CompanyID(v.CompanyID) == tenant {
			out.Audit = append(out.Audit, v)
		}
	}
	for _, v := range s.Sessions {
		if out.Employee(v.EmployeeID) != nil {
			out.Sessions = append(out.Sessions, v)
		}
	}
	for _, v := range s.Operations {
		if CompanyID(v.CompanyID) == tenant {
			out.Operations = append(out.Operations, v)
		}
	}
	for _, v := range s.Security {
		if CompanyID(v.CompanyID) == tenant {
			out.Security = append(out.Security, v)
		}
	}
	return out
}

// Merge only the selected tenant, leaving every other company's records untouched.
func MergeTenant(all *State, next State, tenant string) error {
	if tenant == "" {
		*all = next
		return nil
	}
	tenant = CompanyID(tenant)
	for _, u := range next.Employees {
		if CompanyID(u.CompanyID) != tenant {
			return errors.New("cross-company employee write")
		}
	}
	for _, v := range next.Records {
		if CompanyID(v.CompanyID) != tenant {
			return errors.New("cross-company record write")
		}
	}
	for _, v := range next.Files {
		if CompanyID(v.CompanyID) != tenant {
			return errors.New("cross-company file write")
		}
	}
	keep := State{}
	for _, v := range all.Companies {
		if v.ID != tenant {
			keep.Companies = append(keep.Companies, v)
		}
	}
	for _, v := range all.Employees {
		if CompanyID(v.CompanyID) != tenant {
			keep.Employees = append(keep.Employees, v)
		}
	}
	for _, v := range all.Records {
		if CompanyID(v.CompanyID) != tenant {
			keep.Records = append(keep.Records, v)
		}
	}
	for _, v := range all.Files {
		if CompanyID(v.CompanyID) != tenant {
			keep.Files = append(keep.Files, v)
		}
	}
	for _, v := range all.Audit {
		if CompanyID(v.CompanyID) != tenant {
			keep.Audit = append(keep.Audit, v)
		}
	}
	for _, v := range all.Operations {
		if CompanyID(v.CompanyID) != tenant {
			keep.Operations = append(keep.Operations, v)
		}
	}
	for _, v := range all.Security {
		if CompanyID(v.CompanyID) != tenant {
			keep.Security = append(keep.Security, v)
		}
	}
	for _, v := range all.Sessions {
		if keep.Employee(v.EmployeeID) != nil {
			keep.Sessions = append(keep.Sessions, v)
		}
	}
	keep.Companies = append(keep.Companies, next.Companies...)
	keep.Employees = append(keep.Employees, next.Employees...)
	keep.Records = append(keep.Records, next.Records...)
	keep.Files = append(keep.Files, next.Files...)
	keep.Audit = append(keep.Audit, next.Audit...)
	keep.Sessions = append(keep.Sessions, next.Sessions...)
	keep.Operations = append(keep.Operations, next.Operations...)
	keep.Security = append(keep.Security, next.Security...)
	*all = keep
	return nil
}
func ClonePrivate(s State, out *State) {
	b, _ := json.Marshal(s.Operations)
	_ = json.Unmarshal(b, &out.Operations)
	b, _ = json.Marshal(s.Security)
	_ = json.Unmarshal(b, &out.Security)
}
