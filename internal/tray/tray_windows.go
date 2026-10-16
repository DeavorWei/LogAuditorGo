//go:build windows

package tray

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	shell32                      = windows.NewLazySystemDLL("shell32.dll")
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	procAppendMenuW              = user32.NewProc("AppendMenuW")
	procSetMenuDefaultItem       = user32.NewProc("SetMenuDefaultItem")
	procTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	procDestroyMenu              = user32.NewProc("DestroyMenu")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procOpenClipboard            = user32.NewProc("OpenClipboard")
	procEmptyClipboard           = user32.NewProc("EmptyClipboard")
	procSetClipboardData         = user32.NewProc("SetClipboardData")
	procCloseClipboard           = user32.NewProc("CloseClipboard")
	procShell_NotifyIconW        = shell32.NewProc("Shell_NotifyIconW")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon              = user32.NewProc("DestroyIcon")
	procGlobalAlloc              = kernel32.NewProc("GlobalAlloc")
	procGlobalLock               = kernel32.NewProc("GlobalLock")
	procGlobalUnlock             = kernel32.NewProc("GlobalUnlock")
	procGlobalFree               = kernel32.NewProc("GlobalFree")
	procRtlMoveMemory            = kernel32.NewProc("RtlMoveMemory")
)

const (
	wmUser          = 0x0400
	wmTrayIcon      = wmUser + 100
	wmCommand       = 0x0111
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B
	wmNull          = 0x0000

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010
	niifInfo   = 0x00000001

	mfString     = 0x00000000
	mfSeparator  = 0x00000800
	tpmBottom    = 0x0020
	tpmLeftAlign = 0x0000
	tpmRightBtn  = 0x0002

	mbOk           = 0x00000000
	mbYesNo        = 0x00000004
	mbIconInfo     = 0x00000040
	mbIconQuestion = 0x00000020
	idYes          = 6

	gmemMoveable   = 0x0002
	cfUnicodeText  = 13
)

