package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/spf13/viper"
)

type Config struct {
	Server  ServerConfig  `mapstructure:"server" json:"server" yaml:"server"`
	Storage StorageConfig `mapstructure:"storage" json:"storage" yaml:"storage"`
	Log     LogConfig     `mapstructure:"log" json:"log" yaml:"log"`
}

type ServerConfig struct {
	Port int    `mapstructure:"port" json:"port" yaml:"port"`
	Mode string `mapstructure:"mode" json:"mode" yaml:"mode"`
}

type StorageConfig struct {
	DataDir     string `mapstructure:"data_dir" json:"data_dir" yaml:"data_dir"`
	KnowledgeDB string `mapstructure:"knowledge_db" json:"knowledge_db" yaml:"knowledge_db"`
	BleveIndex  string `mapstructure:"bleve_index" json:"bleve_index" yaml:"bleve_index"`
	TaskDir     string `mapstructure:"task_dir" json:"task_dir" yaml:"task_dir"`
	UploadDir   string `mapstructure:"upload_dir" json:"upload_dir" yaml:"upload_dir"`
	// AllowedRoots 允许被导入类接口读取的服务端根目录白名单 (ARCH-02)。
	// 为空表示不做限制（保留既有的本地单机工具行为）；一旦配置，
	// 任何跳出这些根目录的路径都会被 SecurePathGuard 拒绝。
	AllowedRoots []string `mapstructure:"allowed_roots" json:"allowed_roots" yaml:"allowed_roots"`
}

type LogConfig struct {
	Level     string `mapstructure:"level" json:"level" yaml:"level"`
	Format    string `mapstructure:"format" json:"format" yaml:"format"`
	Dir       string `mapstructure:"dir" json:"dir" yaml:"dir"`
	MaxSizeMB int    `mapstructure:"max_size_mb" json:"max_size_mb" yaml:"max_size_mb"`
	MaxDays   int    `mapstructure:"max_days" json:"max_days" yaml:"max_days"`
}

// LogUpdateHook 定义日志配置变更时的通知钩子函数
type LogUpdateHook func(maxSizeMB, maxDays int, level, format string)

var (
	GlobalConfig   *Config
	ConfigFileUsed string
	configMu       sync.RWMutex

	logUpdateHooks []LogUpdateHook
	hooksMu        sync.Mutex
)

// RegisterLogUpdateHook 注册日志配置更新回调钩子
func RegisterLogUpdateHook(hook LogUpdateHook) {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	logUpdateHooks = append(logUpdateHooks, hook)
}

const DefaultDataDir = "LogAuditorGoData"

const defaultConfigFileTemplate = `# LogAuditorGo 配置文件

server:
  port: 8080        # HTTP 服务监听端口
  mode: debug       # 运行模式: debug 或 release

storage:
  data_dir: "LogAuditorGoData"                         # 默认数据根目录
  knowledge_db: "LogAuditorGoData/knowledge.db"        # 全局知识库 SQLite 数据库
  bleve_index: "LogAuditorGoData/bleve/knowledge.bleve" # Bleve 全文检索索引目录
  task_dir: "LogAuditorGoData/tasks"                   # 日志审计任务专属 SQLite 存储目录
  upload_dir: "LogAuditorGoData/uploads"               # 临时上传目录
  # allowed_roots: 导入类接口允许读取的服务端根目录白名单 (ARCH-02)
  #   留空 = 不限制（本地单机工具默认行为）
  #   配置后，任何跳出这些根目录的路径都会被拒绝并返回 403
  # allowed_roots:
  #   - "D:/logs"
  #   - "D:/hdx"

log:
  level: info                # 日志级别: debug, info, warn, error
  format: console            # 日志格式: console 或 json
  dir: "LogAuditorGoData/log" # 日志存放目录
  max_size_mb: 1024          # 日志最大保留总大小(MB), 默认 1024 (1GB)
  max_days: 180              # 日志最大保留天数(天), 默认 180
`

