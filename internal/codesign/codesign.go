// Package codesign signs a wdx binary on macOS with a self-signed
// certificate kept in a keychain of its own in the home.
//
// macOS keys the permissions it grants a program (Full Disk Access, the
// Downloads folder) to the program's signing identity. An unsigned build's
// identity is the hash of its bytes, new with every version, so each update
// loses the grants; a build signed with the same certificate and identifier
// keeps them. The wdx doc MAC_SIGNING says why and how, and what was checked
// on a Mac.
package codesign

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/srjn45/wdx/internal/atomicfile"
	"github.com/srjn45/wdx/internal/identity"
	"github.com/srjn45/wdx/internal/logging"
)

// Certificate is the common name of the certificate wdx signs with.
var Certificate = identity.Name + "-codesign"

// Supported reports whether binaries can be signed on goos: macOS alone
// keys permissions to a signature.
func Supported(goos string) bool { return goos == "darwin" }

// Result is what Sign did besides signing.
type Result struct {
	// CertificateMade is true when Sign made the keychain and the
	// certificate: a new identity, which macOS must be granted Full Disk
	// Access anew.
	CertificateMade bool
}

// Sign signs binary in place with the certificate in home's keychain,
// under identifier, and verifies the signature. It makes the keychain and
// the certificate when they are missing or unusable, and puts the keychain
// on the user's keychain search list when it is not on it.
func Sign(ctx context.Context, home, binary, identifier string) (Result, error) {
	k := keychainOf(home)
	made, err := k.ensure(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := k.onSearchList(ctx); err != nil {
		return Result{}, err
	}
	if err := k.unlock(ctx); err != nil {
		return Result{}, err
	}
	if _, err := k.run(ctx, "codesign", "--force", "--keychain", k.path, "--sign", Certificate, "--identifier", identifier, binary); err != nil {
		return Result{}, err
	}
	if _, err := k.run(ctx, "codesign", "--verify", "--strict", binary); err != nil {
		return Result{}, err
	}
	logging.FromContext(ctx).Info("binary signed", "path", binary, "identifier", identifier, "certificate", Certificate, "certificate_made", made)
	return Result{CertificateMade: made}, nil
}

// keychain is the keychain of a home and the file holding its password.
type keychain struct {
	dir, path, passwordFile string
	password                string // read or made by ensure
}

func keychainOf(home string) *keychain {
	dir := identity.CodesignDir(home)
	return &keychain{dir: dir, path: filepath.Join(dir, identity.Name+".keychain-db"), passwordFile: filepath.Join(dir, "password")}
}

// ensure makes the keychain and the certificate anew unless both are there
// and the password opens the keychain, and reports whether it made them.
func (k *keychain) ensure(ctx context.Context) (made bool, err error) {
	reason := k.unusable(ctx)
	if reason == "" {
		return false, nil
	}
	logging.FromContext(ctx).Info("keychain made anew", "path", k.path, "reason", reason)
	return true, k.make(ctx)
}

// unusable says why the keychain cannot be signed with as it is, or "" when
// it can.
func (k *keychain) unusable(ctx context.Context) string {
	b, err := os.ReadFile(k.passwordFile)
	if err != nil {
		return "no password: " + err.Error()
	}
	k.password = strings.TrimSpace(string(b))
	if _, err := os.Stat(k.path); err != nil {
		return "no keychain: " + err.Error()
	}
	if err := k.unlock(ctx); err != nil {
		return "the password does not open it"
	}
	if _, err := k.run(ctx, "security", "find-certificate", "-c", Certificate, k.path); err != nil {
		return "no certificate in it"
	}
	return ""
}

// make writes a new password, makes the keychain with it, and imports a
// new certificate and its key, which codesign may use with no dialog.
func (k *keychain) make(ctx context.Context) error {
	if err := os.MkdirAll(k.dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(k.dir, 0o700); err != nil {
		return err
	}
	// A keychain the password does not open is replaced: create-keychain
	// refuses a path that is taken.
	if err := os.Remove(k.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	k.password = hex.EncodeToString(random)
	if err := atomicfile.Write(k.passwordFile, []byte(k.password+"\n"), 0o600); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("keychain password written", "path", k.passwordFile)
	if _, err := k.run(ctx, "security", "create-keychain", "-p", k.password, k.path); err != nil {
		return err
	}
	// No timeout and no lock on sleep: it stays open once unlocked.
	if _, err := k.run(ctx, "security", "set-keychain-settings", k.path); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("keychain created", "path", k.path)

	tmp, err := os.MkdirTemp(k.dir, "certificate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	config := filepath.Join(tmp, "request.cnf")
	key, cert, p12 := filepath.Join(tmp, "key.pem"), filepath.Join(tmp, "cert.pem"), filepath.Join(tmp, "cert.p12")
	if err := os.WriteFile(config, []byte(request), 0o600); err != nil {
		return err
	}
	if _, err := k.run(ctx, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "3650",
		"-keyout", key, "-out", cert, "-config", config); err != nil {
		return err
	}
	export := []string{"pkcs12", "-export", "-inkey", key, "-in", cert, "-out", p12, "-passout", "pass:" + k.password, "-name", Certificate}
	// OpenSSL 3 writes a file the keychain cannot read unless told -legacy;
	// the LibreSSL macOS ships has no such flag and writes the old format.
	if help, _ := k.run(ctx, "openssl", "pkcs12", "-help"); bytes.Contains(help, []byte("-legacy")) {
		export = slices.Insert(export, 2, "-legacy")
	}
	if _, err := k.run(ctx, "openssl", export...); err != nil {
		return err
	}
	if _, err := k.run(ctx, "security", "import", p12, "-k", k.path, "-P", k.password, "-T", "/usr/bin/codesign"); err != nil {
		return err
	}
	if _, err := k.run(ctx, "security", "set-key-partition-list", "-S", "apple-tool:,apple:,codesign:", "-s", "-k", k.password, k.path); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("certificate made", "keychain", k.path, "certificate", Certificate)
	return nil
}

// request is what openssl makes the certificate from: one for code signing
// only, named Certificate.
var request = `[req]
distinguished_name = dn
x509_extensions = v3
prompt = no
[dn]
CN = ` + Certificate + `
[v3]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
`

// onSearchList adds the keychain to the end of the user's keychain search
// list when it is not on it, keeping every keychain the list has: codesign
// finds an identity only in a keychain on that list, whatever --keychain
// says.
func (k *keychain) onSearchList(ctx context.Context) error {
	out, err := k.run(ctx, "security", "list-keychains", "-d", "user")
	if err != nil {
		return err
	}
	list := searchList(out)
	if onList(list, k.path) {
		return nil
	}
	if _, err := k.run(ctx, "security", append(append([]string{"list-keychains", "-d", "user", "-s"}, list...), k.path)...); err != nil {
		return err
	}
	logging.FromContext(ctx).Info("keychain put on the search list", "path", k.path, "list", list)
	return nil
}

// searchList reads what security list-keychains prints: one quoted path
// a line, indented.
func searchList(out []byte) []string {
	var list []string
	for line := range strings.Lines(string(out)) {
		if path := strings.Trim(strings.TrimSpace(line), `"`); path != "" {
			list = append(list, path)
		}
	}
	return list
}

// onList reports whether path is on list, as it is or with its links
// resolved, which security may print.
func onList(list []string, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	return slices.ContainsFunc(list, func(p string) bool {
		p = filepath.Clean(p)
		return p == filepath.Clean(path) || p == resolved
	})
}

func (k *keychain) unlock(ctx context.Context) error {
	_, err := k.run(ctx, "security", "unlock-keychain", "-p", k.password, k.path)
	return err
}

// run runs a program and returns what it printed, stdout and stderr
// together. The keychain's password never reaches the log.
func (k *keychain) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	log := logging.FromContext(ctx)
	shown := k.redact(append([]string{name}, args...))
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	output := k.redact([]string{string(bytes.TrimSpace(out))})[0]
	if err != nil {
		log.Error("signing command failed", "args", shown, "output", output, "err", err)
		if errors.Is(err, exec.ErrNotFound) {
			return out, fmt.Errorf("%s is not installed, or not on the PATH", name)
		}
		return out, fmt.Errorf("%s: %w: %s", strings.Join(shown[:min(2, len(shown))], " "), err, output)
	}
	log.Debug("signing command ran", "args", shown, "output", output)
	return out, nil
}

// redact returns args with the password replaced.
func (k *keychain) redact(args []string) []string {
	shown := slices.Clone(args)
	if k.password == "" {
		return shown
	}
	for i, a := range shown {
		shown[i] = strings.ReplaceAll(a, k.password, "<password>")
	}
	return shown
}
