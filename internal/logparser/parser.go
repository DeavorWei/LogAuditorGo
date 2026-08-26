package logparser

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"logauditorgo/internal/model"
)

// LogParser 统一日志解析接口
type LogParser interface {
	Name() string
	Support(line string) bool
	Parse(line string) (*model.NormalizedLog, error)
}

var (
	parserList atomic.Value // holds []LogParser
	regMu      sync.Mutex
	defaultVRP = &VRPParser{}
)

func init() {
	parserList.Store([]LogParser{})
	RegisterParser(&USGSecurityParser{})
	RegisterParser(&VRPParser{})
}

// RegisterParser 注册自定义日志解析器
func RegisterParser(p LogParser) {
	regMu.Lock()
	defer regMu.Unlock()

	var curr []LogParser
	if val := parserList.Load(); val != nil {
		curr = val.([]LogParser)
	}
	newSlice := make([]LogParser, len(curr)+1)
	copy(newSlice, curr)
	newSlice[len(curr)] = p
	parserList.Store(newSlice)
}

// ParseLine 使用已注册的解析器自动解析单行日志
func ParseLine(line string) (norm *model.NormalizedLog, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in ParseLine: %v", r)
		}
	}()

	parsers, _ := parserList.Load().([]LogParser)
	for _, p := range parsers {
		if p.Support(line) {
			norm, err := p.Parse(line)
			if err == nil {
				return norm, nil
			}
		}
	}

	// 如果没有 Parser 声明完全支持，最后尝试 VRP 标准解析器
	return defaultVRP.Parse(line)
}

// ParseBatch 批量解析多行日志
func ParseBatch(lines []string) ([]*model.NormalizedLog, []error) {
	var logs []*model.NormalizedLog
	var errs []error

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		norm, err := ParseLine(line)
		if err != nil {
			errs = append(errs, fmt.Errorf("line %d parse error: %w", i+1, err))
		} else {
			logs = append(logs, norm)
		}
	}

	return logs, errs
}

