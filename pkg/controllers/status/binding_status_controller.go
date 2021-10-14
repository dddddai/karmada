package status

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/dynamic"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
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

// BindingStatusControllerName is the controller name that will be used when reporting events.
const BindingStatusControllerName = "binding-status-controller"

// BindingStatusController is to sync status of ResourceBinding.
type BindingStatusController struct {
	client.Client                   // used to operate ResourceBinding resources.
	DynamicClient dynamic.Interface // used to fetch arbitrary resources.
	EventRecorder record.EventRecorder
	RESTMapper    meta.RESTMapper
}

// Reconcile syncs status of the given member cluster.
// The Controller will requeue the Request to be processed again if an error is non-nil or
// Result.Requeue is true, otherwise upon completion it will requeue the reconcile key after the duration.
func (c *BindingStatusController) Reconcile(ctx context.Context, req controllerruntime.Request) (controllerruntime.Result, error) {
	klog.V(4).Infof("Reconciling status of ResourceBinding %q", req.NamespacedName.Name)

	rb := &workv1alpha2.ResourceBinding{}
	if err := c.Client.Get(context.TODO(), req.NamespacedName, rb); err != nil {
		// The resource may no longer exist, in which case we stop processing.
		if apierrors.IsNotFound(err) {
			return controllerruntime.Result{}, nil
		}

		return controllerruntime.Result{Requeue: true}, err
	}

	if !rb.DeletionTimestamp.IsZero() {
		return controllerruntime.Result{}, nil
	}

	isReady := helper.IsBindingReady(rb.Spec.Clusters)
	if !isReady {
		klog.Infof("ResourceBinding(%s/%s) is not ready to sync", rb.GetNamespace(), rb.GetName())
		return controllerruntime.Result{}, nil
	}

	return c.syncBindingStatus(rb)
}

func (c *BindingStatusController) syncBindingStatus(rb *workv1alpha2.ResourceBinding) (controllerruntime.Result, error) {
	workload, err := helper.FetchWorkload(c.DynamicClient, c.RESTMapper, rb.Spec.Resource)
	if err != nil {
		klog.Errorf("Failed to fetch workload for resourceBinding(%s/%s). Error: %v.",
			rb.GetNamespace(), rb.GetName(), err)
		return controllerruntime.Result{Requeue: true}, err
	}
	err = helper.AggregateResourceBindingWorkStatus(c.Client, rb, workload)
	if err != nil {
		klog.Errorf("Failed to aggregate workStatuses to resourceBinding(%s/%s). Error: %v.",
			rb.GetNamespace(), rb.GetName(), err)
		c.EventRecorder.Event(rb, corev1.EventTypeWarning, binding.EventReasonAggregateStatusFailed, err.Error())
		return controllerruntime.Result{Requeue: true}, err
	}
	msg := fmt.Sprintf("Update resourceBinding(%s/%s) with AggregatedStatus successfully.", rb.Namespace, rb.Name)
	klog.V(4).Infof(msg)
	c.EventRecorder.Event(rb, corev1.EventTypeNormal, binding.EventReasonAggregateStatusSucceed, msg)
	return controllerruntime.Result{}, nil
}

// SetupWithManager creates a controller and register to controller manager.
func (c *BindingStatusController) SetupWithManager(mgr controllerruntime.Manager) error {
	workFn := handler.MapFunc(
		func(a client.Object) []reconcile.Request {
			var requests []reconcile.Request

			// TODO: Delete this logic in the next release to prevent incompatibility when upgrading the current release (v0.10.0).
			labels := a.GetLabels()
			rbNamespace, namespaceExist := labels[workv1alpha2.ResourceBindingNamespaceLabel]
			rbName, nameExist := labels[workv1alpha2.ResourceBindingNameLabel]
			if namespaceExist && nameExist {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: rbNamespace,
						Name:      rbName,
					},
				})
			}

			annotations := a.GetAnnotations()
			rbNamespace, namespaceExist = annotations[workv1alpha2.ResourceBindingNamespaceLabel]
			rbName, nameExist = annotations[workv1alpha2.ResourceBindingNameLabel]
			if namespaceExist && nameExist {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: rbNamespace,
						Name:      rbName,
					},
				})
			}

			return requests
		})

	return controllerruntime.NewControllerManagedBy(mgr).For(&workv1alpha2.ResourceBinding{}).
		Watches(&source.Kind{Type: &workv1alpha1.Work{}}, handler.EnqueueRequestsFromMapFunc(workFn), binding.WorkPredicateFn).
		Complete(c)
}
