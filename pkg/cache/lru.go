package cache

import (
	"sync"
)

// node represents a node in the doubly linked list.
type node[K comparable, V any] struct {
	key   K
	value V
	prev  *node[K, V]
	next  *node[K, V]
}

// LRUCache is a generic, thread-safe, high-performance LRU (Least Recently Used) cache.
type LRUCache[K comparable, V any] struct {
	mu       sync.RWMutex
	capacity int
	items    map[K]*node[K, V]
	head     *node[K, V] // Sentinel head: head.next is MRU (most recently used)
	tail     *node[K, V] // Sentinel tail: tail.prev is LRU (least recently used)
}

// NewLRUCache creates a new LRUCache with the given capacity.
// If capacity <= 0, a default capacity of 128 is used.
func NewLRUCache[K comparable, V any](capacity int) *LRUCache[K, V] {
	if capacity <= 0 {
		capacity = 128
	}
	head := &node[K, V]{}
	tail := &node[K, V]{}
	head.next = tail
	tail.prev = head

	return &LRUCache[K, V]{
		capacity: capacity,
		items:    make(map[K]*node[K, V], capacity),
		head:     head,
		tail:     tail,
	}
}

// Get retrieves a value from the cache by key.
// If key is found, it is moved to the MRU position and returns (value, true).
// If key is not found or cache is nil, returns (zero value, false).
func (c *LRUCache[K, V]) Get(key K) (V, bool) {
	if c == nil {
		var zero V
		return zero, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if n, ok := c.items[key]; ok {
		c.moveToFront(n)
		return n.value, true
	}

	var zero V
	return zero, false
}

// Put inserts or updates a key-value pair in the cache.
// If the key already exists, its value is updated and moved to the MRU position.
// If the cache reaches capacity, the least recently used item (tail.prev) is evicted.
func (c *LRUCache[K, V]) Put(key K, value V) {
	if c == nil || c.capacity <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if n, ok := c.items[key]; ok {
		n.value = value
		c.moveToFront(n)
		return
	}

	if len(c.items) >= c.capacity {
		c.removeOldest()
	}

	n := &node[K, V]{
		key:   key,
		value: value,
	}
	c.items[key] = n
	c.addToFront(n)
}

// Purge clears all entries from the cache.
func (c *LRUCache[K, V]) Purge() {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[K]*node[K, V], c.capacity)
	c.head.next = c.tail
	c.tail.prev = c.head
}

// Len returns the current number of items in the cache.
func (c *LRUCache[K, V]) Len() int {
	if c == nil {
		return 0
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

// Cap returns the maximum capacity of the cache.
func (c *LRUCache[K, V]) Cap() int {
	if c == nil {
		return 0
	}
	return c.capacity
}

// Remove deletes a key from the cache. Returns true if key was present.
func (c *LRUCache[K, V]) Remove(key K) bool {
	if c == nil {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if n, ok := c.items[key]; ok {
		c.removeNode(n)
		delete(c.items, key)
		return true
	}
	return false
}

// Peek returns the key value without updating the "recently used" state.
func (c *LRUCache[K, V]) Peek(key K) (V, bool) {
	if c == nil {
		var zero V
		return zero, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if n, ok := c.items[key]; ok {
		return n.value, true
	}
	var zero V
	return zero, false
}

// Contains checks if a key is in the cache without updating the "recently used" state.
func (c *LRUCache[K, V]) Contains(key K) bool {
	if c == nil {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	_, ok := c.items[key]
	return ok
}

// Internal linked-list operations. Caller must hold c.mu Lock.

func (c *LRUCache[K, V]) addToFront(n *node[K, V]) {
	n.prev = c.head
	n.next = c.head.next
	c.head.next.prev = n
	c.head.next = n
}

func (c *LRUCache[K, V]) removeNode(n *node[K, V]) {
	n.prev.next = n.next
	n.next.prev = n.prev
	n.prev = nil
	n.next = nil
}

func (c *LRUCache[K, V]) moveToFront(n *node[K, V]) {
	// Unlink
	n.prev.next = n.next
	n.next.prev = n.prev
	// Link at front
	n.prev = c.head
	n.next = c.head.next
	c.head.next.prev = n
	c.head.next = n
}

func (c *LRUCache[K, V]) removeOldest() {
	oldest := c.tail.prev
	if oldest == c.head {
		return
	}
	c.removeNode(oldest)
	delete(c.items, oldest.key)
}
