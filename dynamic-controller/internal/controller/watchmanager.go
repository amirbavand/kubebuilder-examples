package controller

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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
	ctx        context.Context
}

// WatchManager manages dynamic watches.
type WatchManager struct {
	refCounts        map[schema.GroupVersionKind]int
	watchedResources map[schema.GroupVersionKind]ControllerEntry
	mu               sync.Mutex
	cache            cache.Cache
	client           client.Client
	scheme           *runtime.Scheme
	mgr              manager.Manager
}

func NewWatchManager(mgr manager.Manager) *WatchManager {
	return &WatchManager{
		watchedResources: make(map[schema.GroupVersionKind]ControllerEntry),
		refCounts:        make(map[schema.GroupVersionKind]int),
		cache:            mgr.GetCache(),
		client:           mgr.GetClient(),
		scheme:           mgr.GetScheme(),
		mgr:              mgr,
	}
}

func (wm *WatchManager) AddWatch(gvk schema.GroupVersionKind) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if count, exists := wm.refCounts[gvk]; exists {
		wm.refCounts[gvk] = count + 1
		log.Log.Info("Incremented watch reference count", "gvk", gvk, "count", wm.refCounts[gvk])
		return nil
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)

	dynamicReconciler := &DynamicReconciler{
		Client: wm.client,
		GVK:    gvk,
	}

	ctx, cancelFunc := context.WithCancel(context.Background())

	c, err := controller.NewUnmanaged("dynamic-controller-"+gvk.Kind, wm.mgr, controller.Options{
		Reconciler: dynamicReconciler,
	})

	if err != nil {
		cancelFunc()
		return err
	}

	kindSource := source.Kind(wm.cache, obj)
	eventPredicates := wm.getEventPredicates(ctx)
	err = c.Watch(kindSource, &handler.EnqueueRequestForObject{}, eventPredicates)
	if err != nil {
		cancelFunc()
		return err
	}

	wm.watchedResources[gvk] = ControllerEntry{controller: c, cancelFunc: cancelFunc, ctx: ctx}
	wm.refCounts[gvk] = 1
	log.Log.Info("Watch added", "gvk", gvk, "count", wm.refCounts[gvk])

	go func() {
		if err := c.Start(ctx); err != nil && err != context.Canceled {
			log.Log.Error(err, "unable to start controller", "gvk", gvk)
		}
	}()
	return nil
}

func (wm *WatchManager) RemoveWatch(gvk schema.GroupVersionKind) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	log.Log.Info("Removing watch", "gvk", gvk)

	if count, exists := wm.refCounts[gvk]; exists {
		if count > 1 {
			wm.refCounts[gvk] = count - 1
			log.Log.Info("Decremented watch reference count", "gvk", gvk, "count", wm.refCounts[gvk])
			return
		}

		if entry, exists := wm.watchedResources[gvk]; exists {
			entry.cancelFunc() // Cancel the context to stop the controller
			log.Log.Info("Waiting for controller to stop", "gvk", gvk)
			<-entry.ctx.Done() // Wait until the context is fully canceled
			delete(wm.watchedResources, gvk)
			log.Log.Info("Watch removed", "gvk", gvk)
		}

		delete(wm.refCounts, gvk)
		log.Log.Info("Removed watch reference count", "gvk", gvk)
	} else {
		log.Log.Info("No watch found for", "gvk", gvk)
	}

	// Log active controllers
	wm.logActiveControllers()
}

// logActiveControllers logs all active controllers.
func (wm *WatchManager) logActiveControllers() {
	log.Log.Info("Listing all active controllers:")
	for gvk, entry := range wm.watchedResources {
		log.Log.Info("Active controller", "gvk", gvk, "context", entry.ctx)
	}
}

// DynamicReconciler reconciles dynamic resources.
type DynamicReconciler struct {
	client.Client
	GVK schema.GroupVersionKind
}

func (r *DynamicReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Reconciling dynamic resource", "GVK", r.GVK, "name", req.Name, "namespace", req.Namespace)

	resource := &unstructured.Unstructured{}
	resource.SetGroupVersionKind(r.GVK)
	err := r.Get(ctx, req.NamespacedName, resource)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, err
		}
		log.Info("Resource deleted", "GVK", r.GVK, "name", req.Name, "namespace", req.Namespace)
		return reconcile.Result{}, nil
	}

	log.Info("Reconciling dynamic resource", "GVK", r.GVK, "resource", resource)
	return reconcile.Result{}, nil
}

// getEventPredicates returns the event predicates with context check.
func (wm *WatchManager) getEventPredicates(ctx context.Context) predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			select {
			case <-ctx.Done():
				return false
			default:
				log.Log.Info("Resource created", "name", e.Object.GetName(), "kind", e.Object.GetObjectKind().GroupVersionKind().Kind)
				return true
			}
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			select {
			case <-ctx.Done():
				return false
			default:
				log.Log.Info("Resource deleted", "name", e.Object.GetName(), "kind", e.Object.GetObjectKind().GroupVersionKind().Kind)
				return true
			}
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			select {
			case <-ctx.Done():
				log.Log.Info("Context done")
				return false
			default:
				log.Log.Info("Resource updated", "name", e.ObjectNew.GetName(), "kind", e.ObjectNew.GetObjectKind().GroupVersionKind().Kind, "old version", e.ObjectOld, "new version", e.ObjectNew)
				return true
			}
		},
		GenericFunc: func(e event.GenericEvent) bool {
			select {
			case <-ctx.Done():
				return false
			default:
				return true
			}
		},
	}
}
