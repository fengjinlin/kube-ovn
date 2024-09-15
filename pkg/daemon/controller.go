package daemon

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"time"

	apiv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	ovninformer "github.com/fengjinlin/kube-ovn/pkg/client/informers/externalversions"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type Controller interface {
	InitSystem()
	StartWorks(stopCh <-chan struct{})
}

type controller struct {
	config *Configuration

	ipSetsWorker   IPSetsWorker
	iptablesWorker IPTablesWorker
	subnetWorker   SubnetWorker
	podWorker      PodWorker
	ovsWorker      OvsWorker
	nodeWorker     NodeWorker
	cniServer      CniServer
}

func NewController(config *Configuration, stopCh <-chan struct{}) (Controller, error) {
	var (
		err error
	)

	podInformerFactory := informers.NewSharedInformerFactoryWithOptions(config.KubeClient, 0,
		informers.WithTweakListOptions(func(listOption *apiv1.ListOptions) {
			listOption.FieldSelector = fmt.Sprintf("spec.nodeName=%s", config.NodeName)
			listOption.AllowWatchBookmarks = true
		}))
	nodeInformerFactory := informers.NewSharedInformerFactoryWithOptions(config.KubeClient, 0,
		informers.WithTweakListOptions(func(listOption *apiv1.ListOptions) {
			listOption.AllowWatchBookmarks = true
		}))
	ovnInformerFactory := ovninformer.NewSharedInformerFactoryWithOptions(config.KubeOvnClient, 0,
		ovninformer.WithTweakListOptions(func(listOption *apiv1.ListOptions) {
			listOption.AllowWatchBookmarks = true
		}))

	//eventBroadcaster := record.NewBroadcaster()
	//eventBroadcaster.StartLogging(klog.Infof)
	//eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: config.KubeClient.CoreV1().Events("")})
	//recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: config.NodeName})

	subnetInformer := ovnInformerFactory.Kubeovn().V1().Subnets()
	subnetsSynced := subnetInformer.Informer().HasSynced

	podInformer := podInformerFactory.Core().V1().Pods()
	podsSynced := podInformer.Informer().HasSynced

	nodeInformer := nodeInformerFactory.Core().V1().Nodes()
	nodesSynced := nodeInformer.Informer().HasSynced

	node, err := config.KubeClient.CoreV1().Nodes().Get(context.Background(), config.NodeName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "failed to get node info", "node", config.NodeName)
		return nil, err
	}
	protocol := utils.CheckProtocol(node.Annotations[consts.AnnotationJoinIP])

	podInformerFactory.Start(stopCh)
	nodeInformerFactory.Start(stopCh)
	ovnInformerFactory.Start(stopCh)

	if !cache.WaitForCacheSync(stopCh,
		subnetsSynced,
		podsSynced,
		nodesSynced) {
		klog.ErrorS(nil, "failed to wait for caches to sync")
	}

	ctrl := &controller{
		config: config,
	}

	if ctrl.ipSetsWorker, err = NewIPSetsWorker(config, subnetInformer, nodeInformer, protocol); err != nil {
		klog.Error("failed to create ipSets worker", err)
		return nil, err
	}
	if ctrl.iptablesWorker, err = NewIPTablesWorker(config, subnetInformer, nodeInformer, protocol, ctrl.ipSetsWorker); err != nil {
		klog.Error("failed to create iptables worker", err)
		return nil, err
	}
	if ctrl.subnetWorker, err = NewSubnetWorker(config, subnetInformer, nodeInformer); err != nil {
		klog.Error("failed to create subnet worker", err)
		return nil, err
	}
	if ctrl.podWorker, err = NewPodWorker(config, subnetInformer, podInformer); err != nil {
		klog.Error("failed to create pod worker", err)
		return nil, err
	}
	if ctrl.ovsWorker, err = NewOvsWorker(config, nodeInformer); err != nil {
		klog.Error("failed to create ovs worker", err)
		return nil, err
	}
	if ctrl.nodeWorker, err = NewNodeWorker(config, nodeInformer); err != nil {
		klog.Error("failed to create node worker", err)
		return nil, err
	}
	if ctrl.cniServer, err = NewCniServer(config, ctrl.podWorker); err != nil {
		klog.Error("failed to create cni server", err)
		return nil, err
	}

	return ctrl, nil
}

func (c *controller) InitSystem() {
	var err error

	if err = c.ovsWorker.InitOvs(); err != nil {
		klog.ErrorS(err, "failed to init ovs")
		os.Exit(1)
	}
	if err = c.nodeWorker.InitNodeChassis(); err != nil {
		klog.ErrorS(err, "failed to init node chassis")
		os.Exit(1)
	}
	if err = c.nodeWorker.InitNodeGateway(); err != nil {
		klog.ErrorS(err, "failed to init node gateway")
		os.Exit(1)
	}
	if err = c.nodeWorker.InitForOS(); err != nil {
		klog.ErrorS(err, "failed to init for os")
		os.Exit(1)
	}

	if err := writeCniConf(c.config.CniConfDir, c.config.CniConfFile, c.config.CniConfName); err != nil {
		klog.ErrorS(err, "failed to mv cni config file")
		os.Exit(1)
	}
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

func (c *controller) StartWorks(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()

	go c.ipSetsWorker.Run(stopCh)
	go c.iptablesWorker.Run(stopCh)
	go c.subnetWorker.Run(stopCh)
	go c.cniServer.Run(stopCh)
	go c.ovsWorker.Run(stopCh)

	mux := http.NewServeMux()
	if c.config.EnableMetrics {
		mux.Handle("/metrics", promhttp.Handler())
	}
	if c.config.EnablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}
	// conform to Gosec G114
	// https://github.com/securego/gosec#available-rules
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", "", c.config.PprofPort),
		ReadHeaderTimeout: 3 * time.Second,
		Handler:           mux,
	}
	klog.ErrorS(server.ListenAndServe(), "failed to listen and serve", "addr", server.Addr)
}
