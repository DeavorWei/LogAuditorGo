package cache_test

import (
	"fmt"
	"sync"
	"testing"

	"logauditorgo/pkg/cache"
)

func TestLRUCacheBasic(t *testing.T) {
	c := cache.NewLRUCache[string, int](3)
	if c.Cap() != 3 {
		t.Fatalf("expected cap 3, got %d", c.Cap())
	}
	if c.Len() != 0 {
		t.Fatalf("expected initial len 0, got %d", c.Len())
	}

	// Test Get on empty cache
	if val, ok := c.Get("a"); ok || val != 0 {
		t.Fatalf("expected not found on empty cache, got val=%d, ok=%v", val, ok)
	}

	// Test Put and Get
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)

	if c.Len() != 3 {
		t.Fatalf("expected len 3, got %d", c.Len())
	}

	if val, ok := c.Get("a"); !ok || val != 1 {
		t.Fatalf("expected key 'a' to have value 1, got val=%d, ok=%v", val, ok)
	}
	if val, ok := c.Get("b"); !ok || val != 2 {
		t.Fatalf("expected key 'b' to have value 2, got val=%d, ok=%v", val, ok)
	}
	if val, ok := c.Get("c"); !ok || val != 3 {
		t.Fatalf("expected key 'c' to have value 3, got val=%d, ok=%v", val, ok)
	}

	// Update existing key
	c.Put("b", 20)
	if val, ok := c.Get("b"); !ok || val != 20 {
		t.Fatalf("expected updated value 20 for key 'b', got %d", val)
	}
	if c.Len() != 3 {
		t.Fatalf("expected len to stay 3 after update, got %d", c.Len())
	}
}

func TestLRUCacheEviction(t *testing.T) {
	c := cache.NewLRUCache[string, string](3)

	c.Put("k1", "v1") // list: head <-> k1 <-> tail
	c.Put("k2", "v2") // list: head <-> k2 <-> k1 <-> tail
	c.Put("k3", "v3") // list: head <-> k3 <-> k2 <-> k1 <-> tail

	// Access k1 so it becomes MRU: list: head <-> k1 <-> k3 <-> k2 <-> tail
	if val, ok := c.Get("k1"); !ok || val != "v1" {
		t.Fatalf("expected k1 to exist, got %v", val)
	}

	// Insert k4, should evict LRU (k2): list: head <-> k4 <-> k1 <-> k3 <-> tail
	c.Put("k4", "v4")

	if _, ok := c.Peek("k2"); ok {
		t.Fatalf("expected k2 to be evicted")
	}
	if val, ok := c.Peek("k1"); !ok || val != "v1" {
		t.Fatalf("expected k1 to be present, got %v", val)
	}
	if val, ok := c.Peek("k3"); !ok || val != "v3" {
		t.Fatalf("expected k3 to be present, got %v", val)
	}
	if val, ok := c.Peek("k4"); !ok || val != "v4" {
		t.Fatalf("expected k4 to be present, got %v", val)
	}

	// Now list is head <-> k4 <-> k1 <-> k3 <-> tail.
	// Insert k5 without accessing k3. k3 is the LRU and should be evicted!
	c.Put("k5", "v5") // list: head <-> k5 <-> k4 <-> k1 <-> tail

	if _, ok := c.Peek("k3"); ok {
		t.Fatalf("expected k3 to be evicted")
	}
	if val, ok := c.Peek("k5"); !ok || val != "v5" {
		t.Fatalf("expected k5 to be present, got %v", val)
	}
	if val, ok := c.Peek("k4"); !ok || val != "v4" {
		t.Fatalf("expected k4 to be present, got %v", val)
	}
	if val, ok := c.Peek("k1"); !ok || val != "v1" {
		t.Fatalf("expected k1 to be present, got %v", val)
	}
}

func TestLRUCachePurge(t *testing.T) {
	c := cache.NewLRUCache[int, string](5)
	for i := 1; i <= 5; i++ {
		c.Put(i, fmt.Sprintf("val-%d", i))
	}

	if c.Len() != 5 {
		t.Fatalf("expected len 5 before purge, got %d", c.Len())
	}

	c.Purge()

	if c.Len() != 0 {
		t.Fatalf("expected len 0 after purge, got %d", c.Len())
	}
	for i := 1; i <= 5; i++ {
		if _, ok := c.Get(i); ok {
			t.Fatalf("expected key %d to be absent after purge", i)
		}
	}

	// Ensure we can still put and get after purge
	c.Put(100, "hello")
	if val, ok := c.Get(100); !ok || val != "hello" {
		t.Fatalf("expected key 100 to be 'hello', got %s", val)
	}
}

