package controller

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/fengjinlin/kube-ovn/pkg/translator"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type serviceController struct {
	baseController

	recorder record.EventRecorder
	keyMutex keymutex.KeyMutex
}

func (r *serviceController) SetupWithManager(mgr manager.Manager) error {
	endpointsMapFunc := func(ctx context.Context, object client.Object) []reconcile.Request {
		ep, ok := object.(*corev1.Endpoints)
		if ok && ep.DeletionTimestamp.IsZero() {
			return []reconcile.Request{
				{NamespacedName: client.ObjectKeyFromObject(ep)},
			}
		}
		return nil
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}).
		Watches(&corev1.Endpoints{}, handler.EnqueueRequestsFromMapFunc(endpointsMapFunc)).
		Complete(r)
}

func (r *serviceController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		key    = req.String()
	)

	r.keyMutex.LockKey(key)
	defer func() { _ = r.keyMutex.UnlockKey(key) }()

	var svc corev1.Service
	if err := r.Get(ctx, req.NamespacedName, &svc); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("service not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to get service")
		return ctrl.Result{}, err
	}

	if svc.DeletionTimestamp.IsZero() {
		if !utils.ContainsString(svc.Finalizers, consts.FinalizerController) {
			svc.Finalizers = append(svc.Finalizers, consts.FinalizerController)
			if err := r.Update(ctx, &svc); err != nil {
				logger.Error(err, "failed to add finalizer to service")
				return ctrl.Result{}, err
			}
		} else {
			return r.handleAddOrUpdateService(ctx, &svc)
		}
	} else {
		if utils.ContainsString(svc.Finalizers, consts.FinalizerController) {
			result, err := r.handleDeleteService(ctx, &svc)
			if err == nil {
				svc.Finalizers = utils.RemoveString(svc.Finalizers, consts.FinalizerController)
				if err := r.Update(ctx, &svc); err != nil {
					logger.Error(err, "failed to remove finalizer from service")
					return ctrl.Result{}, err
				}
			}

			return result, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *serviceController) handleAddOrUpdateService(ctx context.Context, svc *corev1.Service) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	if r.config.EnableLb {
		var (
			endpoints           corev1.Endpoints
			svcPods             corev1.PodList
			subnetName, vpcName string
		)

		if err = r.Get(ctx, client.ObjectKeyFromObject(svc), &endpoints); err != nil {
			logger.Error(err, "unable to get endpoints")
			return ctrl.Result{}, err
		}

		if err = r.List(ctx, &svcPods, client.InNamespace(svc.Namespace), client.MatchingLabels(svc.Spec.Selector)); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("service pods not found")
				return ctrl.Result{}, nil
			}
			logger.Error(err, "failed to list service pods")
			return ctrl.Result{}, err
		}

		subnetName, vpcName = r.getVpcSubnetName(svc, &endpoints, &svcPods)

		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		if svc.Annotations[consts.AnnotationSubnet] != subnetName ||
			svc.Annotations[consts.AnnotationVpc] != vpcName {
			svc.Annotations[consts.AnnotationSubnet] = subnetName
			svc.Annotations[consts.AnnotationVpc] = vpcName
			if err = r.Update(ctx, svc); err != nil {
				logger.Error(err, "failed to update service subnet and vpc annotation")
				return ctrl.Result{}, err
			}
		}

		var vpc ovnv1.Vpc
		if err = r.Get(ctx, client.ObjectKey{Name: vpcName}, &vpc); err != nil {
			logger.Error(err, "unable to get vpc", "vpc", vpcName)
			return ctrl.Result{}, err
		}
		if !vpc.Status.Ready {
			logger.Info("vpc not ready", "vpc", vpcName)
			return ctrl.Result{}, errors.New("vpc not ready")
		}

		if err = translator.Service.AddServiceLoadBalancerVips(ctx, svc, &endpoints, &vpc); err != nil {
			logger.Error(err, "failed to add service vips")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *serviceController) getVpcSubnetName(svc *corev1.Service, endpoints *corev1.Endpoints, svcPods *corev1.PodList) (string, string) {
	var subnetName, vpcName string

	for _, pod := range svcPods.Items {
		if len(pod.Annotations) == 0 || pod.Annotations[consts.AnnotationAllocated] != "true" {
			continue
		}

		if subnetName == "" && pod.Annotations[consts.AnnotationSubnet] != "" {
			subnetName = pod.Annotations[consts.AnnotationSubnet]
		}

		if vpcName == "" && pod.Annotations[consts.AnnotationVpc] != "" {
			vpcName = pod.Annotations[consts.AnnotationVpc]
		}

		if subnetName != "" && vpcName != "" {
			break
		}
	}

	if subnetName == "" {
		subnetName = consts.DefaultSubnet
	}
	if vpcName == "" {
		vpcName = consts.DefaultVpc
	}

	return subnetName, vpcName
}

func (r *serviceController) handleDeleteService(ctx context.Context, svc *corev1.Service) (ctrl.Result, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	vpcName := svc.Annotations[consts.AnnotationVpc]
	if vpcName == "" {
		logger.Info("service has no vpc annotation")
		return ctrl.Result{}, nil
	}
	var vpc ovnv1.Vpc
	if err = r.Get(ctx, client.ObjectKey{Name: vpcName}, &vpc); err != nil {
		logger.Error(err, "failed to get vpc", "vpc", vpcName)
		return ctrl.Result{}, err
	}

	if err = translator.Service.DeleteServiceLoadBalancerVips(ctx, svc, &vpc); err != nil {
		logger.Error(err, "failed to delete service vips")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
