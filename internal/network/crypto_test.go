package network

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

// TestCFB8NISTVectors verifies our 8-bit CFB implementation against NIST SP
// 800-38A Section F.3.7 & F.3.8 (CFB8-AES128.Encrypt / Decrypt).
func TestCFB8NISTVectors(t *testing.T) {
	key, err := hex.DecodeString("2b7e151628aed2a6abf7158809cf4f3c")
	if err != nil {
		t.Fatal(err)
	}
	iv, err := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := hex.DecodeString("6bc1bee22e409f96e93d7e117393172aae2d")
	if err != nil {
		t.Fatal(err)
	}
	wantCiphertext, err := hex.DecodeString("3b79424c9c0dd436bace9e0ed4586a4f32b9")
	if err != nil {
		t.Fatal(err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	gotCipher := make([]byte, len(plaintext))
	enc := newCFB8Encrypter(block, iv)
	enc.XORKeyStream(gotCipher, plaintext)
	if !bytes.Equal(gotCipher, wantCiphertext) {
		t.Fatalf("CFB8 encrypt = %x, want %x", gotCipher, wantCiphertext)
	}

	// Verify byte-by-byte streaming produces the exact same result.
	dec := newCFB8Decrypter(block, iv)
	gotPlain := make([]byte, len(wantCiphertext))
	for i := range wantCiphertext {
		dec.XORKeyStream(gotPlain[i:i+1], wantCiphertext[i:i+1])
	}
	if !bytes.Equal(gotPlain, plaintext) {
		t.Fatalf("CFB8 byte-by-byte decrypt = %x, want %x", gotPlain, plaintext)
	}

	// Verify in-place encryption and decryption (dst == src).
	inPlace := append([]byte(nil), plaintext...)
	newCFB8Encrypter(block, iv).XORKeyStream(inPlace, inPlace)
	if !bytes.Equal(inPlace, wantCiphertext) {
		t.Fatalf("CFB8 in-place encrypt = %x, want %x", inPlace, wantCiphertext)
	}
	newCFB8Decrypter(block, iv).XORKeyStream(inPlace, inPlace)
	if !bytes.Equal(inPlace, plaintext) {
		t.Fatalf("CFB8 in-place decrypt = %x, want %x", inPlace, plaintext)
	}
}

// TestMinecraftServerHash verifies Minecraft's signed two's-complement SHA-1
// hex formatting against the standard protocol specification vectors.
func TestMinecraftServerHash(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Notch", "4ed1f46bbe04bc756bcb17c0c7ce3e4632f06a48"},
		{"jeb_", "-7c9d5b0044c130109a5d7b5fb5c317c02b4e28c1"},
		{"simon", "88e16a1019277b15d58faf0541e11910eb756f6"},
	}
	for _, tc := range cases {
		got := minecraftHexDigest(sha1.Sum([]byte(tc.input)))
		if got != tc.want {
			t.Errorf("minecraftHexDigest(SHA1(%q)) = %q, want %q", tc.input, got, tc.want)
		}
	}

	// Verify edge cases: all-zero digest, -1 (all 0xff), and -2^159 (0x8000...00).
	var zero [sha1.Size]byte
	if got := minecraftHexDigest(zero); got != "0" {
		t.Errorf("zero digest = %q, want \"0\"", got)
	}
	var allOnes [sha1.Size]byte
	for i := range allOnes {
		allOnes[i] = 0xff
	}
	if got := minecraftHexDigest(allOnes); got != "-1" {
		t.Errorf("all-0xff digest = %q, want \"-1\"", got)
	}
	var minNeg [sha1.Size]byte
	minNeg[0] = 0x80
	if got, want := minecraftHexDigest(minNeg), "-8000000000000000000000000000000000000000"; got != want {
		t.Errorf("minNeg digest = %q, want %q", got, want)
	}

	// Verify 3-part concatenation (serverID || sharedSecret || pubKeyDER).
	combined := minecraftServerHash("", []byte("BC"), []byte("Notch"[:3]))
	if want := minecraftHexDigest(sha1.Sum([]byte("NotBC"))); combined != want {
		t.Errorf("minecraftServerHash = %q, want %q", combined, want)
	}
}

// TestConnEncryptedSendFramedDoesNotMutateSharedBuffer verifies that encrypting
// an outbound frame never mutates the caller's slice (critical for cached chunk
// frames sent via SendFramed).
func TestConnEncryptedSendFramedDoesNotMutateSharedBuffer(t *testing.T) {
	raw := &recordingConn{}
	conn := NewConn(raw)
	defer conn.Close()

	secret := []byte("0123456789abcdef")
	if err := conn.EnableEncryption(secret); err != nil {
		t.Fatal(err)
	}

	shared := protocol.AppendPacket(nil, -1, 0x2d, []byte("cached-chunk-payload"))
	original := append([]byte(nil), shared...)
	if err := conn.SendFramed(shared); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shared, original) {
		t.Fatal("SendFramed mutated the caller's shared buffer")
	}

	raw.mu.Lock()
	wire := append([]byte(nil), raw.buf.Bytes()...)
	raw.mu.Unlock()
	if bytes.Equal(wire, original) {
		t.Fatal("wire bytes were not encrypted")
	}

	block, err := aes.NewCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	decrypted := make([]byte, len(wire))
	newCFB8Decrypter(block, secret).XORKeyStream(decrypted, wire)
	if !bytes.Equal(decrypted, original) {
		t.Fatalf("decrypted wire = %x, want %x", decrypted, original)
	}
}

