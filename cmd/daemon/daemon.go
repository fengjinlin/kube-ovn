package daemon

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/klog/v2"

	"github.com/fengjinlin/kube-ovn/pkg/daemon"
	"github.com/fengjinlin/kube-ovn/versions"
)

var (
	shutdownSignals      = []os.Signal{os.Interrupt, syscall.SIGTERM}
	onlyOneSignalHandler = make(chan struct{})
)

func CmdMain() {
	config := daemon.ParseFlags()
	klog.Infof(versions.String())

	var err error

	if err = config.Init(); err != nil {
		klog.ErrorS(err, "failed to initialize config")
		os.Exit(1)
	}

	stopCh := SetupSignalHandler().Done()

	ctrl, err := daemon.NewController(config, stopCh)
	if err != nil {
		klog.ErrorS(err, "failed to create controller")
		os.Exit(1)
	}
	ctrl.InitSystem()
	ctrl.StartWorks(stopCh)
}

// SetupSignalHandler registered for SIGTERM and SIGINT. A stop channel is returned
// which is closed on one of these signals. If a second signal is caught, the program
// is terminated with exit code 1.
func SetupSignalHandler() context.Context {
	close(onlyOneSignalHandler) // panics when called twice

	c := make(chan os.Signal, 2)
	ctx, cancel := context.WithCancel(context.Background())
	signal.Notify(c, shutdownSignals...)
	go func() {
		<-c
		cancel()
		<-c
		os.Exit(1) // second signal. Exit directly.
	}()

	return ctx
}
