package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type WorkspaceStatus struct {
	Mode       string `json:"mode"`
	Message    string `json:"message"`
	MessageTH  string `json:"messageTh"`
	ExpectedAt string `json:"expectedAt,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}

func (v WorkspaceStatus) Normalized() WorkspaceStatus {
	if v.Mode == "" {
		v.Mode = "operational"
	}
	return v
}
func ValidateWorkspaceStatus(v WorkspaceStatus) error {
	if !Contains([]string{"operational", "degraded", "read-only", "maintenance"}, v.Mode) {
		return errors.New("choose a valid app status")
	}
	if utf8.RuneCountInString(v.Message) > 500 || utf8.RuneCountInString(v.MessageTH) > 500 {
		return errors.New("status messages must be 500 characters or fewer")
	}
	if v.Mode != "operational" && strings.TrimSpace(v.Message) == "" && strings.TrimSpace(v.MessageTH) == "" {
		return errors.New("enter an English or Thai status message")
	}
	if v.ExpectedAt != "" {
		if _, err := time.Parse(time.RFC3339, v.ExpectedAt); err != nil {
			return errors.New("enter a valid expected recovery time")
		}
	}
	return nil
}
