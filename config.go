package main

import (
	"os"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

var (
	lg    = logrus.New()
	isK8s = os.Getenv("KUBERNETES_SERVICE_HOST") != ""
)

func setupConfig() *viper.Viper {

	cfg := viper.New()
	cfg.AddConfigPath(".")
	cfg.AddConfigPath("$HOME/azure-spot-monitor")
	cfg.AddConfigPath("/etc/azure-spot-monitor/")

	cfg.SetConfigName("azure-spot-monitor")

	cfg.SetDefault("name", "AZURE-SPOT-MONITOR")

	cfg.AutomaticEnv()
	cfg.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	cfg.SetDefault("metrics.addr", "0.0.0.0:8080")
	cfg.SetDefault("api.url", "https://prices.azure.com/api/retail/prices")
	cfg.SetDefault("label.region", "topology.kubernetes.io/region")
	cfg.SetDefault("label.instance", "node.kubernetes.io/instance-type")
	cfg.SetDefault("label.nodepool", "agentpool")
	cfg.SetDefault("time.interval", "120") //time interval in seconds
	cfg.SetDefault("configmap.cluster-autoscaler.name", "cluster-autoscaler-priority-expander")
	cfg.SetDefault("configmap.cluster-autoscaler.namespace", "kube-system")

	if isK8s {
		lg.SetFormatter(&logrus.JSONFormatter{})
	}

	if err := cfg.ReadInConfig(); err != nil {
		lg.WithError(err).Error("could not read initial config")
	}

	cfg.OnConfigChange(func(_ fsnotify.Event) {
		if err := cfg.ReadInConfig(); err != nil {
			lg.WithError(err).Warn("could not reload config")
		}
	})

	go cfg.WatchConfig()

	return cfg
}
