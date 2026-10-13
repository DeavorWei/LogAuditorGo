//go:build !windows

package tray

// nonWindowsTray 非 Windows 平台的空实现
type nonWindowsTray struct {
	baseTray
}

// New 非 Windows 平台返回空实现
func New(cfg Config) Tray {
	return &nonWindowsTray{
		baseTray: baseTray{
			cfg: cfg,
		},
	}
}

func (t *nonWindowsTray) Start() error {
	return nil
}

func (t *nonWindowsTray) Stop() {
}

func (t *nonWindowsTray) ShowNotification(title, message string) {
}
