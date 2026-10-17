package network

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"regionio/internal/server"
)

const (
	rsaKeyBits              = 1024
	verifyTokenLen          = 4
	defaultSessionServerURL = "https://sessionserver.mojang.com/session/minecraft/hasJoined"
)

var (
	serverKeyOnce sync.Once
	serverPrivKey *rsa.PrivateKey
	serverPubDER  []byte
	serverKeyErr  error

	// sessionServerURL and sessionHTTPClient are package-level so tests can
	// substitute a local httptest.Server without external network calls.
	sessionServerURL  = defaultSessionServerURL
	sessionHTTPClient = &http.Client{Timeout: 10 * time.Second}
)

// sessionProperty is one entry in the Mojang session-server profile properties
// array (for example, signed "textures").
type sessionProperty struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Signature string `json:"signature,omitempty"`
}

type hasJoinedResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Properties []sessionProperty `json:"properties"`
}

// serverKeyPair returns the process-wide 1024-bit RSA private key and its
// X.509 SubjectPublicKeyInfo (DER) encoding.
func serverKeyPair() (*rsa.PrivateKey, []byte, error) {
	serverKeyOnce.Do(func() {
		priv, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
		if err != nil {
			serverKeyErr = err
			return
		}
		der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		if err != nil {
			serverKeyErr = err
			return
		}
		serverPrivKey = priv
		serverPubDER = der
	})
	return serverPrivKey, serverPubDER, serverKeyErr
}

// generateVerifyToken returns a 4-byte random challenge for Encryption Request.
func generateVerifyToken() ([]byte, error) {
	token := make([]byte, verifyTokenLen)
	if _, err := io.ReadFull(rand.Reader, token); err != nil {
		return nil, err
	}
	return token, nil
}

// decryptRSA decrypts PKCS#1 v1.5 ciphertext with the given RSA private key.
func decryptRSA(priv *rsa.PrivateKey, ciphertext []byte) ([]byte, error) {
	return rsa.DecryptPKCS1v15(rand.Reader, priv, ciphertext)
}

// cfb8 implements an 8-bit Cipher Feedback (CFB8) stream cipher on top of an
// AES block cipher, matching Java's "AES/CFB8/NoPadding".
type cfb8 struct {
	block   cipher.Block
	sr      [aes.BlockSize]byte
	enc     [aes.BlockSize]byte
	decrypt bool
}

func newCFB8Encrypter(block cipher.Block, iv []byte) cipher.Stream {
	c := &cfb8{block: block}
	copy(c.sr[:], iv)
	return c
}

func newCFB8Decrypter(block cipher.Block, iv []byte) cipher.Stream {
	c := &cfb8{block: block, decrypt: true}
	copy(c.sr[:], iv)
	return c
}

func (c *cfb8) XORKeyStream(dst, src []byte) {
	if len(dst) < len(src) {
		panic("network: cfb8 output smaller than input")
	}
	for i := 0; i < len(src); i++ {
		c.block.Encrypt(c.enc[:], c.sr[:])
		in := src[i]
		out := in ^ c.enc[0]
		dst[i] = out
		copy(c.sr[:aes.BlockSize-1], c.sr[1:])
		if c.decrypt {
			c.sr[aes.BlockSize-1] = in
		} else {
			c.sr[aes.BlockSize-1] = out
		}
	}
}

// minecraftServerHash computes the SHA-1 server ID hash used by Mojang's
// session server: SHA-1(serverID || sharedSecret || publicKeyDER) formatted as
// a signed two's-complement hexadecimal string.
func minecraftServerHash(serverID string, publicKeyDER, sharedSecret []byte) string {
	h := sha1.New()
	h.Write([]byte(serverID))
	h.Write(sharedSecret)
	h.Write(publicKeyDER)
	var sum [sha1.Size]byte
	h.Sum(sum[:0])
	return minecraftHexDigest(sum)
}

// minecraftHexDigest formats a 20-byte SHA-1 digest identically to Java's
// new BigInteger(digest).toString(16): big-endian signed two's-complement hex
// without leading zeros.
func minecraftHexDigest(sum [sha1.Size]byte) string {
	if sum[0]&0x80 == 0 {
		return new(big.Int).SetBytes(sum[:]).Text(16)
	}
	var twos [sha1.Size]byte
	for i, b := range sum {
		twos[i] = ^b
	}
	n := new(big.Int).SetBytes(twos[:])
	n.Add(n, big.NewInt(1))
	return "-" + n.Text(16)
}

// verifyMojangSession queries Mojang's session server (hasJoined) to verify
// that the client authenticated the shared secret with Mojang.
func verifyMojangSession(ctx context.Context, username, serverHash string) (server.Profile, []sessionProperty, error) {
	u, err := url.Parse(sessionServerURL)
	if err != nil {
		return server.Profile{}, nil, err
	}
	q := u.Query()
	q.Set("username", username)
	q.Set("serverId", serverHash)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return server.Profile{}, nil, err
	}
	resp, err := sessionHTTPClient.Do(req)
	if err != nil {
		return server.Profile{}, nil, fmt.Errorf("sessionserver request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return server.Profile{}, nil, fmt.Errorf("sessionserver status %d for %q", resp.StatusCode, username)
	}

	var payload hasJoinedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&payload); err != nil {
		return server.Profile{}, nil, fmt.Errorf("decode sessionserver response: %w", err)
	}
	if !validPlayerName(payload.Name) {
		return server.Profile{}, nil, errors.New("invalid username in sessionserver response")
	}
	uuid, err := parseUUIDHex(payload.ID)
	if err != nil {
		return server.Profile{}, nil, fmt.Errorf("invalid uuid in sessionserver response: %w", err)
	}
	return server.Profile{UUID: uuid, Name: payload.Name}, payload.Properties, nil
}

// parseUUIDHex parses a 128-bit UUID in either 32-char undashed hex or
// 36-char dashed 8-4-4-4-12 format.
func parseUUIDHex(s string) ([16]byte, error) {
	var out [16]byte
	clean := strings.ReplaceAll(s, "-", "")
	if len(clean) != 32 {
		return out, errors.New("invalid UUID length")
	}
	if _, err := hex.Decode(out[:], []byte(clean)); err != nil {
		return out, err
	}
	return out, nil
}
