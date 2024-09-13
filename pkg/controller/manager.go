package controller

import (
	"context"
	"crypto/tls"
	"os"
	"runtime"

	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/keymutex"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/ovs"
	"github.com/fengjinlin/kube-ovn/pkg/translator"
)

var (
	scheme        = apiruntime.NewScheme()
	startupLogger = log.Log.WithName("controller-startup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ovnv1.AddToScheme(scheme))
}

func Run() {
	config, err := ParseFlags()
	if err != nil {
		startupLogger.Error(err, "failed to parse flags")
		os.Exit(1)
	}

	ovnNbClient, err := ovs.NewOvnNbClient(config.OvnNbAddr, config.OvnTimeout)
	if err != nil {
		startupLogger.Error(err, "failed to create ovn nb client")
		os.Exit(1)
	}
	ovnSbClient, err := ovs.NewOvnSbClient(config.OvnSbAddr, config.OvnTimeout)
	if err != nil {
		startupLogger.Error(err, "failed to create ovn sb client")
		os.Exit(1)
	}
	translator.Init(ovnNbClient, ovnSbClient)

	disableHTTP2 := func(c *tls.Config) {
		startupLogger.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	var tlsOpts []func(*tls.Config)
	if !config.EnableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   config.MetricsAddr,
			SecureServing: config.SecureMetrics,
			TLSOpts:       tlsOpts,
		},
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: config.ProbeAddr,
		LeaderElection:         config.EnableLeaderElection,
		LeaderElectionID:       "2b8c3bb0.kubeovn.fengjinlin.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		startupLogger.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()
	setupControllers(ctx, mgr, config)

	if err = mgr.Add(&OvnInitializer{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Config: config,
	}); err != nil {
		startupLogger.Error(err, "unable to add ovn initializer")
		os.Exit(1)
	}

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		startupLogger.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		startupLogger.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	startupLogger.Info("starting manager")
	if err = mgr.Start(ctx); err != nil {
		startupLogger.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func setupControllers(ctx context.Context, mgr manager.Manager, config *Configuration) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	numKeyLocks := runtime.NumCPU() * 2
	if numKeyLocks < config.WorkerNum*2 {
		numKeyLocks = config.WorkerNum * 2
	}

	ipam, err := initIPAM(ctx, mgr.GetAPIReader())
	if err != nil {
		logger.Error(err, "failed to init ipam")
		os.Exit(1)
	}
	baseCtrl := baseController{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		config: config,
		ipam:   ipam,
	}

	if err = (&vpcController{
		baseController:    baseCtrl,
		recorder:          mgr.GetEventRecorderFor("vpc-controller"),
		keyMutex:          keymutex.NewHashed(numKeyLocks),
		updateStatusQueue: workqueue.NewRateLimitingQueueWithConfig(workqueue.DefaultControllerRateLimiter(), workqueue.RateLimitingQueueConfig{Name: "UpdateVpcStatus"}),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "Vpc")
		os.Exit(1)
	}
	if err = (&subnetController{
		baseController:    baseCtrl,
		recorder:          mgr.GetEventRecorderFor("subnet-controller"),
		keyMutex:          keymutex.NewHashed(numKeyLocks),
		updateStatusQueue: workqueue.NewRateLimitingQueueWithConfig(workqueue.DefaultControllerRateLimiter(), workqueue.RateLimitingQueueConfig{Name: "UpdateSubnetStatus"}),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "Subnet")
		os.Exit(1)
	}
	if err = (&nodeController{
		baseController: baseCtrl,
		recorder:       mgr.GetEventRecorderFor("node-controller"),
		keyMutex:       keymutex.NewHashed(numKeyLocks),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "Node")
		os.Exit(1)
	}
	if err = (&podController{
		baseController: baseCtrl,
		recorder:       mgr.GetEventRecorderFor("pod-controller"),
		keyMutex:       keymutex.NewHashed(numKeyLocks),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "Pod")
		os.Exit(1)
	}
	if err = (&namespaceController{
		baseController: baseCtrl,
		recorder:       mgr.GetEventRecorderFor("pod-controller"),
		keyMutex:       keymutex.NewHashed(numKeyLocks),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "Namespace")
		os.Exit(1)
	}
	if err = (&ipController{
		baseController: baseCtrl,
		recorder:       mgr.GetEventRecorderFor("ip-controller"),
		keyMutex:       keymutex.NewHashed(numKeyLocks),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "IP")
		os.Exit(1)
	}
	if err = (&serviceController{
		baseController: baseCtrl,
		recorder:       mgr.GetEventRecorderFor("service-controller"),
		keyMutex:       keymutex.NewHashed(numKeyLocks),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "Service")
		os.Exit(1)
	}

}
