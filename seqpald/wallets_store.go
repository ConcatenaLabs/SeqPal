package main

import (
	"database/sql"
	"strings"
	"time"
)

// The wallets a SeqPal ID is held in.
//
// One identity, more than one wallet. A holder who opened their ID with a web
// wallet and also runs the browser extension is one person with one set of
// obligations, and making them keep two SeqPal IDs would say otherwise: two
// passports, two verifications, two sets of eligibility, for one human being.
//
// Every wallet here has been proven, in the way its kind allows:
//
//	"descriptor"  a public wallet descriptor, proven by an ordinary signed
//	              message from an address it derives.
//	"enclave"     an OpenAMP account, proven by a tagged BIP340 challenge from
//	              its enclave key. Having one of these is what lets the ID hold
//	              restricted assets.
//
// The account id never changes when a wallet is added: it is the id the account
// has always had, and every record pointing at it keeps pointing at it.
type AccountWallet struct {
	ID   string `json:"id"`
	AID  string `json:"aid"`
	Kind string `json:"kind"`
	// Descriptor is set for kind "descriptor": canonical, with its checksum.
	Descriptor string `json:"descriptor,omitempty"`
	// XOnly is set for kind "enclave": the m/5/0 key the policy server knows.
	XOnly string `json:"xonly,omitempty"`
	// EnclaveAID is the OpenAMP account id derived from XOnly, which is NOT this
	// SeqPal account's id and must never be confused with it.
	EnclaveAID string `json:"enclave_aid,omitempty"`
	// DescriptorKey is Descriptor normalised to its pkh form: the wallet's
	// identity, independent of which script type it was presented as. Lookups go
	// through this, or one wallet answers to one name and not the other.
	DescriptorKey string `json:"-"`
	Label         string `json:"label,omitempty"`
	Proof         string `json:"proof,omitempty"`
	CreatedAt     int64  `json:"created_at"`
}

const accountWalletCols = `id, aid, kind, descriptor, descriptor_key, xonly, enclave_aid, label, proof, created_at`

func (s *Store) InsertAccountWallet(wl *AccountWallet) error {
	if wl.CreatedAt == 0 {
		wl.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO account_wallets (`+accountWalletCols+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		wl.ID, wl.AID, wl.Kind, wl.Descriptor, descriptorKeyOf(wl), wl.XOnly, wl.EnclaveAID,
		wl.Label, wl.Proof, wl.CreatedAt)
	return err
}

func (s *Store) AccountWallets(aid string) ([]*AccountWallet, error) {
	rows, err := s.db.Query(
		`SELECT `+accountWalletCols+` FROM account_wallets WHERE aid = ? ORDER BY created_at`, aid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AccountWallet{}
	for rows.Next() {
		var wl AccountWallet
		if err := rows.Scan(&wl.ID, &wl.AID, &wl.Kind, &wl.Descriptor, &wl.DescriptorKey, &wl.XOnly,
			&wl.EnclaveAID, &wl.Label, &wl.Proof, &wl.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &wl)
	}
	return out, rows.Err()
}

func (s *Store) AccountWalletByID(id string) (*AccountWallet, error) {
	return scanAccountWallet(s.db.QueryRow(
		`SELECT `+accountWalletCols+` FROM account_wallets WHERE id = ?`, id))
}

// AccountByDescriptor finds whose wallet a descriptor is. This is what lets a
// holder sign in with any wallet they have linked and land in the same account,
// rather than in a second one derived from that descriptor.
func (s *Store) AccountByDescriptor(desc string) (*Account, error) {
	var aid string
	err := s.db.QueryRow(
		`SELECT aid FROM account_wallets WHERE kind = 'descriptor' AND descriptor_key = ?`,
		normaliseDescriptorKey(desc)).Scan(&aid)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.AccountByAID(aid)
}

// AccountByEnclaveKey finds whose wallet an enclave key is, for the same reason.
func (s *Store) AccountByEnclaveKey(xonly string) (*Account, error) {
	var aid string
	err := s.db.QueryRow(
		`SELECT aid FROM account_wallets WHERE kind = 'enclave' AND xonly = ?`,
		strings.ToLower(strings.TrimSpace(xonly))).Scan(&aid)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.AccountByAID(aid)
}

// HasEnclaveWallet reports whether this ID holds any OpenAMP account at all,
// which is what decides whether restricted assets are within its reach.
func (s *Store) HasEnclaveWallet(aid string) (bool, error) {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM account_wallets WHERE aid = ? AND kind = 'enclave'`, aid).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// DescriptorWallets are the linked wallets whose addresses can be derived, which
// is how a holding key can be recognised as one this account already controls.
func (s *Store) DescriptorWallets(aid string) ([]*AccountWallet, error) {
	all, err := s.AccountWallets(aid)
	if err != nil {
		return nil, err
	}
	out := make([]*AccountWallet, 0, len(all))
	for _, wl := range all {
		if wl.Kind == "descriptor" && wl.Descriptor != "" {
			out = append(out, wl)
		}
	}
	return out, nil
}

func (s *Store) DeleteAccountWallet(id, aid string) error {
	_, err := s.db.Exec(`DELETE FROM account_wallets WHERE id = ? AND aid = ?`, id, aid)
	return err
}

// descriptorKeyOf is what a wallet row is looked up by: nothing for an enclave,
// the normalised descriptor for a wallet.
func descriptorKeyOf(wl *AccountWallet) string {
	if wl.Kind != "descriptor" || wl.Descriptor == "" {
		return ""
	}
	return normaliseDescriptorKey(wl.Descriptor)
}

// normaliseDescriptorKey reduces a descriptor to the wallet it names, dropping
// the script type it was written in. Kept in step with toPKH, which does the
// same thing for the address derivation.
func normaliseDescriptorKey(desc string) string {
	return toPKH(desc)
}

func scanAccountWallet(row *sql.Row) (*AccountWallet, error) {
	var wl AccountWallet
	err := row.Scan(&wl.ID, &wl.AID, &wl.Kind, &wl.Descriptor, &wl.DescriptorKey, &wl.XOnly,
		&wl.EnclaveAID, &wl.Label, &wl.Proof, &wl.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &wl, nil
}

// SeqPalAIDsByEnclaveAID maps the account ids the POLICY SERVER uses back to the
// SeqPal IDs that hold them. The register is keyed the policy server's way, and
// a holder's passport shows the SeqPal id: without this, an issuer looking at
// their own cap table cannot tell which verified identity a row belongs to, and
// a holder cannot find themselves on it. They were the same string until a
// SeqPal ID could be founded on a wallet.
//
// Only ids that resolve appear. A row for an account this platform did not
// register -- a holder who never had a SeqPal ID -- simply has no entry.
func (s *Store) SeqPalAIDsByEnclaveAID(enclaveAIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(enclaveAIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(
		`SELECT enclave_aid, aid FROM account_wallets WHERE kind = 'enclave' AND enclave_aid != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	held := map[string]string{}
	for rows.Next() {
		var oaid, aid string
		if err := rows.Scan(&oaid, &aid); err != nil {
			return nil, err
		}
		held[oaid] = aid
	}
	for _, oaid := range enclaveAIDs {
		if aid, ok := held[oaid]; ok && aid != oaid {
			out[oaid] = aid
		}
	}
	return out, rows.Err()
}
