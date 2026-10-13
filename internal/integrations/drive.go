package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/oauth2/google"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

type Drive struct {
	Client    *http.Client
	BaseURL   string
	UploadURL string
}
type DriveFile struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	MimeType     string            `json:"mimeType"`
	ModifiedTime string            `json:"modifiedTime"`
	Properties   map[string]string `json:"appProperties"`
	Trashed      bool              `json:"trashed"`
}

func NewDrive(ctx context.Context, credentials []byte) (*Drive, error) {
	cfg, err := google.JWTConfigFromJSON(credentials, "https://www.googleapis.com/auth/drive")
	if err != nil {
		return nil, err
	}
	return &Drive{Client: cfg.Client(ctx), BaseURL: "https://www.googleapis.com/drive/v3", UploadURL: "https://www.googleapis.com/upload/drive/v3"}, nil
}
func (d *Drive) request(ctx context.Context, method, path string, body io.Reader, contentType string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response, err := d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	b, err := io.ReadAll(io.LimitReader(response.Body, 10*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 10*1024*1024 {
		return nil, errors.New("Drive file exceeds 10 MB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Google Drive returned HTTP %d", response.StatusCode)
	}
	return b, nil
}
func escapeQuery(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "\\'")
}
func (d *Drive) List(ctx context.Context, folder, company, cursor string) ([]DriveFile, string, error) {
	q := url.Values{"q": {"'" + escapeQuery(folder) + "' in parents and trashed=false and appProperties has { key='northstarCompanyId' and value='" + escapeQuery(company) + "' }"}, "supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"}, "fields": {"nextPageToken,files(id,name,mimeType,modifiedTime,appProperties,trashed)"}, "pageSize": {"100"}}
	if cursor != "" {
		q.Set("pageToken", cursor)
	}
	b, err := d.request(ctx, "GET", d.BaseURL+"/files?"+q.Encode(), nil, "")
	if err != nil {
		return nil, "", err
	}
	var out struct {
		Files []DriveFile `json:"files"`
		Next  string      `json:"nextPageToken"`
	}
	err = json.Unmarshal(b, &out)
	return out.Files, out.Next, err
}
func (d *Drive) Find(ctx context.Context, folder, company, file string) (string, error) {
	q := url.Values{"q": {"'" + escapeQuery(folder) + "' in parents and trashed=false and appProperties has { key='northstarCompanyId' and value='" + escapeQuery(company) + "' } and appProperties has { key='northstarFileId' and value='" + escapeQuery(file) + "' }"}, "supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"}, "fields": {"files(id)"}, "pageSize": {"1"}}
	b, err := d.request(ctx, "GET", d.BaseURL+"/files?"+q.Encode(), nil, "")
	if err != nil {
		return "", err
	}
	var out struct {
		Files []DriveFile `json:"files"`
	}
	if err = json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	if len(out.Files) > 0 {
		return out.Files[0].ID, nil
	}
	return "", nil
}
func (d *Drive) Export(ctx context.Context, folder, company, file, employee, name, category string, month, year int, b []byte) (string, error) {
	existing, err := d.Find(ctx, folder, company, file)
	if err != nil || existing != "" {
		return existing, err
	}
	metadata := map[string]any{"name": name, "parents": []string{folder}, "appProperties": map[string]string{"northstarCompanyId": company, "northstarFileId": file, "employeeId": employee, "category": category, "month": fmt.Sprint(month), "year": fmt.Sprint(year), "sha256": Digest(b)}}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
	if err != nil {
		return "", err
	}
	if err = json.NewEncoder(part).Encode(metadata); err != nil {
		return "", err
	}
	part, err = writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/pdf"}})
	if err != nil {
		return "", err
	}
	if _, err = part.Write(b); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	out, err := d.request(ctx, "POST", d.UploadURL+"/files?uploadType=multipart&supportsAllDrives=true&fields=id", &buffer, "multipart/related; boundary="+writer.Boundary())
	if err != nil {
		return "", err
	}
	var result DriveFile
	err = json.Unmarshal(out, &result)
	return result.ID, err
}
func (d *Drive) Download(ctx context.Context, id string) ([]byte, error) {
	return d.request(ctx, "GET", d.BaseURL+"/files/"+url.PathEscape(id)+"?alt=media&supportsAllDrives=true", nil, "")
}
func (d *Drive) Delete(ctx context.Context, id string) error {
	_, err := d.request(ctx, "DELETE", d.BaseURL+"/files/"+url.PathEscape(id)+"?supportsAllDrives=true", nil, "")
	return err
}
