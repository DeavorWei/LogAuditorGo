//go:build windows

package nativedialog

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32              = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx = ole32.NewProc("CoInitializeEx")
	procCoUninitialize = ole32.NewProc("CoUninitialize")
	procCoCreateInst   = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree  = ole32.NewProc("CoTaskMemFree")
)

const (
	coinitApartmentThreaded = 0x2
	clsctxInprocServer      = 0x1

	fosPickFolders      = 0x20
	fosForceFileSystem  = 0x40
	fosAllowMultiSelect = 0x200
	fosPathMustExist    = 0x800
	fosFileMustExist    = 0x1000

	sigdnFileSysPath = 0x80058000

	comCancelled = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidFileOpenDialog = guid{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = guid{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
)

type platformOpener struct{}

// Supported Windows 平台恒为 true；真正失败会在调用时降级为 ErrUnsupported。
func (platformOpener) Supported() bool { return true }

func (platformOpener) OpenFolder(opts Options) (string, error) {
	opts.Multi = false
	paths, err := showDialog(opts, true)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", ErrCanceled
	}
	return paths[0], nil
}

func (platformOpener) OpenFiles(opts Options) ([]string, error) {
	return showDialog(opts, false)
}

func showDialog(opts Options, folder bool) ([]string, error) {
	// COM STA：对话框必须在初始化过 COM 的线程上显示，且线程需锁定避免
	// Go runtime 在调用前后把 goroutine 迁移到别的 OS 线程。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	// S_FALSE(1) 表示该线程已初始化，同样可用；RPC_E_CHANGED_MODE 等负值才是失败。
	if int32(hr) < 0 {
		return nil, fmt.Errorf("%w: CoInitializeEx failed (0x%08x)", ErrUnsupported, uint32(hr))
	}
	defer procCoUninitialize.Call()

	var dialogPtr uintptr
	hr, _, _ = procCoCreateInst.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidFileOpenDialog)),
		uintptr(unsafe.Pointer(&dialogPtr)),
	)
	if int32(hr) < 0 || dialogPtr == 0 {
		return nil, fmt.Errorf("%w: CoCreateInstance failed (0x%08x)", ErrUnsupported, uint32(hr))
	}
	dialog := (*fileOpenDialog)(unsafe.Pointer(dialogPtr))
	defer dialog.release()

	var current uint32
	if hr := dialog.getOptions(&current); int32(hr) >= 0 {
		options := current | fosForceFileSystem | fosPathMustExist
		if folder {
			options |= fosPickFolders
		} else {
			options |= fosFileMustExist
			if opts.Multi {
				options |= fosAllowMultiSelect
			}
		}
		_ = dialog.setOptions(options)
	}

	if title := opts.Title; title != "" {
		if ptr, err := windows.UTF16PtrFromString(title); err == nil {
			_ = dialog.setTitle(ptr)
			runtime.KeepAlive(ptr)
		}
	}
	if !folder {
		if specs, keepAlive := buildFilterSpecs(opts.Exts); len(specs) > 0 {
			_ = dialog.setFileTypes(specs)
			runtime.KeepAlive(keepAlive)
		}
	}

	showHR := dialog.show(0)
	if uint32(showHR) == comCancelled {
		return nil, ErrCanceled
	}
	if int32(showHR) < 0 {
		return nil, fmt.Errorf("show file dialog failed (0x%08x)", uint32(showHR))
	}

	if folder {
		var itemPtr uintptr
		if hr := dialog.getResult(&itemPtr); int32(hr) < 0 || itemPtr == 0 {
			return nil, fmt.Errorf("get dialog result failed (0x%08x)", uint32(hr))
		}
		item := (*shellItem)(unsafe.Pointer(itemPtr))
		defer item.release()
		path, err := item.displayName()
		if err != nil {
			return nil, err
		}
		return []string{path}, nil
	}

	var arrayPtr uintptr
	if hr := dialog.getResults(&arrayPtr); int32(hr) < 0 || arrayPtr == 0 {
		return nil, fmt.Errorf("get dialog results failed (0x%08x)", uint32(hr))
	}
	array := (*shellItemArray)(unsafe.Pointer(arrayPtr))
	defer array.release()

	var count uint32
	if hr := array.getCount(&count); int32(hr) < 0 {
		return nil, fmt.Errorf("count dialog results failed (0x%08x)", uint32(hr))
	}

	paths := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		var itemPtr uintptr
		if hr := array.getItemAt(i, &itemPtr); int32(hr) < 0 || itemPtr == 0 {
			continue
		}
		item := (*shellItem)(unsafe.Pointer(itemPtr))
		path, err := item.displayName()
		item.release()
		if err == nil && path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, ErrCanceled
	}
	return paths, nil
}

// ---- COM 接口与虚表 ----

type fileOpenDialog struct{ vtbl *fileOpenDialogVtbl }

type fileOpenDialogVtbl struct {
	QueryInterface      uintptr
	AddRef              uintptr
	Release             uintptr
	Show                uintptr
	SetFileTypes        uintptr
	SetFileTypeIndex    uintptr
	GetFileTypeIndex    uintptr
	Advise              uintptr
	Unadvise            uintptr
	SetOptions          uintptr
	GetOptions          uintptr
	SetDefaultFolder    uintptr
	SetFolder           uintptr
	GetFolder           uintptr
	GetCurrentSelection uintptr
	SetFileName         uintptr
	GetFileName         uintptr
	SetTitle            uintptr
	SetOkButtonLabel    uintptr
	SetFileNameLabel    uintptr
	GetResult           uintptr
	AddPlace            uintptr
	SetDefaultExtension uintptr
	Close               uintptr
	SetClientGuid       uintptr
	ClearClientData     uintptr
	SetFilter           uintptr
	GetResults          uintptr
	GetSelectedItems    uintptr
}

