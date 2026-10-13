package integrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
)

// PreviewMail captures local messages. It never connects to a mail provider.
type PreviewMail struct{ Dir string }

func (p PreviewMail) Send(ctx context.Context, to, subject, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(p.Dir, 0700); err != nil {
		return err
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return err
	}
	message := "From: Nexilar local preview <preview@localhost>\r\nTo: " + recipient.String() + "\r\nSubject: " + mime.QEncoding.Encode("UTF-8", "[LOCAL PREVIEW] "+strings.NewReplacer("\r", " ", "\n", " ").Replace(subject)) + "\r\nX-Nexilar-Delivery: local-preview-only\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	file, err := os.OpenFile(filepath.Join(p.Dir, hex.EncodeToString(random)+".eml"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(message)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(file.Name())
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(file.Name())
	}
	return closeErr
}