// 菜单项 ID 定义
const (
	cmdOpenConsole   = 1001 // 打开系统控制台
	cmdOpenWorkbench = 1002 // 进入审计工作台
	cmdOpenKnowledge = 1003 // 进入知识中心
	cmdOpenSettings  = 1004 // 系统设置
	cmdOpenLogs      = 1005 // 打开日志目录
	cmdOpenData      = 1006 // 打开数据目录
	cmdCopyURL       = 1007 // 复制访问地址
	cmdStatusAbout   = 1008 // 系统状态与关于
	cmdExitServer    = 1009 // 退出服务
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type point struct {
	x int32
	y int32
}

type msg struct {
	hWnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             windows.Handle
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            windows.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	timeoutOrVersion uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     windows.Handle
}

type windowsTray struct {
	baseTray
	hwnd      windows.Handle
	hIcon     windows.Handle
	nid       notifyIconDataW
	stopOnce  sync.Once
	stopCh    chan struct{}
	stoppedCh chan struct{}
}

// 全局托盘实例指针，供 WndProc 回调安全分发
var (
	currentTrayMu sync.RWMutex
	currentTray   *windowsTray
)

// New 创建系统托盘实例
func New(cfg Config) Tray {
	if cfg.Host == "" {
		cfg.Host = "localhost"
	}
	if cfg.Theme == "" {
		cfg.Theme = ThemeBlueShield
	}
	if cfg.Version == "" {
		cfg.Version = "v1.0.0"
	}

	return &windowsTray{
		baseTray: baseTray{
			cfg:       cfg,
			startTime: time.Now(),
		},
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

// Start 启动 Windows 原生系统托盘 (阻塞于消息循环，外部通常在 goroutine 中调用)
func (t *windowsTray) Start() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	currentTrayMu.Lock()
	currentTray = t
	currentTrayMu.Unlock()

	className, _ := windows.UTF16PtrFromString("LogAuditorGo_TrayWindow")
	wndProcCallback := windows.NewCallback(wndProc)

	var wc wndClassExW
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	wc.lpfnWndProc = wndProcCallback
	wc.lpszClassName = className

	atom, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		// 如果已注册则继续尝试创建窗口
	}

	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0,
		0, 0, 0, 0,
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("failed to create tray message window: %w", err)
	}
	t.hwnd = windows.Handle(hwnd)

	// 加载或生成图标
	var iconBytes []byte
	if t.cfg.IconPath != "" {
		data, err := os.ReadFile(t.cfg.IconPath)
		if err == nil && len(data) > 0 {
			iconBytes = data
		}
	}
	if len(iconBytes) == 0 {
		iconBytes = GenerateIconPNG(t.cfg.Theme, 32)
	}

	hIcon, _, _ := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&iconBytes[0])),
		uintptr(len(iconBytes)),
		1,          // fIcon = TRUE
		0x00030000, // dwVersion
		32,         // cxDesired
		32,         // cyDesired
		0,          // Flags
	)
	if hIcon == 0 {
		return fmt.Errorf("failed to create native HICON from icon bytes")
	}
	t.hIcon = windows.Handle(hIcon)

	// 初始化 NOTIFYICONDATAW
	t.nid.cbSize = uint32(unsafe.Sizeof(t.nid))
	t.nid.hWnd = t.hwnd
	t.nid.uID = 1001
	t.nid.uFlags = nifMessage | nifIcon | nifTip
	t.nid.uCallbackMessage = wmTrayIcon
	t.nid.hIcon = t.hIcon

	tipText := fmt.Sprintf("LogAuditorGo 日志审计系统\n端口: %d (运行中)", t.cfg.Port)
	tipU16, _ := windows.UTF16FromString(tipText)
	copy(t.nid.szTip[:], tipU16)

	ret, _, _ := procShell_NotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&t.nid)))
	if ret == 0 {
		return fmt.Errorf("failed to add icon to system tray via Shell_NotifyIcon")
	}

	defer func() {
		procShell_NotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&t.nid)))
		if t.hIcon != 0 {
			procDestroyIcon.Call(uintptr(t.hIcon))
		}
		if t.hwnd != 0 {
			procDestroyWindow.Call(uintptr(t.hwnd))
		}
		close(t.stoppedCh)
	}()

	// 监听后台主动停止信号
	go func() {
		select {
		case <-t.stopCh:
			if t.hwnd != 0 {
				procPostMessageW.Call(uintptr(t.hwnd), wmUser+999, 0, 0)
			}
		}
	}()

	// Win32 标准消息循环
	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	return nil
}

// Stop 停止托盘
func (t *windowsTray) Stop() {
	t.stopOnce.Do(func() {
		close(t.stopCh)
		select {
		case <-t.stoppedCh:
		case <-time.After(1 * time.Second):
		}
	})
}

