package seed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPayslipsInstalledWithoutOverwritingUploads(t *testing.T) {
	state, err := Demo()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Files) != 24 {
		t.Fatalf("expected 24 sample payslips, got %d", len(state.Files))
	}
	directory := t.TempDir()
	if err := InstallFiles(directory, state); err != nil {
		t.Fatal(err)
	}
	for _, file := range state.Files {
		data, err := os.ReadFile(filepath.Join(directory, file.ID))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != int(file.Size) || string(data[:5]) != "%PDF-" {
			t.Fatalf("invalid payslip %s", file.ID)
		}
	}
	target := filepath.Join(directory, state.Files[0].ID)
	if err := os.WriteFile(target, []byte("existing upload"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallFiles(directory, state); err != nil {
		t.Fatal(err)
	}
	existing, err := os.ReadFile(target)
	if err != nil || string(existing) != "existing upload" {
		t.Fatal("existing upload was overwritten")
	}
}

func TestFixtureUpgradeIsAdditiveAndLimitedToDemoAccounts(t *testing.T) {
	state, err := Demo()
	if err != nil {
		t.Fatal(err)
	}
	state.Files = state.Files[:1]
	for index := range state.Records {
		if state.Records[index].ID == "atlas" {
			state.Records[index].Data["environments"] = map[string]any{"custom": "preserved"}
		}
	}
	if err := MergeFixtures(&state); err != nil {
		t.Fatal(err)
	}
	if err := MergeFixtures(&state); err != nil {
		t.Fatal(err)
	}
	if len(state.Files) != 24 {
		t.Fatal("fixture upgrade is not idempotent")
	}
	for _, project := range state.Records {
		if project.ID == "atlas" && project.Data["environments"].(map[string]any)["custom"] != "preserved" {
			t.Fatal("existing environment data was overwritten")
		}
	}
	state.Employees[0].Email = "real@company.example"
	state.Files = state.Files[:1]
	if err := MergeFixtures(&state); err == nil || len(state.Files) != 1 {
		t.Fatal("fixture upgrade modified a workspace without the original demo accounts")
	}
}
