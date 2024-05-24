package controller

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ct "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ControllerEntry represents a dynamically managed controller.
type ControllerEntry struct {
	controller controller.Controller
	cancelFunc context.CancelFunc
}

// WatchManager manages dynamic watches
type WatchManager struct {
	watchedResources map[schema.GroupVersionKind]ct.Controller
	mu               sync.Mutex
	cache            cache.Cache
	controller       controller.Controller
	mgr              manager.Manager
}

func NewWatchManager(controller controller.Controller, cache cache.Cache, mgr manager.Manager) *WatchManager {
	return &WatchManager{
		watchedResources: make(map[schema.GroupVersionKind]ct.Controller),
		controller:       controller,
		cache:            cache,
		mgr:              mgr,
	}
}

func (wm *WatchManager) AddWatch(gvk schema.GroupVersionKind) error {
	wm.mu.Lock()
	defer wm.mu.Lock()

	if _, exists := wm.watchedResources[gvk]; exists {
		return nil
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)

	kindSource := source.Kind(wm.cache, obj)
	err := wm.controller.Watch(kindSource, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	dynamicReconciler := &DynamicReconciler{
		Client: wm.mgr.GetClient(),
		Scheme: wm.mgr.GetScheme(),
		GVK:    gvk,
	}
	c, err := ctrl.NewControllerManagedBy(wm.mgr).
		For(obj).
		WithEventFilter(eventPredicates).
		Build(dynamicReconciler)
	if err != nil {
		return err
	}

	wm.watchedResources[gvk] = c
	return nil
}

func (wm *WatchManager) RemoveWatch(gvk schema.GroupVersionKind) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if c, exists := wm.watchedResources[gvk]; exists {
		c.Stop()
		delete(wm.watchedResources, gvk)
	}

}

// DynamicReconciler reconciles dynamic resources
type DynamicReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	GVK    schema.GroupVersionKind
}

func (r *DynamicReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the resource
	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(r.GVK)
	err := r.Get(ctx, req.NamespacedName, resource)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			// Error reading the object - requeue the request.
			return reconcile.Result{}, err
		}
		// Resource not found, must have been deleted
		log.Info("Resource deleted", "GVK", r.GVK, "name", req.Name, "namespace", req.Namespace)
		return reconcile.Result{}, nil
	}

	// Get the current generation
	currentGeneration, found, err := unstructured.NestedInt64(resource.Object, "metadata", "generation")
	log.Info("Resource generation", "generation", currentGeneration)
	if err != nil || !found {
		log.Error(err, "Failed to get resource generation", "GVK", r.GVK, "name", req.Name, "namespace", req.Namespace)
		return reconcile.Result{}, err
	}

	// Get the observed generation from the status (if available)
	observedGeneration, found, err := unstructured.NestedInt64(resource.Object, "status", "observedGeneration")
	if err != nil || !found {
		// This could be the first time we're reconciling this resource, so we should proceed
		observedGeneration = 0
	}

	// Reconcile only if the generation has changed
	if currentGeneration <= observedGeneration {
		log.Info("Skipping reconcile as resource generation has not changed", "GVK", r.GVK, "name", req.Name, "namespace", req.Namespace)
		return reconcile.Result{}, nil
	}

	// Perform reconciliation logic here
	log.Info("Reconciling dynamic resource", "GVK", r.GVK, "resource", resource)

	// Update the status with the new observed generation
	// err = unstructured.SetNestedField(resource.Object, currentGeneration, "status", "observedGeneration")
	// if err != nil {
	// 	log.Error(err, "Failed to set observed generation", "GVK", r.GVK, "name", req.Name, "namespace", req.Namespace)
	// 	return reconcile.Result{}, err
	// }

	// Update the resource status
	// err = r.Status().Update(ctx, resource)
	// if err != nil {
	// 	log.Error(err, "Failed to update resource status", "GVK", r.GVK, "name", req.Name, "namespace", req.Namespace)
	// 	return reconcile.Result{}, err
	// }

	return reconcile.Result{}, nil
}

var eventPredicates = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		log.Log.Info("Resource created", "name", e.Object.GetName(), "kind", e.Object.GetObjectKind().GroupVersionKind().Kind)
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		log.Log.Info("Resource deleted", "name", e.Object.GetName(), "kind", e.Object.GetObjectKind().GroupVersionKind().Kind)
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		log.Log.Info("Resource updated",
			"name", e.ObjectNew.GetName(),
			"kind", e.ObjectNew.GetObjectKind().GroupVersionKind().Kind,
			"old version", e.ObjectOld,
			"new version", e.ObjectNew)
		return true
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return true
	},
}
