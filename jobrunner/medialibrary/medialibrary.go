package medialibrary

import (
	"errors"
	"os"

	"github.com/go-logr/logr"
)

var log logr.Logger

// Options contains all possible settings.
type Options struct {
	EnableConfigureLibrary bool

	LibraryRootPath string
}

func MediaLibrary(options *Options) {
	if options.EnableConfigureLibrary {
		err := configureLibrary(options.LibraryRootPath)
		if err != nil {
			log.Error(err, "Failed to configure library")
			os.Exit(1)
		}
	}
}

func configureLibrary(libraryRoot string) error {
	log.Info("Configuring media library at " + libraryRoot)

	//check if libraryRoot exists
	_, err := os.Stat(libraryRoot)
	if os.IsNotExist(err) {
		return errors.New("media library root does not exist")
	}

	//create folders if they do not exist
	folders := []string{"movies", "tv", "music"}
	amountErrors := 0
	for _, folder := range folders {
		folderPath := libraryRoot + "/" + folder
		_, err := os.Stat(folderPath)
		if os.IsNotExist(err) {
			err := os.Mkdir(folderPath, 0755)
			if err != nil {
				log.Error(err, "Failed to create folder: "+folderPath)
				amountErrors++
			}
			log.Info("Created folder: " + folderPath)
		}
	}
	if amountErrors > 0 {
		return errors.New("failed to create some folders")
	}

	return nil
}
