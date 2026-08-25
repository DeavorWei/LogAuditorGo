package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/goccy/go-yaml"
	"github.com/spf13/viper"
	"logauditorgo/pkg/logger"
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
}

type LogConfig struct {
	Level     string `mapstructure:"level" json:"level" yaml:"level"`
	Format    string `mapstructure:"format" json:"format" yaml:"format"`
	Dir       string `mapstructure:"dir" json:"dir" yaml:"dir"`
	MaxSizeMB int    `mapstructure:"max_size_mb" json:"max_size_mb" yaml:"max_size_mb"`
	MaxDays   int    `mapstructure:"max_days" json:"max_days" yaml:"max_days"`
}

var (
	GlobalConfig   *Config
	ConfigFileUsed string
	configMu       sync.RWMutex
)

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

log:
  level: debug               # 日志级别: debug, info, warn, error
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
	v.SetDefault("log.level", "debug")
	v.SetDefault("log.format", "console")
	v.SetDefault("log.dir", filepath.Join(DefaultDataDir, "log"))
	v.SetDefault("log.max_size_mb", 1024)
	v.SetDefault("log.max_days", 180)

	targetConfigFile := configPath
	if targetConfigFile == "" {
		targetConfigFile = filepath.Join(DefaultDataDir, "config.yaml")
	}

	// 如果配置文件不存在，自动在对应目录下生成一份默认配置文件
	if _, err := os.Stat(targetConfigFile); os.IsNotExist(err) {
		configDir := filepath.Dir(targetConfigFile)
		if configDir != "" && configDir != "." {
			_ = os.MkdirAll(configDir, 0755)
		}
		_ = os.WriteFile(targetConfigFile, []byte(defaultConfigFileTemplate), 0644)
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

	if err := os.WriteFile(targetPath, data, 0644); err != nil {
		return fmt.Errorf("write config file %s failed: %w", targetPath, err)
	}

	return nil
}

// UpdateLogConfig 动态更新日志配置，同步至运行期 Logger 并持久化保存到配置文件
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

	// 同步更新底层 logger
	logger.UpdatePolicy(currentLogCfg.MaxSizeMB, currentLogCfg.MaxDays)
	if currentLogCfg.Level != "" {
		logger.SetLevel(currentLogCfg.Level)
	}

	// 持久化保存
	if err := Save(""); err != nil {
		return nil, fmt.Errorf("persist config failed: %w", err)
	}

	return &currentLogCfg, nil
}
