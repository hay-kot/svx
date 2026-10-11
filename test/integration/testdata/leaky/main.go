// Command leaky models a dev stack that svx used to leave running: a
// wrapper that dies on SIGTERM like `go run`, in front of a supervisor that
// starts its worker in a process group of its own and takes time to stop it.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: leaky wrapper <cmd>... | supervisor [-nohup] <dir> | worker")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "wrapper":
		wrapper(os.Args[2:])
	case "supervisor":
		if os.Args[2] == "-nohup" {
			signal.Ignore(syscall.SIGHUP)
			supervisor(os.Args[3])
			return
		}
		supervisor(os.Args[2])
	case "worker":
		for {
			time.Sleep(time.Hour)
		}
	}
}

// wrapper leaves SIGINT and SIGQUIT to its child, which shares its process
// group, and exits at once on SIGTERM, as `go run` does.
func wrapper(argv []string) {
	signal.Ignore(syscall.SIGINT, syscall.SIGQUIT)
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec,noctx // a test helper
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

// supervisor records its pids in dir/pids as "wrapper supervisor worker",
// and on SIGINT or SIGTERM takes two seconds before it stops the worker,
// then writes dir/done-<pid>. Unless -nohup, SIGHUP kills it at once, as it
// would most programs.
func supervisor(dir string) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	worker := exec.Command(os.Args[0], "worker") //nolint:gosec,noctx // a test helper
	worker.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := worker.Start(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	pids := fmt.Sprintf("%d %d %d", os.Getppid(), os.Getpid(), worker.Process.Pid)
	if err := os.WriteFile(filepath.Join(dir, "pids"), []byte(pids), 0o600); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("supervisor up")

	<-stop
	fmt.Println("tearing down")
	time.Sleep(2 * time.Second)
	_ = syscall.Kill(-worker.Process.Pid, syscall.SIGTERM)
	_ = worker.Wait()
	done := filepath.Join(dir, "done-"+strconv.Itoa(os.Getpid()))
	_ = os.WriteFile(done, nil, 0o600)
	fmt.Println("stopped")
}
