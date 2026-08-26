package main

import (
	"fmt"
	"io"
	"time"
)

// enclave kinds held by seqpald.
const (
	enclaveOfferingEscrow = "offering-escrow"
	enclaveEntityTreasury = "entity-treasury"
)

// createEnclave returns the enclave for (kind, refID), creating it on first use:
// a fresh secp256k1 keypair whose private key seqpald custodies, registered with
// openampd as a user so its AID exists as a valid transfer counterparty. It is
// idempotent: a second call for the same (kind, refID) returns the existing
// enclave, so a deploy retry never mints a second escrow.
func (s *server) createEnclave(kind, refID string) (*EnclaveKey, error) {
	if existing, err := s.st.EnclaveKeyByRef(kind, refID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	priv, xonly, err := newEnclaveKey()
	if err != nil {
		return nil, err
	}
	aid, err := s.registerUser(xonly)
	if err != nil {
		return nil, fmt.Errorf("register enclave: %w", err)
	}
	if want := aidFor([]string{xonly}); aid != want {
		return nil, fmt.Errorf("policy server returned unexpected enclave aid %s (want %s)", aid, want)
	}
	k := &EnclaveKey{AID: aid, Kind: kind, RefID: refID, XOnly: xonly, Priv: priv, CreatedAt: time.Now().Unix()}
	if err := s.st.InsertEnclaveKey(k); err != nil {
		// Lost a race with a concurrent create for the same thing. The unique
		// index is what makes one of them lose; the winner's key is the one the
		// policy server and every later lookup agree on, so it is the one to
		// return. The key this call registered is simply never used.
		if existing, qerr := s.st.EnclaveKeyByRef(kind, refID); qerr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	// The private key is deliberately excluded from the audit detail.
	s.st.Audit("", "enclave.create", map[string]any{"kind": kind, "ref_id": refID, "aid": aid, "xonly": xonly})
	return k, nil
}

// --- small shared utilities --------------------------------------------------

func readAllLimited(r io.Reader, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, max))
}
