package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	corev1 "github.com/amirbavand/dynamic-controller/api/v1"
)

const resourceTemplateFinalizer = "core.example.com/finalizer"

// containsString checks if a string is in a slice
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// removeString removes a string from a slice
func removeString(slice []string, s string) []string {
	var result []string
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// EnsureFinalizer adds a finalizer to the resource if not present
func (r *ResourceTemplateReconciler) EnsureFinalizer(resourceTemplate *corev1.ResourceTemplate) bool {
	if !containsString(resourceTemplate.GetFinalizers(), resourceTemplateFinalizer) {
		resourceTemplate.SetFinalizers(append(resourceTemplate.GetFinalizers(), resourceTemplateFinalizer))
		return true
	}
	return false
}

// RemoveFinalizer removes the finalizer from the resource
func (r *ResourceTemplateReconciler) RemoveFinalizer(resourceTemplate *corev1.ResourceTemplate) bool {
	if containsString(resourceTemplate.GetFinalizers(), resourceTemplateFinalizer) {
		resourceTemplate.SetFinalizers(removeString(resourceTemplate.GetFinalizers(), resourceTemplateFinalizer))
		return true
	}
	return false
}

// ResourceTemplateReconciler reconciles a ResourceTemplate object
type ResourceTemplateReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	WatchManager      *WatchManager
	DynamicReconciler reconcile.Reconciler
}

//+kubebuilder:rbac:groups=core.example.com,resources=resourcetemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.example.com,resources=resourcetemplates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.example.com,resources=resourcetemplates/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ResourceTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Reconciling ResourceTemplate", "name", req.NamespacedName)

	resourceTemplate := &corev1.ResourceTemplate{}
	err := r.Get(ctx, req.NamespacedName, resourceTemplate)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the resource is being deleted
	if !resourceTemplate.DeletionTimestamp.IsZero() {
		// The resource is being deleted
		log.Info("ResourceTemplate deleted", "name", resourceTemplate.Name)
		r.WatchManager.RemoveWatch(resourceTemplate.Name)

		// Remove finalizer
		if r.RemoveFinalizer(resourceTemplate) {
			err := r.Update(ctx, resourceTemplate)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	// Ensure finalizer is added to the resource
	if r.EnsureFinalizer(resourceTemplate) {
		err := r.Update(ctx, resourceTemplate)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Convert resources to GVKs
	var gvkList []schema.GroupVersionKind
	for _, res := range resourceTemplate.Spec.Resources {
		gvk := schema.GroupVersionKind{
			Group:   res.Group,
			Version: res.Version,
			Kind:    res.Kind,
		}
		gvkList = append(gvkList, gvk)
	}

	// Update watches for the resources specified in the spec
	err = r.WatchManager.UpdateWatch(resourceTemplate.Name, gvkList)
	if err != nil {
		log.Error(err, "unable to update watch for resource", "resourceTemplate", resourceTemplate.Name)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a new controller named "resourcetemplate-controller"
	c, err := controller.New("resourcetemplate-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to ResourceTemplate
	err = c.Watch(source.Kind(mgr.GetCache(), &corev1.ResourceTemplate{}), &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Initialize the WatchManager with the controller and the manager's cache
	r.WatchManager = NewWatchManager(mgr)

	return nil
}
