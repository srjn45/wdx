package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/srjn45/wdx/internal/hub/audit"
	"github.com/srjn45/wdx/internal/logging"
	"github.com/srjn45/wdx/internal/sqlite"
)

// Flow is how a login is finished.
type Flow string

// The flows.
const (
	// FlowBrowser is the authorization code flow: the browser on the
	// machine approves, and is sent to the node's loopback callback with
	// the code.
	FlowBrowser Flow = "browser"
	// FlowDevice is the device flow: the person types the user code from
	// any browser, and the node polls.
	FlowDevice Flow = "device"
)

// The statuses of a login.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDenied   = "denied"
)

// Login is a machine asking to become a node of an account.
type Login struct {
	ID        string
	Flow      Flow
	Name      string // the name it asked for
	OS        string
	Version   string // of wdx
	PublicKey ed25519.PublicKey
	Callback  string // the browser flow's
	Status    string
	UserID    string // once approved or denied
	NodeID    string // once approved
	Address   string // that started it
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Expired reports whether the login may no longer be approved or finished at
// now.
func (l Login) Expired(now time.Time) bool { return !now.Before(l.ExpiresAt) }

// StartRequest is what a machine starting a login gives.
type StartRequest struct {
	Flow      Flow
	Name      string
	OS        string
	Version   string
	PublicKey []byte
	// PKCEChallenge and Callback are the browser flow's, and only its.
	PKCEChallenge string
	Callback      string
}

// Started is a login that has just started, with the device flow's codes,
// which are shown only now.
type Started struct {
	Login
	DeviceCode string // the secret the node polls with
	UserCode   string // the code the person types, as WDX-4F7K-9QPA
}

// userCodeAlphabet are the characters of a user code: letters and digits no
// one mistakes for another, no I, L, O, 0 or 1.
const userCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// userCodeLength is how many characters a user code has, besides its WDX-
// and dash.
const userCodeLength = 8

// newUserCode returns a user code as shown, WDX-XXXX-XXXX, and its eight
// characters as they are hashed.
func newUserCode() (shown, normal string) {
	b := make([]byte, userCodeLength)
	for i := range b {
		b[i] = userCodeAlphabet[randIndex(len(userCodeAlphabet))]
	}
	normal = string(b)
	return "WDX-" + normal[:4] + "-" + normal[4:], normal
}

// randIndex is a random number from 0 to n-1, unbiased.
func randIndex(n int) int {
	limit := 256 - 256%n
	var b [1]byte
	for {
		_, _ = rand.Read(b[:]) // never fails
		if int(b[0]) < limit {
			return int(b[0]) % n
		}
	}
}

// NormalUserCode is code as typed made to its eight characters: upper case,
// without spaces, dashes and its WDX; empty when it cannot be a user code.
func NormalUserCode(code string) string {
	code = strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code)))
	if len(code) == userCodeLength+3 {
		code = strings.TrimPrefix(code, "WDX")
	}
	if len(code) != userCodeLength {
		return ""
	}
	for _, c := range code {
		if !strings.ContainsRune(userCodeAlphabet, c) {
			return ""
		}
	}
	return code
}

// checkStart refuses what a login cannot start with.
func checkStart(req StartRequest) error {
	if err := CheckName(req.Name); err != nil {
		return err
	}
	if len(req.PublicKey) != ed25519.PublicKeySize {
		return &InvalidError{Field: "public_key", Reason: "must be an Ed25519 public key, 32 bytes"}
	}
	if strings.TrimSpace(req.OS) == "" {
		return &InvalidError{Field: "os", Reason: "must not be empty"}
	}
	if strings.TrimSpace(req.Version) == "" {
		return &InvalidError{Field: "wdx_version", Reason: "must not be empty"}
	}
	switch req.Flow {
	case FlowBrowser:
		if err := checkChallenge(req.PKCEChallenge); err != nil {
			return err
		}
		return checkCallback(req.Callback)
	case FlowDevice:
		if req.PKCEChallenge != "" || req.Callback != "" {
			return &InvalidError{Field: "flow", Reason: "device takes no pkce_challenge or callback"}
		}
		return nil
	}
	return &InvalidError{Field: "flow", Reason: "must be browser or device"}
}

