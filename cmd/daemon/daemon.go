package daemon

import (
	"context"
	"fmt"
	ovninformer "github.com/fengjinlin/kube-ovn/pkg/client/informers/externalversions"
	"github.com/fengjinlin/kube-ovn/pkg/daemon"
	"github.com/fengjinlin/kube-ovn/versions"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/klog/v2"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
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

	if err = daemon.InitOvsBridges(config); err != nil {
		klog.ErrorS(err, "failed to initialize ovs bridges")
		os.Exit(1)
	}

	if err = daemon.InitNodeChassis(config); err != nil {
		klog.ErrorS(err, "failed to initialize node chassis")
		os.Exit(1)
	}

	if err = daemon.InitNodeGateway(config); err != nil {
		klog.ErrorS(err, "failed to initialize node gateway")
		os.Exit(1)
	}

	if err = daemon.InitForOS(); err != nil {
		klog.ErrorS(err, "failed to initialize os")
		os.Exit(1)
	}

	podInformerFactory := informers.NewSharedInformerFactoryWithOptions(config.KubeClient, 0,
		informers.WithTweakListOptions(func(listOption *v1.ListOptions) {
			listOption.FieldSelector = fmt.Sprintf("spec.nodeName=%s", config.NodeName)
			listOption.AllowWatchBookmarks = true
		}))
	nodeInformerFactory := informers.NewSharedInformerFactoryWithOptions(config.KubeClient, 0,
		informers.WithTweakListOptions(func(listOption *v1.ListOptions) {
			listOption.AllowWatchBookmarks = true
		}))
	ovnInformerFactory := ovninformer.NewSharedInformerFactoryWithOptions(config.KubeOvnClient, 0,
		ovninformer.WithTweakListOptions(func(listOption *v1.ListOptions) {
			listOption.AllowWatchBookmarks = true
		}))

	stopCh := SetupSignalHandler().Done()

	ctl, err := daemon.NewController(config, stopCh, podInformerFactory, nodeInformerFactory, ovnInformerFactory)
	if err != nil {
		klog.ErrorS(err, "failed to create controller")
		os.Exit(1)
	}
	klog.Info("start daemon controller")
	go ctl.Run(stopCh)
	go daemon.RunCniServer(config, ctl)

	if err := writeCniConf(config.CniConfDir, config.CniConfFile, config.CniConfName); err != nil {
		klog.ErrorS(err, "failed to mv cni config file")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	if config.EnableMetrics {
		mux.Handle("/metrics", promhttp.Handler())
	}

	if config.EnablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	// conform to Gosec G114
	// https://github.com/securego/gosec#available-rules
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", "", config.PprofPort),
		ReadHeaderTimeout: 3 * time.Second,
		Handler:           mux,
	}
	klog.ErrorS(server.ListenAndServe(), "failed to listen and serve", "addr", server.Addr)
}

func writeCniConf(configDir, configFile, confName string) error {
	// #nosec
	data, err := os.ReadFile(configFile)
	if err != nil {
		klog.Errorf("failed to read cni config file %s, %v", configFile, err)
		return err
	}

	cniConfPath := filepath.Join(configDir, confName)
	return os.WriteFile(cniConfPath, data, 0o644)
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
