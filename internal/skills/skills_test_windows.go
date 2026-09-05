//go:build windows

package skills

import "errors"

func makeFIFO(string) error { return errors.New("unix FIFOs are unavailable on Windows") }