// ShowNotification 弹出 Windows 系统气泡通知
func (t *windowsTray) ShowNotification(title, message string) {
	if t.hwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = t.hwnd
	nid.uID = t.nid.uID
	nid.uFlags = nifInfo
	nid.dwInfoFlags = niifInfo

	titleU16, _ := windows.UTF16FromString(title)
	copy(nid.szInfoTitle[:], titleU16)

	msgU16, _ := windows.UTF16FromString(message)
	copy(nid.szInfo[:], msgU16)

	procShell_NotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// wndProc 消息回调函数
func wndProc(hwnd windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
	currentTrayMu.RLock()
	t := currentTray
	currentTrayMu.RUnlock()

	if t == nil {
		r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
		return r
	}

	switch message {
	case wmTrayIcon:
		switch lParam {
		case wmLButtonDblClk:
			// 审计修复 ISSUE-TRAY-02: 仅响应左键双击，避免与单击消息重叠导致连续多次拉起浏览器
			go t.openURL("/")
		case wmRButtonUp, wmContextMenu:
			// 右键点击：弹出右键上下文菜单
			t.showContextMenu()
		}
		return 0

	case wmCommand:
		menuID := uint16(wParam & 0xFFFF)
		t.handleMenuCommand(menuID)
		return 0

	case wmUser + 999:
		procPostQuitMessage.Call(0)
		return 0
	}

	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}

// showContextMenu 弹出右键菜单
func (t *windowsTray) showContextMenu() {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	appendMenuItem := func(id uint16, title string) {
		uTitle, _ := windows.UTF16PtrFromString(title)
		procAppendMenuW.Call(hMenu, mfString, uintptr(id), uintptr(unsafe.Pointer(uTitle)))
	}
	appendSeparator := func() {
		procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
	}

	// 1. 核心业务直达
	appendMenuItem(cmdOpenConsole, "🌐 打开系统控制台 (&C)")
	appendMenuItem(cmdOpenWorkbench, "📊 审计工作台 (&W)")
	appendMenuItem(cmdOpenKnowledge, "📚 知识库中心 (&K)")
	appendMenuItem(cmdOpenSettings, "⚙️ 系统设置 (&S)")

	appendSeparator()

	// 2. 本地资源与快捷操作
	appendMenuItem(cmdOpenLogs, "📁 打开日志目录 (&L)")
	appendMenuItem(cmdOpenData, "💾 打开数据目录 (&D)")
	appendMenuItem(cmdCopyURL, "📋 复制访问地址 (&U)")

	appendSeparator()

	// 3. 系统状态与关于
	appendMenuItem(cmdStatusAbout, "ℹ️ 系统状态与关于... (&A)")

	appendSeparator()

	// 4. 退出服务
	appendMenuItem(cmdExitServer, "🛑 退出服务 (&Q)")

	// 默认项加粗高亮 (打开系统控制台)
	procSetMenuDefaultItem.Call(hMenu, uintptr(cmdOpenConsole), 0)

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	// 设置前台窗口，保证点击外部菜单正常自动隐藏
	procSetForegroundWindow.Call(uintptr(t.hwnd))

	procTrackPopupMenu.Call(
		hMenu,
		tpmRightBtn|tpmBottom|tpmLeftAlign,
		uintptr(pt.x),
		uintptr(pt.y),
		0,
		uintptr(t.hwnd),
		0,
	)

	procPostMessageW.Call(uintptr(t.hwnd), wmNull, 0, 0)
}

// handleMenuCommand 异步处理菜单项指令
func (t *windowsTray) handleMenuCommand(menuID uint16) {
	switch menuID {
	case cmdOpenConsole:
		go t.openURL("/")
	case cmdOpenWorkbench:
		go t.openURL("/audit")
	case cmdOpenKnowledge:
		go t.openURL("/knowledge")
	case cmdOpenSettings:
		go t.openURL("/settings")
	case cmdOpenLogs:
		go t.openFolder(t.cfg.LogDir)
	case cmdOpenData:
		go t.openFolder(t.cfg.DataDir)
	case cmdCopyURL:
		go t.copyURLToClipboard()
	case cmdStatusAbout:
		go t.showAboutDialog()
	case cmdExitServer:
		go t.confirmAndExit()
	}
}

// openURL 在默认浏览器中打开指定子路径
func (t *windowsTray) openURL(subpath string) {
	url := t.GetURL(subpath)
	verbPtr, _ := windows.UTF16PtrFromString("open")
	urlPtr, _ := windows.UTF16PtrFromString(url)
	_ = windows.ShellExecute(0, verbPtr, urlPtr, nil, nil, windows.SW_SHOWNORMAL)
}

// openFolder 在 Windows 文件资源管理器中打开目录
func (t *windowsTray) openFolder(folderPath string) {
	if folderPath == "" {
		folderPath = "."
	}
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		absPath = folderPath
	}
	_ = os.MkdirAll(absPath, 0755)
	cmd := exec.Command("explorer", absPath)
	_ = cmd.Start()
}

