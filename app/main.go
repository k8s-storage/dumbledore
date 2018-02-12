package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/golang/glog"

	"github.com/k8s-storage/dumbledore/pkg/controller"

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	defaultInitializerAnnotation = "initializer.kubernetes.io/pv"
	defaultInitializerName       = "pv.initializer.kubernetes.io"
	defaultConfigmapName         = "pv-initializer"
	defaultConfigMapNamespace    = "default"
)

var (
        kubeConfig      string                                                                                                                                                              
        kubeMaster      string  
)
func main() {
	flag.StringVar(&controller.IntializerAnnotation, "annotation", defaultInitializerAnnotation, "The annotation to trigger initialization")
	flag.StringVar(&controller.IntializerConfigmapName, "configmap", defaultConfigmapName, "storage initializer configuration configmap")
	flag.StringVar(&controller.InitializerName, "initializer-name", defaultInitializerName, "The initializer name")
	flag.StringVar(&controller.IntializerNamespace, "namespace", defaultConfigMapNamespace, "The configuration namespace")
	flag.StringVar(&kubeConfig, "kubeconfig", "", "Absolute path to the kubeconfig")
	flag.StringVar(&kubeMaster, "kubemaster", "", "Kubernetes Controller Master URL")
	flag.Parse()
	flag.Set("logtostderr", "true")

	var clusterConfig *rest.Config
	var err error
	if len(kubeMaster) > 0 || len(kubeConfig) > 0 {
		clusterConfig, err = clientcmd.BuildConfigFromFlags(kubeMaster, kubeConfig)
	} else {
		clusterConfig, err = rest.InClusterConfig()
	}

	if err != nil {
		glog.Fatal(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		glog.Fatal(err)
	}

	_, err = clientset.CoreV1().ConfigMaps(controller.IntializerNamespace).Get(controller.IntializerConfigmapName, metaV1.GetOptions{})
	if err != nil {
		glog.Warning(err)
	}

	ctrl, err := controller.NewPVInitializer(clientset)
	if err != nil {
		glog.Fatal(err)
	}

	stop := make(chan struct{})
	go ctrl.Run(stop)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	close(stop)
}
