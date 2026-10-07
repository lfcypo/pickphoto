package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/lfcypo/pickphoto/internal/app"
	"github.com/lfcypo/pickphoto/internal/catalog"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (runErr error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	store, err := catalog.OpenStore(filepath.Join(configDir, "pickphoto", "state.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	application := app.New(store)
	defer func() {
		runErr = errors.Join(runErr, application.Close())
	}()
	mygo.App.WhenReady(application.OpenWindow)
	return mygo.App.Run()
}
