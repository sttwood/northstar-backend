package bootstrap

import (
	"golang.org/x/crypto/bcrypt"
	"northstar/backend/internal/domain"
	"testing"
)

func TestFreshInstallHasOneSuperadminAndNoSeedData(t *testing.T) {
	s := domain.State{}
	if err := Owner(&s, "ADMIN@example.test", "SyntheticOwnerSecret2026!", "Superadmin", "Nexilar", "nexilar"); err != nil {
		t.Fatal(err)
	}
	if len(s.Employees) != 1 || s.Employees[0].Role != "owner" || s.Employees[0].Name != "Superadmin" || s.Employees[0].CompanyID != "default" || s.Companies[0].Slug != "nexilar" || len(s.Records)+len(s.Files)+len(s.Operations)+len(s.Sessions) != 0 {
		t.Fatal("unexpected bootstrap data")
	}
	if bcrypt.CompareHashAndPassword([]byte(s.Employees[0].PasswordHash), []byte("SyntheticOwnerSecret2026!")) != nil {
		t.Fatal("invalid password hash")
	}
	hash := s.Employees[0].PasswordHash
	if err := Owner(&s, "other@example.test", "DifferentSecret2026!", "Other", "Other", "other"); err != nil || s.Employees[0].PasswordHash != hash || s.Companies[0].Name != "Nexilar" {
		t.Fatal("restart replaced installation")
	}
}
func TestOwnerRejectsInvalidInitialization(t *testing.T) {
	for _, v := range []struct{ email, password, slug string }{{"invalid", "SyntheticOwnerSecret2026!", "nexilar"}, {"owner@example.test", "short", "nexilar"}, {"owner@example.test", "change-this-before-deployment", "nexilar"}, {"owner@example.test", "SyntheticOwnerSecret2026!", "Invalid Slug"}} {
		s := domain.State{}
		if Owner(&s, v.email, v.password, "", "", v.slug) == nil || len(s.Employees) != 0 {
			t.Fatal("invalid bootstrap was accepted")
		}
	}
}
