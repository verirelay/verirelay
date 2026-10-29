package storage

import "time"

type ttlEntry[V any] struct {
	value     V
	size      int64
	expiresAt time.Time
	sequence  uint64
}

// ttlMap is a map whose entries expire, bounded by the total size of its
// entries. When a new entry does not fit, the oldest insertions are evicted.
type ttlMap[V any] struct {
	entries      map[string]ttlEntry[V]
	limit        int64
	used         int64
	size         func(V) int64
	nextSequence uint64
}

func newTTLMap[V any](limit int64, size func(V) int64) ttlMap[V] {
	return ttlMap[V]{entries: make(map[string]ttlEntry[V]), limit: limit, size: size}
}

// countEntries sizes every entry as 1, which bounds a ttlMap by entry count.
func countEntries[V any](V) int64 { return 1 }

// put stores value under key for ttl, replacing any entry for key. It reports
// false, storing nothing, when value alone is larger than the limit.
func (m *ttlMap[V]) put(now time.Time, key string, value V, ttl time.Duration) bool {
	size := m.size(value)
	if size > m.limit {
		return false
	}
	m.removeExpired(now)
	m.remove(key)
	for m.used+size > m.limit {
		m.evictOldest()
	}
	m.nextSequence++
	m.entries[key] = ttlEntry[V]{value: value, size: size, expiresAt: now.Add(ttl), sequence: m.nextSequence}
	m.used += size
	return true
}

func (m *ttlMap[V]) get(key string, now func() time.Time) (V, bool) {
	var zero V
	entry, ok := m.entries[key]
	if !ok {
		return zero, false
	}
	if !entry.expiresAt.After(now()) {
		m.remove(key)
		return zero, false
	}
	return entry.value, true
}

func (m *ttlMap[V]) remove(key string) {
	if entry, ok := m.entries[key]; ok {
		m.used -= entry.size
		delete(m.entries, key)
	}
}

func (m *ttlMap[V]) removeExpired(now time.Time) {
	for key, entry := range m.entries {
		if !entry.expiresAt.After(now) {
			m.remove(key)
		}
	}
}

func (m *ttlMap[V]) evictOldest() {
	var oldest string
	var sequence uint64
	found := false
	for key, entry := range m.entries {
		if !found || entry.sequence < sequence {
			oldest, sequence, found = key, entry.sequence, true
		}
	}
	m.remove(oldest)
}
