package medialibrary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
)

const (
	// DefaultLibraryRootPath is where the initialization Job mounts the MediaLibrary PVC.
	DefaultLibraryRootPath = "/library"

	moviesFolder = "movies"
	tvFolder     = "tv"
	musicFolder  = "music"
)

// Options contains all possible settings.
type Options struct {
	EnableConfigureLibrary bool

	LibraryRootPath string
}

func MediaLibrary(log logr.Logger, options *Options) error {
	if options == nil {
		return errors.New("MediaLibrary options are required")
	}
	if options.EnableConfigureLibrary {
		return configureLibrary(log, options.LibraryRootPath)
	}
	return nil
}

func configureLibrary(log logr.Logger, libraryRoot string) error {
	log.V(1).Info("Configuring media library", "path", libraryRoot)

	info, err := os.Stat(libraryRoot)
	if err != nil {
		return fmt.Errorf("unable to access media library root %q: %w", libraryRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("media library root %q is not a directory", libraryRoot)
	}

	// Create folders if they do not exist.
	folders := []string{moviesFolder, tvFolder, musicFolder}
	var folderErrors []error
	for _, folder := range folders {
		folderPath := filepath.Join(libraryRoot, folder)
		info, err := os.Stat(folderPath)
		switch {
		case err == nil:
			if !info.IsDir() {
				folderErrors = append(folderErrors, fmt.Errorf("media library folder %q is not a directory", folderPath))
			}
		case !errors.Is(err, os.ErrNotExist):
			folderErrors = append(folderErrors, fmt.Errorf("unable to access media library folder %q: %w", folderPath, err))
		default:
			if err := os.Mkdir(folderPath, 0755); err != nil {
				folderErrors = append(folderErrors, fmt.Errorf("unable to create media library folder %q: %w", folderPath, err))
				continue
			}
			log.Info("Media library folder created", "path", folderPath)
		}
	}
	return errors.Join(folderErrors...)
}
