package controller

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/keymutex"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/translator"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type vpcController struct {
	baseController

	recorder          record.EventRecorder
	keyMutex          keymutex.KeyMutex
	updateStatusQueue workqueue.RateLimitingInterface
}

func (r *vpcController) SetupWithManager(mgr manager.Manager) error {
	subnetMapFunc := func(ctx context.Context, object client.Object) []reconcile.Request {
		subnet, ok := object.(*ovnv1.Subnet)
		if ok && subnet.Status.IsReady() {
			r.updateStatusQueue.Add(subnet.Spec.Vpc)
		}
		return nil
	}
	go func() {
		for r.processNextUpdateVpcStatusWorkItem() {
		}
	}()
	return ctrl.NewControllerManagedBy(mgr).
		For(&ovnv1.Vpc{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.config.WorkerNum,
		}).
		Watches(&ovnv1.Subnet{}, handler.EnqueueRequestsFromMapFunc(subnetMapFunc)).
		Complete(r)
}

func (r *vpcController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		logger  = log.FromContext(ctx)
		vpcName = req.Name
	)

	r.keyMutex.LockKey(vpcName)
	defer func() { _ = r.keyMutex.UnlockKey(vpcName) }()

	var vpc ovnv1.Vpc
	if err := r.Get(ctx, req.NamespacedName, &vpc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch vpc")
		return ctrl.Result{}, err
	}

	if vpc.DeletionTimestamp.IsZero() {
		if !utils.ContainsString(vpc.Finalizers, consts.FinalizerController) {
			vpc.Finalizers = append(vpc.Finalizers, consts.FinalizerController)
			if err := r.Update(ctx, &vpc); err != nil {
				logger.Error(err, "failed to add finalizer to vpc")
				return ctrl.Result{}, err
			}
		} else {
			return r.handleAddOrUpdateVpc(ctx, &vpc)
		}
	} else {
		if vpcName == r.config.DefaultVpc &&
			utils.ContainsString(vpc.Finalizers, consts.FinalizerDefaultProtection) {
			r.recorder.Eventf(&vpc, corev1.EventTypeWarning, "SubnetDefaultProtection",
				"vpc has been protected by kube-ovn, if you are sure to delete it, remove default-protection finalizer from vpc")
			return ctrl.Result{}, nil
		}
		if utils.ContainsString(vpc.Finalizers, consts.FinalizerController) {
			result, err := r.handleDeleteVpc(ctx, &vpc)
			if err == nil {
				vpc.Finalizers = utils.RemoveString(vpc.Finalizers, consts.FinalizerController)
				if err := r.Update(ctx, &vpc); err != nil {
					logger.Error(err, "failed to remove finalizer from subnet")
					return ctrl.Result{}, err
				}
			}
			return result, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *vpcController) handleAddOrUpdateVpc(ctx context.Context, vpc *ovnv1.Vpc) (ctrl.Result, error) {
	var (
		logger         = log.FromContext(ctx)
		err            error
		vpcName        = vpc.Name
		defaultVpcName = r.config.DefaultVpc
		joinSubnetName = r.config.JoinSubnet
	)

	logger.Info("handle add or update vpc")

	if err = r.formatVpc(ctx, vpc); err != nil {
		logger.Error(err, "failed to format vpc")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if err = translator.Vpc.CreateVpcRouter(ctx, vpc); err != nil {
		logger.Error(err, "failed to create vpc")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	isDefaultVpc := vpc.Name == defaultVpcName

	// handle static route
	{
		var gatewayIPs string
		if isDefaultVpc { // when default vpc, add
			joinSubnet := &ovnv1.Subnet{}
			if err = r.Get(ctx, client.ObjectKey{Namespace: "", Name: joinSubnetName}, joinSubnet); err != nil {
				logger.Error(err, "failed to get join subnet", "joinSubnet", joinSubnetName)
				return ctrl.Result{RequeueAfter: time.Second}, nil
			}
			gatewayIPs = joinSubnet.Spec.Gateway
		}
		if err = translator.Vpc.ReconcileVpcStaticRoutes(ctx, vpc, gatewayIPs); err != nil {
			logger.Error(err, "failed to reconcile vpc static routes")
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
	}

	// handle policy route
	if err = translator.Vpc.ReconcileVpcPolicyRoutes(ctx, vpc, isDefaultVpc); err != nil {
		logger.Error(err, "failed to reconcile vpc policy routes")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// load balancer
	{
		var tcpLBName, udpLBName, sctpLBName string
		var tcpSessionLBName, udpSessionLBName, sctpSessionLBName string
		if vpcName == r.config.DefaultVpc {
			tcpLBName = r.config.ClusterTCPLoadBalancer
			udpLBName = r.config.ClusterUDPLoadBalancer
			sctpLBName = r.config.ClusterSctpLoadBalancer
			tcpSessionLBName = r.config.ClusterTCPSessionLoadBalancer
			udpSessionLBName = r.config.ClusterUDPSessionLoadBalancer
			sctpSessionLBName = r.config.ClusterSctpSessionLoadBalancer
		} else {
			tcpLBName = fmt.Sprintf("vpc-%s-tcp-lb", vpcName)
			udpLBName = fmt.Sprintf("vpc-%s-udp-lb", vpcName)
			sctpLBName = fmt.Sprintf("vpc-%s-sctp-lb", vpcName)
			tcpSessionLBName = fmt.Sprintf("vpc-%s-tcp-session-lb", vpcName)
			udpSessionLBName = fmt.Sprintf("vpc-%s-udp-session-lb", vpcName)
			sctpSessionLBName = fmt.Sprintf("vpc-%s-sctp-session-lb", vpcName)
		}

		if r.config.EnableLb {
			vpc.Status.TCPLoadBalancer = tcpLBName
			vpc.Status.UDPLoadBalancer = udpLBName
			vpc.Status.SctpLoadBalancer = sctpLBName
			vpc.Status.TCPSessionLoadBalancer = tcpSessionLBName
			vpc.Status.UDPSessionLoadBalancer = udpSessionLBName
			vpc.Status.SctpSessionLoadBalancer = sctpSessionLBName

			if err = translator.Vpc.CreateVpcLoadBalancers(ctx, vpc); err != nil {
				logger.Error(err, "failed to create vpc load balancers")
				return ctrl.Result{}, err
			}
		} else {
			if vpc.Status.TCPLoadBalancer != "" {
				if err = translator.Vpc.DeleteVpcLoadBalancers(ctx, vpc); err != nil {
					logger.Error(err, "failed to delete vpc load balancers")
					return ctrl.Result{}, err
				}

				vpc.Status.TCPLoadBalancer = ""
				vpc.Status.UDPLoadBalancer = ""
				vpc.Status.SctpLoadBalancer = ""
				vpc.Status.TCPSessionLoadBalancer = ""
				vpc.Status.UDPSessionLoadBalancer = ""
				vpc.Status.SctpSessionLoadBalancer = ""
			}
		}
	}

	// update vpc status
	vpc.Status.Router = vpcName
	vpc.Status.Ready = true
	vpc.Status.Default = isDefaultVpc
	if err = r.Status().Update(ctx, vpc); err != nil {
		logger.Error(err, "failed to update vpc status")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *vpcController) formatVpc(ctx context.Context, vpc *ovnv1.Vpc) error {
	var (
		logger  = log.FromContext(ctx)
		changed bool
	)
	for _, item := range vpc.Spec.StaticRoutes {
		// check policy
		if item.Policy == "" {
			item.Policy = ovnv1.PolicyDst
			changed = true
		}
		if item.Policy != ovnv1.PolicyDst && item.Policy != ovnv1.PolicySrc {
			return fmt.Errorf("unknown policy type: %s", item.Policy)
		}
		// check cidr
		if strings.Contains(item.CIDR, "/") {
			if _, _, err := net.ParseCIDR(item.CIDR); err != nil {
				return fmt.Errorf("invalid cidr %s: %w", item.CIDR, err)
			}
		} else if ip := net.ParseIP(item.CIDR); ip == nil {
			return fmt.Errorf("invalid ip %s", item.CIDR)
		}
		// check next hop ip
		if ip := net.ParseIP(item.NextHopIP); ip == nil {
			return fmt.Errorf("invalid next hop ip %s", item.NextHopIP)
		}
	}

	for _, route := range vpc.Spec.PolicyRoutes {
		if route.Action != ovnv1.PolicyRouteActionReroute {
			if route.NextHopIP != "" {
				route.NextHopIP = ""
				changed = true
			}
		} else {
			// ecmp policy route may reroute to multiple next hop ips
			for _, ipStr := range strings.Split(route.NextHopIP, ",") {
				if ip := net.ParseIP(ipStr); ip == nil {
					return fmt.Errorf("invalid next hop ips: %s", route.NextHopIP)
				}
			}
		}
	}

	if changed {
		if err := r.Update(ctx, vpc); err != nil {
			logger.Error(err, "failed to update vpc")
			return err
		}
	}

	return nil
}

func (r *vpcController) handleDeleteVpc(ctx context.Context, vpc *ovnv1.Vpc) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	logger.Info("handle delete vpc")

	if err = translator.Vpc.DeleteVpcLoadBalancers(ctx, vpc); err != nil {
		logger.Error(err, "failed to delete vpc load balancers")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *vpcController) processNextUpdateVpcStatusWorkItem() bool {
	obj, shutdown := r.updateStatusQueue.Get()
	if shutdown {
		return false
	}

	if err := func(obj interface{}) error {
		defer r.updateStatusQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			r.updateStatusQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := r.handleUpdateVpcStatus(key); err != nil {
			r.updateStatusQueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		r.updateStatusQueue.Forget(obj)
		return nil
	}(obj); err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (r *vpcController) handleUpdateVpcStatus(key string) error {
	r.keyMutex.LockKey(key)
	defer func() { _ = r.keyMutex.UnlockKey(key) }()

	var (
		ctx, cancel = context.WithCancel(context.TODO())
		logger      = log.FromContext(ctx).WithValues("vpc", key, "reconcileID", uuid.New().String())
		err         error
	)
	defer cancel()
	logger.Info("handle update vpc status")

	var vpc ovnv1.Vpc
	if err = r.Get(ctx, client.ObjectKey{Name: key}, &vpc); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("vpc not found")
			return nil
		} else {
			logger.Error(err, "failed to get vpc")
			return err
		}
	}

	var allSubnets ovnv1.SubnetList
	if err = r.List(ctx, &allSubnets); err != nil {
		logger.Error(err, "failed to list subnets")
		return err
	}
	var subnets []string
	var defaultSubnet string
	for _, subnet := range allSubnets.Items {
		if subnet.Spec.Vpc != key || !subnet.DeletionTimestamp.IsZero() || !isOvnSubnet(&subnet) {
			continue
		}
		subnets = append(subnets, subnet.Name)
		if subnet.Spec.Default {
			defaultSubnet = subnet.Name
		}
	}

	vpc.Status.DefaultSubnet = defaultSubnet
	vpc.Status.Subnets = subnets
	if err = r.Status().Update(ctx, &vpc); err != nil {
		logger.Error(err, "failed to update vpc status")
		return err
	}

	return nil
}