// checkChallenge refuses what is not an S256 challenge: the SHA-256 of the
// verifier in base64url without padding, 43 characters.
func checkChallenge(challenge string) error {
	b, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil || len(b) != sha256.Size {
		return &InvalidError{Field: "pkce_challenge", Reason: "must be an S256 challenge, 43 characters of base64url"}
	}
	return nil
}

// checkCallback refuses a callback that is not on http://127.0.0.1 with a
// port: the code is delivered only to the machine the browser runs on, which
// is what makes a login link forwarded to someone else useless.
func checkCallback(callback string) error {
	invalid := &InvalidError{Field: "callback", Reason: "must be http://127.0.0.1:<port>/<path>, on the machine the browser runs on"}
	u, err := url.Parse(callback)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return invalid
	}
	if port, err := strconv.Atoi(u.Port()); err != nil || port < 1 || port > 65535 {
		return invalid
	}
	return nil
}

// verifies reports whether verifier is the one challenge was made from.
func verifies(challenge, verifier string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// StartLogin starts a login of req, which lives LoginLife, for the address
// in by. The device flow's codes are in the result alone: the hub keeps
// their hashes. Its start is audited only when it is approved or denied: a
// login no one answers has no account, and anyone may start one.
func (s *Store) StartLogin(ctx context.Context, by By, req StartRequest) (Started, error) {
	log := logging.FromContext(ctx)
	if err := checkStart(req); err != nil {
		log.Info("login refused", "reason", err.Error())
		return Started{}, err
	}
	id := newLoginID()
	now := s.now()
	started := Started{Login: Login{
		ID: id, Flow: req.Flow, Name: req.Name, OS: req.OS, Version: req.Version, PublicKey: req.PublicKey,
		Callback: req.Callback, Status: StatusPending, Address: by.Address, CreatedAt: now.UTC(), ExpiresAt: now.Add(LoginLife).UTC(),
	}}
	var codeHash, userCodeHash []byte
	if req.Flow == FlowDevice {
		started.DeviceCode, codeHash = newToken()
		var normal string
		started.UserCode, normal = newUserCode()
		userCodeHash = hashOf(normal)
	}
	if _, err := sqlite.Exec(ctx, s.db, "INSERT INTO node_logins (id, flow, code_hash, pkce_challenge, callback, user_code_hash, name, os, version, public_key, status, address, created_at, expires_at) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		id, string(req.Flow), secretOrNull(codeHash), nullable(req.PKCEChallenge), nullable(req.Callback), secretOrNull(userCodeHash),
		req.Name, req.OS, req.Version, []byte(req.PublicKey), StatusPending, by.Address, ms(now), ms(started.ExpiresAt)); err != nil {
		return Started{}, err
	}
	log.Info("login started", "login_id", id, "flow", string(req.Flow), "name", req.Name, "address", by.Address)
	return started, nil
}

// newLoginID is the id of a new login, nl- and 128 random bits in hex: the
// browser flow's approval link names it, so it must not be guessed while
// the login waits.
func newLoginID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails
	return "nl-" + hex.EncodeToString(b)
}

// recordStarted audits the start of l, as it is approved or denied by
// userID: the row is written with the answer, from the address that started
// it.
func (s *Store) recordStarted(ctx context.Context, tx *sql.Tx, userID string, l Login) error {
	by := By{Actor: audit.ActorUser, Address: l.Address}
	return s.record(ctx, tx, by, userID, "", ActionLoginStarted, map[string]any{
		"login_id": l.ID, "flow": string(l.Flow), "name": l.Name, "started_at": l.CreatedAt.Format(time.RFC3339Nano)})
}

// secretOrNull is a hash as Exec logs it, or NULL for none.
func secretOrNull(hash []byte) any {
	if hash == nil {
		return nil
	}
	return secret(hash)
}

const loginColumns = "id, flow, name, os, version, public_key, callback, status, user_id, node_id, address, created_at, expires_at"

