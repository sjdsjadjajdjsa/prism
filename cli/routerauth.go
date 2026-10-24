//go:build linux || darwin

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Unsetenv alone leaves the original token readable in /proc/$PPID/environ.
// Exec replaces that environment block; only an inherited pipe carries the token.
func init() {
	if routerToken != "" {
		if len(routerToken) > 4096 {
			panic("router token is too large for secure handover")
		}
		executable, err := os.Executable()
		if err != nil {
			panic(fmt.Errorf("locate executable for secure router handover: %w", err))
		}
		read, write, err := os.Pipe()
		if err != nil {
			panic(fmt.Errorf("create secure router handover pipe: %w", err))
		}
		if _, err = io.WriteString(write, routerToken); err != nil {
			panic(fmt.Errorf("write secure router handover: %w", err))
		}
		write.Close()
		fd := read.Fd()
		if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, 0); errno != 0 {
			panic(fmt.Errorf("inherit secure router handover pipe: %w", errno))
		}
		envv := make([]string, 0, len(os.Environ())+1)
		for _, item := range os.Environ() {
			if !strings.HasPrefix(item, "PRISM_ROUTER_TOKEN=") && !strings.HasPrefix(item, "PRISM_ROUTER_TOKEN_FD=") {
				envv = append(envv, item)
			}
		}
		envv = append(envv, "PRISM_ROUTER_TOKEN_FD="+strconv.FormatUint(uint64(fd), 10))
		err = syscall.Exec(executable, os.Args, envv)
		read.Close()
		panic(fmt.Errorf("re-exec for secure router handover: %w", err))
	}
	if value := os.Getenv("PRISM_ROUTER_TOKEN_FD"); value != "" {
		os.Unsetenv("PRISM_ROUTER_TOKEN_FD")
		fd, err := strconv.Atoi(value)
		if err != nil || fd < 3 {
			panic("invalid secure router handover fd")
		}
		pipe := os.NewFile(uintptr(fd), "router-token")
		if pipe == nil {
			panic("invalid secure router handover pipe")
		}
		token, err := io.ReadAll(io.LimitReader(pipe, 4097))
		pipe.Close()
		if err != nil || len(token) == 0 || len(token) > 4096 {
			panic("could not read secure router handover")
		}
		routerToken = string(token)
	}
}
