package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsColabConsoleImportIsLazy(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows compatibility test")
	}
	a := &App{dataDir: t.TempDir()}
	path := filepath.Join(a.colabVenv(), "Lib", "site-packages", "colab_cli", "commands", "execution.py")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	const source = "from colab_cli.console import connect_console\n\ndef console(s):\n    try:\n        connect_console(s)\n    except Exception:\n        pass\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.patchWindowsColab(); err != nil {
		t.Fatal(err)
	}
	if err := a.patchWindowsColab(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "from colab_cli.console import connect_console") != 1 || !strings.Contains(text, "        from colab_cli.console import connect_console\n        connect_console(s)") {
		t.Fatalf("unexpected patched import: %q", text)
	}
}