// TestOnlineModeLoginHandshakeWithFakeSessionServer drives a complete online-mode
// login handshake against an httptest session server, including RSA key exchange,
// AES/CFB8 stream activation, compression negotiation, and Login Success with
// signed profile properties.
func TestOnlineModeLoginHandshakeWithFakeSessionServer(t *testing.T) {
	const (
		wantName      = "Notch"
		wantUUIDHex   = "069a79f444e94726a5befca90e38aaf5"
		wantTexValue  = "dGV4dHVyZXM="
		wantTexSig    = "c2lnbmF0dXJl"
		compressThres = 256
	)
	sharedSecret := []byte("16-byte-aes-key!")

	var (
		gotUsername   string
		gotServerHash string
		sessionCalls  atomic.Int32
	)
	fakeMojang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionCalls.Add(1)
		gotUsername = r.URL.Query().Get("username")
		gotServerHash = r.URL.Query().Get("serverId")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hasJoinedResponse{
			ID:   wantUUIDHex,
			Name: wantName,
			Properties: []sessionProperty{
				{Name: "textures", Value: wantTexValue, Signature: wantTexSig},
			},
		})
	}))
	defer fakeMojang.Close()

	origURL := sessionServerURL
	origClient := sessionHTTPClient
	sessionServerURL = fakeMojang.URL
	sessionHTTPClient = fakeMojang.Client()
	defer func() {
		sessionServerURL = origURL
		sessionHTTPClient = origClient
	}()

	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.OnlineMode = true
	cfg.CompressionThreshold = compressThres
	srv, err := server.NewWithCache(cfg, world.NewCache(compressThres, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()

	h := &handler{
		conn: NewConn(serverSide),
		srv:  srv,
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		h.serve()
	}()
	defer func() {
		_ = clientSide.Close()
		<-serveDone
	}()

	clientBr := bufio.NewReader(clientSide)

	// 1. Handshake → Login
	hs := protocol.NewWriter(16).
		VarInt(protocol.ProtocolVersion).
		String("localhost").
		Uint16(25565).
		VarInt(protocol.NextStateLogin)
	if err := protocol.WritePacket(clientSide, -1, protocol.HandshakeID, hs.Bytes()); err != nil {
		t.Fatal(err)
	}

	// 2. Login Start
	ls := protocol.NewWriter(32).
		String(wantName).
		UUID(server.OfflineUUID(wantName))
	if err := protocol.WritePacket(clientSide, -1, protocol.LoginStartID, ls.Bytes()); err != nil {
		t.Fatal(err)
	}

	// 3. Read Encryption Request (0x01, unencrypted, uncompressed)
	encReq, err := protocol.ReadPacket(clientBr, -1)
	if err != nil {
		t.Fatalf("read EncryptionRequest: %v", err)
	}
	if encReq.ID != protocol.EncryptionRequest {
		t.Fatalf("packet ID = %#x, want EncryptionRequest (%#x)", encReq.ID, protocol.EncryptionRequest)
	}
	reqReader := encReq.Body()
	serverID, err := reqReader.String()
	if err != nil {
		t.Fatal(err)
	}
	if serverID != "" {
		t.Fatalf("serverID = %q, want empty string", serverID)
	}
	pubDER, err := readLoginByteArray(reqReader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	verifyToken, err := readLoginByteArray(reqReader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	shouldAuth, err := reqReader.Bool()
	if err != nil {
		t.Fatal(err)
	}
	if !shouldAuth {
		t.Fatal("shouldAuthenticate = false, want true")
	}

	parsedKey, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		t.Fatalf("parse server public key: %v", err)
	}
	rsaPub, ok := parsedKey.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("public key type = %T, want *rsa.PublicKey", parsedKey)
	}

	encSecret, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, sharedSecret)
	if err != nil {
		t.Fatal(err)
	}
	encToken, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, verifyToken)
	if err != nil {
		t.Fatal(err)
	}

	// 4. Send Encryption Response (0x01, unencrypted, uncompressed)
	encResp := protocol.NewWriter(16 + len(encSecret) + len(encToken)).
		VarInt(int32(len(encSecret))).Raw(encSecret).
		VarInt(int32(len(encToken))).Raw(encToken)
	if err := protocol.WritePacket(clientSide, -1, protocol.EncryptionResponse, encResp.Bytes()); err != nil {
		t.Fatal(err)
	}

	// 5. Enable AES-128/CFB8 on the client side of the pipe.
	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		t.Fatal(err)
	}
	clientDec := cipher.StreamReader{S: newCFB8Decrypter(block, sharedSecret), R: clientBr}
	clientEnc := cipher.StreamWriter{S: newCFB8Encrypter(block, sharedSecret), W: clientSide}
	encClientBr := bufio.NewReader(clientDec)

	// 6. Read Set Compression (0x03, encrypted, uncompressed framing)
	setComp, err := protocol.ReadPacket(encClientBr, -1)
	if err != nil {
		t.Fatalf("read SetCompression: %v", err)
	}
	if setComp.ID != protocol.SetCompressionID {
		t.Fatalf("packet ID = %#x, want SetCompression (%#x)", setComp.ID, protocol.SetCompressionID)
	}
	gotThreshold, err := setComp.Body().VarInt()
	if err != nil || gotThreshold != compressThres {
		t.Fatalf("compression threshold = %d (%v), want %d", gotThreshold, err, compressThres)
	}

	// 7. Read Login Success (0x02, encrypted, compressed framing)
	loginSucc, err := protocol.ReadPacket(encClientBr, compressThres)
	if err != nil {
		t.Fatalf("read LoginSuccess: %v", err)
	}
	if loginSucc.ID != protocol.LoginSuccessID {
		t.Fatalf("packet ID = %#x, want LoginSuccess (%#x)", loginSucc.ID, protocol.LoginSuccessID)
	}
	succReader := loginSucc.Body()
	gotUUID, err := succReader.UUID()
	if err != nil {
		t.Fatal(err)
	}
	wantUUID, err := parseUUIDHex(wantUUIDHex)
	if err != nil {
		t.Fatal(err)
	}
	if gotUUID != wantUUID {
		t.Fatalf("LoginSuccess UUID = %x, want %x", gotUUID, wantUUID)
	}
	gotPlayerName, err := succReader.String()
	if err != nil || gotPlayerName != wantName {
		t.Fatalf("LoginSuccess name = %q (%v), want %q", gotPlayerName, err, wantName)
	}
	propCount, err := succReader.VarInt()
	if err != nil || propCount != 1 {
		t.Fatalf("LoginSuccess propCount = %d (%v), want 1", propCount, err)
	}
	propName, _ := succReader.String()
	propVal, _ := succReader.String()
	hasSig, _ := succReader.Bool()
	propSig, _ := succReader.String()
	if propName != "textures" || propVal != wantTexValue || !hasSig || propSig != wantTexSig {
		t.Fatalf("property = (%q, %q, %v, %q)", propName, propVal, hasSig, propSig)
	}

	// Verify what fake sessionserver received.
	if sessionCalls.Load() != 1 {
		t.Fatalf("sessionserver calls = %d, want 1", sessionCalls.Load())
	}
	wantHash := minecraftServerHash("", pubDER, sharedSecret)
	if gotUsername != wantName || gotServerHash != wantHash {
		t.Fatalf("sessionserver query = (user=%q, serverId=%q), want (user=%q, serverId=%q)",
			gotUsername, gotServerHash, wantName, wantHash)
	}

	// 8. Send Login Acknowledged (0x03, encrypted, compressed) → Configuration
	if err := protocol.WritePacket(clientEnc, compressThres, protocol.LoginAcknowledgedID, nil); err != nil {
		t.Fatal(err)
	}
	cfgPkt, err := protocol.ReadPacket(encClientBr, compressThres)
	if err != nil {
		t.Fatalf("read first configuration packet: %v", err)
	}
	if cfgPkt.ID != protocol.ConfigCustomPayloadCB {
		t.Fatalf("first config packet ID = %#x, want ConfigCustomPayloadCB (%#x)", cfgPkt.ID, protocol.ConfigCustomPayloadCB)
	}
	if h.conn.State() != protocol.StateConfiguration {
		t.Fatalf("conn state = %v, want StateConfiguration", h.conn.State())
	}
}

