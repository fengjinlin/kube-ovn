package daemon

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	k8sexec "k8s.io/utils/exec"

	ovninformer "github.com/fengjinlin/kube-ovn/pkg/client/informers/externalversions"
	ovnlister "github.com/fengjinlin/kube-ovn/pkg/client/listers/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type Controller struct {
	config *Configuration

	subnetsLister ovnlister.SubnetLister
	subnetsSynced cache.InformerSynced
	subnetQueue   workqueue.RateLimitingInterface

	podsLister listerv1.PodLister
	podsSynced cache.InformerSynced
	podQueue   workqueue.RateLimitingInterface

	nodesLister listerv1.NodeLister
	nodesSynced cache.InformerSynced

	recorder record.EventRecorder

	protocol string

	ControllerRuntime
	localPodName   string
	localNamespace string

	k8sExec k8sexec.Interface
}

func NewController(config *Configuration, stopCh <-chan struct{}, podInformerFactory, nodeInformerFactory informers.SharedInformerFactory, ovnInformerFactory ovninformer.SharedInformerFactory) (*Controller, error) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: config.KubeClient.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: config.NodeName})

	subnetInformer := ovnInformerFactory.Kubeovn().V1().Subnets()
	podInformer := podInformerFactory.Core().V1().Pods()
	nodeInformer := nodeInformerFactory.Core().V1().Nodes()

	controller := &Controller{
		config: config,

		subnetsLister: subnetInformer.Lister(),
		subnetsSynced: subnetInformer.Informer().HasSynced,
		subnetQueue:   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Subnet"),

		podsLister: podInformer.Lister(),
		podsSynced: podInformer.Informer().HasSynced,
		podQueue:   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Pod"),

		nodesLister: nodeInformer.Lister(),
		nodesSynced: nodeInformer.Informer().HasSynced,

		recorder: recorder,
		k8sExec:  k8sexec.New(),
	}

	node, err := config.KubeClient.CoreV1().Nodes().Get(context.Background(), config.NodeName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "failed to get node info", "node", config.NodeName)
		return nil, err
	}
	controller.protocol = utils.CheckProtocol(node.Annotations[consts.AnnotationJoinIP])

	if err = controller.initRuntime(); err != nil {
		return nil, err
	}

	podInformerFactory.Start(stopCh)
	nodeInformerFactory.Start(stopCh)
	ovnInformerFactory.Start(stopCh)

	if !cache.WaitForCacheSync(stopCh,
		controller.subnetsSynced,
		controller.podsSynced,
		controller.nodesSynced) {
		klog.ErrorS(nil, "failed to wait for caches to sync")
	}

	if _, err = subnetInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueAddSubnet,
		UpdateFunc: controller.enqueueUpdateSubnet,
		DeleteFunc: controller.enqueueDeleteSubnet,
	}); err != nil {
		return nil, err
	}
	if _, err = podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: controller.enqueuePod,
	}); err != nil {
		return nil, err
	}

	return controller, nil
}

// ControllerRuntime represents runtime specific controller members
type ControllerRuntime struct {
	iptables         map[string]*iptables.IPTables
	ipSetsController *IPSetsController
}

func (c *Controller) initRuntime() error {
	if err := c.initIPTables(); err != nil {
		klog.Errorf("failed to init iptables: %v", err)
		return err
	}
	if err := c.initIPSets(); err != nil {
		klog.Errorf("failed to init ipsets: %v", err)
		return err
	}
	return nil
}

func (c *Controller) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.subnetQueue.ShutDown()
	defer c.podQueue.ShutDown()

	klog.Info("Started workers")
	go wait.Until(c.runSubnetWorker, time.Second, stopCh)
	//go wait.Until(c.runPodWorker, time.Second, stopCh)
	go wait.Until(c.runGateway, 3*time.Second, stopCh)
	//go wait.Until(c.loopEncapIPCheck, 3*time.Second, stopCh)
	//go wait.Until(c.ovnMetricsUpdate, 3*time.Second, stopCh)
	go wait.Until(func() {
		if err := c.reconcileRouters(); err != nil {
			klog.Errorf("failed to reconcile ovn0 routes: %v", err)
		}
	}, 3*time.Second, stopCh)
	//go wait.Until(func() {
	//	if err := c.markAndCleanInternalPort(); err != nil {
	//		klog.Errorf("gc ovs port error: %v", err)
	//	}
	//}, 5*time.Minute, stopCh)

	<-stopCh
	klog.Info("Shutting down workers")
}

func (c *Controller) runGateway() {
	if err := c.setUpIPSets(); err != nil {
		klog.Errorf("failed to update gw ipsets: %v", err)
	}

	if err := c.setUpIPTables(); err != nil {
		klog.Errorf("failed to set up gw iptables")
	}

}

