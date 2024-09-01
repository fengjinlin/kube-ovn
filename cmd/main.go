package main

import (
	"fmt"
	"github.com/fengjinlin/kube-ovn/cmd/daemon"
	"github.com/fengjinlin/kube-ovn/cmd/ovn_leader_checker"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"syscall"
	"time"

	"k8s.io/klog/v2"

	"github.com/fengjinlin/kube-ovn/cmd/controller"
)

const (
	timeFormat = "2006-01-02_15:04:05"
)

func main() {
	cmd := filepath.Base(os.Args[0])
	switch cmd {
	case "kube-ovn-controller":
		dumpProfile()
		controller.CmdMain()

	case "kube-ovn-daemon":
		dumpProfile()
		daemon.CmdMain()

	case "kube-ovn-leader-checker":
		ovn_leader_checker.CmdMain()

	default:
		klog.Errorf("unknown cmd: %s", cmd)
	}
}

func dumpProfile() {
	ch1 := make(chan os.Signal, 1)
	ch2 := make(chan os.Signal, 1)
	signal.Notify(ch1, syscall.SIGUSR1)
	signal.Notify(ch2, syscall.SIGUSR2)
	go func() {
		for {
			<-ch1
			name := fmt.Sprintf("cpu-profile-%s.pprof", time.Now().Format(timeFormat))
			f, err := os.Create(filepath.Join(os.TempDir(), name)) // #nosec G303,G304
			if err != nil {
				klog.Errorf("failed to create cpu profile file: %v", err)
				return
			}
			if err = pprof.StartCPUProfile(f); err != nil {
				klog.Errorf("failed to start cpu profile: %v", err)
				return
			}
			defer f.Close()
			time.Sleep(30 * time.Second)
			pprof.StopCPUProfile()
		}
	}()
	go func() {
		for {
			<-ch2
			name := fmt.Sprintf("mem-profile-%s.pprof", time.Now().Format(timeFormat))
			f, err := os.Create(filepath.Join(os.TempDir(), name)) // #nosec G303,G304
			if err != nil {
				klog.Errorf("failed to create memory profile file: %v", err)
				return
			}
			if err = pprof.WriteHeapProfile(f); err != nil {
				klog.Errorf("failed to write memory profile file: %v", err)
			}
			defer f.Close()
		}
	}()
}
