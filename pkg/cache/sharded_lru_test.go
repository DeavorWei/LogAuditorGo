package cache_test

import (
	"fmt"
	"sync"
	"testing"

	"logauditorgo/pkg/cache"
)

func TestShardedLRUCache_Basic(t *testing.T) {
	c := cache.NewShardedLRUCache[int](16, 4)
	if c.Cap() < 16 {
		t.Fatalf("expected cap >= 16, got %d", c.Cap())
	}
	if c.Len() != 0 {
		t.Fatalf("expected len 0, got %d", c.Len())
	}

	c.Put("key1", 100)
	c.Put("key2", 200)

	if val, ok := c.Get("key1"); !ok || val != 100 {
		t.Fatalf("expected key1=100, got %v (ok=%v)", val, ok)
	}
	if val, ok := c.Peek("key2"); !ok || val != 200 {
		t.Fatalf("expected key2=200, got %v (ok=%v)", val, ok)
	}
	if !c.Contains("key1") {
		t.Fatalf("expected Contains(key1)=true")
	}

	c.Remove("key1")
	if c.Contains("key1") {
		t.Fatalf("expected Contains(key1)=false after Remove")
	}

	c.Purge()
	if c.Len() != 0 {
		t.Fatalf("expected len 0 after Purge, got %d", c.Len())
	}
}

func TestShardedLRUCache_Concurrent(t *testing.T) {
	c := cache.NewShardedLRUCache[string](1000, 16)
	var wg sync.WaitGroup

	numWorkers := 20
	opsPerWorker := 500

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				key := fmt.Sprintf("k_%d_%d", workerID, i%50)
				val := fmt.Sprintf("v_%d_%d", workerID, i)

				c.Put(key, val)
				_, _ = c.Peek(key)
				_, _ = c.Get(key)
				_ = c.Contains(key)
			}
		}(w)
	}

	wg.Wait()
	if c.Len() == 0 {
		t.Fatalf("expected non-zero len after concurrent puts")
	}
}
