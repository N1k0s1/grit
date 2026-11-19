//go:build linux

package activity

import (
	"os"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/screensaver"
	"github.com/jezek/xgb/xproto"
)

// Linux: on X11 the MIT-SCREEN-SAVER extension reports milliseconds since
// the last input. On GNOME under Wayland, Mutter exposes the same number over
// the session D-Bus. Other Wayland desktops offer nothing portable, so the
// answer there is "unknown" rather than a guess.

func idle() (time.Duration, string, error) {
	if os.Getenv("DISPLAY") != "" {
		if d, err := x11Idle(); err == nil {
			return d, "x11", nil
		}
	}
	if d, err := mutterIdle(); err == nil {
		return d, "mutter", nil
	}
	return 0, "", ErrUnknown
}

func x11Idle() (time.Duration, error) {
	c, err := xgb.NewConn()
	if err != nil {
		return 0, err
	}
	defer c.Close()
	if err := screensaver.Init(c); err != nil {
		return 0, err
	}
	root := xproto.Setup(c).DefaultScreen(c).Root
	info, err := screensaver.QueryInfo(c, xproto.Drawable(root)).Reply()
	if err != nil {
		return 0, err
	}
	return time.Duration(info.MsSinceUserInput) * time.Millisecond, nil
}

func mutterIdle() (time.Duration, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return 0, err
	}
	var ms uint64
	obj := conn.Object("org.gnome.Mutter.IdleMonitor", "/org/gnome/Mutter/IdleMonitor/Core")
	if err := obj.Call("org.gnome.Mutter.IdleMonitor.GetIdletime", 0).Store(&ms); err != nil {
		return 0, err
	}
	return time.Duration(ms) * time.Millisecond, nil
}
