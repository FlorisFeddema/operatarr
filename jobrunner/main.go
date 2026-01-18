package main

import (
	"flag"
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

	log := zap.New(zap.UseFlagOptions(&opts))

	switch mode {
	case OperationMode.MediaLibrary:
		runMediaLibraryInitializer(log)
	default:
		log.Error(nil, "unknown mode: "+mode)
		os.Exit(1)
	}
}

func runMediaLibraryInitializer(log logr.Logger) {
	log.Info("starting medialibrary initializer")
	// Initialize the media library
	initOptions := &medialibrary.Options{
		EnableConfigureLibrary: true,
		LibraryRootPath:        "/library",
	}
	medialibrary.MediaLibrary(initOptions)
}
