/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ResourceTemplate object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.3/pkg/reconcile
func (r *ResourceTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// TODO(user): your logic here
	resourceTemplate := &corev1.ResourceTemplate{}
	err := r.Get(ctx, req.NamespacedName, resourceTemplate)
	if err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	// Handle deletion
	if !resourceTemplate.DeletionTimestamp.IsZero() {
		r.cleanupWatches(resourceTemplate)
		return reconcile.Result{}, nil
	}
	// Add watches for the resources specified in the spec
	for _, res := range resourceTemplate.Spec.Resources {
		gvk := schema.GroupVersionKind{
			Group:   res.Group,
			Version: res.Version,
			Kind:    res.Kind,
		}

		err := r.WatchManager.AddWatch(gvk)
		if err != nil {
			log.Log.Error(err, "unable to watch for resource", "gvk", gvk)
		}
	}

	return ctrl.Result{}, nil
}

// cleanupWatches removes watches for resources specified in the ResourceTemplate spec
func (r *ResourceTemplateReconciler) cleanupWatches(resourceTemplate *corev1.ResourceTemplate) {
	for _, res := range resourceTemplate.Spec.Resources {
		gvk := schema.GroupVersionKind{
			Group:   res.Group,
			Version: res.Version,
			Kind:    res.Kind,
		}
		r.WatchManager.RemoveWatch(gvk)
	}
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
	r.WatchManager = NewWatchManager(c, mgr.GetCache(), mgr)

	return nil
}
