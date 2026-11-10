package account

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2 are the parameters of argon2id.
type Argon2 struct {
	Memory  uint32 // KiB
	Time    uint32 // passes
	Threads uint8
}

// OWASP are the parameters OWASP recommends for argon2id: 19 MiB, 2 passes,
// 1 thread.
var OWASP = Argon2{Memory: 19 * 1024, Time: 2, Threads: 1}

const (
	saltSize = 16
	hashSize = 32
)

// The lengths a password may have, in characters; it has no rules of
// composition.
const (
	MinPassword = 12
	MaxPassword = 128
)

var b64 = base64.RawStdEncoding

// hash returns password hashed as a PHC string,
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>, which says its parameters,
// so that raising them leaves the older hashes working.
func (a Argon2) hash(password string) (string, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, a.Time, a.Memory, a.Threads, hashSize)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, a.Memory, a.Time, a.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var phc = regexp.MustCompile(`^\$argon2id\$v=(\d+)\$m=(\d+),t=(\d+),p=(\d+)\$([A-Za-z0-9+/]+)\$([A-Za-z0-9+/]+)$`)

// matches reports whether password is the one hashed in encoded, a PHC
// string of argon2id.
func matches(encoded, password string) (bool, error) {
	m := phc.FindStringSubmatch(encoded)
	if m == nil {
		return false, errors.New("the password hash is not argon2id in PHC")
	}
	var version int
	var a Argon2
	if _, err := fmt.Sscanf(strings.Join(m[1:5], " "), "%d %d %d %d", &version, &a.Memory, &a.Time, &a.Threads); err != nil {
		return false, err
	}
	if version != argon2.Version {
		return false, fmt.Errorf("the password hash is argon2 version %d", version)
	}
	salt, err := b64.DecodeString(m[5])
	if err != nil {
		return false, err
	}
	want, err := b64.DecodeString(m[6])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, a.Time, a.Memory, a.Threads, uint32(len(want))) //nolint:gosec // a hash is 32 bytes
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// checkPassword refuses a password shorter than MinPassword or longer than
// MaxPassword characters.
func checkPassword(password string) error {
	if n := utf8.RuneCountInString(password); n < MinPassword || n > MaxPassword {
		return &InvalidError{Field: "password", Reason: fmt.Sprintf("must be %d to %d characters", MinPassword, MaxPassword)}
	}
	return nil
}

// usernamePattern is GitHub's: letters, digits and -, starting with a letter
// or digit, 1 to 39 characters.
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

// ValidUsername reports whether a user could have username.
func ValidUsername(username string) bool { return usernamePattern.MatchString(username) }

func checkUsername(username string) error {
	if !ValidUsername(username) {
		return &InvalidError{Field: "username", Reason: "must be 1 to 39 letters, digits and -, starting with a letter or digit"}
	}
	return nil
}
