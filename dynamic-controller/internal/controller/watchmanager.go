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
	refCounts         map[schema.GroupVersionKind]int
	watchedResources  map[schema.GroupVersionKind]ControllerEntry
	resourceTemplates map[string]map[schema.GroupVersionKind]struct{}
	mu                sync.Mutex
	cache             cache.Cache
	client            client.Client
	scheme            *runtime.Scheme
	mgr               manager.Manager
}

func NewWatchManager(mgr manager.Manager) *WatchManager {
	return &WatchManager{
		watchedResources:  make(map[schema.GroupVersionKind]ControllerEntry),
		refCounts:         make(map[schema.GroupVersionKind]int),
		resourceTemplates: make(map[string]map[schema.GroupVersionKind]struct{}),
		cache:             mgr.GetCache(),
		client:            mgr.GetClient(),
		scheme:            mgr.GetScheme(),
		mgr:               mgr,
	}
}

func (wm *WatchManager) AddWatch(resourceTemplateName string, gvks []schema.GroupVersionKind) error {
	if _, exists := wm.resourceTemplates[resourceTemplateName]; !exists {
		wm.resourceTemplates[resourceTemplateName] = make(map[schema.GroupVersionKind]struct{})
	}

	for _, gvk := range gvks {
		if _, exists := wm.resourceTemplates[resourceTemplateName][gvk]; exists {
			continue
		}
		wm.resourceTemplates[resourceTemplateName][gvk] = struct{}{}
		if wm.refCounts[gvk] == 0 {
			if err := wm.startWatching(gvk); err != nil {
				return err
			}
		}
		wm.refCounts[gvk]++
		log.Log.Info("Incremented watch reference count", "gvk", gvk, "count", wm.refCounts[gvk])
	}

	return nil
}

func (wm *WatchManager) UpdateWatch(resourceTemplateName string, newGVKs []schema.GroupVersionKind) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if _, exists := wm.resourceTemplates[resourceTemplateName]; !exists {
		log.Log.Info("Resource template not found", "resourceTemplateName", resourceTemplateName)
		return wm.AddWatch(resourceTemplateName, newGVKs)
	}

	oldGVKs := wm.resourceTemplates[resourceTemplateName]
	removedGVKs := make(map[schema.GroupVersionKind]struct{})
	addedGVKs := make(map[schema.GroupVersionKind]struct{})

	// Determine removed GVKs
	for gvk := range oldGVKs {
		removedGVKs[gvk] = struct{}{}
	}
	for _, gvk := range newGVKs {
		if _, exists := removedGVKs[gvk]; exists {
			delete(removedGVKs, gvk)
		} else {
			addedGVKs[gvk] = struct{}{}
		}
	}

	// Remove watches for removed GVKs
	for gvk := range removedGVKs {
		wm.removeWatchForGVK(resourceTemplateName, gvk)
	}

	// Add new watches
	for gvk := range addedGVKs {
		if err := wm.addWatchForGVK(resourceTemplateName, gvk); err != nil {
			return err
		}
	}

	return nil
}

func (wm *WatchManager) RemoveWatch(resourceTemplateName string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	log.Log.Info("Removing watch", "resourceTemplateName", resourceTemplateName)

	if watchedGVKs, exists := wm.resourceTemplates[resourceTemplateName]; exists {
		for gvk := range watchedGVKs {
			wm.refCounts[gvk]--
			if wm.refCounts[gvk] <= 0 {
				wm.stopWatching(gvk)
				delete(wm.refCounts, gvk)
			}
		}
		delete(wm.resourceTemplates, resourceTemplateName)
	}
	//log reference counts
	for gvk, count := range wm.refCounts {
		log.Log.Info("Reference count", "gvk", gvk, "count", count)
	}

	wm.logActiveControllers()
}

func (wm *WatchManager) addWatchForGVK(resourceTemplateName string, gvk schema.GroupVersionKind) error {
	wm.resourceTemplates[resourceTemplateName][gvk] = struct{}{}
	if wm.refCounts[gvk] == 0 {
		if err := wm.startWatching(gvk); err != nil {
			log.Log.Error(err, "unable to start watching", "gvk", gvk)
			return err
		}
	}
	wm.refCounts[gvk]++
	log.Log.Info("Incremented watch reference count", "gvk", gvk, "count", wm.refCounts[gvk])
	return nil
}

func (wm *WatchManager) removeWatchForGVK(resourceTemplateName string, gvk schema.GroupVersionKind) {
	wm.refCounts[gvk]--
	if wm.refCounts[gvk] <= 0 {
		wm.stopWatching(gvk)
		delete(wm.refCounts, gvk)
	}
	delete(wm.resourceTemplates[resourceTemplateName], gvk)
	log.Log.Info("Decremented watch reference count", "gvk", gvk, "count", wm.refCounts[gvk])
}

func (wm *WatchManager) startWatching(gvk schema.GroupVersionKind) error {
	log.Log.Info("Starting watch", "gvk", gvk)
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
	go func() {
		if err := c.Start(ctx); err != nil && err != context.Canceled {
			log.Log.Error(err, "unable to start controller", "gvk", gvk)
		}
	}()
	return nil
}

func (wm *WatchManager) stopWatching(gvk schema.GroupVersionKind) {
	log.Log.Info("Stopping watch", "gvk", gvk)
	if entry, exists := wm.watchedResources[gvk]; exists {
		entry.cancelFunc() // Cancel the context to stop the controller
		<-entry.ctx.Done() // Wait until the context is fully canceled
		delete(wm.watchedResources, gvk)
	}
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
