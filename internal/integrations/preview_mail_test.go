package integrations

import (
	"context"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrivateLocalEmailPreview(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mail")
	p := PreviewMail{Dir: dir}
	for i := 0; i < 2; i++ {
		if err := p.Send(t.Context(), "employee@example.test", "Invitation\r\nBcc: attacker@example.test", "ยินดีต้อนรับ\nhttp://localhost:8080/signup?token=private"); err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("preview files: %v %v", files, err)
	}
	for _, entry := range files {
		path := filepath.Join(dir, entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		m, err := mail.ReadMessage(strings.NewReader(string(b)))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(m.Body)
		if m.Header.Get("X-Nexilar-Delivery") != "local-preview-only" || m.Header.Get("Bcc") != "" || !strings.Contains(string(body), "ยินดีต้อนรับ") || !strings.Contains(string(body), "token=private") {
			t.Fatal("preview metadata/body or header injection protection failed")
		}
		if runtime.GOOS != "windows" {
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0600 {
				t.Fatal("preview file is not private")
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = p.Send(ctx, "employee@example.test", "Cancel", "body"); err == nil {
		t.Fatal("canceled delivery accepted")
	}
	files, _ = os.ReadDir(dir)
	if len(files) != 2 {
		t.Fatal("canceled message created a file")
	}
}
