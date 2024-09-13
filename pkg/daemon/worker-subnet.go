package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformer "k8s.io/client-go/informers/core/v1"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	ovninformer "github.com/fengjinlin/kube-ovn/pkg/client/informers/externalversions/kubeovn/v1"
	ovnlister "github.com/fengjinlin/kube-ovn/pkg/client/listers/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type SubnetWorker interface {
	Run(stopCh <-chan struct{})
}

func NewSubnetWorker(config *Configuration,
	subnetInformer ovninformer.SubnetInformer,
	nodeInformer coreinformer.NodeInformer) (SubnetWorker, error) {

	subnetQueue := workqueue.NewRateLimitingQueueWithConfig(workqueue.DefaultControllerRateLimiter(),
		workqueue.RateLimitingQueueConfig{Name: "Subnet"})

	mgr := &subnetWorker{
		config:        config,
		subnetsLister: subnetInformer.Lister(),
		nodesLister:   nodeInformer.Lister(),
		subnetQueue:   subnetQueue,
	}

	if _, err := subnetInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    mgr.enqueueAddSubnet,
		UpdateFunc: mgr.enqueueUpdateSubnet,
		DeleteFunc: mgr.enqueueDeleteSubnet,
	}); err != nil {
		return nil, err
	}

	return mgr, nil
}

type subnetWorker struct {
	config *Configuration

	subnetsLister ovnlister.SubnetLister
	nodesLister   corelister.NodeLister
	subnetQueue   workqueue.RateLimitingInterface
}

func (sw *subnetWorker) Run(stopCh <-chan struct{}) {
	go wait.Until(func() {
		if err := sw.reconcileAllSubnetRouters(); err != nil {
			klog.Error("failed to reconcile subnet routers", err)
		}
	}, 3*time.Second, stopCh)

	go sw.runSubnetWorker()

	<-stopCh
	klog.Info("stopping subnet worker")
}

func (sw *subnetWorker) reconcileAllSubnetRouters() error {
	defer runtime.HandleCrash()

	node, err := sw.nodesLister.Get(sw.config.NodeName)
	if err != nil {
		klog.Errorf("failed to get node %s %v", sw.config.NodeName, err)
		return err
	}
	v4NodeIP, v6NodeIP := utils.GetNodeInternalIP(node)

	subnets, err := sw.subnetsLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list subnets %v", err)
		return err
	}

	var subnetCIDRs []string
	var joinCIDRs []string
	for _, subnet := range subnets {
		if (subnet.Spec.Vlan != "" && !subnet.Spec.LogicalGateway) || subnet.Spec.Vpc != sw.config.ClusterRouter || !subnet.Status.IsReady() {
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
				if subnet.Name == sw.config.NodeSwitch {
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

type subnetEvent struct {
	oldObj, newObj *ovnv1.Subnet
}

func (sw *subnetWorker) enqueueAddSubnet(obj interface{}) {
	sw.subnetQueue.Add(subnetEvent{newObj: obj.(*ovnv1.Subnet)})
}

func (sw *subnetWorker) enqueueUpdateSubnet(oldObj, newObj interface{}) {
	sw.subnetQueue.Add(subnetEvent{oldObj: oldObj.(*ovnv1.Subnet), newObj: newObj.(*ovnv1.Subnet)})
}

func (sw *subnetWorker) enqueueDeleteSubnet(obj interface{}) {
	sw.subnetQueue.Add(subnetEvent{oldObj: obj.(*ovnv1.Subnet)})
}

func (sw *subnetWorker) runSubnetWorker() {
	for sw.processNextSubnetWorkItem() {
	}
}

func (sw *subnetWorker) processNextSubnetWorkItem() bool {
	obj, shutdown := sw.subnetQueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer sw.subnetQueue.Done(obj)
		event, ok := obj.(subnetEvent)
		if !ok {
			sw.subnetQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected subnetEvent in workqueue but got %#v", obj))
			return nil
		}
		if err := sw.reconcileSubnetRouters(&event); err != nil {
			sw.subnetQueue.AddRateLimited(event)
			return fmt.Errorf("failed to sync event: %s, requeuing. oldObj: %+v, newObj: %+v", err.Error(), event.oldObj, event.newObj)
		}
		sw.subnetQueue.Forget(obj)
		return nil
	}(obj)
	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (sw *subnetWorker) reconcileSubnetRouters(_ *subnetEvent) error {
	return sw.reconcileAllSubnetRouters()
}
