package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/config"
)

// OnboardingState 首启引导状态。
//
// 状态存后端数据目录（不是 localStorage），清浏览器缓存不会重复触发。
type OnboardingState struct {
	CompletedSteps []int     `json:"completed_steps"`
	Skipped        bool      `json:"skipped"`
	Dismissed      bool      `json:"dismissed"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// OnboardingFile 引导状态文件名。
const OnboardingFile = "onboarding.json"

var onboardingMu sync.Mutex

func onboardingPath() (string, error) {
	if config.GlobalConfig == nil {
		return "", fmt.Errorf("config not initialized")
	}
	return filepath.Join(config.GlobalConfig.Storage.DataDir, OnboardingFile), nil
}

func loadOnboarding() (OnboardingState, error) {
	state := OnboardingState{CompletedSteps: []int{}}
	path, err := onboardingPath()
	if err != nil {
		return state, err
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return state, nil
		}
		return state, readErr
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return OnboardingState{CompletedSteps: []int{}}, nil
	}
	if state.CompletedSteps == nil {
		state.CompletedSteps = []int{}
	}
	return state, nil
}

func saveOnboarding(state OnboardingState) error {
	path, err := onboardingPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	state.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// shouldShowOnboarding 从未完成且未跳过/关闭时自动弹出。
func shouldShowOnboarding(state OnboardingState) bool {
	return !state.Dismissed && !state.Skipped && len(state.CompletedSteps) < 4
}

// GetOnboarding 返回引导状态（含 should_show）。
func (h *SystemHandler) GetOnboarding(c *gin.Context) {
	onboardingMu.Lock()
	defer onboardingMu.Unlock()
	state, err := loadOnboarding()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	SuccessResponse(c, gin.H{
		"state":       state,
		"should_show": shouldShowOnboarding(state),
	})
}

// UpdateOnboarding 更新引导状态（完成步骤/跳过/关闭 checklist）。
func (h *SystemHandler) UpdateOnboarding(c *gin.Context) {
	var body struct {
		CompletedSteps []int `json:"completed_steps"`
		Skipped        *bool `json:"skipped"`
		Dismissed      *bool `json:"dismissed"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request body: "+err.Error())
		return
	}

	onboardingMu.Lock()
	defer onboardingMu.Unlock()
	state, err := loadOnboarding()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	seen := make(map[int]bool, len(state.CompletedSteps)+len(body.CompletedSteps))
	merged := make([]int, 0, 4)
	for _, step := range append(state.CompletedSteps, body.CompletedSteps...) {
		if step < 1 || step > 4 {
			ErrorResponse(c, http.StatusBadRequest, -1, "completed_steps must be within 1~4")
			return
		}
		if !seen[step] {
			seen[step] = true
			merged = append(merged, step)
		}
	}
	sort.Ints(merged)
	state.CompletedSteps = merged
	if body.Skipped != nil {
		state.Skipped = *body.Skipped
	}
	if body.Dismissed != nil {
		state.Dismissed = *body.Dismissed
	}
	if err := saveOnboarding(state); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "保存引导状态失败: "+err.Error())
		return
	}
	SuccessResponse(c, gin.H{
		"state":       state,
		"should_show": shouldShowOnboarding(state),
	}, "引导状态已更新")
}
