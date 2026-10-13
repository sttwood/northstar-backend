package bootstrap

import (
	"errors"
	"golang.org/x/crypto/bcrypt"
	"net/mail"
	"northstar/backend/internal/domain"
	"regexp"
	"strings"
)

// Owner initializes only an empty installation. Environment changes never overwrite existing accounts.
func Owner(s *domain.State, email, password, name, companyName, slug string) error {
	if len(s.Employees) > 0 {
		return nil
	}
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(password) < 12 || len(password) > 72 || password == "change-this-before-deployment" {
		return errors.New("set ADMIN_EMAIL and a unique ADMIN_PASSWORD of 12-72 bytes before first boot")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,49}$`).MatchString(slug) {
		return errors.New("COMPANY_SLUG must contain 3-50 lowercase letters, digits or hyphens")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Superadmin"
	}
	companyName = strings.TrimSpace(companyName)
	if companyName == "" {
		companyName = "Nexilar"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	company := s.Company(domain.DefaultCompany)
	if company == nil {
		s.Companies = append(s.Companies, domain.DefaultPolicy(domain.DefaultCompany, companyName, slug))
		company = s.Company(domain.DefaultCompany)
	}
	company.Name = companyName
	company.Slug = slug
	company.ApprovalStages = []string{"owner"}
	company.WorkspaceStatus = domain.WorkspaceStatus{Mode: "operational"}
	s.Employees = []domain.Employee{{ID: "owner", CompanyID: domain.DefaultCompany, Name: name, Email: email, Role: "owner", Status: "active", Team: "Leadership", Title: "Superadmin", Avatar: "SA", LeaveAllowance: 20, PasswordHash: string(hash)}}
	return nil
}
