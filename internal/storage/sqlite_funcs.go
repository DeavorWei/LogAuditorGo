package storage

import (
	"database/sql/driver"
	"fmt"
	"regexp"
	"sync"

	gosqlite "github.com/glebarez/go-sqlite"
)

// regexpCache 提供并发安全、容量有界的正则表达式编译缓存。
type regexpCache struct {
	sync.RWMutex
	maxSize int
	cache   map[string]*regexp.Regexp
}

func newRegexpCache(maxSize int) *regexpCache {
	if maxSize <= 0 {
		maxSize = 256
	}
	return &regexpCache{
		maxSize: maxSize,
		cache:   make(map[string]*regexp.Regexp, maxSize),
	}
}

// GetOrCompile 优先从缓存获取已编译正则，未命中则编译并写入缓存。
func (c *regexpCache) GetOrCompile(pattern string) (*regexp.Regexp, error) {
	c.RLock()
	if re, ok := c.cache[pattern]; ok {
		c.RUnlock()
		return re, nil
	}
	c.RUnlock()

	c.Lock()
	defer c.Unlock()

	// 双检锁二次确认
	if re, ok := c.cache[pattern]; ok {
		return re, nil
	}

	// 超过上限则整体清空，避免动态正则耗尽内存
	if len(c.cache) >= c.maxSize {
		c.cache = make(map[string]*regexp.Regexp, c.maxSize)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	c.cache[pattern] = re
	return re, nil
}

var (
	globalReCache = newRegexpCache(256)
	registerOnce  sync.Once
)

// RegisterSQLiteFunctions 注册 SQLite 自定义函数（如 REGEXP）。
// 该函数是幂等的，由 init() 自动调用，也可在应用启动前显式调用。
func RegisterSQLiteFunctions() {
	registerOnce.Do(func() {
		// SQLite 中 `val REGEXP pat` 语法会被映射为 `REGEXP(pat, val)`。
		// args[0] 为正则表达式模式 (pattern)，args[1] 为目标匹配值 (value)。
		_ = gosqlite.RegisterDeterministicScalarFunction("REGEXP", 2,
			func(ctx *gosqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				// 审计修正 #1：NULL 防御
				// SQL NULL 在底层驱动以 nil 传入，直接 .(string) 会引发 panic 导致进程崩溃。
				// NULL 不匹配正则，符合 SQL 三值逻辑，安全返回 false。
				if len(args) < 2 || args[0] == nil || args[1] == nil {
					return false, nil
				}

				// 安全提取 pattern
				var pat string
				switch v := args[0].(type) {
				case string:
					pat = v
				case []byte:
					pat = string(v)
				default:
					return false, fmt.Errorf("regexp pattern must be string, got %T", args[0])
				}

				// 安全提取 value
				var val string
				switch v := args[1].(type) {
				case string:
					val = v
				case []byte:
					val = string(v)
				default:
					val = fmt.Sprintf("%v", v)
				}

				re, err := globalReCache.GetOrCompile(pat)
				if err != nil {
					// 正则表达式错误返回给 SQL 引擎，不崩溃进程
					return nil, err
				}

				return re.MatchString(val), nil
			})
	})
}

func init() {
	RegisterSQLiteFunctions()
}
