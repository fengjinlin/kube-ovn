package daemon

import (
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/translator"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	coreinformer "k8s.io/client-go/informers/core/v1"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	ovninformer "github.com/fengjinlin/kube-ovn/pkg/client/informers/externalversions/kubeovn/v1"
	ovnlister "github.com/fengjinlin/kube-ovn/pkg/client/listers/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/cni/request"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type PodWorker interface {
	Run(stopCh <-chan struct{})

	HandleCniAddPodPort(req *request.CniRequest) (*request.CniResponse, error)
	HandleCniDelPodPort(req *request.CniRequest) (*request.CniResponse, error)
}

func NewPodWorker(config *Configuration,
	subnetInformer ovninformer.SubnetInformer,
	podInformer coreinformer.PodInformer,
	ovsWorker OvsWorker) (PodWorker, error) {

	w := &podWorker{
		config:         config,
		subnetInformer: subnetInformer,
		subnetLister:   subnetInformer.Lister(),
		podInformer:    podInformer,
		podLister:      podInformer.Lister(),
		ovsWorker:      ovsWorker,
	}

	w.podQueue = workqueue.NewRateLimitingQueueWithConfig(workqueue.DefaultControllerRateLimiter(),
		workqueue.RateLimitingQueueConfig{Name: "Pod"})

	if _, err := podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			if key, err := cache.MetaNamespaceKeyFunc(newObj); err != nil {
				utilruntime.HandleError(err)
				return
			} else {
				w.podQueue.Add(key)
			}
		},
	}); err != nil {
		return nil, err
	}

	return w, nil
}

type podWorker struct {
	config    *Configuration
	ovsWorker OvsWorker

	subnetInformer ovninformer.SubnetInformer
	subnetLister   ovnlister.SubnetLister
	podInformer    coreinformer.PodInformer
	podLister      corelister.PodLister

	podQueue workqueue.RateLimitingInterface
}

func (w *podWorker) Run(stopCh <-chan struct{}) {
	go w.runPodWorker()

	<-stopCh
	klog.Info("stopping pod worker")
}

