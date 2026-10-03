package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// keyVersion prefixes every dedupe key string. Changing how a key is built means a new version
// and a re-key migration, never a silent change: existing rows would stop matching.
const keyVersion = "v1"

// keySource is the account part shared by every key of one connection.
type keySource struct {
	provider string // provider code
	account  string // hex of connections.account_key, or "conn:<id>" while the account is unknown
}

func newKeySource(provider string, accountKey []byte, connID uuid.UUID) keySource {
	if len(accountKey) == 0 {
		// Without an account key a reconnect cannot be recognised; keys stay stable per connection.
		return keySource{provider, "conn:" + connID.String()}
	}
	return keySource{provider, hex.EncodeToString(accountKey)}
}

// idKey is the key of a record with a stable upstream id (data-model.md#identifiers-and-dedupe-keys):
// v1|provider|account_key|record_type|external_id|component.
func (s keySource) idKey(k Key) []byte {
	return dedupeKey(keyVersion, s.provider, s.account, k.RecordType, k.ExternalID, k.Component)
}

// naturalKey is the key of a record without an upstream id:
// v1|provider|account_key|metric|kind|start_utc_us|end_utc_us|device_fingerprint|origin_key.
// Events use their table ("sleep", "workout", the group kind) as metric.
func (s keySource) naturalKey(metric, kind string, start time.Time, end *time.Time, device, origin string) []byte {
	e := ""
	if end != nil {
		e = strconv.FormatInt(end.UnixMicro(), 10)
	}
	return dedupeKey(keyVersion, s.provider, s.account, metric, kind, strconv.FormatInt(start.UnixMicro(), 10), e, device, origin)
}

// key picks idKey when the record has an upstream id.
func (s keySource) key(k Key, metric, kind string, start time.Time, end *time.Time, device, origin string) []byte {
	if k.ExternalID != "" {
		return s.idKey(k)
	}
	return s.naturalKey(metric, kind, start, end, device, origin)
}

// dedupeKey is the first 16 bytes of SHA-256 over the parts joined by "|". A backslash or "|"
// inside a part is backslash-escaped, so distinct part lists never collide on the joined string.
func dedupeKey(parts ...string) []byte {
	esc := strings.NewReplacer(`\`, `\\`, `|`, `\|`)
	for i, p := range parts {
		parts[i] = esc.Replace(p)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return sum[:16]
}
