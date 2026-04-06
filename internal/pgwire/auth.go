package pgwire

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
)

// MD5Auth implements PostgreSQL MD5 authentication.
// Hash = "md5" + md5(md5(password + username) + salt)
type MD5Auth struct {
	Users map[string]string // username → password
}

// GenerateSalt returns 4 random bytes for MD5 auth challenge.
func (a *MD5Auth) GenerateSalt() ([4]byte, error) {
	var salt [4]byte
	_, err := rand.Read(salt[:])
	return salt, err
}

// Verify checks the client's MD5 password response.
func (a *MD5Auth) Verify(username, response string, salt [4]byte) bool {
	password, ok := a.Users[username]
	if !ok {
		return false
	}

	expected := ComputeMD5(username, password, salt)
	return response == expected
}

// ComputeMD5 computes the PostgreSQL MD5 password hash.
func ComputeMD5(username, password string, salt [4]byte) string {
	// Step 1: md5(password + username)
	inner := md5.Sum([]byte(password + username))
	innerHex := hex.EncodeToString(inner[:])

	// Step 2: md5(step1 + salt)
	outer := md5.Sum(append([]byte(innerHex), salt[:]...))
	return "md5" + hex.EncodeToString(outer[:])
}