func TestLRUCacheRemove(t *testing.T) {
	c := cache.NewLRUCache[string, int](3)
	c.Put("a", 1)
	c.Put("b", 2)

	if !c.Remove("a") {
		t.Fatalf("expected Remove('a') to return true")
	}
	if c.Remove("a") {
		t.Fatalf("expected second Remove('a') to return false")
	}
	if c.Remove("nonexistent") {
		t.Fatalf("expected Remove('nonexistent') to return false")
	}
	if c.Len() != 1 {
		t.Fatalf("expected len 1 after remove, got %d", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatalf("expected key 'a' to be removed")
	}
	if val, ok := c.Get("b"); !ok || val != 2 {
		t.Fatalf("expected key 'b' to still exist with value 2")
	}
}

func TestLRUCachePeekAndContains(t *testing.T) {
	c := cache.NewLRUCache[string, int](2)
	c.Put("a", 1)
	c.Put("b", 2)

	if !c.Contains("a") || !c.Contains("b") {
		t.Fatalf("expected cache to contain 'a' and 'b'")
	}
	if c.Contains("c") {
		t.Fatalf("expected cache to not contain 'c'")
	}

	if val, ok := c.Peek("a"); !ok || val != 1 {
		t.Fatalf("expected Peek('a') to return 1, true, got val=%d, ok=%v", val, ok)
	}

	// Peek should NOT update LRU order.
	// Since "a" was added before "b", "a" is LRU.
	// Adding "c" should evict "a", even though we just peeked "a".
	c.Put("c", 3)

	if _, ok := c.Get("a"); ok {
		t.Fatalf("expected 'a' to be evicted because Peek does not update LRU position")
	}
	if val, ok := c.Get("b"); !ok || val != 2 {
		t.Fatalf("expected 'b' to exist, got %d", val)
	}
	if val, ok := c.Get("c"); !ok || val != 3 {
		t.Fatalf("expected 'c' to exist, got %d", val)
	}
}

func TestLRUCacheEdgeCases(t *testing.T) {
	// Capacity <= 0 defaults gracefully
	c0 := cache.NewLRUCache[string, int](0)
	if c0.Cap() <= 0 {
		t.Fatalf("expected positive default capacity, got %d", c0.Cap())
	}
	c0.Put("x", 42)
	if val, ok := c0.Get("x"); !ok || val != 42 {
		t.Fatalf("expected key 'x' to be 42, got %d", val)
	}

	// Capacity = 1
	c1 := cache.NewLRUCache[string, int](1)
	c1.Put("x", 1)
	if val, ok := c1.Get("x"); !ok || val != 1 {
		t.Fatalf("expected 'x' to be 1")
	}
	c1.Put("y", 2)
	if _, ok := c1.Get("x"); ok {
		t.Fatalf("expected 'x' to be evicted in cap-1 cache")
	}
	if val, ok := c1.Get("y"); !ok || val != 2 {
		t.Fatalf("expected 'y' to be 2")
	}

	// Nil safety
	var nilCache *cache.LRUCache[string, int]
	if val, ok := nilCache.Get("key"); ok || val != 0 {
		t.Fatalf("expected nil cache Get to return 0, false")
	}
	nilCache.Put("key", 10) // should not panic
	nilCache.Purge()        // should not panic
	if nilCache.Len() != 0 {
		t.Fatalf("expected nil cache Len to be 0")
	}
	if nilCache.Cap() != 0 {
		t.Fatalf("expected nil cache Cap to be 0")
	}
	if nilCache.Remove("key") {
		t.Fatalf("expected nil cache Remove to be false")
	}
	if nilCache.Contains("key") {
		t.Fatalf("expected nil cache Contains to be false")
	}
	if _, ok := nilCache.Peek("key"); ok {
		t.Fatalf("expected nil cache Peek to be false")
	}
}

func TestLRUCacheConcurrency(t *testing.T) {
	c := cache.NewLRUCache[int, int](100)
	const goroutines = 20
	const opsPerGoroutine = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Concurrent Putters
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := (gid*opsPerGoroutine + i) % 200
				c.Put(key, key*10)
			}
		}(g)
	}

	// Concurrent Getters
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := (gid*opsPerGoroutine + i) % 200
				c.Get(key)
			}
		}(g)
	}

	// Concurrent Mix (Contains, Peek, Len, Purge)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := (gid*opsPerGoroutine + i) % 200
				c.Contains(key)
				c.Peek(key)
				_ = c.Len()
				if i == opsPerGoroutine/2 && gid == 0 {
					c.Purge()
				}
			}
		}(g)
	}

	wg.Wait()

	if c.Len() > c.Cap() {
		t.Fatalf("cache len %d exceeded cap %d after concurrency test", c.Len(), c.Cap())
	}
}

func BenchmarkLRUCachePut(b *testing.B) {
	c := cache.NewLRUCache[int, int](10000)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Put(i, i)
	}
}

func BenchmarkLRUCacheGet(b *testing.B) {
	c := cache.NewLRUCache[int, int](10000)
	for i := 0; i < 10000; i++ {
		c.Put(i, i)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Get(i % 10000)
	}
}

func BenchmarkLRUCacheConcurrent(b *testing.B) {
	c := cache.NewLRUCache[int, int](10000)
	for i := 0; i < 10000; i++ {
		c.Put(i, i)
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			if i%4 == 0 {
				c.Put(i%15000, i)
			} else {
				c.Get(i % 15000)
			}
		}
	})
}
