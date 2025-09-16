package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type PriorityData struct {
	Data map[int][]string `yaml:"data"`
}

func getK8SClient() (clientset *kubernetes.Clientset, err error) {
	var cfg *rest.Config
	if isK8s {
		// load incluster config
		cfg, err = rest.InClusterConfig()
	} else {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		lg.WithError(err).Fatal("failed to build config")
		return nil, err
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func checkSpotIsSafe(nodePools NodepoolMap, priorities map[int][]string) bool {

	// Get default nodepool names
	regularNodepoolNames := []string{}
	for _, np := range nodePools {
		if np.Type == "Regular" {
			regularNodepoolNames = append(regularNodepoolNames, fmt.Sprintf(".*%s.*", strings.TrimSpace(np.Name)))
		}
	}

	// Find the highest priority value
	var maxPriority int
	for priority := range priorities {
		if priority > maxPriority {
			maxPriority = priority
		}
	}

	// Get the nodepool patterns at the highest priority
	highestPriorityPools := priorities[maxPriority]

	// Check if any of them match a default nodepool name
	for _, pattern := range highestPriorityPools {
		for _, defaultName := range regularNodepoolNames {
			if pattern == defaultName {
				return false
			}
		}
	}

	return true
}

func calculatePriority(nodePools NodepoolMap) (priorities map[int][]string) {
	priorityMap := make(map[int][]string)
	for _, nodePool := range nodePools {
		var priority int
		// Calculate the components of the discount and placementscore on the priority with placementscore having more weight
		availabilityRateFactor := (1 - nodePool.EvictionRate) * 0.2
		discountFactor := nodePool.Discount * 0.1
		placementScoreFactor := float64(nodePool.PlacementScore) / 100 * 0.6
		versionFactor := float64(min(10, nodePool.Version)) / 10 * 0.1
		priority = int((availabilityRateFactor + discountFactor + versionFactor + placementScoreFactor) * 100)

		priorityMap[priority] = append(priorityMap[priority], fmt.Sprintf(".*%s.*", nodePool.Name))
	}

	return priorityMap
}

func updateConfigMap(ctx context.Context, config *viper.Viper, nodePools NodepoolMap) error {

	calculatedPriorities := calculatePriority(nodePools)
	clientset, err := getK8SClient()
	if err != nil {
		lg.WithError(err).Fatal("failed to create clientset")
		return err
	}

	clusterAutoscalerCmName := config.GetString("configmap.cluster-autoscaler.name")
	clusterAutoscalerCmNamespace := config.GetString("configmap.cluster-autoscaler.namespace")

	//prepare yaml for cluster-autoscaler configmap
	newData := PriorityData{calculatedPriorities}
	newDataYaml, err := yaml.Marshal(newData.Data)
	if err != nil {
		return err
	}
	newDataYamlString := string(newDataYaml)

	// Retrieve the existing cluster-autoscaler ConfigMap
	cm, err := clientset.CoreV1().ConfigMaps(clusterAutoscalerCmNamespace).Get(ctx, clusterAutoscalerCmName, metav1.GetOptions{})
	if err != nil {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterAutoscalerCmName,
				Namespace: clusterAutoscalerCmNamespace,
			},
			Data: map[string]string{
				"priorities": newDataYamlString,
			},
		}
		_, err = clientset.CoreV1().ConfigMaps(clusterAutoscalerCmNamespace).Create(ctx, cm, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		lg.Info("Autoscaler configmap created")
		return nil
	}

	if newDataYamlString == cm.Data["priorities"] {
		lg.Info("No need to update, existing data is already up to date")
		return nil
	}

	cm.Data["priorities"] = newDataYamlString

	// Update the cluster-autoscaler ConfigMap
	_, err = clientset.CoreV1().ConfigMaps(clusterAutoscalerCmNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	lg.Info("Autoscaler configmap updated")

	return nil
}
