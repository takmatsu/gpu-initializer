package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
	"io/ioutil"

	"github.com/ghodss/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	defaultInitializerName = "gpu.initializer.kubernetes.io"
	defaultConfigmap       = "gpu-initializer"
)

var (
	initializerName   string
	configmap         string
)

type config struct {
	IgnoreNamespaces []string
}


func main() {
	flag.StringVar(&initializerName, "initializer-name", defaultInitializerName, "The initializer name")
	flag.StringVar(&configmap, "configmap", defaultConfigmap, "The gpu initializer configuration configmap")
	flag.Parse()

	log.Println("Starting the Kubernetes initializer...")
	log.Printf("Initializer name set to: %s", initializerName)

	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		log.Fatal(err)
	}

	bs, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		log.Fatal("getting namespace from pod service account data: %s", err)
	}
	namespace := string(bs)

	// Load the GPU Initializer configuration from a Kubernetes ConfigMap.
	cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(configmap, metav1.GetOptions{})
	if err != nil {
		log.Fatal(err)
	}

	c, err := configmapToConfig(cm)
	if err != nil {
		log.Fatal(err)
	}

	// Watch uninitialized Pods in all namespaces.
	restClient := clientset.Core().RESTClient()
	watchlist := cache.NewListWatchFromClient(restClient, "pods", corev1.NamespaceAll, fields.Everything())

	// Wrap the returned watchlist to workaround the inability to include
	// the `IncludeUninitialized` list option when setting up watch clients.
	includeUninitializedWatchlist := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.IncludeUninitialized = true
			return watchlist.List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.IncludeUninitialized = true
			return watchlist.Watch(options)
		},
	}

	resyncPeriod := 30 * time.Second

	_, controller := cache.NewInformer(includeUninitializedWatchlist, &corev1.Pod{}, resyncPeriod,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				err := initializePod(obj.(*corev1.Pod), c, clientset)
				if err != nil {
					log.Println(err)
				}
			},
		},
	)

	stop := make(chan struct{})
	go controller.Run(stop)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	log.Println("Shutdown signal received, exiting...")
	close(stop)
}

func initializePod(pod *corev1.Pod, c *config, clientset *kubernetes.Clientset) error {
	if pod.ObjectMeta.GetInitializers() != nil {
		pendingInitializers := pod.ObjectMeta.GetInitializers().Pending

		if initializerName == pendingInitializers[0].Name {
			log.Printf("Initializing pod: %s", pod.Name)

			initializedPod := pod.DeepCopyObject().(*corev1.Pod)

			// Remove self from the list of pending Initializers while preserving ordering.
			if len(pendingInitializers) == 1 {
				initializedPod.ObjectMeta.Initializers = nil
			} else {
				initializedPod.ObjectMeta.Initializers.Pending = append(pendingInitializers[:0], pendingInitializers[1:]...)
			}

			// If the Pod is in ignoring namespace, do nothing
			for _, v := range c.IgnoreNamespaces {
				if v == initializedPod.ObjectMeta.Namespace {
					log.Printf("Pod: %s is ignored", initializedPod.Name)
					return applyNewPod(pod, initializedPod, clientset)
				}
			}

			// Modify the Pod spec to include the env NVIDIA_VISIBLE_DEVICES.
			// Then patch the original pod.
			inject_env := corev1.EnvVar{Name:"NVIDIA_VISIBLE_DEVICES", Value:"none"}
			for i, v := range initializedPod.Spec.Containers {
				// Delete original NVIDIA_VISIBLE_DEVICES parameter.
				newEnv := []corev1.EnvVar{}
				for _, vv := range v.Env {
					if vv.Name != "NVIDIA_VISIBLE_DEVICES" {
						newEnv = append(newEnv, vv)
					}
				}
				// If not specified gpu resources, inject env.
				gpu_limits, ok := v.Resources.Limits["nvidia.com/gpu"]
				if !ok || (ok && gpu_limits.IsZero()) {
					initializedPod.Spec.Containers[i].Env = append(newEnv, inject_env)
				}
			}
			return applyNewPod(pod, initializedPod, clientset)
		}
	}
	return nil
}

func configmapToConfig(configmap *corev1.ConfigMap) (*config, error) {
	var c config
	err := yaml.Unmarshal([]byte(configmap.Data["config"]), &c)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func applyNewPod(oldPod *corev1.Pod, newPod *corev1.Pod, clientset *kubernetes.Clientset) error {
	oldData, err := json.Marshal(oldPod)
	if err != nil {
		return err
	}

	newData, err := json.Marshal(newPod)
	if err != nil {
		return err
	}

	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, corev1.Pod{})
	if err != nil {
		return err
	}

	_, err = clientset.CoreV1().Pods(oldPod.Namespace).Patch(oldPod.Name, types.StrategicMergePatchType, patchBytes)
	if err != nil {
		return err
	}
	return nil
} 
