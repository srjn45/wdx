// Package authcode is the host's authenticator: the TOTP secret an
// authenticator app holds for this node, the last step a code was accepted
// at, so that a code opens one session, and the recovery key that sets up a
// new authenticator when the old is lost. They are the files code.secret,
// code.step and recovery.hash in <home>/hub/, each only the user may read
// (the wdx doc HUB_TUNNEL, "The authenticator, on the host").
//
// Nothing here is ever logged: not the secret, a code or the recovery key.
package authcode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/logging"
)

// The TOTP every authenticator app takes, as the hub's own: SHA-1, 6 digits,
// 30-second steps, and the step before and after the current one accepted.
const (
	Period = 30 * time.Second
	digits = otp.DigitsSix
	skew   = 1
)

// The files, in <home>/hub/.
const (
	secretFile   = "code.secret"
	stepFile     = "code.step"
	recoveryFile = "recovery.hash"
	fileMode     = 0o600
)

var (
	// ErrNotSetUp is a node with no code set up.
	ErrNotSetUp = errors.New("no code is set up on this node")
	// ErrWrongCode is a code that is not the secret's at any step accepted.
	ErrWrongCode = errors.New("the code is wrong, or was used already")
	// ErrStepRefused is a step out of range, one either side of now, or not
	// after the last one accepted.
	ErrStepRefused = errors.New("the code's step is too old, too new, or used already")
)

// Pending is a new secret, not yet written: what an authenticator app is
// given, as text and as the otpauth URL its QR code holds.
type Pending struct {
	Secret string
	URL    string
}

// Generate makes a new secret, labelled issuer: account in the app.
func Generate(issuer, account string) (Pending, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: issuer, AccountName: account, Period: uint(Period / time.Second), Digits: digits, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		return Pending{}, err
	}
	return Pending{Secret: key.Secret(), URL: key.URL()}, nil
}

// Step is the 30-second step t is in.
func Step(t time.Time) int64 { return t.Unix() / int64(Period/time.Second) }

// CodeAt is secret's code at step.
func CodeAt(secret string, step int64) (string, error) {
	return hotp.GenerateCodeCustom(secret, uint64(step), hotp.ValidateOpts{Digits: digits, Algorithm: otp.AlgorithmSHA1}) //nolint:gosec // a step after 1970 is positive
}

// inRange reports whether step is one either side of now's and after last.
func inRange(step, last int64, now time.Time) bool {
	current := Step(now)
	return step > last && step >= current-skew && step <= current+skew
}

// Match returns the step code is secret's code at: now's, the one before or
// the one after, and after last. It fails with ErrWrongCode.
func Match(secret, code string, now time.Time, last int64) (int64, error) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	current := Step(now)
	for step := current - skew; step <= current+skew; step++ {
		if !inRange(step, last, now) {
			continue
		}
		want, err := CodeAt(secret, step)
		if err != nil {
			return 0, err
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, nil
		}
	}
	return 0, ErrWrongCode
}

// Files are the code's files in a directory, <home>/hub/, which must exist:
// it is there only while the node is logged in. One Files is made per
// directory, as the daemon does, since it orders the changes to them.
type Files struct {
	dir string
	mu  sync.Mutex
}

// Open returns the code's files in dir.
func Open(dir string) *Files { return &Files{dir: dir} }

func (f *Files) path(name string) string { return filepath.Join(f.dir, name) }

