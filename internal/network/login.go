package network

import (
	"context"
	"crypto/aes"
	"crypto/subtle"
	"errors"
	"fmt"

	"regionio/internal/protocol"
	"regionio/internal/server"
)

const (
	loginStepStart = iota
	loginStepWaitKey
	loginStepReady
)

// handleLogin drives the login phase in both offline and online modes:
//
//	Offline mode:
//	  C→S Login Start        → derive offline profile
//	  S→C Set Compression    → (optional) enable zlib for later packets
//	  S→C Login Success      → confirm the profile
//	  C→S Login Acknowledged → switch to the Configuration phase
//
//	Online mode:
//	  C→S Login Start        → send Encryption Request (RSA pubkey + token)
//	  C→S Encryption Response→ verify token, enable AES/CFB8, check hasJoined
//	  S→C Set Compression    → (optional) enable zlib for later packets
//	  S→C Login Success      → confirm the Mojang-authenticated profile
//	  C→S Login Acknowledged → switch to the Configuration phase
func (h *handler) handleLogin(pkt protocol.Packet) error {
	switch pkt.ID {
	case protocol.LoginStartID:
		return h.handleLoginStart(pkt)
	case protocol.EncryptionResponse:
		return h.handleEncryptionResponse(pkt)
	case protocol.LoginAcknowledgedID:
		if h.conn.loginStep != loginStepReady {
			return errors.New("unexpected login acknowledged packet")
		}
		// Client acknowledges Login Success; both sides enter Configuration.
		h.conn.SetState(protocol.StateConfiguration)
		h.log.Info("player logged in",
			"name", h.conn.Profile.Name,
			"uuid", uuidString(h.conn.Profile.UUID))
		return h.beginConfiguration()
	default:
		return errors.New("unexpected packet in login state")
	}
}

func (h *handler) handleLoginStart(pkt protocol.Packet) error {
	if h.conn.loginStep != loginStepStart {
		return errors.New("unexpected login start packet")
	}
	if h.protocolVersion != protocol.ProtocolVersion {
		return fmt.Errorf("unsupported protocol %d, want %d", h.protocolVersion, protocol.ProtocolVersion)
	}
	r := pkt.Body()
	name, err := r.String()
	if err != nil {
		return err
	}
	if !validPlayerName(name) {
		return errors.New("invalid login name")
	}
	// The client also sends a UUID; in offline mode we derive our own, and in
	// online mode Mojang's session server is authoritative.
	if _, err := r.UUID(); err != nil {
		return err
	}

	if h.srv.Config().OnlineMode {
		_, pubDER, err := serverKeyPair()
		if err != nil {
			return fmt.Errorf("server keypair: %w", err)
		}
		token, err := generateVerifyToken()
		if err != nil {
			return fmt.Errorf("verify token: %w", err)
		}
		h.conn.loginName = name
		h.conn.verifyToken = token
		h.conn.loginStep = loginStepWaitKey
		return h.sendEncryptionRequest(pubDER, token)
	}

	h.conn.Profile = server.Profile{
		UUID: server.OfflineUUID(name),
		Name: name,
	}
	return h.finishLogin()
}

func (h *handler) sendEncryptionRequest(pubKeyDER, verifyToken []byte) error {
	w := protocol.NewWriter(8 + len(pubKeyDER) + len(verifyToken))
	w.String("") // empty server ID
	w.VarInt(int32(len(pubKeyDER)))
	w.Raw(pubKeyDER)
	w.VarInt(int32(len(verifyToken)))
	w.Raw(verifyToken)
	w.Bool(true) // shouldAuthenticate
	return h.conn.SendWriter(protocol.EncryptionRequest, w)
}

func (h *handler) handleEncryptionResponse(pkt protocol.Packet) error {
	if !h.srv.Config().OnlineMode || h.conn.loginStep != loginStepWaitKey || len(h.conn.verifyToken) == 0 {
		return errors.New("unexpected encryption response")
	}
	r := pkt.Body()
	encryptedSecret, err := readLoginByteArray(r, 1024)
	if err != nil {
		return err
	}
	encryptedToken, err := readLoginByteArray(r, 1024)
	if err != nil {
		return err
	}
	if r.Remaining() != 0 {
		return errors.New("trailing bytes in encryption response")
	}

	expectedToken := h.conn.verifyToken
	h.conn.verifyToken = nil

	privKey, pubDER, err := serverKeyPair()
	if err != nil {
		return err
	}
	token, err := decryptRSA(privKey, encryptedToken)
	if err != nil || subtle.ConstantTimeCompare(token, expectedToken) != 1 {
		return errors.New("verify token mismatch")
	}
	sharedSecret, err := decryptRSA(privKey, encryptedSecret)
	if err != nil || len(sharedSecret) != aes.BlockSize {
		return errors.New("invalid shared secret")
	}

	if err := h.conn.EnableEncryption(sharedSecret); err != nil {
		return err
	}

	serverHash := minecraftServerHash("", pubDER, sharedSecret)
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	profile, props, err := verifyMojangSession(ctx, h.conn.loginName, serverHash)
	if err != nil {
		return fmt.Errorf("session verification failed: %w", err)
	}
	h.conn.Profile = profile
	h.conn.loginProps = props
	return h.finishLogin()
}

func (h *handler) finishLogin() error {
	// Negotiate compression before Login Success so that packet (and every
	// later one) is sent in the compressed format.
	if t := h.srv.Config().CompressionThreshold; t >= 0 {
		w := protocol.NewWriter(protocol.VarIntLen(int32(t)))
		w.VarInt(int32(t))
		if err := h.conn.SendWriter(protocol.SetCompressionID, w); err != nil {
			return err
		}
		h.conn.EnableCompression(int32(t))
	}
	h.conn.loginStep = loginStepReady
	return h.sendLoginSuccess()
}

func readLoginByteArray(r *protocol.Reader, maxLen int) ([]byte, error) {
	n, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if n <= 0 || int(n) > maxLen || r.Remaining() < int(n) {
		return nil, errors.New("invalid byte array length")
	}
	out := make([]byte, n)
	for i := range out {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}

func validPlayerName(name string) bool {
	if len(name) == 0 || len(name) > 16 {
		return false
	}
	for _, r := range name {
		if r != '_' && (r < '0' || r > '9') && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

// sendLoginSuccess writes the Login Success packet. For protocol 775 the body
// is: UUID, Username, then a VarInt-prefixed array of profile properties.
func (h *handler) sendLoginSuccess() error {
	p := h.conn.Profile
	props := h.conn.loginProps
	w := protocol.NewWriter(16 + len(p.Name) + 4)
	w.UUID(p.UUID)
	w.String(p.Name)
	w.VarInt(int32(len(props)))
	for _, prop := range props {
		w.String(prop.Name)
		w.String(prop.Value)
		if prop.Signature != "" {
			w.Bool(true)
			w.String(prop.Signature)
		} else {
			w.Bool(false)
		}
	}
	return h.conn.SendWriter(protocol.LoginSuccessID, w)
}

// uuidString formats a UUID as the canonical 8-4-4-4-12 hex string.
func uuidString(u [16]byte) string {
	const hexdigits = "0123456789abcdef"
	var b [36]byte
	j := 0
	for i := 0; i < 16; i++ {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			b[j] = '-'
			j++
		}
		b[j] = hexdigits[u[i]>>4]
		b[j+1] = hexdigits[u[i]&0x0f]
		j += 2
	}
	return string(b[:j])
}
