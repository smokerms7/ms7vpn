//go:build windows

package xray

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func configureHiddenProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func terminateProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
)

// processJob создаёт job object, который убивает все дочерние процессы, когда
// MS7VPN завершается — штатно, крестиком или падением. Пока этого не было,
// упавшее приложение оставляло висеть xray.exe: он держал порты 10808/10809
// и сетевой адаптер, и следующий запуск не мог подключиться.
func processJob() windows.Handle {
	jobOnce.Do(func() {
		handle, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		_, err = windows.SetInformationJobObject(
			handle,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		)
		if err != nil {
			_ = windows.CloseHandle(handle)
			return
		}
		jobHandle = handle
	})
	return jobHandle
}

func adoptProcess(command *exec.Cmd) {
	job := processJob()
	if job == 0 || command == nil || command.Process == nil {
		return
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(command.Process.Pid),
	)
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)
	_ = windows.AssignProcessToJobObject(job, handle)
}

// KillOrphans убивает процессы xray.exe, запущенные из нашей папки и
// оставшиеся от предыдущего сеанса. Вызывается при старте приложения.
// Чужие копии Xray (v2rayN, Nekoray) не трогаются: сверяется полный путь.
func KillOrphans(corePath string) int {
	if corePath == "" {
		return 0
	}
	target := strings.ToLower(filepath.Clean(corePath))
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snapshot)

	self := uint32(os.Getpid())
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	killed := 0
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if name != "xray.exe" || entry.ProcessID == self {
			continue
		}
		handle, openErr := windows.OpenProcess(
			windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE,
			false,
			entry.ProcessID,
		)
		if openErr != nil {
			continue
		}
		if strings.ToLower(filepath.Clean(processImagePath(handle))) == target {
			if windows.TerminateProcess(handle, 1) == nil {
				killed++
			}
		}
		_ = windows.CloseHandle(handle)
	}
	return killed
}

func processImagePath(handle windows.Handle) string {
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buffer[:size])
}
