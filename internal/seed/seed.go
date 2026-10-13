package seed

import (
	"embed"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"northstar/backend/internal/domain"
	"os"
	"path/filepath"
)

//go:embed data.json
var data []byte

//go:embed payslips/*.pdf
var payslips embed.FS

// InstallFiles restores only embedded synthetic files referenced by this state.
// Existing uploads are never overwritten, and production without seeding never calls it.
func InstallFiles(directory string, state domain.State) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	for _, file := range state.Files {
		bytes, err := payslips.ReadFile("payslips/" + file.ID + ".pdf")
		if err != nil {
			continue
		}
		if int64(len(bytes)) != file.Size {
			return fmt.Errorf("sample payslip size mismatch: %s", file.ID)
		}
		target, err := os.OpenFile(filepath.Join(directory, file.ID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		_, writeErr := target.Write(bytes)
		closeErr := target.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

const DemoPassword = "NorthstarDemo2026!"

// MergeFixtures adds the current synthetic fixtures to an existing demo workspace.
// It preserves records, employee edits, files, and any configured environments.
func MergeFixtures(state *domain.State) error {
	var sample domain.State
	if err := json.Unmarshal(data, &sample); err != nil {
		return err
	}
	for _, employee := range sample.Employees {
		existing := state.Employee(employee.ID)
		if existing == nil || existing.Email != employee.Email {
			return fmt.Errorf("fixture upgrade requires the original demo accounts; mismatch: %s", employee.ID)
		}
	}
	for _, file := range sample.Files {
		found := false
		for _, existing := range state.Files {
			if existing.ID == file.ID {
				found = true
				break
			}
		}
		if !found {
			state.Files = append(state.Files, file)
		}
	}
	for _, project := range sample.Records {
		if project.Kind != "project" {
			continue
		}
		for index := range state.Records {
			existing := &state.Records[index]
			if existing.Kind == "project" && existing.ID == project.ID && existing.Title == project.Title && existing.Data["environments"] == nil {
				existing.Data["environments"] = project.Data["environments"]
			}
		}
	}
	return nil
}

func Demo() (domain.State, error) {
	var state domain.State
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(DemoPassword), bcrypt.DefaultCost)
	if err != nil {
		return state, err
	}
	for i := range state.Employees {
		state.Employees[i].PasswordHash = string(hash)
	}
	return state, nil
}
