package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Storage StorageConfig `mapstructure:"storage"`
	Log     LogConfig     `mapstructure:"log"`
}

type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
}

type StorageConfig struct {
	DataDir     string `mapstructure:"data_dir"`
	KnowledgeDB string `mapstructure:"knowledge_db"`
	BleveIndex  string `mapstructure:"bleve_index"`
	TaskDir     string `mapstructure:"task_dir"`
	UploadDir   string `mapstructure:"upload_dir"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

var GlobalConfig *Config

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
  level: debug      # 日志级别: debug, info, warn, error
  format: console   # 日志格式: console 或 json
`

func Load(configPath string) (*Config, error) {
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
	configFileUsed := targetConfigFile
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config failed: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config failed: %w", err)
	}

	// 确保存储目录存在
	dirs := []string{
		cfg.Storage.DataDir,
		filepath.Dir(cfg.Storage.KnowledgeDB),
		filepath.Dir(cfg.Storage.BleveIndex),
		cfg.Storage.TaskDir,
		cfg.Storage.UploadDir,
	}
	for _, d := range dirs {
		if d != "" && d != "." {
			if err := os.MkdirAll(d, 0755); err != nil {
				return nil, fmt.Errorf("create dir %s failed: %w", d, err)
			}
		}
	}

	GlobalConfig = &cfg
	if configFileUsed != "" {
		fmt.Printf("[Config] Loaded configuration from: %s\n", configFileUsed)
	}
	return &cfg, nil
}

