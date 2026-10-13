package integrations

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClamAVStreamAndFailClosed(t *testing.T) {
	for _, verdict := range []string{"stream: OK\x00", "stream: Eicar FOUND\x00", "stream: ERROR\x00", ""} {
		t.Run(verdict, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			received := make(chan []byte, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				reader := bufio.NewReader(conn)
				command, _ := reader.ReadString(0)
				if command != "zINSTREAM\x00" {
					received <- nil
					return
				}
				var data []byte
				for {
					var n uint32
					if binary.Read(reader, binary.BigEndian, &n) != nil {
						break
					}
					if n == 0 {
						break
					}
					chunk := make([]byte, n)
					if _, err = io.ReadFull(reader, chunk); err != nil {
						break
					}
					data = append(data, chunk...)
				}
				received <- data
				for _, c := range []byte(verdict) {
					conn.Write([]byte{c})
				}
			}()
			payload := bytes.Repeat([]byte("scan-me"), 20000)
			err = (ClamAV{Address: listener.Addr().String()}).Scan(context.Background(), payload)
			if (err == nil) != (verdict == "stream: OK\x00") {
				t.Fatalf("verdict accepted incorrectly: %v", err)
			}
			if !bytes.Equal(<-received, payload) {
				t.Fatal("stream truncated")
			}
		})
	}
}

func TestS3PrivateObjectRoundTrip(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	payload := []byte("%PDF-private-payroll")
	stored := []byte(nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("request not signed")
		}
		if r.Method == "HEAD" {
			w.WriteHeader(200)
			return
		}
		if r.URL.Path != "/private-bucket/company/file" {
			t.Errorf("unexpected key: %s", r.URL.Path)
		}
		switch r.Method {
		case "PUT":
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("overwrite allowed")
			}
			stored, _ = io.ReadAll(r.Body)
			w.WriteHeader(200)
		case "GET":
			w.Write(stored)
		case "DELETE":
			stored = nil
			w.WriteHeader(204)
		default:
			t.Error("unexpected method")
		}
	}))
	defer server.Close()
	blobs, err := NewS3(context.Background(), "private-bucket", "ap-southeast-1", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err = blobs.Put(context.Background(), "company/file", payload); err != nil {
		t.Fatal(err)
	}
	actual, err := blobs.Get(context.Background(), "company/file")
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatalf("private bytes lost: %v", err)
	}
	if err = blobs.Delete(context.Background(), "company/file"); err != nil || stored != nil {
		t.Fatalf("delete failed: %v", err)
	}
}

func TestDriveTaggedFolderExportDownloadAndDelete(t *testing.T) {
	exists := false
	payload := []byte("%PDF-payroll")
	uploads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("supportsAllDrives") != "true" {
			t.Error("Shared Drive option absent")
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/files":
			q := r.URL.Query().Get("q")
			if !strings.Contains(q, "'private-folder' in parents") || !strings.Contains(q, "northstarCompanyId") {
				t.Error("unscoped Drive query")
			}
			if strings.Contains(q, "northstarFileId") && exists {
				io.WriteString(w, `{"files":[{"id":"remote-id"}]}`)
			} else {
				io.WriteString(w, `{"files":[]}`)
			}
		case r.Method == "POST":
			uploads++
			_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil {
				t.Error(err)
				return
			}
			reader := multipart.NewReader(r.Body, params["boundary"])
			part, _ := reader.NextPart()
			var metadata struct {
				Parents    []string
				Properties map[string]string `json:"appProperties"`
			}
			json.NewDecoder(part).Decode(&metadata)
			if len(metadata.Parents) != 1 || metadata.Parents[0] != "private-folder" || metadata.Properties["northstarCompanyId"] != "tenant" || metadata.Properties["employeeId"] != "employee" || metadata.Properties["sha256"] != Digest(payload) {
				t.Error("private metadata incorrect")
			}
			part, _ = reader.NextPart()
			b, _ := io.ReadAll(part)
			if !bytes.Equal(b, payload) {
				t.Error("export bytes changed")
			}
			exists = true
			io.WriteString(w, `{"id":"remote-id"}`)
		case r.Method == "GET" && r.URL.Path == "/files/remote-id":
			w.Write(payload)
		case r.Method == "DELETE" && r.URL.Path == "/files/remote-id":
			exists = false
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected Drive call: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	drive := Drive{Client: server.Client(), BaseURL: server.URL, UploadURL: server.URL}
	for range 2 {
		id, err := drive.Export(context.Background(), "private-folder", "tenant", "file", "employee", "payroll.pdf", "payslip", 10, 2026, payload)
		if err != nil || id != "remote-id" {
			t.Fatalf("export failed: %v", err)
		}
	}
	if uploads != 1 {
		t.Fatal("retry duplicated upload")
	}
	b, err := drive.Download(context.Background(), "remote-id")
	if err != nil || !bytes.Equal(b, payload) {
		t.Fatal("download failed")
	}
	if err = drive.Delete(context.Background(), "remote-id"); err != nil || exists {
		t.Fatal("permanent deletion failed")
	}
}

func TestSMTPVerifiedTLSDelivery(t *testing.T) {
	certificateServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer certificateServer.Close()
	cert := certificateServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certificateServer.Certificate())
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	result := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(conn)
		io.WriteString(conn, "220 test ESMTP\r\n")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				result <- ""
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				io.WriteString(conn, "250-test\r\n250 8BITMIME\r\n")
			case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				io.WriteString(conn, "250 OK\r\n")
			case strings.HasPrefix(line, "DATA"):
				io.WriteString(conn, "354 end with dot\r\n")
				body := ""
				for {
					line, err = r.ReadString('\n')
					if err != nil || line == ".\r\n" {
						break
					}
					body += line
				}
				result <- body
				io.WriteString(conn, "250 queued\r\n")
			case strings.HasPrefix(line, "QUIT"):
				io.WriteString(conn, "221 bye\r\n")
				return
			default:
				io.WriteString(conn, "500 unsupported\r\n")
			}
		}
	}()
	mailer := SMTP{Address: listener.Addr().String(), From: "Northstar <noreply@example.test>", ImplicitTLS: true, RootCAs: roots}
	if err = mailer.Send(context.Background(), "employee@example.test", "คำขอ approved", "Private invitation\nhttps://example.test/signup"); err != nil {
		t.Fatal(err)
	}
	body := <-result
	if !strings.Contains(body, "Private invitation\r\nhttps://example.test/signup") || !strings.Contains(body, "charset=UTF-8") {
		t.Fatal("email payload lost")
	}
}