func (d *fileOpenDialog) release() {
	if d != nil && d.vtbl != nil {
		syscall.SyscallN(d.vtbl.Release, uintptr(unsafe.Pointer(d)))
	}
}

func (d *fileOpenDialog) show(hwnd uintptr) int32 {
	r, _, _ := syscall.SyscallN(d.vtbl.Show, uintptr(unsafe.Pointer(d)), hwnd)
	return int32(r)
}

func (d *fileOpenDialog) setOptions(options uint32) int32 {
	r, _, _ := syscall.SyscallN(d.vtbl.SetOptions, uintptr(unsafe.Pointer(d)), uintptr(options))
	return int32(r)
}

func (d *fileOpenDialog) getOptions(out *uint32) int32 {
	r, _, _ := syscall.SyscallN(d.vtbl.GetOptions, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(out)))
	return int32(r)
}

func (d *fileOpenDialog) setTitle(title *uint16) int32 {
	r, _, _ := syscall.SyscallN(d.vtbl.SetTitle, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(title)))
	return int32(r)
}

type comFilterSpec struct {
	Name *uint16
	Spec *uint16
}

func (d *fileOpenDialog) setFileTypes(specs []comFilterSpec) int32 {
	if len(specs) == 0 {
		return 0
	}
	r, _, _ := syscall.SyscallN(
		d.vtbl.SetFileTypes,
		uintptr(unsafe.Pointer(d)),
		uintptr(len(specs)),
		uintptr(unsafe.Pointer(&specs[0])),
	)
	return int32(r)
}

func (d *fileOpenDialog) getResult(out *uintptr) int32 {
	r, _, _ := syscall.SyscallN(d.vtbl.GetResult, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(out)))
	return int32(r)
}

func (d *fileOpenDialog) getResults(out *uintptr) int32 {
	r, _, _ := syscall.SyscallN(d.vtbl.GetResults, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(out)))
	return int32(r)
}

type shellItemArray struct{ vtbl *shellItemArrayVtbl }

type shellItemArrayVtbl struct {
	QueryInterface             uintptr
	AddRef                     uintptr
	Release                    uintptr
	BindToHandler              uintptr
	GetPropertyStore           uintptr
	GetPropertyDescriptionList uintptr
	GetAttributes              uintptr
	GetCount                   uintptr
	GetItemAt                  uintptr
	EnumItems                  uintptr
}

func (a *shellItemArray) release() {
	if a != nil && a.vtbl != nil {
		syscall.SyscallN(a.vtbl.Release, uintptr(unsafe.Pointer(a)))
	}
}

func (a *shellItemArray) getCount(out *uint32) int32 {
	r, _, _ := syscall.SyscallN(a.vtbl.GetCount, uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(out)))
	return int32(r)
}

func (a *shellItemArray) getItemAt(index uint32, out *uintptr) int32 {
	r, _, _ := syscall.SyscallN(a.vtbl.GetItemAt, uintptr(unsafe.Pointer(a)), uintptr(index), uintptr(unsafe.Pointer(out)))
	return int32(r)
}

type shellItem struct{ vtbl *shellItemVtbl }

type shellItemVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	BindToHandler  uintptr
	GetParent      uintptr
	GetDisplayName uintptr
	GetAttributes  uintptr
	Compare        uintptr
}

func (s *shellItem) release() {
	if s != nil && s.vtbl != nil {
		syscall.SyscallN(s.vtbl.Release, uintptr(unsafe.Pointer(s)))
	}
}

func (s *shellItem) displayName() (string, error) {
	var namePtr *uint16
	hr, _, _ := syscall.SyscallN(
		s.vtbl.GetDisplayName,
		uintptr(unsafe.Pointer(s)),
		uintptr(sigdnFileSysPath),
		uintptr(unsafe.Pointer(&namePtr)),
	)
	if int32(hr) < 0 || namePtr == nil {
		return "", fmt.Errorf("get item display name failed (0x%08x)", uint32(hr))
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(namePtr)))
	return windows.UTF16PtrToString(namePtr), nil
}

// buildFilterSpecs 构造文件类型过滤器；返回的 keepAlive 需在调用期间保持引用。
func buildFilterSpecs(exts []string) ([]comFilterSpec, []*uint16) {
	normalized := NormalizeExts(exts)
	if len(normalized) == 0 {
		return nil, nil
	}

	patterns := make([]string, 0, len(normalized))
	for _, ext := range normalized {
		patterns = append(patterns, "*."+ext)
	}
	spec := strings.Join(patterns, ";")
	name := "日志文件 (" + spec + ")"

	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, nil
	}
	specPtr, err := windows.UTF16PtrFromString(spec)
	if err != nil {
		return nil, nil
	}
	allNamePtr, _ := windows.UTF16PtrFromString("所有文件 (*.*)")
	allSpecPtr, _ := windows.UTF16PtrFromString("*.*")

	specs := []comFilterSpec{
		{Name: namePtr, Spec: specPtr},
		{Name: allNamePtr, Spec: allSpecPtr},
	}
	keepAlive := []*uint16{namePtr, specPtr, allNamePtr, allSpecPtr}
	return specs, keepAlive
}
