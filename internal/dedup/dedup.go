// Package dedup provides the visited-URL set used to gate frontier enqueue.
package dedup

import "sync"

// VisitedSet tracks which normalized URL keys have already been seen.
type VisitedSet interface {
	// MarkIfNew atomically checks-and-inserts key. It returns true if this
	// is the first time key has been seen, in which case the caller should
	// proceed (e.g. enqueue the URL); false means it's a duplicate.
	MarkIfNew(key string) bool
	Len() int
}

// shardedSet is a VisitedSet backed by sharded maps, each guarded by its
// own mutex, to reduce lock contention under highly concurrent workloads.
type shardedSet struct {
	shards [numShards]shard
}

const numShards = 32

type shard struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// New returns the default in-memory VisitedSet implementation.
func New() VisitedSet {
	s := &shardedSet{}
	for i := range s.shards {
		s.shards[i].seen = make(map[string]struct{})
	}
	return s
}

func (s *shardedSet) MarkIfNew(key string) bool {
	sh := &s.shards[fnv32(key)%numShards]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if _, ok := sh.seen[key]; ok {
		return false
	}
	sh.seen[key] = struct{}{}
	return true
}

func (s *shardedSet) Len() int {
	total := 0
	for i := range s.shards {
		s.shards[i].mu.Lock()
		total += len(s.shards[i].seen)
		s.shards[i].mu.Unlock()
	}
	return total
}

// fnv32 is a tiny, allocation-free string hash used only to pick a shard.
func fnv32(s string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	h := uint32(offset32)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime32
	}
	return h
}