func scanLogin(row interface{ Scan(...any) error }) (Login, error) {
	var l Login
	var flow string
	var pub []byte
	var callback, userID, nodeID sql.NullString
	var created, expires int64
	if err := row.Scan(&l.ID, &flow, &l.Name, &l.OS, &l.Version, &pub, &callback, &l.Status, &userID, &nodeID, &l.Address, &created, &expires); err != nil {
		return Login{}, err
	}
	l.Flow, l.PublicKey = Flow(flow), ed25519.PublicKey(pub)
	l.Callback, l.UserID, l.NodeID = callback.String, userID.String, nodeID.String
	l.CreatedAt, l.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
	return l, nil
}

// Login returns the login id, failing with ErrLoginNotFound when there is
// none, and with ErrLoginExpired, the login with it, when it has expired.
func (s *Store) Login(ctx context.Context, id string) (Login, error) {
	return s.loginWhere(ctx, s.db, "id = ?", id)
}

// LoginByUserCode returns the device login whose user code is code, as the
// person typed it, as Login does.
func (s *Store) LoginByUserCode(ctx context.Context, code string) (Login, error) {
	normal := NormalUserCode(code)
	if normal == "" {
		logging.FromContext(ctx).Info("login not found", "reason", "not a user code")
		return Login{}, ErrLoginNotFound
	}
	return s.loginWhere(ctx, s.db, "flow = 'device' AND user_code_hash = ?", hashOf(normal))
}

func (s *Store) loginWhere(ctx context.Context, q sqlite.Querier, where string, arg any) (Login, error) {
	l, err := scanLogin(q.QueryRowContext(ctx, "SELECT "+loginColumns+" FROM node_logins WHERE "+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		logging.FromContext(ctx).Info("login not found")
		return Login{}, ErrLoginNotFound
	}
	if err != nil {
		return Login{}, err
	}
	if l.Expired(s.now()) {
		logging.FromContext(ctx).Info("login expired", "login_id", l.ID)
		return l, ErrLoginExpired
	}
	return l, nil
}

// pendingLogin returns the login id in tx when it may still be approved or
// denied.
func (s *Store) pendingLogin(ctx context.Context, tx *sql.Tx, id string) (Login, error) {
	l, err := s.loginWhere(ctx, tx, "id = ?", id)
	if err != nil {
		return Login{}, err
	}
	if l.Status != StatusPending {
		logging.FromContext(ctx).Info("login refused", "login_id", id, "reason", "status "+l.Status)
		return l, ErrLoginDone
	}
	return l, nil
}

// Approved is a login approved, the node it made, and for the browser flow
// the authorization code the browser takes to the callback, which is shown
// only now.
type Approved struct {
	Login Login
	Node  Node
	Code  string
}

// Approve makes the login id a node of userID named name, which the account
// must not have already, and for the browser flow gives the one-time
// authorization code. The node gets its certificate when the login is
// finished.
func (s *Store) Approve(ctx context.Context, by By, userID, id, name string) (Approved, error) {
	log := logging.FromContext(ctx)
	if err := CheckName(name); err != nil {
		return Approved{}, err
	}
	nodeID, err := sqlite.NewID("node")
	if err != nil {
		return Approved{}, err
	}
	var a Approved
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		l, err := s.pendingLogin(ctx, tx, id)
		if err != nil {
			return err
		}
		if taken, err := nameTaken(ctx, tx, userID, name); err != nil {
			return err
		} else if taken {
			log.Info("approval refused", "login_id", id, "reason", "name_taken", "name", name)
			return ErrNameTaken
		}
		now := s.now()
		if _, err := sqlite.Exec(ctx, tx, "INSERT INTO nodes (id, user_id, name, public_key, os, version, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			nodeID, userID, name, []byte(l.PublicKey), l.OS, l.Version, ms(now)); err != nil {
			return err
		}
		var codeHash []byte
		if l.Flow == FlowBrowser {
			a.Code, codeHash = newToken()
		}
		if _, err := sqlite.Exec(ctx, tx, "UPDATE node_logins SET status = ?, user_id = ?, node_id = ?, code_hash = COALESCE(?, code_hash) WHERE id = ?",
			StatusApproved, userID, nodeID, secretOrNull(codeHash), id); err != nil {
			return err
		}
		if err := s.recordStarted(ctx, tx, userID, l); err != nil {
			return err
		}
		l.Status, l.UserID, l.NodeID = StatusApproved, userID, nodeID
		a.Login = l
		a.Node = Node{ID: nodeID, UserID: userID, Name: name, PublicKey: l.PublicKey, OS: l.OS, Version: l.Version, CreatedAt: time.UnixMilli(ms(now)).UTC()}
		details := map[string]any{"login_id": id, "flow": string(l.Flow), "name": name, "login_address": l.Address}
		return s.record(ctx, tx, by, userID, nodeID, ActionApproved, details)
	})
	if err != nil {
		return Approved{}, err
	}
	log.Info("login approved", "login_id", id, "user_id", userID, "node_id", nodeID, "name", name)
	return a, nil
}

