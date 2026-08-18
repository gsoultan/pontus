package protocol

import "encoding/binary"

// CancelKey identifies the backend process a cancel request is aimed at.
//
// Pontus forwards the backend's own BackendKeyData to the client rather than
// inventing its own, so these are the *backend's* real values and the backend
// will accept them. What Pontus has to supply is the routing: which server that
// process is on. Nothing in the packet says.
type CancelKey struct {
	ProcessID uint32
	Secret    uint32

	// Raw is the packet as it arrived, forwarded unchanged. The secret is the
	// only authorisation there is, and it is the backend's to check — Pontus
	// relays rather than validates.
	Raw []byte
}

// cancelKeyLen is the fixed size of a CancelRequest: length, request code,
// process id, secret.
const cancelKeyLen = 16

// parseCancelKey reads a CancelRequest packet.
func parseCancelKey(raw []byte) *CancelKey {
	if len(raw) < cancelKeyLen {
		return nil
	}
	return &CancelKey{
		ProcessID: binary.BigEndian.Uint32(raw[8:12]),
		Secret:    binary.BigEndian.Uint32(raw[12:16]),
		Raw:       append([]byte(nil), raw[:cancelKeyLen]...),
	}
}

// BackendKeyProcessID reads the process id out of a raw BackendKeyData payload,
// which is the process id and secret with no header.
func BackendKeyProcessID(payload []byte) (uint32, bool) {
	if len(payload) < 8 {
		return 0, false
	}
	return binary.BigEndian.Uint32(payload[:4]), true
}
