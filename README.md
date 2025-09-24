# Azure Spot Monitor

Azure Spot Monitor checks the availability of [spot instances](https://learn.microsoft.com/en-us/azure/virtual-machines/spot-vms) in an Azure subscription using the [Placement Score API](https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/spot-placement-score?tabs=portal) to provide data used to infer instance availability and set priority scaling weights for AKS Nodepools using the cluster-autoscaler configmap

Azure Spot Monitor is intended for use in Azure Kubernetes Service clusters, with Spot Instance backed Nodepools and [Cluster Autoscaler configured with priority expander](https://learn.microsoft.com/en-us/azure/aks/cluster-autoscaler-overview) already in use.
Adding Azure Spot Monitor to the cluster enabled data backed configuration for more reliable scaling of Nodepools configured to use spot instances.

1. The tool fetches the Spot Instance Price from the [Azure Retail Prices API](https://learn.microsoft.com/en-gb/rest/api/cost-management/retail-prices/azure-retail-prices) and exposes metrics with the current instance price and percentage discount
2. It also fetches the Current Spot Instance Eviction rates from [Azure Resource Graph](https://learn.microsoft.com/en-us/rest/api/azure-resourcegraph/) and exposes metrics with this data as well.
3. It then fetches the current Placement Score for each instance based on its Availability Zone and ranks instance availability based on the the Placement Scores and uses this data to create a configmap for cluster-autoscaler instructing it to preferably the Nodepools that have instances with the highest placement scores. It also exposes the placement score as metrics.

---

## Prerequisites

- A running Kubernetes cluster (v1.22+ recommended).  
- Cluster Autoscaler deployed and configured in your cluster with priority-expander.
- Nodepools configured to use Spot Instances (preferably Nodepools configured to use a single availability zone).  
- A Managed Identity assigned to the AKS Nodepools or a Workload Identity?, with the following permissions
  - Compute Recommendations Role on the Azure Subscription
  - Reader Permissions on the AKS Cluster Resource Group
 📖 See [Azure Docs: Spot Placement Score](https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/spot-placement-score?tabs=portal) for guidance.

---

## Usage

[Helm Instructions](/helm/charts/azure-spot-monitor/README.md)

### Install chart

First provide the required parameters in a `values.yaml` file.
```yaml
# ./values.yaml
azure:
  resourceGroupName: "<resource-group-name>"
  clientId: "<managed-identity-client-id>"
  clusterName: "<aks-cluster-name>"
  subscriptionId: "<subscription-id>"
```

To install the chart with the release name my-release:

`helm install my-release oci://ghcr.io/nebed/azure-spot-monitor/charts/azure-spot-monitor --version 1.0.1 -f values.yaml`

## Metric Reference

```
# HELP azure_spot_monitor_current_discount The current effective spot discount from original VM price
# TYPE azure_spot_monitor_current_discount gauge
azure_spot_monitor_current_discount{instance="Standard_D32ads_v6",region="eastus"} 0.8137001096491229
# HELP azure_spot_monitor_eviction_rate The current spot instance eviciton rate
# TYPE azure_spot_monitor_eviction_rate gauge
azure_spot_monitor_eviction_rate{instance="Standard_D32ads_v6",region="eastus"} 0.15
# HELP azure_spot_monitor_placement_score The current placement score for the spot instance
# TYPE azure_spot_monitor_placement_score gauge
azure_spot_monitor_placement_score{instance="Standard_D32ads_v6",region="eastus",zone="1"} 25
# HELP azure_spot_monitor_regular_price The original VM price
# TYPE azure_spot_monitor_regular_price gauge
azure_spot_monitor_regular_price{instance="Standard_D32ads_v6",region="eastus"} 1.824
# HELP azure_spot_monitor_spot_price The current spot instance price
# TYPE azure_spot_monitor_spot_price gauge
azure_spot_monitor_spot_price{instance="Standard_D32ads_v6",region="eastus"} 0.339811
```