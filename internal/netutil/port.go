package netutil

import (
	"fmt"
	"net"
)

// Logger 最小日志接口，解耦具体日志组件便于测试
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// DefaultMaxPortRetries 默认端口最大重试次数
const DefaultMaxPortRetries = 5

// ListenWithPortRetry 尝试在 host 且从 startPort 开始监听 TCP 端口。
// 如果当前端口启动失败（如端口冲突/已被占用），会自动累加端口继续尝试，最多尝试 maxRetries 次。
// 成功时返回创建好的 net.Listener 与实际绑定的端口号；
// 失败达到上限时返回错误，调用方可据此终止程序。
func ListenWithPortRetry(host string, startPort int, maxRetries int, log Logger) (net.Listener, int, error) {
	if maxRetries <= 0 {
		maxRetries = DefaultMaxPortRetries
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		currentPort := startPort + (attempt - 1)
		if currentPort < 1 || currentPort > 65535 {
			lastErr = fmt.Errorf("端口号 %d 超出有效范围 [1, 65535]", currentPort)
			if log != nil {
				log.Errorf("端口重试越界终止: %v", lastErr)
			}
			break
		}

		addr := fmt.Sprintf("%s:%d", host, currentPort)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			if attempt > 1 {
				if log != nil {
					log.Warnf("起始端口 %d 启动失败（可能被占用），已自动递增端口至 %d 成功绑定 (第 %d/%d 次尝试)", startPort, currentPort, attempt, maxRetries)
				}
			} else {
				if log != nil {
					log.Infof("HTTP 服务成功在端口 %d 启动监听", currentPort)
				}
			}
			return ln, currentPort, nil
		}

		lastErr = err
		if log != nil {
			log.Warnf("端口 %d 启动失败 (第 %d/%d 次尝试): %v", currentPort, attempt, maxRetries, err)
		}
	}

	finalErr := fmt.Errorf("连续 %d 次尝试绑定端口失败 (起始端口: %d): %w", maxRetries, startPort, lastErr)
	if log != nil {
		log.Errorf("%v，终止启动", finalErr)
	}
	return nil, 0, finalErr
}
