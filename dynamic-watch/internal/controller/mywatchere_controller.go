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
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// MyWatchereReconciler reconciles a MyWatchere object
type MyWatchereReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=core,resources=mywatchers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=mywatchers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=mywatchers/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=configmaps/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=pods/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=services/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MyWatchere object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.3/pkg/reconcile

// Reconcile reads that state of the cluster for a watched object
func (r *MyWatchereReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling resource", "resource", req.NamespacedName)
	r.createConfigMap(ctx, req.NamespacedName, "created")
	//add more resource types to watch here and create configmaps

	return reconcile.Result{}, nil

}

func (r *MyWatchereReconciler) createConfigMap(ctx context.Context, name types.NamespacedName, action string) error {
	//create a new configmap and set the owner reference
	//generate randome string for the configmap
	randStr := rand.String(5)

	cm := &v1.ConfigMap{
		ObjectMeta: ctrl.ObjectMeta{
			Name:      randStr,
			Namespace: name.Namespace,
		},
		Data: map[string]string{
			"action": action,
		},
	}

	//create the configmap
	if err := r.Create(ctx, cm); err != nil {
		return err
	}
	return nil

}

func (r *MyWatchereReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Add a dummy For() call
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("apps/v1") // Set the APIVersion to "apps/v1" for Deployments
	obj.SetKind("Deployment")    // Set the Kind to Deployment

	builder := ctrl.NewControllerManagedBy(mgr).For(obj)

	//Initialize a list of GVKs for the resources you want to watch
	gvks := []schema.GroupVersionKind{
		{Group: "", Version: "v1", Kind: "Pod"},
		{Group: "", Version: "v1", Kind: "Service"},
		{Group: "apps", Version: "v1", Kind: "Deployment"},
	}

	for _, gvk := range gvks {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvk)

		// Create a source for each GVK and set up the watch using the manager's cache
		src := source.Kind(mgr.GetCache(), u)
		builder = builder.WatchesRawSource(src, &handler.EnqueueRequestForObject{})
		fmt.Printf("Watching: %s\n", gvk.String())
	}

	return builder.Complete(r)
}
