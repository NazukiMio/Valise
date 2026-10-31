package vault

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Relative paths inside a vault.
const (
	DescriptorFile = "VAULT"
	EpochsDir      = "epochs"
	KeysDir        = "keys"
	PacksDir       = "packs"
	IndexDir       = "index"
	SnapshotsDir   = "snapshots"
)

// EpochPath is epochs/<n>.json.
func EpochPath(n int) string { return path.Join(EpochsDir, strconv.Itoa(n)+".json") }

// DedupeKeyPath is keys/dedupe.<n>.age.
func DedupeKeyPath(n int) string { return path.Join(KeysDir, "dedupe."+strconv.Itoa(n)+".age") }

// PackPath is packs/<first 2>/<id>.pack.
func PackPath(id string) string { return path.Join(PacksDir, id[:2], id+".pack") }

// IndexPath is index/<snapshot_id>.json.
func IndexPath(snapshotID string) string { return path.Join(IndexDir, snapshotID+".json") }

// SnapshotPath is snapshots/<snapshot_id>.age.
func SnapshotPath(snapshotID string) string { return path.Join(SnapshotsDir, snapshotID+".age") }

// MetaPath is snapshots/<snapshot_id>.meta.json.
func MetaPath(snapshotID string) string { return path.Join(SnapshotsDir, snapshotID+".meta.json") }

var (
	idRe   = regexp.MustCompile(`^[vs]_[0-9A-HJKMNP-TV-Z]{26}$`)
	hexRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	relRes = []*regexp.Regexp{
		regexp.MustCompile(`^VAULT$`),
		regexp.MustCompile(`^PERIOD$`),
		regexp.MustCompile(`^epochs/[1-9][0-9]*\.json$`),
		regexp.MustCompile(`^keys/dedupe\.[1-9][0-9]*\.age$`),
		regexp.MustCompile(`^packs/[0-9a-f]{2}/[0-9a-f]{64}\.pack$`),
		regexp.MustCompile(`^index/s_[0-9A-HJKMNP-TV-Z]{26}\.json$`),
		regexp.MustCompile(`^snapshots/s_[0-9A-HJKMNP-TV-Z]{26}\.(age|meta\.json)$`),
	}
)

// ValidID reports whether id is a vault or snapshot id.
func ValidID(id string) bool { return idRe.MatchString(id) }

// ValidRel reports whether rel is a path a vault may contain. It is the
// only thing the data-plane reader serves, so nothing outside the vault
// (and no temporary file) can ever be read through it.
func ValidRel(rel string) bool {
	for _, re := range relRes {
		if re.MatchString(rel) {
			return true
		}
	}
	return false
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID returns prefix + "_" + a ULID (48-bit millisecond time, 80 random
// bits, Crockford Base32).
func NewID(prefix string, now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	if _, err := rand.Read(b[6:]); err != nil {
		panic(err)
	}
	// 128 bits -> 26 base32 digits (the first digit carries 3 bits).
	var out [26]byte
	var acc uint64
	bits := 0
	j := 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 && j >= 0 {
			out[j] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			j--
		}
	}
	for ; j >= 0; j-- {
		out[j] = crockford[acc&31]
		acc >>= 5
	}
	return prefix + "_" + string(out[:])
}

// SHA256Hex hashes b.
func SHA256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// joinRel turns a vault-relative slash path into a filesystem path.
func joinRel(dir, rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }

var errBadName = errors.New("vault: unexpected file name")

// packIDFromName extracts the id from "<id>.pack".
func packIDFromName(name string) (string, error) {
	id, ok := strings.CutSuffix(name, ".pack")
	if !ok || !hexRe.MatchString(id) {
		return "", errBadName
	}
	return id, nil
}
