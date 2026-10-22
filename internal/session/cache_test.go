package session_test

import (
	"bytes"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"tmux-gitlab-pipeline/internal/session"
)

func cache(t *testing.T, entries, size int) *session.Cache {
	t.Helper()
	result, err := session.NewCache(session.CacheLimits{Bytes: size, Entries: entries, ValueBytes: size / 2})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCacheOwnsItsValuesAndExpiresThem(t *testing.T) {
	c := cache(t, 4, 8192)
	now := time.Now()
	data := []byte("running")
	if err := c.Put("branch-a", session.Record{Data: data, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	first, ok := c.Get("branch-a", now)
	if !ok || string(first.Data) != "running" {
		t.Fatal(first, ok)
	}
	first.Data[0] = 'Y'
	second, ok := c.Get("branch-a", now)
	if !ok || string(second.Data) != "running" {
		t.Fatal(second, ok)
	}
	if _, ok := c.Get("branch-a", now.Add(time.Minute)); ok {
		t.Fatal("expired value survived")
	}
	if stats := c.Stats(); stats.Bytes != 0 || stats.Entries != 0 {
		t.Fatal(stats)
	}
}

func TestCacheEvictsLeastRecentlyUsedAndHonorsByteLimits(t *testing.T) {
	c := cache(t, 2, 4096)
	now := time.Now()
	for _, key := range []string{"first", "second"} {
		if err := c.Put(key, session.Record{Data: []byte(key), ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	c.Get("first", now)
	if err := c.Put("third", session.Record{Data: []byte("third"), ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("second", now); ok {
		t.Fatal("least recently used value survived")
	}
	if err := c.Put("oversized", session.Record{Data: make([]byte, 2049)}); err == nil {
		t.Fatal("accepted oversized value")
	}
	c.Invalidate("fir")
	if stats := c.Stats(); stats.Entries != 1 || stats.Bytes > 4096 || stats.Evictions != 1 {
		t.Fatal(stats)
	}
	c.Sweep(now.Add(time.Hour))
	if stats := c.Stats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatal(stats)
	}
}

func TestCacheIsBoundedUnderConcurrentReplacement(t *testing.T) {
	c := cache(t, 16, 1<<20)
	now := time.Now()
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range 500 {
				key := fmt.Sprintf("repo-%d/job-%d", worker, i%32)
				if err := c.Put(key, session.Record{Data: bytes.Repeat([]byte("x"), 8192), ExpiresAt: now.Add(time.Minute)}); err != nil {
					t.Error(err)
					return
				}
				c.Get(key, now)
			}
		}()
	}
	workers.Wait()
	if stats := c.Stats(); stats.Entries > 16 || stats.Bytes > 1<<20 {
		t.Fatal(stats)
	}
	c.Clear()
	if stats := c.Stats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatal(stats)
	}
}

func TestEvictedPayloadsDoNotAccumulateOnHeap(t *testing.T) {
	c := cache(t, 16, 1<<20)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range 3000 {
		if err := c.Put(fmt.Sprint(i), session.Record{Data: bytes.Repeat([]byte("x"), 32<<10), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	c.Clear()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+(2<<20) {
		t.Fatalf("retained heap grew by %d bytes", after.HeapAlloc-before.HeapAlloc)
	}
	runtime.KeepAlive(c)
}