// TestOnlineModeRejectsInvalidVerifyTokenAndUnverifiedSession verifies that a
// tampered challenge or a non-200 sessionserver response terminates login.
func TestOnlineModeRejectsInvalidVerifyTokenAndUnverifiedSession(t *testing.T) {
	fakeMojang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // 204 = unverified session
	}))
	defer fakeMojang.Close()

	origURL := sessionServerURL
	origClient := sessionHTTPClient
	sessionServerURL = fakeMojang.URL
	sessionHTTPClient = fakeMojang.Client()
	defer func() {
		sessionServerURL = origURL
		sessionHTTPClient = origClient
	}()

	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.OnlineMode = true
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	newOnlineHandler := func() (*handler, *recordingConn) {
		rec := &recordingConn{}
		h := &handler{
			conn:            NewConn(rec),
			srv:             srv,
			log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
			protocolVersion: protocol.ProtocolVersion,
		}
		h.conn.SetState(protocol.StateLogin)
		return h, rec
	}

	// Case 1: LoginAcknowledged before LoginStart / EncryptionResponse is rejected.
	h1, _ := newOnlineHandler()
	defer h1.conn.Close()
	if err := h1.handleLogin(protocol.Packet{ID: protocol.LoginAcknowledgedID}); err == nil {
		t.Fatal("accepted LoginAcknowledged before login completed")
	}

	// Case 2: Wrong verify token in EncryptionResponse is rejected.
	h2, rec2 := newOnlineHandler()
	defer h2.conn.Close()
	ls := protocol.NewWriter(32).String("Steve").UUID(server.OfflineUUID("Steve"))
	if err := h2.handleLogin(protocol.Packet{ID: protocol.LoginStartID, Data: ls.Bytes()}); err != nil {
		t.Fatal(err)
	}
	_ = rec2.take(t) // EncryptionRequest
	_, pubDER, err := serverKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		t.Fatal(err)
	}
	rsaPub := pub.(*rsa.PublicKey)
	encSecret, _ := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, []byte("0123456789abcdef"))
	badToken, _ := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, []byte{9, 9, 9, 9})
	resp := protocol.NewWriter(300).
		VarInt(int32(len(encSecret))).Raw(encSecret).
		VarInt(int32(len(badToken))).Raw(badToken)
	if err := h2.handleLogin(protocol.Packet{ID: protocol.EncryptionResponse, Data: resp.Bytes()}); err == nil {
		t.Fatal("accepted EncryptionResponse with wrong verify token")
	}

	// Case 3: Valid verify token, but sessionserver returns 204 No Content.
	h3, rec3 := newOnlineHandler()
	defer h3.conn.Close()
	if err := h3.handleLogin(protocol.Packet{ID: protocol.LoginStartID, Data: ls.Bytes()}); err != nil {
		t.Fatal(err)
	}
	reqPkt := rec3.take(t)[0]
	r := reqPkt.Body()
	_, _ = r.String()
	_, _ = readLoginByteArray(r, 1024)
	validToken, err := readLoginByteArray(r, 1024)
	if err != nil {
		t.Fatal(err)
	}
	encValidToken, _ := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, validToken)
	resp3 := protocol.NewWriter(300).
		VarInt(int32(len(encSecret))).Raw(encSecret).
		VarInt(int32(len(encValidToken))).Raw(encValidToken)
	if err := h3.handleLogin(protocol.Packet{ID: protocol.EncryptionResponse, Data: resp3.Bytes()}); err == nil {
		t.Fatal("accepted login when sessionserver returned 204")
	}
}
