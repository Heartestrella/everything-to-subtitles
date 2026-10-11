package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var colabInstallMu sync.Mutex

const managedColabPackage = "google-colab-cli==0.7.4"

func (a *App) colabVenv() string { return filepath.Join(a.dataDir, "tools", "colabcli") }

func (a *App) managedColab() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(a.colabVenv(), "Scripts", "colab.exe")
	}
	return filepath.Join(a.colabVenv(), "bin", "colab")
}

func (a *App) managedPython() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(a.colabVenv(), "Scripts", "python.exe")
	}
	return filepath.Join(a.colabVenv(), "bin", "python")
}

func (a *App) installedColab() (string, bool) {
	if configured := os.Getenv("SUBTITLE_COLAB"); configured != "" {
		path, err := exec.LookPath(configured)
		return path, err == nil
	}
	if _, err := os.Stat(filepath.Join(a.colabVenv(), "install.ok")); err == nil {
		if _, err := os.Stat(a.managedColab()); err == nil {
			return a.managedColab(), true
		}
	}
	path, err := exec.LookPath("colab")
	return path, err == nil
}

func (a *App) ensureColab(ctx context.Context, id string) (string, error) {
	colabInstallMu.Lock()
	defer colabInstallMu.Unlock()
	if path, ok := a.installedColab(); ok {
		return path, nil
	}
	if os.Getenv("SUBTITLE_COLAB") != "" {
		return "", errors.New("SUBTITLE_COLAB 指向的命令不存在")
	}
	venv := a.colabVenv()
	if err := os.MkdirAll(filepath.Dir(venv), 0700); err != nil {
		return "", err
	}
	a.change(id, func(j *Job) { j.Stage, j.Detail = "preparing", "首次使用：正在准备内置 Colab CLI" })
	if uv, err := exec.LookPath("uv"); err == nil {
		if err := a.runCommand(ctx, id, uv, "venv", "--allow-existing", "--python", "3.12", venv); err != nil {
			return "", fmt.Errorf("准备 Colab Python 3.12 失败: %w", err)
		}
		if err := a.runCommand(ctx, id, uv, "pip", "install", "--python", a.managedPython(), managedColabPackage); err != nil {
			return "", fmt.Errorf("安装内置 Colab CLI 失败: %w", err)
		}
	} else {
		python, args, err := python312()
		if err != nil {
			return "", errors.New("内置 Colab CLI 需要 Python 3.12+；请安装 uv 或设置 SUBTITLE_COLAB_PYTHON")
		}
		args = append(args, "-m", "venv", venv)
		if err := a.runCommand(ctx, id, python, args...); err != nil {
			return "", fmt.Errorf("创建 Colab Python 环境失败: %w", err)
		}
		if err := a.runCommand(ctx, id, a.managedPython(), "-m", "pip", "install", managedColabPackage); err != nil {
			return "", fmt.Errorf("安装内置 Colab CLI 失败: %w", err)
		}
	}
	if err := a.patchWindowsColab(); err != nil {
		return "", err
	}
	if err := a.runCommand(ctx, id, a.managedColab(), "version"); err != nil {
		return "", fmt.Errorf("内置 Colab CLI 无法启动: %w", err)
	}
	if err := os.WriteFile(filepath.Join(venv, "install.ok"), []byte(managedColabPackage+"\n"), 0600); err != nil {
		return "", err
	}
	return a.managedColab(), nil
}

// google-colab-cli 0.7.4 imports its Unix-only console module while loading
// commands that do not use a console. Keep that import inside the console path.
func (a *App) patchWindowsColab() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	path := filepath.Join(a.colabVenv(), "Lib", "site-packages", "colab_cli", "commands", "execution.py")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("检查 Colab CLI Windows 兼容性失败: %w", err)
	}
	source := string(data)
	const eager = "from colab_cli.console import connect_console"
	if !strings.Contains(source, eager) {
		return nil
	}
	const call = "        connect_console(s)"
	if !strings.Contains(source, call) {
		return errors.New("Colab CLI 更新了控制台结构，无法应用 Windows 兼容处理")
	}
	source = strings.Replace(source, eager, "", 1)
	source = strings.Replace(source, call, "        from colab_cli.console import connect_console\n"+call, 1)
	temp := path + ".subtitle-temp"
	if err := os.WriteFile(temp, []byte(source), 0600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return nil
}

func python312() (string, []string, error) {
	if path := os.Getenv("SUBTITLE_COLAB_PYTHON"); path != "" {
		resolved, err := exec.LookPath(path)
		return resolved, nil, err
	}
	if runtime.GOOS == "windows" {
		if launcher, err := exec.LookPath("py"); err == nil {
			if exec.Command(launcher, "-3.12", "--version").Run() == nil {
				return launcher, []string{"-3.12"}, nil
			}
		}
	}
	if python, err := exec.LookPath("python3.12"); err == nil {
		return python, nil, nil
	}
	return "", nil, errors.New("Python 3.12 不可用")
}

func runColabCLI(args []string) error {
	dataDir := os.Getenv("SUBTITLE_DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	a := &App{dataDir: dataDir, ctx: context.Background()}
	path, err := a.ensureColab(context.Background(), "")
	if err != nil {
		return err
	}
	command := exec.Command(path, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("colab %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