func (w *podWorker) HandleCniAddPodPort(req *request.CniRequest) (*request.CniResponse, error) {
	var (
		err error
	)

	ifName := req.IfName
	if ifName == "" {
		ifName = "eth0"
	}

	var (
		gatewayCheckMode                                               int
		macAddr, ip, ipAddr, cidr, gw, subnetName, nicType, podNicName string
		ingressRate, egressRate, latency, limit, loss, jitter          string
		routes                                                         []request.Route
		isDefaultRoute                                                 = ifName == "eth0"
		pod                                                            *corev1.Pod
	)

	for i := 0; i < 20; i++ {
		if pod, err = w.podLister.Pods(req.PodNamespace).Get(req.PodName); err != nil {
			err = fmt.Errorf("get pod %s/%s failed %v", req.PodNamespace, req.PodName, err)
			klog.Error(err)
			return nil, err
		}
		if pod.Annotations[fmt.Sprintf(consts.AnnotationAllocatedTemplate, req.Provider)] != "true" {
			klog.Infof("wait address for pod %s/%s provider %s", req.PodNamespace, req.PodName, req.Provider)
			// wait controller assign an address
			time.Sleep(1 * time.Second)
			continue
		}

		if isDefaultRoute &&
			pod.Annotations[fmt.Sprintf(consts.AnnotationRoutedTemplate, req.Provider)] != "true" &&
			strings.HasSuffix(req.Provider, consts.OvnProviderName) {
			klog.Infof("wait route ready for pod %s/%s provider %s", req.PodNamespace, req.PodName, req.Provider)
			//cniWaitRouteResult.WithLabelValues(nodeName).Inc()
			time.Sleep(1 * time.Second)
			continue
		}

		ip = pod.Annotations[fmt.Sprintf(consts.AnnotationIPAddressTemplate, req.Provider)]
		cidr = pod.Annotations[fmt.Sprintf(consts.AnnotationCidrTemplate, req.Provider)]
		gw = pod.Annotations[fmt.Sprintf(consts.AnnotationGatewayTemplate, req.Provider)]
		macAddr = pod.Annotations[fmt.Sprintf(consts.AnnotationMacAddressTemplate, req.Provider)]
		subnetName = pod.Annotations[fmt.Sprintf(consts.AnnotationSubnetTemplate, req.Provider)]
		ingressRate = pod.Annotations[fmt.Sprintf(consts.AnnotationIngressRateTemplate, req.Provider)]
		egressRate = pod.Annotations[fmt.Sprintf(consts.AnnotationEgressRateTemplate, req.Provider)]
		//latency = pod.Annotations[fmt.Sprintf(util.NetemQosLatencyAnnotationTemplate, req.Provider)]
		//limit = pod.Annotations[fmt.Sprintf(util.NetemQosLimitAnnotationTemplate, req.Provider)]
		//loss = pod.Annotations[fmt.Sprintf(util.NetemQosLossAnnotationTemplate, req.Provider)]
		//jitter = pod.Annotations[fmt.Sprintf(util.NetemQosJitterAnnotationTemplate, req.Provider)]
		//providerNetwork = pod.Annotations[fmt.Sprintf(consts.ProviderNetworkTemplate, req.Provider)]
		//vmName = pod.Annotations[fmt.Sprintf(util.VMAnnotationTemplate, req.Provider)]
		ipAddr = utils.GetIPAddrWithMask(ip, cidr)
		//if s := pod.Annotations[fmt.Sprintf(utils.RoutesAnnotationTemplate, req.Provider)]; s != "" {
		//	if err = json.Unmarshal([]byte(s), &routes); err != nil {
		//		errMsg := fmt.Errorf("invalid routes for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		//		klog.Error(errMsg)
		//		if err = resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
		//			klog.Errorf("failed to write response: %v", err)
		//		}
		//		return
		//	}
		//}

		switch {
		case req.DeviceID != "":
			nicType = consts.PortTypeOffload
			err = fmt.Errorf("offload port not supported")
			return nil, err

		case req.VhostUserSocketVolumeName != "":
			nicType = consts.PortTypeDpdk
			err = fmt.Errorf("dpdk port not supported")
			return nil, err

		default:
			nicType = pod.Annotations[fmt.Sprintf(consts.AnnotationPodNicTypeTemplate, req.Provider)]
		}

		break
	}

	if pod == nil {
		err = fmt.Errorf("pod %s/%s not found", req.PodNamespace, req.PodName)
		klog.Error(err)
		return nil, err
	}

	if pod.Annotations[fmt.Sprintf(consts.AnnotationAllocatedTemplate, req.Provider)] != "true" {
		err = fmt.Errorf("no address allocated to pod %s/%s provider %s, please see kube-ovn-controller logs to find errors", pod.Namespace, pod.Name, req.Provider)
		klog.Error(err)
		return nil, err
	}

	if isDefaultRoute &&
		pod.Annotations[fmt.Sprintf(consts.AnnotationRoutedTemplate, req.Provider)] != "true" &&
		strings.HasSuffix(req.Provider, consts.OvnProviderName) {
		err = fmt.Errorf("route is not ready for pod %s/%s provider %s, please see kube-ovn-controller logs to find errors",
			pod.Namespace, pod.Name, req.Provider)
		klog.Error(err)
		return nil, err
	}

	if subnetName == "" {
		err = fmt.Errorf("pod %s/%s provider %s not bind to subnet", pod.Namespace, pod.Name, req.Provider)
		klog.Error(err)
		return nil, err
	}

	//if err := csh.UpdateIPCr(req, subnet, ip); err != nil {
	//	if err := resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: err.Error()}); err != nil {
	//		klog.Errorf("failed to write response, %v", err)
	//	}
	//	return
	//}

	routes = append(req.Routes, routes...)
	if strings.HasSuffix(req.Provider, consts.OvnProviderName) {
		podSubnet, err := w.subnetLister.Get(subnetName)
		if err != nil {
			err = fmt.Errorf("failed to get subnet %s: %v", subnetName, err)
			klog.Error(err)
			return nil, err
		}

		subnetHasVlan := podSubnet.Spec.Vlan != ""
		detectIPConflict := w.config.EnableArpDetectIPConflict && subnetHasVlan

		var mtu int
		if podSubnet.Spec.Mtu > 0 {
			mtu = int(podSubnet.Spec.Mtu)
		} else {
			mtu = w.config.MTU
		}

		klog.Infof("create container interface %s mac %s, ip %s, cidr %s, gw %s, custom routes %v", ifName, macAddr, ipAddr, cidr, gw, routes)
		podPort := PodPort{
			VSwitchClient: w.config.vSwitchClient,
			OvsWorker:     w.ovsWorker,

			PodName:            req.PodName,
			PodNS:              req.PodNamespace,
			routes:             routes,
			Provider:           req.Provider,
			NetNS:              req.NetNs,
			ContainerID:        req.ContainerID,
			VfDriver:           req.VfDriver,
			IfaceName:          ifName,
			IfaceID:            translator.PodNameToPortName(req.PodName, req.PodNamespace, req.Provider),
			IPAddr:             ipAddr,
			Mac:                macAddr,
			MTU:                mtu,
			Gateway:            gw,
			GatewayCheckMode:   gatewayCheckMode,
			DeviceID:           req.DeviceID,
			PortType:           nicType,
			IsDefaultRoute:     isDefaultRoute,
			IsDetectIPConflict: detectIPConflict,
			IngressRate:        ingressRate,
			EgressRate:         egressRate,
			Latency:            latency,
			Limit:              limit,
			Loss:               loss,
			Jitter:             jitter,
		}

		//podNicName = ifName
		//switch nicType {
		//case consts.PortTypeInternal:
		//	//podNicName, routes, err = csh.configureNicWithInternalPort(req.PodName, req.PodNamespace, req.Provider, req.NetNS, req.ContainerID, ifName, macAddr, mtu, ipAddr, gw, isDefaultRoute, detectIPConflict, routes, req.DNS.Nameservers, req.DNS.Search, ingress, egress, req.DeviceID, nicType, latency, limit, loss, jitter, gatewayCheckMode, u2oInterconnectionIP)
		//case consts.PortTypeDpdk:
		//	//err = csh.configureDpdkNic(req.PodName, req.PodNamespace, req.Provider, req.NetNS, req.ContainerID, ifName, macAddr, mtu, ipAddr, gw, ingress, egress, getShortSharedDir(pod.UID, req.VhostUserSocketVolumeName), req.VhostUserSocketName)
		//	routes = nil
		//default:
		//	routes, err = w.configureNic(req.PodName, req.PodNamespace, req.Provider, req.NetNS, req.ContainerID, req.VfDriver, ifName, macAddr, mtu, ipAddr, gw, isDefaultRoute, detectIPConflict, routes, req.DNS.Nameservers, req.DNS.Search, ingressRate, egressRate, req.DeviceID, nicType, latency, limit, loss, jitter, gatewayCheckMode, u2oInterconnectionIP)
		//}
		routes, err = podPort.Install()
		if err != nil {
			err = fmt.Errorf("configure nic failed %v", err)
			klog.Error(err)
			return nil, err
		}

		podNicName = podPort.IfaceName

		//ifaceID := translator.PodNameToPortName(req.PodName, req.PodNamespace, req.Provider)
		//if err = ovs.ConfigInterfaceMirror(csh.Config.EnableMirror, pod.Annotations[fmt.Sprintf(util.MirrorControlAnnotationTemplate, req.Provider)], ifaceID); err != nil {
		//	klog.Errorf("failed mirror to mirror0, %v", err)
		//	return
		//}

		//if err = csh.controller.addEgressConfig(podSubnet, ip); err != nil {
		//	errMsg := fmt.Errorf("failed to add egress configuration: %v", err)
		//	klog.Error(errMsg)
		//	if err = resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
		//		klog.Errorf("failed to write response, %v", err)
		//	}
		//	return
		//}
	}

	response := &request.CniResponse{
		Protocol:   utils.CheckProtocol(cidr),
		IPAddress:  ip,
		MacAddress: macAddr,
		CIDR:       cidr,
		PodNicName: podNicName,
		Routes:     routes,
	}
	if isDefaultRoute {
		response.Gateway = gw
	}

	return response, nil
}

