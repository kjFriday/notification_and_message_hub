package desktop

import (
	"fmt"
	"net"
	"net/http"
	"runtime"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
)

func init() {
	// WebView2 and the Win32 message loop must stay on the thread that created the window.
	runtime.LockOSThread()
}

// Options describe the window.
type Options struct {
	Title         string
	Width, Height uint
	DataPath      string       // WebView2 profile folder
	Handler       http.Handler // the program's pages
	// Bind adds JavaScript functions (window.<name>) that call into Go.
	Bind map[string]any
	// Ready, when set, runs on the UI thread once the window exists (e.g. for a smoke check).
	Ready func(w Window)
}

// Window is the open window.
type Window struct{ view webview2.WebView }

// Close ends Run.
func (w Window) Close() { w.view.Dispatch(func() { w.view.Terminate() }) }

// Eval runs JavaScript in the page.
func (w Window) Eval(js string) { w.view.Dispatch(func() { w.view.Eval(js) }) }

// IconSet reports whether the window has its own icon (the exe's icon resource 1 loaded as the class icon).
func (w Window) IconSet() bool {
	user32 := syscall.NewLazyDLL("user32.dll")
	hwnd := uintptr(w.view.Window())
	icon, _, _ := user32.NewProc("GetClassLongPtrW").Call(hwnd, ^uintptr(13)) // GCLP_HICON (-14)
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	module, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	group, _, _ := kernel32.NewProc("FindResourceW").Call(module, 1, 14) // RT_GROUP_ICON 1
	return icon != 0 && group != 0
}

// Run serves Handler on 127.0.0.1 behind Guard, opens the window on it and returns when it closes.
func Run(o Options) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	token, host := NewToken(), ln.Addr().String()
	srv := &http.Server{Handler: Guard(o.Handler, token, host)}
	go srv.Serve(ln)
	defer srv.Close()

	view := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:      o.DataPath,
		AutoFocus:     true,
		WindowOptions: webview2.WindowOptions{Title: o.Title, Width: o.Width, Height: o.Height, IconId: 1, Center: true},
	})
	if view == nil {
		return fmt.Errorf("could not open a WebView2 window (is the Microsoft Edge WebView2 Runtime installed?)")
	}
	defer view.Destroy()
	for name, fn := range o.Bind {
		if err := view.Bind(name, fn); err != nil {
			return err
		}
	}
	view.Navigate("http://" + host + "/__open?t=" + token)
	if o.Ready != nil {
		o.Ready(Window{view})
	}
	view.Run()
	return nil
}

// MessageBox shows a plain Windows message box (GUI programs have no console to print to).
func MessageBox(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x40)
}
