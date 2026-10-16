package netutil_test

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"logauditorgo/internal/netutil"
)

// mockLogger 记录测试日志
type mockLogger struct {
	infos  []string
	warns  []string
	errors []string
}

func (m *mockLogger) Infof(format string, args ...any) {
	m.infos = append(m.infos, fmt.Sprintf(format, args...))
}

func (m *mockLogger) Warnf(format string, args ...any) {
	m.warns = append(m.warns, fmt.Sprintf(format, args...))
}

func (m *mockLogger) Errorf(format string, args ...any) {
	m.errors = append(m.errors, fmt.Sprintf(format, args...))
}

func TestListenWithPortRetry_SuccessFirstAttempt(t *testing.T) {
	logger := &mockLogger{}

	// 动态申请一个可用起始端口
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to probe port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	ln, actualPort, err := netutil.ListenWithPortRetry("127.0.0.1", port, 5, logger)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	defer ln.Close()

	if actualPort != port {
		t.Errorf("expected port %d, got %d", port, actualPort)
	}
	if len(logger.infos) == 0 {
		t.Errorf("expected info logs")
	}
}

func TestListenWithPortRetry_ConflictAndAutoIncrement(t *testing.T) {
	logger := &mockLogger{}

	// 先占用一个端口
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to occupy port: %v", err)
	}
	defer blocker.Close()
	blockedPort := blocker.Addr().(*net.TCPAddr).Port

	// 尝试从 blockedPort 开始监听
	ln, actualPort, err := netutil.ListenWithPortRetry("127.0.0.1", blockedPort, 5, logger)
	if err != nil {
		t.Fatalf("expected retry success on next port, got error: %v", err)
	}
	defer ln.Close()

	if actualPort <= blockedPort {
		t.Errorf("expected actual port > %d, got %d", blockedPort, actualPort)
	}

	// 验证记录了告警日志
	hasWarn := false
	for _, w := range logger.warns {
		if strings.Contains(w, fmt.Sprintf("%d", blockedPort)) {
			hasWarn = true
			break
		}
	}
	if !hasWarn {
		t.Errorf("expected warning log about blocked port %d, got: %v", blockedPort, logger.warns)
	}
}

func TestListenWithPortRetry_MaxRetriesExceeded(t *testing.T) {
	logger := &mockLogger{}

	// 占用起始端口并连续占用 5 个端口
	const maxRetries = 5
	blockers := make([]net.Listener, maxRetries)
	
	// 先找到一块连续可用端口区域
	baseProbe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	basePort := baseProbe.Addr().(*net.TCPAddr).Port
	_ = baseProbe.Close()

	// 尝试连续占用 basePort 开始的 maxRetries 个端口
	occupied := 0
	for i := 0; i < maxRetries; i++ {
		p := basePort + i
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			blockers[occupied] = l
			occupied++
		}
	}
	defer func() {
		for i := 0; i < occupied; i++ {
			if blockers[i] != nil {
				_ = blockers[i].Close()
			}
		}
	}()

	// 如果连续占满了，测试超过最大重试次数失败
	if occupied == maxRetries {
		ln, _, err := netutil.ListenWithPortRetry("127.0.0.1", basePort, maxRetries, logger)
		if err == nil {
			_ = ln.Close()
			t.Errorf("expected failure when all ports are occupied, got nil error")
		}
		if len(logger.errors) == 0 {
			t.Errorf("expected error log when max retries exceeded")
		}
	}
}
