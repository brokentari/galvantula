//go:build !(linux || darwin)

package main

import "os"

// shortcut: no lock here, so two servers can share one database; add one if the factory runs on this system.
func lockDB(string) (*os.File, error) { return nil, nil }
