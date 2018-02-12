package controller

import (
	"encoding/json"
	"time"

	"github.com/golang/glog"

	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

var (
	IntializerAnnotation    string
	IntializerConfigmapName string
	InitializerName         string
	IntializerNamespace     string
)

func NewPVInitializer(clientset *kubernetes.Clientset) (cache.Controller, error) {
	restClient := clientset.CoreV1().RESTClient()
	watchlist := cache.NewListWatchFromClient(restClient, "pods", coreV1.NamespaceAll, fields.Everything())

	// Wrap the returned watchlist to workaround the inability to include
	// the `IncludeUninitialized` list option when setting up watch clients.
	includeUninitializedWatchlist := &cache.ListWatch{
		ListFunc: func(options metaV1.ListOptions) (runtime.Object, error) {
			options.IncludeUninitialized = true
			return watchlist.List(options)
		},
		WatchFunc: func(options metaV1.ListOptions) (watch.Interface, error) {
			options.IncludeUninitialized = true
			return watchlist.Watch(options)
		},
	}

	resyncPeriod := 30 * time.Second

	_, controller := cache.NewInformer(includeUninitializedWatchlist, &coreV1.Pod{}, resyncPeriod,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				err := addPod(obj.(*coreV1.Pod), clientset)
				if err != nil {
					glog.Warningf("failed to initialized: %v", err)
					return
				}
			},
		},
	)
	return controller, nil
}

func addPod(pod *coreV1.Pod, clientset *kubernetes.Clientset) error {
	glog.V(3).Infof("Creating Pod: %+v", *pod)

	if pod != nil && pod.ObjectMeta.GetInitializers() != nil {
		pendingInitializers := pod.ObjectMeta.GetInitializers().Pending

		if InitializerName == pendingInitializers[0].Name {
			glog.V(3).Infof("Initializing: %s", pod.Name)

			initializedPod := pod.DeepCopy()
			// Remove self from the list of pending Initializers while preserving ordering.
			if len(pendingInitializers) == 1 {
				initializedPod.ObjectMeta.Initializers = nil
			} else {
				initializedPod.ObjectMeta.Initializers.Pending = append(pendingInitializers[:0], pendingInitializers[1:]...)
			}
			oldData, err := json.Marshal(pod)
			if err != nil {
				return err
			}

			newData, err := json.Marshal(initializedPod)
			if err != nil {
				return err
			}

			patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, coreV1.Pod{})
			if err != nil {
				return err
			}

			_, err = clientset.CoreV1().Pods(pod.Namespace).Patch(pod.Name, types.StrategicMergePatchType, patchBytes)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
