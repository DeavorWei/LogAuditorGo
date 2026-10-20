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
	Server   ServerConfig   `mapstructure:"server" json:"server" yaml:"server"`
	Storage  StorageConfig  `mapstructure:"storage" json:"storage" yaml:"storage"`
	Log      LogConfig      `mapstructure:"log" json:"log" yaml:"log"`
	RCA      RCAConfig      `mapstructure:"rca" json:"rca" yaml:"rca"`
	Task     TaskConfig     `mapstructure:"task" json:"task" yaml:"task"`
	Features FeaturesConfig `mapstructure:"features" json:"features" yaml:"features"`
}

// FeaturesConfig 产品功能开关（v1.1 阶段引入）。
//
// 设计约束：
//   - 只影响"是否自动执行 / 是否在 UI 暴露"，不删除任何既有能力；
//   - RCA 默认关闭：规划要求"只保留、不演进"，关闭后导入/重分析不再自动派发，
//     仅在设置页实验室功能开启、且用户从工作台"高级"抽屉手动触发时才运行；
//   - BrowserUpload / LANMode / Masking 是产品明确"不做"的能力，保留字段用于
//     配置自检与文档一致性，置 true 不会改变行为。
type FeaturesConfig struct {
	RCA              bool `mapstructure:"rca" json:"rca" yaml:"rca"`
	NativeFileDialog bool `mapstructure:"native_file_dialog" json:"native_file_dialog" yaml:"native_file_dialog"`
	BrowserUpload    bool `mapstructure:"browser_upload" json:"browser_upload" yaml:"browser_upload"`
	LANMode          bool `mapstructure:"lan_mode" json:"lan_mode" yaml:"lan_mode"`
	Masking          bool `mapstructure:"masking" json:"masking" yaml:"masking"`
}

// DefaultFeatures 返回产品默认开关状态（RCA 关闭，原生对话框开启）。
func DefaultFeatures() FeaturesConfig {
	return FeaturesConfig{
		RCA:              false,
		NativeFileDialog: true,
		BrowserUpload:    false,
		LANMode:          false,
		Masking:          false,
	}
}

type TaskConfig struct {
	ParseWorkers int `mapstructure:"parse_workers" json:"parse_workers" yaml:"parse_workers"`
}

type RCAConfig struct {
	Timeout     int `mapstructure:"timeout" json:"timeout" yaml:"timeout"`
	Concurrency int `mapstructure:"concurrency" json:"concurrency" yaml:"concurrency"`
}

type ServerConfig struct {
	Host string `mapstructure:"host" json:"host" yaml:"host"`
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
  mode: release     # 运行模式: release 或 debug

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

rca:
  timeout: 300              # 根因分析单任务执行超时时间(秒), 默认 300
  concurrency: 2            # 全局最大并行分析任务数, 默认 2

task:
  parse_workers: 0          # 日志分行解析与参数提取并发协程数, 0 表示自适应当前宿主机 CPU 核心数

# 产品功能开关
#   rca: 关联事件（实验性）分析。默认关闭；开启后仅在工作台"高级"抽屉中手动触发，
#        导入/重分析不会自动运行。本产品对 RCA 只保留、不演进。
#   其余开关为产品边界的显式声明（浏览器上传/LAN 多用户/脱敏均不做），修改不改变行为。
features:
  rca: false
  native_file_dialog: true
  browser_upload: false
  lan_mode: false
  masking: false
`

func Load(configPath string) (*Config, error) {
	configMu.Lock()
	defer configMu.Unlock()

	v := viper.New()

	// 默认值
	v.SetDefault("server.host", "127.0.0.1")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.mode", "release")
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
	v.SetDefault("rca.timeout", 300)
	v.SetDefault("rca.concurrency", 2)
	v.SetDefault("task.parse_workers", 0)
	v.SetDefault("features.rca", false)
	v.SetDefault("features.native_file_dialog", true)
	v.SetDefault("features.browser_upload", false)
	v.SetDefault("features.lan_mode", false)
	v.SetDefault("features.masking", false)

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
	"info":    true,
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
		cfg.Server.Mode = "release"
	}
	modeLower := strings.ToLower(strings.TrimSpace(cfg.Server.Mode))
	if modeLower == "info" {
		cfg.Server.Mode = "release"
	} else if !validServerModes[modeLower] {
		fmt.Fprintf(os.Stderr, "[Config][WARN] unknown server.mode=%q, falling back to release (config file: %s)\n",
			cfg.Server.Mode, where)
		cfg.Server.Mode = "release"
	} else {
		cfg.Server.Mode = modeLower
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

// Features 返回当前功能开关快照。
//
// 未初始化配置（单元测试/工具函数）时返回安全默认值：RCA 关闭，
// 从而保证"默认不自动跑 RCA"不会因为漏初始化而被破坏。
func Features() FeaturesConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	if GlobalConfig == nil {
		return DefaultFeatures()
	}
	return GlobalConfig.Features
}

// featureToggleKeys 允许运行时修改的开关；其余开关属于产品边界声明，不可开启。
var featureToggleKeys = map[string]bool{
	"rca":                true,
	"native_file_dialog": true,
}

// ErrFeatureNotImplemented 表示该开关属于"产品明确不做"的能力，禁止通过接口开启。
var ErrFeatureNotImplemented = fmt.Errorf("feature is not implemented by product decision")

// UpdateFeatures 按补丁更新功能开关并持久化到配置文件。
//
// 仅允许修改 featureToggleKeys 中的开关；浏览器上传 / LAN 多用户 / 脱敏
// 属于规划中"本年度明确不做"的能力，显式拒绝而不是静默接受，
// 避免使用者误以为自己开启了某个不存在的功能。
func UpdateFeatures(patch map[string]bool) (FeaturesConfig, error) {
	if len(patch) == 0 {
		return Features(), nil
	}

	configMu.Lock()
	if GlobalConfig == nil {
		configMu.Unlock()
		return DefaultFeatures(), fmt.Errorf("config not initialized")
	}
	for key := range patch {
		if !featureToggleKeys[key] {
			snapshot := GlobalConfig.Features
			configMu.Unlock()
			return snapshot, fmt.Errorf("%w: %s", ErrFeatureNotImplemented, key)
		}
	}
	next := GlobalConfig.Features
	for key, enabled := range patch {
		switch key {
		case "rca":
			next.RCA = enabled
		case "native_file_dialog":
			next.NativeFileDialog = enabled
		}
	}
	GlobalConfig.Features = next
	configMu.Unlock()

	if err := Save(""); err != nil {
		return next, fmt.Errorf("persist features failed: %w", err)
	}
	return next, nil
}