func (c *Controller) reconcileRouters() error {
	node, err := c.nodesLister.Get(c.config.NodeName)
	if err != nil {
		klog.Errorf("failed to get node %s %v", c.config.NodeName, err)
		return err
	}
	v4NodeIP, v6NodeIP := utils.GetNodeInternalIP(node)

	subnets, err := c.subnetsLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list subnets %v", err)
		return err
	}

	var subnetCIDRs []string
	var joinCIDRs []string
	for _, subnet := range subnets {
		if (subnet.Spec.Vlan != "" && !subnet.Spec.LogicalGateway) || subnet.Spec.Vpc != c.config.ClusterRouter || !subnet.Status.IsReady() {
			continue
		}

		for _, cidrBlock := range strings.Split(subnet.Spec.CIDRBlock, ",") {
			if _, ipNet, err := net.ParseCIDR(cidrBlock); err != nil {
				klog.Errorf("%s is not a valid cidr block", cidrBlock)
			} else {
				if v4NodeIP != "" && utils.CIDRContainIP(cidrBlock, v4NodeIP) {
					continue
				}
				if v6NodeIP != "" && utils.CIDRContainIP(cidrBlock, v6NodeIP) {
					continue
				}
				subnetCIDRs = append(subnetCIDRs, ipNet.String())
				if subnet.Name == c.config.NodeSwitch {
					joinCIDRs = append(joinCIDRs, ipNet.String())
				}
			}
		}
	}

	if len(subnetCIDRs) == 0 {
		klog.Infof("no subnet is ready")
		return nil
	}

	if len(joinCIDRs) == 0 {
		klog.Infof("join subnet is not ready")
		return nil
	}

	joinGw, ok := node.Annotations[consts.AnnotationJoinGateway]
	if !ok {
		err := fmt.Errorf("annotation for node %s ovn.kubernetes.io/gateway not exists", node.Name)
		klog.Error(err)
		return err
	}

	nic, err := netlink.LinkByName(consts.NodeNicName)
	if err != nil {
		klog.Errorf("failed to get nic %s", consts.NodeNicName)
		return fmt.Errorf("failed to get nic %s", consts.NodeNicName)
	}

	allRoutes, err := getNicExistRoutes(nil, joinGw)
	if err != nil {
		return err
	}
	nodeNicRoutes, err := getNicExistRoutes(nic, joinGw)
	if err != nil {
		return err
	}

	toAdd, toDel := routeDiff(nodeNicRoutes, allRoutes, subnetCIDRs, joinCIDRs, joinGw, net.ParseIP(v4NodeIP), net.ParseIP(v6NodeIP))
	klog.Infof("route to del %v", toDel)
	klog.Infof("route to add %v", toAdd)
	for _, r := range toDel {
		if err = netlink.RouteDel(&netlink.Route{Dst: r.Dst}); err != nil {
			klog.Errorf("failed to del route %v", err)
		}
	}

	for _, r := range toAdd {
		r.LinkIndex = nic.Attrs().Index
		if err = netlink.RouteReplace(&r); err != nil {
			klog.Errorf("failed to replace route %v: %v", r, err)
		}
	}

	return nil
}

func routeDiff(nodeNicRoutes []netlink.Route, allRoutes []netlink.Route, subnetCIDRs []string, joinCIDRs []string, joinGw string, v4NodeIP net.IP, v6NodeIP net.IP) (toAdd []netlink.Route, toDel []netlink.Route) {
	for _, nr := range nodeNicRoutes {
		if nr.Scope == netlink.SCOPE_LINK || nr.Dst == nil || nr.Dst.IP.IsLinkLocalUnicast() {
			continue
		}

		found := false
		for _, cidr := range subnetCIDRs {
			if nr.Dst.String() == cidr {
				found = true
				break
			}
		}
		if !found {
			toDel = append(toDel, nr)
		}

		conflict := false
		for _, ar := range allRoutes {
			if ar.Dst != nil && ar.Dst == nr.Dst && ar.LinkIndex != nr.LinkIndex {
				conflict = true
				break
			}
		}
		if conflict {
			toDel = append(toDel, nr)
		}
	}

	// route: node -> pod
	v4GwStr, v6GwStr := utils.SplitStringIP(joinGw)
	v4Gw, v6Gw := net.ParseIP(v4GwStr), net.ParseIP(v6GwStr)
	for _, cidr := range subnetCIDRs {
		if utils.ContainsString(joinCIDRs, cidr) {
			continue
		}

		found := false
		for _, ar := range allRoutes {
			if ar.Dst != nil && ar.Dst.String() == cidr {
				found = true
				break
			}
		}
		if found {
			continue
		}

		var src, gw net.IP
		switch utils.CheckProtocol(cidr) {
		case utils.ProtocolIPv4:
			src = v4NodeIP
			gw = v4Gw
		case utils.ProtocolIPv6:
			src = v6NodeIP
			gw = v6Gw
		default:
			klog.Warningf("invalid cidr %s", cidr)
			continue
		}
		for _, nr := range nodeNicRoutes {
			if nr.Dst != nil && nr.Dst.String() == cidr {
				if (src == nil && nr.Src == nil) ||
					(src != nil && src.Equal(nr.Src)) {
					found = true
					break
				}
			}
		}

		if !found {
			_, c, _ := net.ParseCIDR(cidr)
			toAdd = append(toAdd, netlink.Route{
				Dst:   c,
				Src:   src,
				Gw:    gw,
				Scope: netlink.SCOPE_UNIVERSE,
			})
		}
	}

	return
}

func getNicExistRoutes(nic netlink.Link, gateway string) ([]netlink.Route, error) {
	var routes, existRoutes []netlink.Route
	var err error
	for _, gw := range strings.Split(gateway, ",") {
		if utils.CheckProtocol(gw) == utils.ProtocolIPv4 {
			routes, err = netlink.RouteList(nic, netlink.FAMILY_V4)
		} else {
			routes, err = netlink.RouteList(nic, netlink.FAMILY_V6)
		}
		if err != nil {
			return nil, err
		}
		existRoutes = append(existRoutes, routes...)
	}
	return existRoutes, nil
}
