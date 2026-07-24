//go:build windows

package ffmpeg

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func startLongRunningProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "ping", "-n", "30", "127.0.0.1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000010}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start subprocess: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	return cmd
}

func processAlive(cmd *exec.Cmd) bool {
	if cmd.Process == nil {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)

	var exitCode uint32
	if err := syscall.GetExitCodeProcess(handle, &exitCode); err == nil {
		return exitCode == 259 // 259 = STILL_ACTIVE
	}
	return false
}

func TestSuspendResumeProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	cmd := startLongRunningProcess(t)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	pid := cmd.Process.Pid
	t.Logf("started subprocess with PID %d", pid)

	suspendProcess(pid)
	time.Sleep(200 * time.Millisecond)

	if !processAlive(cmd) {
		t.Fatal("process should still be alive after suspend")
	}

	resumeProcess(pid)
	time.Sleep(200 * time.Millisecond)

	if !processAlive(cmd) {
		t.Fatal("process should still be alive after resume")
	}
}

func TestSuspendInvalidPID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	suspendProcess(-1)
	suspendProcess(99999999)
}

func TestResumeInvalidPID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	resumeProcess(-1)
	resumeProcess(99999999)
}

func TestKillProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	cmd := startLongRunningProcess(t)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	pid := cmd.Process.Pid
	t.Logf("started subprocess with PID %d, now killing", pid)

	killProcess(cmd)

	err := cmd.Wait()
	if err == nil {
		t.Fatal("expected an error from Wait after killProcess, got nil")
	}
	t.Logf("cmd.Wait() returned expected error: %v", err)

	if processAlive(cmd) {
		t.Fatal("process should not be alive after killProcess")
	}
}

func TestKillProcessNilProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	cmd := exec.Command("cmd", "/c", "echo", "hello")
	killProcess(cmd)
}

func TestSuspendResumeRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	cmd := startLongRunningProcess(t)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	pid := cmd.Process.Pid
	t.Logf("round-trip: started PID %d", pid)

	suspendProcess(pid)
	time.Sleep(200 * time.Millisecond)
	if !processAlive(cmd) {
		t.Fatal("process should be alive after suspend")
	}

	resumeProcess(pid)
	time.Sleep(200 * time.Millisecond)
	if !processAlive(cmd) {
		t.Fatal("process should be alive after resume")
	}

	killProcess(cmd)
	err := cmd.Wait()
	if err == nil {
		t.Fatal("expected error from Wait after kill")
	}
	t.Logf("round-trip complete, Wait returned: %v", err)
}
