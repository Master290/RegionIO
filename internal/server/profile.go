package server

import (
	"crypto/md5"
)

// Profile is an authenticated (or, in offline mode, derived) player identity.
type Profile struct {
	UUID [16]byte
	Name string
}

// OfflineUUID derives the stable offline-mode UUID for a username, matching
// vanilla's java.util.UUID.nameUUIDFromBytes("OfflinePlayer:" + name): an
// MD5-based (version 3) UUID with the IETF variant bits set.
func OfflineUUID(name string) [16]byte {
	sum := md5.Sum([]byte("OfflinePlayer:" + name))
	sum[6] = (sum[6] & 0x0f) | 0x30 // version 3
	sum[8] = (sum[8] & 0x3f) | 0x80 // IETF variant
	return sum
}