// copyURLToClipboard 将服务访问地址写入剪贴板并提示
func (t *windowsTray) copyURLToClipboard() {
	url := t.GetURL("/")
	if err := writeClipboardText(url); err == nil {
		t.ShowNotification("已复制访问地址", fmt.Sprintf("服务地址已复制到剪贴板:\n%s", url))
	}
}

// showAboutDialog 弹出系统状态与关于信息
func (t *windowsTray) showAboutDialog() {
	uptime := time.Since(t.startTime).Round(time.Second)
	hours := int(uptime.Hours())
	minutes := int(uptime.Minutes()) % 60
	seconds := int(uptime.Seconds()) % 60

	url := t.GetURL("/")
	pid := os.Getpid()

	infoText := fmt.Sprintf(
		"【LogAuditorGo 日志审计与分析系统】\n\n"+
			"• 软件版本：%s\n"+
			"• 进程 PID：%d\n"+
			"• 运行状态：正常运行中 (Active)\n"+
			"• 服务地址：%s\n"+
			"• 持续运行：%d 小时 %d 分钟 %d 秒\n"+
			"• 日志目录：%s\n"+
			"• 数据目录：%s\n\n"+
			"提示：左键单击或双击托盘图标可快速打开控制台主页。",
		t.cfg.Version,
		pid,
		url,
		hours, minutes, seconds,
		t.cfg.LogDir,
		t.cfg.DataDir,
	)

	titleU16, _ := windows.UTF16PtrFromString("关于 LogAuditorGo")
	textU16, _ := windows.UTF16PtrFromString(infoText)
	procMessageBoxW.Call(
		uintptr(t.hwnd),
		uintptr(unsafe.Pointer(textU16)),
		uintptr(unsafe.Pointer(titleU16)),
		mbOk|mbIconInfo,
	)
}

// confirmAndExit 弹出退出确认框并在确认后执行优雅退出
func (t *windowsTray) confirmAndExit() {
	titleU16, _ := windows.UTF16PtrFromString("退出确认")
	textU16, _ := windows.UTF16PtrFromString("确定要停止并退出 LogAuditorGo 服务吗？\n\n退出后将停止日志解析、分析引擎与 Web 控制台服务。")

	ret, _, _ := procMessageBoxW.Call(
		uintptr(t.hwnd),
		uintptr(unsafe.Pointer(textU16)),
		uintptr(unsafe.Pointer(titleU16)),
		mbYesNo|mbIconQuestion,
	)

	if ret == idYes {
		t.Stop()
		if t.cfg.OnExit != nil {
			t.cfg.OnExit()
		}
	}
}

// writeClipboardText 纯 Win32 API 写入文本到剪贴板
func writeClipboardText(text string) error {
	u16, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}

	ret, _, _ := procOpenClipboard.Call(0)
	if ret == 0 {
		return fmt.Errorf("failed to open clipboard")
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()

	size := uintptr(len(u16) * 2)
	hMem, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if hMem == 0 {
		return fmt.Errorf("failed to alloc global memory")
	}

	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("failed to lock global memory")
	}

	// 审计修复 ISSUE-TRAY-01: 使用 Win32 原生 RtlMoveMemory 直接进行内存拷贝，
	// 彻底杜绝 unsafe.Slice((*uint16)(unsafe.Pointer(ptr))) 引发的 unsafe.Pointer 违规告警与 GC 风险
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&u16[0])), size)

	procGlobalUnlock.Call(hMem)

	// SetClipboardData 成功后由操作系统接管内存所有权；失败则必须显式释放
	hData, _, _ := procSetClipboardData.Call(cfUnicodeText, hMem)
	if hData == 0 {
		procGlobalFree.Call(hMem)
		return fmt.Errorf("failed to set clipboard data")
	}
	return nil
}
