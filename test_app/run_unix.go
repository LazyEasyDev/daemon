//go:build !windows

package main

import "errors"

func runApplication(app *application, windowsNativeService bool) error {
	if windowsNativeService {
		return errors.New("windows-native-service is only supported on Windows")
	}
	return app.run()
}
