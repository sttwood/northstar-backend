package integrations

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type BlobStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
	Name() string
}
type Local struct{ Dir string }

func (l Local) Name() string { return "filesystem" }
func safeKey(key string) bool {
	return key != "" && !strings.Contains(key, "..") && !strings.ContainsAny(key, "\\:") && !strings.HasPrefix(key, "/")
}
func (l Local) Put(ctx context.Context, key string, b []byte) error {
	if !safeKey(key) {
		return errors.New("invalid storage key")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path := filepath.Join(l.Dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(b)
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	return closeErr
}
func (l Local) Get(ctx context.Context, key string) ([]byte, error) {
	if !safeKey(key) {
		return nil, errors.New("invalid storage key")
	}
	return os.ReadFile(filepath.Join(l.Dir, filepath.FromSlash(key)))
}
func (l Local) Delete(ctx context.Context, key string) error {
	if !safeKey(key) {
		return errors.New("invalid storage key")
	}
	err := os.Remove(filepath.Join(l.Dir, filepath.FromSlash(key)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type S3 struct {
	client *s3.Client
	bucket string
}

func NewS3(ctx context.Context, bucket, region, endpoint string) (*S3, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})
	if _, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err != nil {
		return nil, fmt.Errorf("S3 bucket unavailable: %w", err)
	}
	return &S3{client: client, bucket: bucket}, nil
}
func (s *S3) Name() string { return "s3" }
func (s *S3) Put(ctx context.Context, key string, b []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(b), ContentType: aws.String("application/octet-stream"), IfNoneMatch: aws.String("*")})
	return err
}
func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	v, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	defer v.Body.Close()
	b, err := io.ReadAll(io.LimitReader(v.Body, 10*1024*1024+1))
	if len(b) > 10*1024*1024 {
		return nil, errors.New("stored file exceeds upload limit")
	}
	return b, err
}
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}

type Scanner interface {
	Scan(context.Context, []byte) error
	Name() string
}
type ClamAV struct{ Address string }

func (c ClamAV) Name() string { return "clamav" }
func (c ClamAV) Scan(ctx context.Context, b []byte) error {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return errors.New("file scanner unavailable")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err = io.WriteString(conn, "zINSTREAM\x00"); err != nil {
		return err
	}
	for start := 0; start < len(b); start += 65536 {
		end := start + 65536
		if end > len(b) {
			end = len(b)
		}
		if err = binary.Write(conn, binary.BigEndian, uint32(end-start)); err != nil {
			return err
		}
		if _, err = conn.Write(b[start:end]); err != nil {
			return err
		}
	}
	if err = binary.Write(conn, binary.BigEndian, uint32(0)); err != nil {
		return err
	}
	reply, err := bufio.NewReader(io.LimitReader(conn, 4096)).ReadString(0)
	if err != nil {
		return errors.New("file scanner did not return a verdict")
	}
	verdict := strings.TrimRight(reply, "\x00\r\n")
	if !strings.HasSuffix(verdict, ": OK") {
		return errors.New("file rejected by scanner")
	}
	return nil
}
func Digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
