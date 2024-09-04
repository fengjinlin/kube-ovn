package controller

import (
	"context"
	"errors"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/keymutex"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type namespaceController struct {
	baseController

	recorder record.EventRecorder
	keyMutex keymutex.KeyMutex
}

func (r *namespaceController) SetupWithManager(mgr manager.Manager) error {
	subnetMapFunc := func(ctx context.Context, object client.Object) []reconcile.Request {
		subnet, ok := object.(*ovnv1.Subnet)
		if ok && len(subnet.Spec.Namespaces) > 0 {
			reqs := make([]reconcile.Request, 0, len(subnet.Spec.Namespaces))
			for _, ns := range subnet.Spec.Namespaces {
				reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: ns}})
			}
			return reqs
		}
		return nil
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Watches(&ovnv1.Subnet{}, handler.EnqueueRequestsFromMapFunc(subnetMapFunc)).
		Complete(r)
}

func (r *namespaceController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		nsName = req.Name
	)

	r.keyMutex.LockKey(nsName)
	defer func() { _ = r.keyMutex.UnlockKey(nsName) }()

	var ns corev1.Namespace
	if err := r.Get(ctx, req.NamespacedName, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Namespace not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to get Namespace")
		return ctrl.Result{}, err
	}

	return r.handleAddOrUpdateNamespace(ctx, &ns)
}

func (r *namespaceController) handleAddOrUpdateNamespace(ctx context.Context, ns *corev1.Namespace) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
		nsName = ns.Name
	)

	logger.Info("handle add or update namespace")

	var subnetList ovnv1.SubnetList
	if err = r.List(ctx, &subnetList); err != nil {
		logger.Error(err, "failed to list subnets")
		return ctrl.Result{}, err
	}
	var bindSubnets []ovnv1.Subnet
	for _, subnet := range subnetList.Items {
		if utils.ContainsString(subnet.Spec.Namespaces, nsName) {
			bindSubnets = append(bindSubnets, subnet)
		}
	}

	if len(bindSubnets) > 1 {
		for _, subnet := range bindSubnets {
			r.recorder.Eventf(&subnet, corev1.EventTypeWarning, "NamespaceRepeatBinding", "namespace %s bind to more than one subnet", nsName)
		}
		return ctrl.Result{}, errors.New("namespace bind to more than one subnet")
	}

	var (
		changed bool
		subnet  ovnv1.Subnet
	)
	if len(bindSubnets) == 1 {
		subnet = bindSubnets[0]
	} else {
		if err = r.Get(ctx, client.ObjectKey{Name: r.config.DefaultSubnet}, &subnet); err != nil {
			logger.Error(err, "failed to get default subnet")
			return ctrl.Result{}, err
		}
	}

	if len(ns.Annotations) == 0 {
		ns.Annotations = make(map[string]string)
	}
	if ns.Annotations[consts.AnnotationSubnet] != subnet.Name {
		ns.Annotations[consts.AnnotationSubnet] = subnet.Name
		ns.Annotations[consts.AnnotationSubnetCidr] = subnet.Spec.CIDRBlock
		ns.Annotations[consts.AnnotationSubnetExcludeIPs] = strings.Join(subnet.Spec.ExcludeIPs, ",")
		changed = true
	}

	if changed {
		if err = r.Update(ctx, ns); err != nil {
			logger.Error(err, "failed to update namespace")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}