func Load(configPath string) (*Config, error) {
	configMu.Lock()
	defer configMu.Unlock()

	v := viper.New()

	// 默认值
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.mode", "debug")
	v.SetDefault("storage.data_dir", DefaultDataDir)
	v.SetDefault("storage.knowledge_db", filepath.Join(DefaultDataDir, "knowledge.db"))
	v.SetDefault("storage.bleve_index", filepath.Join(DefaultDataDir, "bleve", "knowledge.bleve"))
	v.SetDefault("storage.task_dir", filepath.Join(DefaultDataDir, "tasks"))
	v.SetDefault("storage.upload_dir", filepath.Join(DefaultDataDir, "uploads"))
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "console")
	v.SetDefault("log.dir", filepath.Join(DefaultDataDir, "log"))
	v.SetDefault("log.max_size_mb", 1024)
	v.SetDefault("log.max_days", 180)

	targetConfigFile := configPath
	if targetConfigFile == "" {
		targetConfigFile = filepath.Join(DefaultDataDir, "config.yaml")
	}

	// 如果配置文件不存在，自动在对应目录下生成一份默认配置文件
	//
	// ARCH-17: 原实现 `_ = os.WriteFile(...)` 把写入错误完全吞掉，
	// 只读目录下会静默降级——用户以为自己在改配置，实际改的文件从未生效。
	// 产品决策：默认配置回写失败不阻断启动，但必须打 WARN 并带上配置路径，
	// 让用户知道"当前跑的是内存默认配置"。
	if _, err := os.Stat(targetConfigFile); os.IsNotExist(err) {
		configDir := filepath.Dir(targetConfigFile)
		if configDir != "" && configDir != "." {
			if mkdirErr := os.MkdirAll(configDir, 0755); mkdirErr != nil {
				// 此阶段 zap 尚未初始化（main.go 中 config.Load 先于 logger.Init），直接写 stderr
				fmt.Fprintf(os.Stderr, "[Config][WARN] create config dir %s failed: %v (falling back to in-memory defaults)\n", configDir, mkdirErr)
			}
		}
		if writeErr := os.WriteFile(targetConfigFile, []byte(defaultConfigFileTemplate), 0600); writeErr != nil {
			fmt.Fprintf(os.Stderr, "[Config][WARN] write default config to %s failed: %v (server starts with in-memory defaults; settings will NOT persist)\n",
				targetConfigFile, writeErr)
		}
	}

	v.SetConfigFile(targetConfigFile)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config failed: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config failed: %w", err)
	}

	// 当 DataDir 被显式指定为非默认路径时（如测试环境临时目录），自动将所有未覆盖的默认子路径收敛到该 DataDir 下
	if cfg.Storage.DataDir != "" && cfg.Storage.DataDir != DefaultDataDir {
		if cfg.Storage.KnowledgeDB == filepath.Join(DefaultDataDir, "knowledge.db") {
			cfg.Storage.KnowledgeDB = filepath.Join(cfg.Storage.DataDir, "knowledge.db")
		}
		if cfg.Storage.BleveIndex == filepath.Join(DefaultDataDir, "bleve", "knowledge.bleve") {
			cfg.Storage.BleveIndex = filepath.Join(cfg.Storage.DataDir, "bleve", "knowledge.bleve")
		}
		if cfg.Storage.TaskDir == filepath.Join(DefaultDataDir, "tasks") {
			cfg.Storage.TaskDir = filepath.Join(cfg.Storage.DataDir, "tasks")
		}
		if cfg.Storage.UploadDir == filepath.Join(DefaultDataDir, "uploads") {
			cfg.Storage.UploadDir = filepath.Join(cfg.Storage.DataDir, "uploads")
		}
		if cfg.Log.Dir == filepath.Join(DefaultDataDir, "log") || cfg.Log.Dir == "" {
			cfg.Log.Dir = filepath.Join(cfg.Storage.DataDir, "log")
		}
	}

	// 确保必要字段不为空
	if cfg.Log.Dir == "" {
		cfg.Log.Dir = filepath.Join(cfg.Storage.DataDir, "log")
	}
	if cfg.Log.MaxSizeMB <= 0 {
		cfg.Log.MaxSizeMB = 1024
	}
	if cfg.Log.MaxDays <= 0 {
		cfg.Log.MaxDays = 180
	}

	// ARCH-16: 原实现只设默认值不做任何范围校验。
	// `port: 0` 会监听随机端口（客户端连不上且不报错），`port: 99999` 启动失败
	// 且错误信息里没有配置路径，用户根本不知道该改哪个文件。
	// 这里统一做范围校验，错误信息一律附上 ConfigFileUsed 便于定位。
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	// 确保存储目录存在
	dirs := []string{
		cfg.Storage.DataDir,
		filepath.Dir(cfg.Storage.KnowledgeDB),
		filepath.Dir(cfg.Storage.BleveIndex),
		cfg.Storage.TaskDir,
		cfg.Storage.UploadDir,
		cfg.Log.Dir,
	}
	for _, d := range dirs {
		if d != "" && d != "." {
			if err := os.MkdirAll(d, 0755); err != nil {
				return nil, fmt.Errorf("create dir %s failed: %w", d, err)
			}
		}
	}

	ConfigFileUsed = targetConfigFile
	GlobalConfig = &cfg
	return &cfg, nil
}

// validServerModes 允许的运行模式
var validServerModes = map[string]bool{
	"debug":   true,
	"release": true,
	"test":    true,
}

