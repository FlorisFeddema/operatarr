package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/FlorisFeddema/operatarr/jobrunner/medialibrary"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var OperationMode = struct {
	MediaLibrary string
}{
	MediaLibrary: "medialibrary",
}

func main() {
	var mode string

	flag.StringVar(&mode, "mode", "medialibrary", "Mode to run: medialibrary")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	log := zap.New(zap.UseFlagOptions(&opts)).WithName("jobrunner")

	if err := run(mode, log); err != nil {
		log.Error(err, "jobrunner failed", "mode", mode)
		os.Exit(1)
	}
}

func run(mode string, log logr.Logger) error {
	switch mode {
	case OperationMode.MediaLibrary:
		return runMediaLibraryInitializer(log)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

func runMediaLibraryInitializer(log logr.Logger) error {
	log.Info("starting medialibrary initializer")
	// Initialize the media library
	initOptions := &medialibrary.Options{
		EnableConfigureLibrary: true,
		LibraryRootPath:        medialibrary.DefaultLibraryRootPath,
	}
	if err := medialibrary.MediaLibrary(log, initOptions); err != nil {
		return fmt.Errorf("unable to initialize MediaLibrary: %w", err)
	}
	log.Info("MediaLibrary initialization completed")
	return nil
}