// SetUp reports whether a code is set up.
func (f *Files) SetUp() (bool, error) {
	_, err := os.Stat(f.path(secretFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Write sets up secret, a code of it accepted at step, with a new recovery
// key, which it returns: the old secret and recovery key stop working.
func (f *Files) Write(ctx context.Context, secret string, step int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := NewRecoveryKey()
	// The step first: with it moved, no code of an older secret is accepted
	// twice, whatever else is written.
	if err := atomicfile.Write(f.path(stepFile), []byte(strconv.FormatInt(step, 10)+"\n"), fileMode); err != nil {
		return "", err
	}
	if err := atomicfile.Write(f.path(secretFile), []byte(secret+"\n"), fileMode); err != nil {
		return "", err
	}
	if err := atomicfile.Write(f.path(recoveryFile), []byte(recoveryHash(key)+"\n"), fileMode); err != nil {
		return "", err
	}
	logging.FromContext(ctx).Info("code files written", "path", f.dir, "step", step)
	return key, nil
}

// secret reads the secret and the last step accepted.
func (f *Files) secret() (string, int64, error) {
	b, err := os.ReadFile(f.path(secretFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, ErrNotSetUp
	}
	if err != nil {
		return "", 0, err
	}
	last, err := f.lastStep()
	return strings.TrimSpace(string(b)), last, err
}

func (f *Files) lastStep() (int64, error) {
	b, err := os.ReadFile(f.path(stepFile))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// CodeAt returns the code at step, which must be one either side of now's
// and after the last step accepted; Use then moves it. It fails with
// ErrNotSetUp or ErrStepRefused.
func (f *Files) CodeAt(ctx context.Context, step int64, now time.Time) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	secret, last, err := f.secret()
	if err != nil {
		return "", err
	}
	if !inRange(step, last, now) {
		logging.FromContext(ctx).Info("code step refused", "step", step, "last_step", last, "now_step", Step(now))
		return "", ErrStepRefused
	}
	return CodeAt(secret, step)
}

// Check returns the step code is the code of, one either side of now's and
// after the last accepted, and moves the last step to it. It fails with
// ErrNotSetUp or ErrWrongCode.
func (f *Files) Check(ctx context.Context, code string, now time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	secret, last, err := f.secret()
	if err != nil {
		return 0, err
	}
	step, err := Match(secret, code, now, last)
	if err != nil {
		logging.FromContext(ctx).Info("code refused", "last_step", last, "now_step", Step(now))
		return 0, err
	}
	return step, f.use(ctx, step, last)
}

// Use records step as accepted, so that no code at it or before is again.
// It fails with ErrStepRefused for a step not after the last one.
func (f *Files) Use(ctx context.Context, step int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	last, err := f.lastStep()
	if err != nil {
		return err
	}
	return f.use(ctx, step, last)
}

func (f *Files) use(ctx context.Context, step, last int64) error {
	if step <= last {
		logging.FromContext(ctx).Info("code step refused", "step", step, "last_step", last, "reason", "used")
		return ErrStepRefused
	}
	if err := atomicfile.Write(f.path(stepFile), []byte(strconv.FormatInt(step, 10)+"\n"), fileMode); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("code step used", "step", step)
	return nil
}

// CheckRecoveryKey reports whether key is the recovery key, however it is
// grouped or cased. It fails with ErrNotSetUp.
func (f *Files) CheckRecoveryKey(key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := os.ReadFile(f.path(recoveryFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, ErrNotSetUp
	}
	if err != nil {
		return false, err
	}
	normal, ok := normalRecoveryKey(key)
	if !ok {
		return false, nil
	}
	return subtle.ConstantTimeCompare([]byte(recoveryHash(normal)), []byte(strings.TrimSpace(string(b)))) == 1, nil
}

// crockford is the recovery key's alphabet: base32 without I, L, O and U,
// which are read as 1, 1, 0 and V.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// recoveryLength is the recovery key's characters: 100 bits.
const recoveryLength = 20

// NewRecoveryKey returns a new recovery key, 20 characters in groups of four,
// as ABCD-EFGH-JKMN-PQRS-TVWX.
func NewRecoveryKey() string {
	b := make([]byte, recoveryLength)
	_, _ = rand.Read(b) // never fails
	var key strings.Builder
	for i := range b {
		if i > 0 && i%4 == 0 {
			key.WriteByte('-')
		}
		key.WriteByte(crockford[b[i]&31])
	}
	return key.String()
}

// normalRecoveryKey is key as hashed: its 20 characters, upper case, with
// the groups' dashes and spaces left out and I, L and O read as 1, 1 and 0.
// It is false for a key of another length or alphabet.
func normalRecoveryKey(key string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(key) {
		switch r {
		case '-', ' ':
			continue
		case 'I', 'L':
			r = '1'
		case 'O':
			r = '0'
		}
		if !strings.ContainsRune(crockford, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	return b.String(), b.Len() == recoveryLength
}

// recoveryHash is the SHA-256 of a recovery key, in hex.
func recoveryHash(key string) string {
	normal, _ := normalRecoveryKey(key)
	sum := sha256.Sum256([]byte(normal))
	return hex.EncodeToString(sum[:])
}