// validate 校验配置合法范围。
//
// 产品决策：端口越界与未知模式采用"回退到安全默认值 + WARN"，而不是拒绝启动——
// 本工具定位为本地离线单机工具，因配置笔误直接拒绝启动会显著降低可用性。
// 但回退必须留下可追溯的告警，绝不能像原实现那样静默接受。
func validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	where := ConfigFileUsed
	if where == "" {
		where = "(in-memory defaults)"
	}

	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		fmt.Fprintf(os.Stderr, "[Config][WARN] server.port=%d out of range [1,65535], falling back to 8080 (config file: %s)\n",
			cfg.Server.Port, where)
		cfg.Server.Port = 8080
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "debug"
	}
	if !validServerModes[strings.ToLower(strings.TrimSpace(cfg.Server.Mode))] {
		fmt.Fprintf(os.Stderr, "[Config][WARN] unknown server.mode=%q, falling back to release (config file: %s)\n",
			cfg.Server.Mode, where)
		cfg.Server.Mode = "release"
	} else {
		cfg.Server.Mode = strings.ToLower(strings.TrimSpace(cfg.Server.Mode))
	}

	if cfg.Log.MaxSizeMB < 0 {
		fmt.Fprintf(os.Stderr, "[Config][WARN] log.max_size_mb=%d invalid, falling back to %d (config file: %s)\n",
			cfg.Log.MaxSizeMB, 1024, where)
		cfg.Log.MaxSizeMB = 1024
	}
	if cfg.Log.MaxDays < 0 {
		fmt.Fprintf(os.Stderr, "[Config][WARN] log.max_days=%d invalid, falling back to %d (config file: %s)\n",
			cfg.Log.MaxDays, 180, where)
		cfg.Log.MaxDays = 180
	}

	// 白名单根目录必须存在且为目录，否则该条目无效（打 WARN 后丢弃）
	if len(cfg.Storage.AllowedRoots) > 0 {
		valid := make([]string, 0, len(cfg.Storage.AllowedRoots))
		for _, root := range cfg.Storage.AllowedRoots {
			abs, err := filepath.Abs(strings.TrimSpace(root))
			if err != nil || abs == "" {
				continue
			}
			st, err := os.Stat(abs)
			if err != nil || !st.IsDir() {
				fmt.Fprintf(os.Stderr, "[Config][WARN] storage.allowed_roots entry %q is not an accessible directory, ignored (config file: %s)\n",
					root, where)
				continue
			}
			valid = append(valid, abs)
		}
		cfg.Storage.AllowedRoots = valid
	}

	return nil
}

// Save 将当前全局配置持久化保存至配置文件
func Save(configPath string) error {
	configMu.RLock()
	defer configMu.RUnlock()

	if GlobalConfig == nil {
		return fmt.Errorf("global config is nil")
	}

	targetPath := configPath
	if targetPath == "" {
		targetPath = ConfigFileUsed
	}
	if targetPath == "" {
		if GlobalConfig.Storage.DataDir != "" {
			targetPath = filepath.Join(GlobalConfig.Storage.DataDir, "config.yaml")
		} else {
			targetPath = filepath.Join(DefaultDataDir, "config.yaml")
		}
	}

	dir := filepath.Dir(targetPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create config dir %s failed: %w", dir, err)
		}
	}

	data, err := yaml.Marshal(GlobalConfig)
	if err != nil {
		return fmt.Errorf("marshal config to yaml failed: %w", err)
	}

	if err := os.WriteFile(targetPath, data, 0600); err != nil {
		return fmt.Errorf("write config file %s failed: %w", targetPath, err)
	}

	return nil
}

// UpdateLogConfig 动态更新日志配置，通知所有已注册的回调钩子并持久化保存到配置文件
func UpdateLogConfig(maxSizeMB, maxDays int, level, format string) (*LogConfig, error) {
	configMu.Lock()
	if GlobalConfig == nil {
		configMu.Unlock()
		return nil, fmt.Errorf("global config not initialized")
	}

	if maxSizeMB > 0 {
		GlobalConfig.Log.MaxSizeMB = maxSizeMB
	}
	if maxDays > 0 {
		GlobalConfig.Log.MaxDays = maxDays
	}
	if level != "" {
		GlobalConfig.Log.Level = level
	}
	if format != "" {
		GlobalConfig.Log.Format = format
	}

	currentLogCfg := GlobalConfig.Log
	configMu.Unlock()

	// 触发所有已注册的日志更新钩子
	hooksMu.Lock()
	hooks := make([]LogUpdateHook, len(logUpdateHooks))
	copy(hooks, logUpdateHooks)
	hooksMu.Unlock()

	for _, hook := range hooks {
		if hook != nil {
			hook(currentLogCfg.MaxSizeMB, currentLogCfg.MaxDays, currentLogCfg.Level, currentLogCfg.Format)
		}
	}

	// 持久化保存
	if err := Save(""); err != nil {
		return nil, fmt.Errorf("persist config failed: %w", err)
	}

	return &currentLogCfg, nil
}
