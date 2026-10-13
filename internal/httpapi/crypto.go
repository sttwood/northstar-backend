package httpapi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (a *API) seal(plain string) (string, error) {
	if len(a.cfg.MasterKey) != 32 {
		return "", errors.New("ENCRYPTION_KEY must be configured")
	}
	block, err := aes.NewCipher(a.cfg.MasterKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), []byte("northstar-v1"))), nil
}
func (a *API) open(value string) (string, error) {
	if len(a.cfg.MasterKey) != 32 {
		return "", errors.New("encryption key unavailable")
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, _ := aes.NewCipher(a.cfg.MasterKey)
	gcm, _ := cipher.NewGCM(block)
	if len(b) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted value")
	}
	plain, err := gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], []byte("northstar-v1"))
	return string(plain), err
}
func newTOTP() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}
func totpAt(secret string, step int64) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return ""
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	h := hmac.New(sha1.New, key)
	_, _ = h.Write(counter[:])
	digest := h.Sum(nil)
	offset := digest[len(digest)-1] & 15
	value := (binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", value)
}
func verifyTOTP(secret, code string, last int64, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	step := now.Unix() / 30
	for _, value := range []int64{step, step - 1, step + 1} {
		if value > last && subtle.ConstantTimeCompare([]byte(totpAt(secret, value)), []byte(code)) == 1 {
			return value, true
		}
	}
	return 0, false
}