// Deny refuses the login id, as userID.
func (s *Store) Deny(ctx context.Context, by By, userID, id string) error {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		l, err := s.pendingLogin(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := sqlite.Exec(ctx, tx, "UPDATE node_logins SET status = ?, user_id = ? WHERE id = ?", StatusDenied, userID, id); err != nil {
			return err
		}
		if err := s.recordStarted(ctx, tx, userID, l); err != nil {
			return err
		}
		return s.record(ctx, tx, by, userID, "", ActionDenied, map[string]any{"login_id": id, "flow": string(l.Flow), "name": l.Name, "login_address": l.Address})
	})
	if err != nil {
		return err
	}
	logging.FromContext(ctx).Info("login denied", "login_id", id, "user_id", userID)
	return nil
}

// Issue issues the first certificate of node, for its key, returning it in
// PEM and when it ends.
type Issue func(node Node) (certPEM []byte, notAfter time.Time, err error)

// Credentials are what a node gets when its login is finished.
type Credentials struct {
	Node        Node
	Username    string
	Certificate []byte // in PEM
}

// finishing is an approved login being finished, and its user.
type finishing struct {
	loginID, challenge string
	status             string
	expires, polled    sql.NullInt64
	userID, nodeID     sql.NullString
	username           sql.NullString
	disabled, removed  sql.NullInt64
}

func (s *Store) finishingWhere(ctx context.Context, tx *sql.Tx, where string, arg any) (finishing, error) {
	var f finishing
	var challenge sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT l.id, l.pkce_challenge, l.status, l.expires_at, l.polled_at, l.user_id, l.node_id, u.username, u.disabled_at, u.removed_at "+
		"FROM node_logins l LEFT JOIN users u ON u.id = l.user_id WHERE "+where, arg).
		Scan(&f.loginID, &challenge, &f.status, &f.expires, &f.polled, &f.userID, &f.nodeID, &f.username, &f.disabled, &f.removed)
	if errors.Is(err, sql.ErrNoRows) {
		logging.FromContext(ctx).Info("login not found", "reason", "no such code")
		return finishing{}, ErrLoginNotFound
	}
	f.challenge = challenge.String
	return f, err
}

// finish issues the approved login's node its certificate and deletes the
// login, so that its code finds nothing again.
func (s *Store) finish(ctx context.Context, tx *sql.Tx, f finishing, issue Issue) (Credentials, error) {
	log := logging.FromContext(ctx)
	if f.disabled.Valid || f.removed.Valid {
		log.Info("login refused", "login_id", f.loginID, "reason", "account disabled")
		return Credentials{}, ErrDisabled
	}
	n, err := scanNode(tx.QueryRowContext(ctx, "SELECT "+nodeColumns+" FROM nodes WHERE id = ? AND revoked_at IS NULL", f.nodeID.String))
	if errors.Is(err, sql.ErrNoRows) {
		log.Info("login refused", "login_id", f.loginID, "reason", "node removed")
		return Credentials{}, ErrRevoked
	}
	if err != nil {
		return Credentials{}, err
	}
	if _, err := sqlite.Exec(ctx, tx, "DELETE FROM node_logins WHERE id = ?", f.loginID); err != nil {
		return Credentials{}, err
	}
	cert, notAfter, err := issue(n)
	if err != nil {
		return Credentials{}, err
	}
	if _, err := sqlite.Exec(ctx, tx, "UPDATE nodes SET cert_expires_at = ? WHERE id = ?", ms(notAfter), n.ID); err != nil {
		return Credentials{}, err
	}
	n.CertExpiresAt = time.UnixMilli(ms(notAfter)).UTC()
	log.Info("login finished", "login_id", f.loginID, "node_id", n.ID, "user_id", n.UserID, "cert_expires_at", n.CertExpiresAt)
	return Credentials{Node: n, Username: f.username.String, Certificate: cert}, nil
}