func (w *podWorker) HandleCniDelPodPort(req *request.CniRequest) (*request.CniResponse, error) {

	pod, err := w.podLister.Pods(req.PodNamespace).Get(req.PodName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}

		errMsg := fmt.Errorf("parse del request failed %v", err)
		klog.Error(errMsg)
		return nil, err
	}

	klog.Infof("del port request: %+v", req)

	if pod.Annotations != nil && (req.Provider == consts.OvnProviderName || req.CniType == consts.CniVendorName) {
		var nicType string
		switch {
		case req.DeviceID != "":
			nicType = consts.PortTypeOffload

		case req.VhostUserSocketVolumeName != "":
			nicType = consts.PortTypeDpdk

		default:
			nicType = pod.Annotations[fmt.Sprintf(consts.AnnotationPodNicTypeTemplate, req.Provider)]

		}

		podPort := &PodPort{
			VSwitchClient: w.config.vSwitchClient,

			PodName:     req.PodName,
			PodNS:       req.PodNamespace,
			ContainerID: req.ContainerID,
			NetNS:       req.NetNs,
			DeviceID:    req.DeviceID,
			IfaceName:   req.IfName,
			PortType:    nicType,
		}

		err = podPort.Uninstall()
		if err != nil {
			errMsg := fmt.Errorf("del nic failed %v", err)
			klog.Error(errMsg)
			return nil, err
		}
	}

	return nil, nil
}

func (w *podWorker) runPodWorker() {
	for w.processNextPodWorkItem() {
	}
}

func (w *podWorker) processNextPodWorkItem() bool {
	obj, shutdown := w.podQueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer w.podQueue.Done(obj)
		key, ok := obj.(string)
		if !ok {
			w.podQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := w.handleUpdatePod(key); err != nil {
			w.podQueue.AddRateLimited(key)
			return fmt.Errorf("error sync '%s': %s, requeuing", key, err.Error())
		}
		w.podQueue.Forget(obj)
		return nil
	}(obj)
	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (w *podWorker) handleUpdatePod(key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.Errorf("invalid resource key: %s", key)
		return nil
	}
	klog.Infof("handle qos update for pod %s/%s", namespace, name)

	pod, err := w.podLister.Pods(namespace).Get(name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	ifaceID := translator.PodNameToPortName(name, namespace, "")
	ingressRate := pod.Annotations[consts.AnnotationIngressRate]
	egressRate := pod.Annotations[consts.AnnotationEgressRate]

	if err = w.ovsWorker.SetInterfaceBandwidth(name, namespace, ifaceID, egressRate, ingressRate); err != nil {
		klog.Errorf("failed to set port qos: %v", err)
		return err
	}

	return nil
}
