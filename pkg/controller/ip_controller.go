package controller

import (
	"context"
	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/translator"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/keymutex"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type ipController struct {
	baseController

	recorder record.EventRecorder
	keyMutex keymutex.KeyMutex
}

func (r *ipController) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ovnv1.IP{}).
		Complete(r)
}

func (r *ipController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error

		ipName = req.Name
	)

	r.keyMutex.LockKey(ipName)
	defer func() { _ = r.keyMutex.UnlockKey(ipName) }()

	var ipCr ovnv1.IP
	if err = r.Get(ctx, types.NamespacedName{Name: ipName}, &ipCr); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ip not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to get ip")
		return ctrl.Result{}, err
	}

	if ipCr.DeletionTimestamp.IsZero() {
		if !utils.ContainsString(ipCr.Finalizers, consts.FinalizerController) {
			ipCr.Finalizers = append(ipCr.Finalizers, consts.FinalizerController)
			if err := r.Update(ctx, &ipCr); err != nil {
				logger.Error(err, "failed to add finalizer to subnet")
				return ctrl.Result{}, err
			}
		} else {
			return r.handleAddOrUpdateIP(ctx, &ipCr)
		}
	} else {
		if utils.ContainsString(ipCr.Finalizers, consts.FinalizerController) {
			result, err := r.handleDeleteIP(ctx, &ipCr)
			if err == nil {
				ipCr.Finalizers = utils.RemoveString(ipCr.Finalizers, consts.FinalizerController)
				if err := r.Update(ctx, &ipCr); err != nil {
					logger.Error(err, "failed to remove finalizer from subnet")
					return ctrl.Result{}, err
				}
			}

			return result, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ipController) handleAddOrUpdateIP(ctx context.Context, ipCr *ovnv1.IP) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	switch ipCr.Spec.PodType {
	case consts.KindNode:
		nodeName := ipCr.Spec.PodName
		logger = logger.WithValues("node", nodeName)
		var node corev1.Node
		if err = r.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("node not found, delete ip cr", "node", nodeName)
				if err = r.Delete(ctx, ipCr); err != nil {
					logger.Error(err, "failed to delete ip cr")
					return ctrl.Result{}, err
				}
			} else {
				logger.Error(err, "failed to get node", "node", nodeName)
				return ctrl.Result{}, err
			}
		}
	default:
		podName := ipCr.Spec.PodName
		podNs := ipCr.Spec.Namespace
		logger = logger.WithValues("pod", podName, "namespace", podNs)
		var pod corev1.Pod
		if err = r.Get(ctx, types.NamespacedName{Name: podName, Namespace: podNs}, &pod); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("pod not found, delete ip cr")
				if err = r.Delete(ctx, ipCr); err != nil {
					logger.Error(err, "failed to delete ip cr")
					return ctrl.Result{}, err
				}
			} else {
				logger.Error(err, "failed to get pod")
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *ipController) handleDeleteIP(ctx context.Context, ipCr *ovnv1.IP) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	var subnet ovnv1.Subnet
	if err = r.Get(ctx, types.NamespacedName{Name: ipCr.Spec.Subnet}, &subnet); err != nil {
		logger.Error(err, "failed to get subnet")
		return ctrl.Result{}, err
	}
	if isOvnSubnet(&subnet) {
		portName := ipCr.Name
		releaseIP, err := translator.IP.CleanupPort(ctx, portName, ipCr.Spec.MacAddress)
		if err != nil {
			logger.Error(err, "failed to cleanup ip port", "portName", portName)
			return ctrl.Result{}, err
		}
		if releaseIP {
			r.ipam.ReleaseAddressByPod(ctx, ipCr.Name, ipCr.Spec.Subnet)
		}
	}

	return ctrl.Result{}, nil
}