// Exchange finishes the browser login whose authorization code is code,
// once, given the PKCE verifier its challenge was made from.
func (s *Store) Exchange(ctx context.Context, code, verifier string, issue Issue) (Credentials, error) {
	var c Credentials
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		f, err := s.finishingWhere(ctx, tx, "l.flow = 'browser' AND l.status = 'approved' AND l.code_hash = ?", hashOf(code))
		if err != nil {
			return err
		}
		if !s.now().Before(time.UnixMilli(f.expires.Int64)) {
			logging.FromContext(ctx).Info("login refused", "login_id", f.loginID, "reason", "expired")
			return ErrLoginExpired
		}
		if !verifies(f.challenge, verifier) {
			logging.FromContext(ctx).Info("login refused", "login_id", f.loginID, "reason", "pkce verifier")
			return ErrWrongVerifier
		}
		c, err = s.finish(ctx, tx, f, issue)
		return err
	})
	return c, err
}

// Poll answers the polling of the device login whose device code is
// deviceCode: the credentials once it is approved, once; otherwise
// ErrPending, ErrSlowDown when polled within PollInterval of the last time,
// ErrDenied or ErrLoginExpired.
func (s *Store) Poll(ctx context.Context, deviceCode string, issue Issue) (Credentials, error) {
	log := logging.FromContext(ctx)
	var c Credentials
	var state error // a state told the poller, kept with polled_at
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		f, err := s.finishingWhere(ctx, tx, "l.flow = 'device' AND l.code_hash = ?", hashOf(deviceCode))
		if err != nil {
			return err
		}
		now := s.now()
		if !now.Before(time.UnixMilli(f.expires.Int64)) {
			log.Info("login refused", "login_id", f.loginID, "reason", "expired")
			return ErrLoginExpired
		}
		switch f.status {
		case StatusApproved:
			c, err = s.finish(ctx, tx, f, issue)
			return err
		case StatusDenied:
			log.Info("login polled", "login_id", f.loginID, "status", f.status)
			return ErrDenied
		}
		state = ErrPending
		if f.polled.Valid && now.Sub(time.UnixMilli(f.polled.Int64)) < PollInterval {
			state = ErrSlowDown
		}
		log.Info("login polled", "login_id", f.loginID, "status", f.status, "slow_down", errors.Is(state, ErrSlowDown))
		_, err = sqlite.Exec(ctx, tx, "UPDATE node_logins SET polled_at = ? WHERE id = ?", ms(now), f.loginID)
		return err
	})
	if err != nil {
		return Credentials{}, err
	}
	if state != nil {
		return Credentials{}, state
	}
	return c, nil
}

// Expired is what DeleteExpired deleted.
type Expired struct {
	// Logins is the logins expired; Nodes the nodes of those approved but
	// never finished, which never got a certificate.
	Logins, Nodes int64
}

// DeleteExpired deletes the logins past their expiry, with the nodes of
// those approved but never finished, as the hub does at startup and once an
// hour.
func (s *Store) DeleteExpired(ctx context.Context) (Expired, error) {
	var e Expired
	now := ms(s.now())
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if e.Nodes, err = sqlite.Exec(ctx, tx, "DELETE FROM nodes WHERE cert_expires_at IS NULL AND id IN "+
			"(SELECT node_id FROM node_logins WHERE expires_at <= ? AND node_id IS NOT NULL)", now); err != nil {
			return err
		}
		// Their logins went with them.
		var logins int64
		logins, err = sqlite.Exec(ctx, tx, "DELETE FROM node_logins WHERE expires_at <= ?", now)
		e.Logins = logins + e.Nodes
		return err
	})
	if err != nil {
		return Expired{}, err
	}
	logging.FromContext(ctx).Info("expired logins deleted", "logins", e.Logins, "nodes", e.Nodes)
	return e, nil
}
