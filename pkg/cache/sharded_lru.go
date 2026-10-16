package cache

// ShardedLRUCache 是并发分片的 LRU 缓存，将全局读写锁争用分散至 N 个独立的分片（Shard）中。
// 针对高并发 Worker 场景（如 16~32 协程并发匹配与高频写入负缓存），有效消除单把互斥锁的全局串行等待。
type ShardedLRUCache[V any] struct {
	shards    []*LRUCache[string, V]
	numShards int
	mask      uint64
}

// NewShardedLRUCache 创建指定总容量与分片数的分片 LRU 缓存。
// numShards 会向上取整为 2 的幂次方以支持位运算哈希取模。
func NewShardedLRUCache[V any](totalCap, numShards int) *ShardedLRUCache[V] {
	if numShards <= 0 {
		numShards = 16
	}
	n := 1
	for n < numShards {
		n <<= 1
	}
	numShards = n
	mask := uint64(numShards - 1)

	shardCap := totalCap / numShards
	if shardCap < 1 {
		shardCap = 1
	}

	shards := make([]*LRUCache[string, V], numShards)
	for i := 0; i < numShards; i++ {
		shards[i] = NewLRUCache[string, V](shardCap)
	}

	return &ShardedLRUCache[V]{
		shards:    shards,
		numShards: numShards,
		mask:      mask,
	}
}

// fnv64aString 计算字符串的 64 位 FNV-1a 哈希值（零堆分配）
func fnv64aString(s string) uint64 {
	const offset64 = 14695981039346656037
	const prime64 = 1099511628211
	var h uint64 = offset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

func (s *ShardedLRUCache[V]) shard(key string) *LRUCache[string, V] {
	idx := fnv64aString(key) & s.mask
	return s.shards[idx]
}

// Get 查询键值并将对应节点提升至 MRU 头部
func (s *ShardedLRUCache[V]) Get(key string) (V, bool) {
	if s == nil {
		var zero V
		return zero, false
	}
	return s.shard(key).Get(key)
}

// Peek 查询键值但不改变 LRU 访问淘汰顺序（走纯读锁快路径）
func (s *ShardedLRUCache[V]) Peek(key string) (V, bool) {
	if s == nil {
		var zero V
		return zero, false
	}
	return s.shard(key).Peek(key)
}

// Contains 检查键是否存在于缓存中（纯读锁）
func (s *ShardedLRUCache[V]) Contains(key string) bool {
	if s == nil {
		return false
	}
	return s.shard(key).Contains(key)
}

// Put 插入或更新键值对
func (s *ShardedLRUCache[V]) Put(key string, value V) {
	if s == nil {
		return
	}
	s.shard(key).Put(key, value)
}

// Remove 删除指定键
func (s *ShardedLRUCache[V]) Remove(key string) bool {
	if s == nil {
		return false
	}
	return s.shard(key).Remove(key)
}

// Purge 清空所有分片缓存
func (s *ShardedLRUCache[V]) Purge() {
	if s == nil {
		return
	}
	for _, shard := range s.shards {
		shard.Purge()
	}
}

// Len 返回所有分片当前条目总数
func (s *ShardedLRUCache[V]) Len() int {
	if s == nil {
		return 0
	}
	total := 0
	for _, shard := range s.shards {
		total += shard.Len()
	}
	return total
}

// Cap 返回所有分片容量之和
func (s *ShardedLRUCache[V]) Cap() int {
	if s == nil {
		return 0
	}
	total := 0
	for _, shard := range s.shards {
		total += shard.Cap()
	}
	return total
}
