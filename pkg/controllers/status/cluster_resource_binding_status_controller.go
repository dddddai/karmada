package status

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	workv1alpha1 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha1"
	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/controllers/binding"
	"github.com/karmada-io/karmada/pkg/util/helper"
)

// ClusterResourceBindingStatusControllerName is the controller name that will be used when reporting events.
const ClusterResourceBindingStatusControllerName = "cluster-resource-binding-status-controller"

// ClusterResourceBindingController is to sync status of ClusterResourceBinding.
type ClusterResourceBindingStatusController struct {
	client.Client                   // used to operate ClusterResourceBinding resources.
	DynamicClient dynamic.Interface // used to fetch arbitrary resources.
	EventRecorder record.EventRecorder
	RESTMapper    meta.RESTMapper
}

// Reconcile performs a full reconciliation for the object referred to by the Request.
// The Controller will requeue the Request to be processed again if an error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (c *ClusterResourceBindingStatusController) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).Infof("Reconciling status of ClusterResourceBinding %q", req.NamespacedName.String())

	crb := &workv1alpha2.ClusterResourceBinding{}
	if err := c.Client.Get(context.TODO(), req.NamespacedName, crb); err != nil {
		// The resource may no longer exist, in which case we stop processing.
		if apierrors.IsNotFound(err) {
			return controllerruntime.Result{}, nil
		}

		return controllerruntime.Result{Requeue: true}, err
	}

	if !crb.DeletionTimestamp.IsZero() {
		return controllerruntime.Result{}, nil
	}

	isReady := helper.IsBindingReady(crb.Spec.Clusters)
	if !isReady {
		klog.Infof("ClusterResourceBinding %s is not ready to sync", crb.GetName())
		return controllerruntime.Result{}, nil
	}

	return c.syncBindingStatus(crb)
}

// syncBinding will sync clusterResourceBinding to Works.
func (c *ClusterResourceBindingStatusController) syncBindingStatus(crb *workv1alpha2.ClusterResourceBinding) (controllerruntime.Result, error) {
	workload, err := helper.FetchWorkload(c.DynamicClient, c.RESTMapper, crb.Spec.Resource)
	if err != nil {
		klog.Errorf("Failed to fetch workload for clusterResourceBinding(%s). Error: %v.", crb.GetName(), err)
		return controllerruntime.Result{Requeue: true}, err
	}

	err = helper.AggregateClusterResourceBindingWorkStatus(c.Client, crb, workload)
	if err != nil {
		klog.Errorf("Failed to aggregate workStatuses to clusterResourceBinding(%s). Error: %v.", crb.GetName(), err)
		c.EventRecorder.Event(crb, corev1.EventTypeWarning, binding.EventReasonAggregateStatusFailed, err.Error())
		return controllerruntime.Result{Requeue: true}, err
	}
	msg := fmt.Sprintf("Update clusterResourceBinding(%s) with AggregatedStatus successfully.", crb.Name)
	klog.V(4).Infof(msg)
	c.EventRecorder.Event(crb, corev1.EventTypeNormal, binding.EventReasonAggregateStatusSucceed, msg)
	return controllerruntime.Result{}, nil
}

// SetupWithManager creates a controller and register to controller manager.
func (c *ClusterResourceBindingStatusController) SetupWithManager(mgr controllerruntime.Manager) error {
	workFn := handler.MapFunc(
		func(a client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// TODO: Delete this logic in the next release to prevent incompatibility when upgrading the current release (v0.10.0).
			labels := a.GetLabels()
			crbName, nameExist := labels[workv1alpha2.ClusterResourceBindingLabel]
			if nameExist {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: crbName,
					},
				})
			}

			annotations := a.GetAnnotations()
			crbName, nameExist = annotations[workv1alpha2.ClusterResourceBindingLabel]
			if nameExist {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: crbName,
					},
				})
			}

			return requests
		})

	return controllerruntime.NewControllerManagedBy(mgr).For(&workv1alpha1.ClusterResourceBinding{}).
		Watches(&source.Kind{Type: &workv1alpha1.Work{}}, handler.EnqueueRequestsFromMapFunc(workFn), binding.WorkPredicateFn).
		Complete(c)
}
