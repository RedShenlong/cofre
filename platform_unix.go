//go:build linux || darwin

package main

// Código compartido por Linux y macOS.

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"syscall"
)

// listenLocal crea el socket Unix del comando "cofre enviar" con permisos 0600.
func listenLocal(path string) (net.Listener, error) {
	old := syscall.Umask(0o177)
	defer syscall.Umask(old)
	return net.Listen("unix", path)
}

func freeSpace(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// checkKeyPerms exige que la clave maestra solo la pueda leer tu usuario.
func checkKeyPerms(path string, mode fs.FileMode) error {
	if mode.Perm()&0o077 != 0 {
		return fmt.Errorf("%s tiene permisos demasiado abiertos (%v); usa chmod 600", path, mode.Perm())
	}
	return nil
}

func ansiOK(*os.File) bool { return true }
