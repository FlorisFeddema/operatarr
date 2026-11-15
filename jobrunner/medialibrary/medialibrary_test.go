package medialibrary

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
)

func TestMediaLibraryCreatesFoldersIdempotently(t *testing.T) {
	root := t.TempDir()
	var logs []string
	log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
	options := &Options{EnableConfigureLibrary: true, LibraryRootPath: root}

	for range 2 {
		if err := MediaLibrary(log, options); err != nil {
			t.Fatal(err)
		}
	}
	for _, folder := range []string{moviesFolder, tvFolder, musicFolder} {
		path := filepath.Join(root, folder)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("expected directory %s: %v", path, err)
		}
	}
	if len(logs) != 3 {
		t.Fatalf("expected one log per created directory, got %v", logs)
	}
	for _, output := range logs {
		if !strings.Contains(output, `"path"`) || !strings.Contains(output, "Media library folder created") {
			t.Fatalf("expected structured creation log, got %s", output)
		}
	}
}

func TestMediaLibraryInvalidOptions(t *testing.T) {
	if err := MediaLibrary(logr.Discard(), nil); err == nil {
		t.Fatal("expected nil options to fail")
	}
	if err := MediaLibrary(logr.Discard(), &Options{}); err != nil {
		t.Fatalf("disabled configuration should do nothing: %v", err)
	}
}

func TestMediaLibraryRootErrors(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "missing"), file} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			err := MediaLibrary(logr.Discard(), &Options{EnableConfigureLibrary: true, LibraryRootPath: path})
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("expected contextual root error, got %v", err)
			}
			if path != file && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected preserved not-found cause, got %v", err)
			}
		})
	}
}

func TestMediaLibraryPreservesStatErrors(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{moviesFolder, tvFolder} {
		path := filepath.Join(root, folder)
		if err := os.Symlink(path, path); err != nil {
			t.Fatal(err)
		}
	}
	var logs []string
	log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
	err := MediaLibrary(log, &Options{EnableConfigureLibrary: true, LibraryRootPath: root})
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected preserved filesystem cause, got %v", err)
	}
	for _, folder := range []string{moviesFolder, tvFolder} {
		if !strings.Contains(err.Error(), filepath.Join(root, folder)) {
			t.Fatalf("expected both independent failures, got %v", err)
		}
	}
	if len(logs) != 1 || !strings.Contains(logs[0], filepath.Join(root, musicFolder)) {
		t.Fatalf("expected only successful creation to be logged, got %v", logs)
	}
}

func TestMediaLibraryRejectsNonDirectoryFolders(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, moviesFolder)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	err := MediaLibrary(logr.Discard(), &Options{EnableConfigureLibrary: true, LibraryRootPath: root})
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected a non-directory error, got %v", err)
	}
}

func TestMediaLibraryCreateErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(root, 0700); err != nil {
			t.Error(err)
		}
	})
	var logs []string
	log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
	err := MediaLibrary(log, &Options{EnableConfigureLibrary: true, LibraryRootPath: root})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected preserved permission cause, got %v", err)
	}
	for _, folder := range []string{moviesFolder, tvFolder, musicFolder} {
		if !strings.Contains(err.Error(), filepath.Join(root, folder)) {
			t.Fatalf("expected all mkdir failures, got %v", err)
		}
	}
	if len(logs) != 0 {
		t.Fatalf("failed creates must not be logged as successes: %v", logs)
	}
}
