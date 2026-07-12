package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// migrations are applied in order; the schema_version row holds the count of
// migrations already applied, so a migration is never re-run.
var migrations = []string{
	`
CREATE TABLE accounts (
    aid          TEXT PRIMARY KEY,
    kind         TEXT NOT NULL DEFAULT 'individual',
    xonly        TEXT UNIQUE NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    id_number    TEXT UNIQUE,
    profile      TEXT NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL
);
CREATE TABLE entities (
    id           TEXT PRIMARY KEY,
    owner_aid    TEXT NOT NULL REFERENCES accounts(aid),
    name         TEXT NOT NULL DEFAULT '',
    jurisdiction TEXT NOT NULL DEFAULT '',
    profile      TEXT NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL
);
CREATE INDEX idx_entities_owner ON entities(owner_aid);
CREATE TABLE sessions (
    token      TEXT PRIMARY KEY,
    aid        TEXT NOT NULL REFERENCES accounts(aid),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE TABLE challenges (
    challenge  TEXT PRIMARY KEY,
    xonly      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used       INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE issuances (
    id              TEXT PRIMARY KEY,
    owner_aid       TEXT NOT NULL REFERENCES accounts(aid),
    entity_id       TEXT,
    name            TEXT NOT NULL DEFAULT '',
    ticker          TEXT NOT NULL DEFAULT '',
    structure_id    TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'draft',
    terms           TEXT NOT NULL DEFAULT '{}',
    supply          INTEGER NOT NULL DEFAULT 0,
    precision       INTEGER NOT NULL DEFAULT 0,
    confidential    INTEGER NOT NULL DEFAULT 0,
    clawback        INTEGER NOT NULL DEFAULT 1,
    asset_id        TEXT NOT NULL DEFAULT '',
    txid            TEXT NOT NULL DEFAULT '',
    contract_hash   TEXT NOT NULL DEFAULT '',
    holder_aid      TEXT NOT NULL DEFAULT '',
    enclave_address TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX idx_issuances_owner ON issuances(owner_aid);
CREATE TABLE deploys (
    idem_key      TEXT PRIMARY KEY,
    issuance_id   TEXT NOT NULL,
    asset_id      TEXT NOT NULL DEFAULT '',
    txid          TEXT NOT NULL DEFAULT '',
    contract_hash TEXT NOT NULL DEFAULT '',
    aid           TEXT NOT NULL DEFAULT '',
    address       TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
CREATE TABLE audit_log (
    seq       INTEGER PRIMARY KEY AUTOINCREMENT,
    ts        INTEGER NOT NULL,
    actor_aid TEXT NOT NULL DEFAULT '',
    action    TEXT NOT NULL,
    detail    TEXT NOT NULL DEFAULT '{}',
    prev_hash TEXT NOT NULL DEFAULT '',
    hash      TEXT NOT NULL
);
`,
}

// Store is seqpald's persistent state. Every financial fact the UI shows is
// read from here or from the chain, never asserted by the browser.
type Store struct {
	db      *sql.DB
	auditMu sync.Mutex // serializes the audit chain's read-head/append
}

