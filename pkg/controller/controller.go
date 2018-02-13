package controller

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/golang/glog"

	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
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

type Controller struct {
	clientset     *kubernetes.Clientset
	podPVCMap     map[string][]*coreV1.PersistentVolumeClaim
	podPVCLock    *sync.Mutex
	podController cache.Controller
	pvcController cache.Controller
}

func NewPVInitializer(clientset *kubernetes.Clientset) *Controller {
	c := &Controller{
		clientset:  clientset,
		podPVCMap:  make(map[string][]*coreV1.PersistentVolumeClaim),
		podPVCLock: &sync.Mutex{},
	}

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

	_, podController := cache.NewInformer(
		includeUninitializedWatchlist,
		&coreV1.Pod{},
		resyncPeriod,
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
	c.podController = podController

	pvcListWatcher := cache.NewListWatchFromClient(
		restClient,
		"persistentvolumeclaims",
		coreV1.NamespaceAll,
		fields.Everything())

	_, pvcController := cache.NewInformer(
		pvcListWatcher,
		&coreV1.PersistentVolumeClaim{},
		resyncPeriod,
		cache.ResourceEventHandlerFuncs{
			UpdateFunc: func(old, new interface{}) {
				err := updatePVC(old.(*coreV1.PersistentVolumeClaim), new.(*coreV1.PersistentVolumeClaim), clientset)
				if err != nil {
					glog.Warningf("failed to initialized: %v", err)
					return
				}
			},
		},
	)
	c.pvcController = pvcController
	return c
}

func (c *Controller) Run(ctx <-chan struct{}) {
	glog.Infof("pod controller starting")
	go c.podController.Run(ctx)
	glog.Infof("Waiting for pod informer initial sync")
	wait.Poll(time.Second, 5*time.Minute, func() (bool, error) {
		return c.podController.HasSynced(), nil
	})
	if !c.podController.HasSynced() {
		glog.Errorf("pod informer controller initial sync timeout")
		os.Exit(1)
	}
	glog.Infof("pvc controller starting")
	go c.pvcController.Run(ctx)
	glog.Infof("Waiting for pvc informer initial sync")
	wait.Poll(time.Second, 5*time.Minute, func() (bool, error) {
		return c.pvcController.HasSynced(), nil
	})
	if !c.pvcController.HasSynced() {
		glog.Errorf("pvc informer controller initial sync timeout")
		os.Exit(1)
	}
}

func addPod(pod *coreV1.Pod, clientset *kubernetes.Clientset) error {
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
			vols := pod.Spec.Volumes
			for _, vol := range vols {
				if vol.VolumeSource.PersistentVolumeClaim != nil {
					pvcName := vol.VolumeSource.PersistentVolumeClaim.ClaimName
					glog.V(3).Infof("PVC %s", pvcName)
					pvc, err := clientset.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(pvcName, metaV1.GetOptions{})
					if err == nil {
						// if PVC is bound, update PV.
						pvName := pvc.Spec.VolumeName
						if len(pvName) > 0 {
							pv, err := clientset.CoreV1().PersistentVolumes().Get(pvName, metaV1.GetOptions{})
							if err == nil {
								glog.V(3).Infof("update PV %s", pv.Name)
								//TODO update PV here
							}
						} else {
							// defer till PVC is bound
							//TODO remember this PVC and update PV later
						}
					}
				}
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
			glog.V(3).Infof("Initialized: %s", pod.Name)
		}
	}

	return nil
}

func updatePVC(oldPVC, newPVC *coreV1.PersistentVolumeClaim, clientset *kubernetes.Clientset) error {
	glog.V(3).Infof("updating pvc %+v", *oldPVC)
	return nil
}
