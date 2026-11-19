package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/n1k0s1/grit/internal/autostart"
	"github.com/n1k0s1/grit/internal/collect"
)

func binDir() string { return filepath.Join(gritDir(), "bin") }

func installedExe() string {
	name := "grit"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(binDir(), name)
}

func logPath() string { return filepath.Join(gritDir(), "daemon.log") }

// daemonArgs are the arguments the login item starts grit with.
func daemonArgs() []string {
	if runtime.GOOS == "windows" {
		return []string{"daemon", "-background"}
	}
	return []string{"daemon"}
}

// copySelf puts this binary at ~/.grit/bin/grit so the login item does not
// depend on wherever it was downloaded. It writes a temp file and renames
// it, which works even while an older copy is running.
func copySelf() (string, error) {
	src, err := os.Executable()
	if err != nil {
		return "", err
	}
	if s, err := filepath.EvalSymlinks(src); err == nil {
		src = s
	}
	dst := installedExe()
	if same, _ := filepath.Abs(src); same == dst {
		return dst, nil
	}
	if err := os.MkdirAll(binDir(), 0o755); err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		// A running exe cannot be replaced but can be moved aside.
		_ = os.Remove(dst + ".old")
		_ = os.Rename(dst, dst+".old")
	}
	return dst, os.Rename(tmp, dst)
}

// stopDaemon ends a daemon started outside launchd (a terminal, or the Run
// key on Windows, or XDG autostart on Linux).
func stopDaemon() {
	pid, ok := daemonPID()
	if !ok {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
	for i := 0; i < 20; i++ {
		if !processAlive(pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.Remove(pidPath())
}

// startDetached starts the daemon in the background now, so there is no
// need to log out and back in.
func startDetached(exe string) error {
	f, err := os.OpenFile(logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(exe, daemonArgs()...)
	cmd.Stdout, cmd.Stderr = f, f
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func installCmd(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	_ = fs.Parse(args)
	if err := os.MkdirAll(gritDir(), 0o700); err != nil {
		die("%v", err)
	}
	exe, err := copySelf()
	if err != nil {
		die("copying grit to %s: %v", binDir(), err)
	}
	if runtime.GOOS != "darwin" {
		stopDaemon() // launchd replaces its own copy in Install
	}
	msg, err := autostart.Install(autostart.Spec{Home: collect.HomeDir(), Exe: exe, Args: daemonArgs(), Log: logPath()})
	if err != nil {
		die("%v", err)
	}
	if runtime.GOOS != "darwin" {
		if err := startDetached(exe); err != nil {
			die("installed, but could not start the daemon now: %v", err)
		}
		msg += " Started it now."
	}
	fmt.Println("Installed grit to " + exe + ".")
	fmt.Println(msg)
	fmt.Println()
	fmt.Println("It reads your AI tools' logs, samples seconds since your last keyboard or mouse input")
	fmt.Println("(never what you type), and watches projects you use agents in for edited files.")
	fmt.Println("Keep a folder out entirely with: grit exclude add ~/path")
	fmt.Println("Remove everything with: grit uninstall -purge")
}

func uninstallCmd(args []string) {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete ~/.grit: settings, activity minutes, edit log and the device secret")
	_ = fs.Parse(args)
	msg, err := autostart.Uninstall(collect.HomeDir())
	if err != nil {
		die("%v", err)
	}
	stopDaemon()
	fmt.Println(msg)
	if err := os.RemoveAll(binDir()); err != nil {
		die("%v", err)
	}
	fmt.Println("Removed " + binDir() + ".")
	if *purge {
		if err := os.RemoveAll(gritDir()); err != nil {
			die("%v", err)
		}
		fmt.Println("Deleted " + gritDir() + ". Nothing of grit is left on this machine.")
		return
	}
	fmt.Println("Your data is still in " + gritDir() + ". Run grit uninstall -purge to delete it too.")
}