func openStore(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// The PRAGMAs above are per connection, and the audit chain wants a single
	// writer anyway; one connection keeps both invariants without ceremony.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var version int
	err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version)
	switch {
	case err == sql.ErrNoRows:
		if _, err := s.db.Exec(`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return err
		}
		version = 0
	case err != nil:
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// --- records ---

type Account struct {
	AID         string          `json:"aid"`
	Kind        string          `json:"kind"`
	XOnly       string          `json:"xonly"`
	DisplayName string          `json:"display_name"`
	IDNumber    string          `json:"id_number,omitempty"`
	Profile     json.RawMessage `json:"profile"`
	CreatedAt   int64           `json:"created_at"`
}

type Entity struct {
	ID           string          `json:"id"`
	OwnerAID     string          `json:"owner_aid"`
	Name         string          `json:"name"`
	Jurisdiction string          `json:"jurisdiction"`
	Profile      json.RawMessage `json:"profile"`
	CreatedAt    int64           `json:"created_at"`
}

type Issuance struct {
	ID             string          `json:"id"`
	OwnerAID       string          `json:"owner_aid"`
	EntityID       string          `json:"entity_id,omitempty"`
	Name           string          `json:"name"`
	Ticker         string          `json:"ticker"`
	StructureID    string          `json:"structure_id"`
	Status         string          `json:"status"`
	Terms          json.RawMessage `json:"terms"`
	Supply         uint64          `json:"supply"`
	Precision      int             `json:"precision"`
	Confidential   bool            `json:"confidential"`
	Clawback       bool            `json:"clawback"`
	AssetID        string          `json:"asset_id,omitempty"`
	Txid           string          `json:"txid,omitempty"`
	ContractHash   string          `json:"contract_hash,omitempty"`
	HolderAID      string          `json:"holder_aid,omitempty"`
	EnclaveAddress string          `json:"enclave_address,omitempty"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`
}

type DeployRecord struct {
	IdemKey      string `json:"-"`
	IssuanceID   string `json:"issuance_id"`
	AssetID      string `json:"asset"`
	Txid         string `json:"txid"`
	ContractHash string `json:"contract_hash"`
	AID          string `json:"aid"`
	Address      string `json:"address"`
	CreatedAt    int64  `json:"created_at"`
}

// --- accounts ---

func (s *Store) CreateAccount(a *Account) error {
	var idNum any
	if a.IDNumber != "" {
		idNum = a.IDNumber
	}
	_, err := s.db.Exec(
		`INSERT INTO accounts (aid, kind, xonly, display_name, id_number, profile, created_at) VALUES (?,?,?,?,?,?,?)`,
		a.AID, a.Kind, a.XOnly, a.DisplayName, idNum, string(rawOrEmpty(a.Profile)), a.CreatedAt)
	return err
}

func (s *Store) AccountByAID(aid string) (*Account, error) {
	return s.scanAccount(s.db.QueryRow(
		`SELECT aid, kind, xonly, display_name, COALESCE(id_number,''), profile, created_at FROM accounts WHERE aid = ?`, aid))
}

func (s *Store) AccountByXOnly(xonly string) (*Account, error) {
	return s.scanAccount(s.db.QueryRow(
		`SELECT aid, kind, xonly, display_name, COALESCE(id_number,''), profile, created_at FROM accounts WHERE xonly = ?`, xonly))
}

func (s *Store) scanAccount(row *sql.Row) (*Account, error) {
	var a Account
	var profile string
	err := row.Scan(&a.AID, &a.Kind, &a.XOnly, &a.DisplayName, &a.IDNumber, &profile, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Profile = json.RawMessage(profile)
	return &a, nil
}

// --- entities ---

func (s *Store) CreateEntity(e *Entity) error {
	_, err := s.db.Exec(
		`INSERT INTO entities (id, owner_aid, name, jurisdiction, profile, created_at) VALUES (?,?,?,?,?,?)`,
		e.ID, e.OwnerAID, e.Name, e.Jurisdiction, string(rawOrEmpty(e.Profile)), e.CreatedAt)
	return err
}

func (s *Store) EntitiesByOwner(aid string) ([]*Entity, error) {
	rows, err := s.db.Query(
		`SELECT id, owner_aid, name, jurisdiction, profile, created_at FROM entities WHERE owner_aid = ? ORDER BY created_at`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Entity{}
	for rows.Next() {
		var e Entity
		var profile string
		if err := rows.Scan(&e.ID, &e.OwnerAID, &e.Name, &e.Jurisdiction, &profile, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Profile = json.RawMessage(profile)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *Store) EntityByID(id string) (*Entity, error) {
	var e Entity
	var profile string
	err := s.db.QueryRow(
		`SELECT id, owner_aid, name, jurisdiction, profile, created_at FROM entities WHERE id = ?`, id).
		Scan(&e.ID, &e.OwnerAID, &e.Name, &e.Jurisdiction, &profile, &e.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.Profile = json.RawMessage(profile)
	return &e, nil
}

// --- issuances ---

const issuanceCols = `id, owner_aid, COALESCE(entity_id,''), name, ticker, structure_id, status, terms,
    supply, precision, confidential, clawback, asset_id, txid, contract_hash, holder_aid, enclave_address,
    created_at, updated_at`

func (s *Store) CreateIssuance(i *Issuance) error {
	var entity any
	if i.EntityID != "" {
		entity = i.EntityID
	}
	_, err := s.db.Exec(
		`INSERT INTO issuances (id, owner_aid, entity_id, name, ticker, structure_id, status, terms,
            supply, precision, confidential, clawback, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.OwnerAID, entity, i.Name, i.Ticker, i.StructureID, i.Status, string(rawOrEmpty(i.Terms)),
		i.Supply, i.Precision, boolInt(i.Confidential), boolInt(i.Clawback), i.CreatedAt, i.UpdatedAt)
	return err
}

func (s *Store) IssuanceByID(id string) (*Issuance, error) {
	return scanIssuance(s.db.QueryRow(`SELECT `+issuanceCols+` FROM issuances WHERE id = ?`, id))
}

func (s *Store) IssuancesByOwner(aid string) ([]*Issuance, error) {
	rows, err := s.db.Query(`SELECT `+issuanceCols+` FROM issuances WHERE owner_aid = ? ORDER BY created_at`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Issuance{}
	for rows.Next() {
		i, err := scanIssuanceRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanIssuanceInto(sc scanner) (*Issuance, error) {
	var i Issuance
	var terms string
	var confidential, clawback int
	err := sc.Scan(&i.ID, &i.OwnerAID, &i.EntityID, &i.Name, &i.Ticker, &i.StructureID, &i.Status, &terms,
		&i.Supply, &i.Precision, &confidential, &clawback, &i.AssetID, &i.Txid, &i.ContractHash,
		&i.HolderAID, &i.EnclaveAddress, &i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		return nil, err
	}
	i.Terms = json.RawMessage(terms)
	i.Confidential = confidential != 0
	i.Clawback = clawback != 0
	return &i, nil
}

func scanIssuance(row *sql.Row) (*Issuance, error) {
	i, err := scanIssuanceInto(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return i, err
}

func scanIssuanceRows(rows *sql.Rows) (*Issuance, error) { return scanIssuanceInto(rows) }

// UpdateIssuanceFields applies a partial update. Callers pass only columns the
// caller is allowed to set; ownership is checked before this is reached.
func (s *Store) UpdateIssuanceFields(id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	cols := make([]string, 0, len(fields)+1)
	for k := range fields {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	set := ""
	args := make([]any, 0, len(cols)+2)
	for _, c := range cols {
		if set != "" {
			set += ", "
		}
		set += c + " = ?"
		args = append(args, fields[c])
	}
	set += ", updated_at = ?"
	args = append(args, time.Now().Unix(), id)
	_, err := s.db.Exec(`UPDATE issuances SET `+set+` WHERE id = ?`, args...)
	return err
}

// --- deploys ---

func (s *Store) DeployByIdem(key string) (*DeployRecord, error) {
	var d DeployRecord
	err := s.db.QueryRow(
		`SELECT idem_key, issuance_id, asset_id, txid, contract_hash, aid, address, created_at FROM deploys WHERE idem_key = ?`, key).
		Scan(&d.IdemKey, &d.IssuanceID, &d.AssetID, &d.Txid, &d.ContractHash, &d.AID, &d.Address, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) InsertDeploy(d *DeployRecord) error {
	_, err := s.db.Exec(
		`INSERT INTO deploys (idem_key, issuance_id, asset_id, txid, contract_hash, aid, address, created_at)
         VALUES (?,?,?,?,?,?,?,?)`,
		d.IdemKey, d.IssuanceID, d.AssetID, d.Txid, d.ContractHash, d.AID, d.Address, d.CreatedAt)
	return err
}

// --- sessions ---

func (s *Store) CreateSession(aid string, ttl time.Duration) (string, int64, error) {
	token, err := randHex(32)
	if err != nil {
		return "", 0, err
	}
	now := time.Now().Unix()
	exp := now + int64(ttl.Seconds())
	if _, err := s.db.Exec(
		`INSERT INTO sessions (token, aid, created_at, expires_at) VALUES (?,?,?,?)`, token, aid, now, exp); err != nil {
		return "", 0, err
	}
	return token, exp, nil
}

// SessionAccount returns the account behind a live session token, or nil when
// the token is unknown or expired.
func (s *Store) SessionAccount(token string) (*Account, error) {
	var aid string
	err := s.db.QueryRow(`SELECT aid FROM sessions WHERE token = ? AND expires_at > ?`, token, time.Now().Unix()).Scan(&aid)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.AccountByAID(aid)
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Store) PurgeExpired() error {
	now := time.Now().Unix()
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM challenges WHERE expires_at <= ?`, now-3600)
	return err
}

// --- challenges ---

func (s *Store) CreateChallenge(xonly string, ttl time.Duration) (string, int64, error) {
	challenge, err := randHex(32)
	if err != nil {
		return "", 0, err
	}
	now := time.Now().Unix()
	exp := now + int64(ttl.Seconds())
	if _, err := s.db.Exec(
		`INSERT INTO challenges (challenge, xonly, created_at, expires_at) VALUES (?,?,?,?)`,
		challenge, xonly, now, exp); err != nil {
		return "", 0, err
	}
	return challenge, exp, nil
}

// ConsumeChallenge marks a challenge used, and only succeeds once: the UPDATE
// itself is the single-use guard, so two concurrent presentations cannot both
// pass. It also binds the challenge to the key it was issued for.
func (s *Store) ConsumeChallenge(challenge, xonly string) error {
	res, err := s.db.Exec(
		`UPDATE challenges SET used = 1 WHERE challenge = ? AND xonly = ? AND used = 0 AND expires_at > ?`,
		challenge, xonly, time.Now().Unix())
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("challenge is unknown, expired, or already used")
	}
	return nil
}

// --- audit log ---

// Audit appends one hash-chained row. Rows are never updated or deleted:
// hash = sha256(prev_hash || ts || actor_aid || action || canonical(detail)).
func (s *Store) Audit(actorAID, action string, detail any) error {
	body, err := canonicalJSON(detail)
	if err != nil {
		return err
	}
	s.auditMu.Lock()
	defer s.auditMu.Unlock()

	var prev string
	err = s.db.QueryRow(`SELECT hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&prev)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	ts := time.Now().Unix()
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write([]byte(strconv.FormatInt(ts, 10)))
	h.Write([]byte(actorAID))
	h.Write([]byte(action))
	h.Write(body)
	hash := hex.EncodeToString(h.Sum(nil))
	_, err = s.db.Exec(
		`INSERT INTO audit_log (ts, actor_aid, action, detail, prev_hash, hash) VALUES (?,?,?,?,?,?)`,
		ts, actorAID, action, string(body), prev, hash)
	return err
}

// --- helpers ---

// canonicalJSON encodes v with object keys sorted lexicographically and no
// whitespace, so the same object always hashes to the same value in the SPA and
// here. Numbers keep the literal the caller supplied (JSON has no canonical
// number form); the SPA's JSON.stringify output is that literal.
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var norm any
	if err := dec.Decode(&norm); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, norm); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case string:
		return writeCanonicalString(buf, t)
	case json.Number:
		buf.WriteString(t.String())
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	default:
		return fmt.Errorf("canonical json: unsupported value %T", v)
	}
	return nil
}

// writeCanonicalString matches JSON.stringify: no HTML escaping of < > &.
func writeCanonicalString(buf *bytes.Buffer, s string) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	buf.Write(bytes.TrimRight(tmp.Bytes(), "\n"))
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func rawOrEmpty(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("{}")
	}
	return r
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
